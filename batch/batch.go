package batch

import (
	"bufio"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/guoyyit/genome-liftover-go/liftover"
)

// Options controls batch mapping behavior.
type Options struct {
	Direction liftover.Direction
	Workers   int
	// IncludeAllCandidates emits every tie/overlap candidate instead of only
	// the deterministically selected one.
	IncludeAllCandidates bool
}

// Stats summarizes a batch run.
type Stats struct {
	Total      int
	Mapped     int
	Partial    int
	Unmapped   int
	RecordErrs int
}

type record struct {
	seq    int
	lineNo int
	chrom  string
	start  int
	end    int
	name   string
}

type jobResult struct {
	seq    int
	rec    record
	result *liftover.Result
	err    error
}

// Run streams TSV interval records through the mapper and writes results
// deterministically to out; bad records go to errOut without aborting.
func Run(m *liftover.Mapper, in io.Reader, out, errOut io.Writer, opts Options) (Stats, error) {
	if opts.Workers < 1 {
		opts.Workers = 1
	}

	outW := bufio.NewWriterSize(out, 1<<20)
	errW := bufio.NewWriterSize(errOut, 1<<20)
	defer outW.Flush()
	defer errW.Flush()

	type parseOutcome struct {
		rec record
		err error
	}
	parsed := make(chan parseOutcome, opts.Workers*2)
	results := make(chan jobResult, opts.Workers*2)

	// Stage 1: sequential, single-goroutine parsing keeps record order.
	go func() {
		sc := bufio.NewScanner(in)
		sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
		seq := 0
		lineNo := 0
		for sc.Scan() {
			lineNo++
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			seq++
			fields := strings.Split(line, "\t")
			if len(fields) < 3 {
				parsed <- parseOutcome{rec: record{seq: seq, lineNo: lineNo},
					err: fmt.Errorf("need at least 3 tab-separated fields (chrom start end)")}
				continue
			}
			start, err1 := strconv.Atoi(fields[1])
			end, err2 := strconv.Atoi(fields[2])
			if err1 != nil || err2 != nil {
				parsed <- parseOutcome{rec: record{seq: seq, lineNo: lineNo, chrom: fields[0]},
					err: fmt.Errorf("start/end must be integers")}
				continue
			}
			name := fmt.Sprintf("%s:%d-%d", fields[0], start, end)
			if len(fields) >= 4 && fields[3] != "" {
				name = fields[3]
			}
			parsed <- parseOutcome{rec: record{
				seq: seq, lineNo: lineNo,
				chrom: fields[0], start: start, end: end, name: name,
			}}
		}
		if err := sc.Err(); err != nil {
			parsed <- parseOutcome{err: fmt.Errorf("read batch input: %w", err)}
		}
		close(parsed)
	}()

	// Stage 2: bounded worker pool; the immutable index makes this safe.
	var wg sync.WaitGroup
	for i := 0; i < opts.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for po := range parsed {
				if po.err != nil {
					if po.rec.seq == 0 {
						results <- jobResult{seq: 0, err: po.err}
					} else {
						results <- jobResult{seq: po.rec.seq, rec: po.rec, err: po.err}
					}
					continue
				}
				rec := po.rec
				r, err := m.Map(rec.chrom, rec.start, rec.end, opts.Direction)
				results <- jobResult{seq: rec.seq, rec: rec, result: r, err: err}
			}
		}()
	}
	go func() {
		wg.Wait()
		close(results)
	}()

	// Stage 3: reorder by sequence and emit, so output order equals input
	// order regardless of worker scheduling.
	var stats Stats
	var scanErr error
	pending := make(map[int]jobResult)
	next := 1
	for jr := range results {
		if jr.seq == 0 && jr.err != nil {
			scanErr = jr.err
			continue
		}
		pending[jr.seq] = jr
		for {
			cur, ok := pending[next]
			if !ok {
				break
			}
			delete(pending, next)
			next++
			emitRecord(cur, outW, errW, &stats, opts)
		}
	}
	return stats, scanErr
}

func emitRecord(jr jobResult, outW, errW *bufio.Writer, stats *Stats, opts Options) {
	stats.Total++
	if jr.err != nil {
		stats.RecordErrs++
		fmt.Fprintf(errW, "line %d\tERROR\t%s\n", jr.rec.lineNo, jr.err)
		return
	}
	status := classify(jr.result)
	switch status {
	case "mapped":
		stats.Mapped++
	case "partial":
		stats.Partial++
	default:
		stats.Unmapped++
	}
	candidates := jr.result.Candidates
	if len(candidates) == 0 {
		fmt.Fprintf(outW, "%s\tunmapped\t%s\t%d\t%d\t.\t.\t.\t.\t%s\n",
			jr.rec.name, jr.rec.chrom, jr.rec.start, jr.rec.end, status)
		return
	}
	chosen := candidates[:1]
	if opts.IncludeAllCandidates {
		chosen = candidates
	}
	for ci, cand := range chosen {
		segs := append([]liftover.Segment(nil), cand.Segments...)
		sort.SliceStable(segs, func(i, j int) bool {
			if segs[i].SourceStart != segs[j].SourceStart {
				return segs[i].SourceStart < segs[j].SourceStart
			}
			return segs[i].SourceEnd < segs[j].SourceEnd
		})
		primary := ""
		if ci == 0 {
			primary = status
		}
		for _, s := range segs {
			if s.Status == liftover.StatusMapped {
				fmt.Fprintf(outW, "%s\tmapped\t%s\t%d\t%d\t%s\t%d\t%d\t%c\t%s\tcandidate=%d\tscore=%d\n",
					jr.rec.name, jr.rec.chrom, s.SourceStart, s.SourceEnd,
					s.DestChrom, s.DestStart, s.DestEnd, byte(s.Strand), primary,
					cand.ChainID, cand.Score)
			} else {
				fmt.Fprintf(outW, "%s\tunmapped\t%s\t%d\t%d\t.\t.\t.\t.\t%s\tcandidate=%d\tscore=%d\n",
					jr.rec.name, jr.rec.chrom, s.SourceStart, s.SourceEnd, primary,
					cand.ChainID, cand.Score)
			}
		}
	}
}

func classify(r *liftover.Result) string {
	best, ok := r.BestCandidate()
	if !ok {
		return "unmapped"
	}
	mapped := 0
	for _, s := range best.Segments {
		if s.Status == liftover.StatusMapped {
			mapped++
		}
	}
	if mapped == 0 {
		return "unmapped"
	}
	if mapped == len(best.Segments) {
		return "mapped"
	}
	return "partial"
}
