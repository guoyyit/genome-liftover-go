// Package batch 提供制表符分隔区间文件的批量映射。
package batch

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"

	"github.com/guoyyit/genome-liftover-go/liftover"
)

// Stats 汇总一批记录的映射情况。
type Stats struct {
	Total    int
	Mapped   int
	Partial  int
	Unmapped int
	Failed   int
}

// 输出列（制表符分隔，首行为以 '#' 开头的表头）：
// src_name src_start src_end name status piece_start piece_end
// dst_name dst_start dst_end strand chain_id score tied error
const header = "#src_name\tsrc_start\tsrc_end\tname\tstatus\tpiece_start\tpiece_end\t" +
	"dst_name\tdst_start\tdst_end\tstrand\tchain_id\tscore\ttied\terror\n"

// Run 从 r 读取 TSV 区间记录（chrom<TAB>start<TAB>end[TAB>name...]，忽略空行
// 与 '#' 注释行），把每条记录的映射结果按输入顺序写入 w。
//
// workers 指定并发度（<1 时按 1 处理）；输出顺序与并发度无关，同一输入在
// 任意 workers 下产生逐字节一致的结果。单条记录解析失败以 error 行输出，
// 不会中断整批处理。内存占用与输入大小无关：在途记录数有界。
func Run(r io.Reader, w io.Writer, ix *liftover.Index, dir liftover.Direction, workers int) (Stats, error) {
	if workers < 1 {
		workers = 1
	}
	tasks := make(chan task, workers*8)
	results := make(chan result, workers*8)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range tasks {
				results <- mapRecord(t)
			}
		}()
	}

	type emitOutcome struct {
		stats Stats
		err   error
	}
	emitDone := make(chan emitOutcome, 1)
	go func() {
		stats, err := emit(w, results)
		emitDone <- emitOutcome{stats, err}
	}()

	seq := 0
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		rec, err := parseRecord(line, seq)
		if err != nil {
			results <- errorResult(seq, rec, err)
		} else {
			tasks <- task{rec: rec, ix: ix, dir: dir}
		}
		seq++
	}
	scanErr := sc.Err()
	close(tasks)
	wg.Wait()
	close(results)
	out := <-emitDone
	if scanErr != nil {
		return out.stats, fmt.Errorf("batch: read input: %w", scanErr)
	}
	if out.err != nil {
		return out.stats, out.err
	}
	return out.stats, nil
}

// record 是一条待映射的输入记录。
type record struct {
	seq   int
	name  string // 第 4 列，缺省为 "."
	query liftover.Interval
}

type task struct {
	rec record
	ix  *liftover.Index
	dir liftover.Direction
}

type result struct {
	seq    int
	rows   string
	status liftover.Status // StatusUnmapped 之外的失败用 failed 标记
	failed bool
}

func parseRecord(line string, seq int) (record, error) {
	rec := record{seq: seq, name: "."}
	fields := strings.Split(line, "\t")
	if len(fields) < 3 {
		return rec, fmt.Errorf("need at least 3 tab-separated fields, got %d", len(fields))
	}
	if fields[0] == "" {
		return rec, fmt.Errorf("empty chromosome name")
	}
	start, err := strconv.Atoi(fields[1])
	if err != nil || start < 0 {
		return rec, fmt.Errorf("invalid start %q", fields[1])
	}
	end, err := strconv.Atoi(fields[2])
	if err != nil || end < 0 {
		return rec, fmt.Errorf("invalid end %q", fields[2])
	}
	if start >= end {
		return rec, fmt.Errorf("start %d must be less than end %d", start, end)
	}
	if len(fields) >= 4 && fields[3] != "" {
		rec.name = fields[3]
	}
	rec.query = liftover.Interval{Name: fields[0], Start: start, End: end}
	return rec, nil
}

func mapRecord(t task) result {
	res := t.ix.Map(t.rec.query, t.dir)
	return result{seq: t.rec.seq, rows: renderRows(t.rec, res), status: res.Status}
}

func errorResult(seq int, rec record, err error) result {
	row := fmt.Sprintf("%s\t%d\t%d\t%s\terror\t.\t.\t.\t.\t.\t.\t.\t.\t.\t%s\n",
		rec.query.Name, rec.query.Start, rec.query.End, rec.name, sanitize(err.Error()))
	return result{seq: seq, rows: row, failed: true}
}

// emit 按 seq 顺序把结果写入 w，并汇总统计。乱序到达的结果先缓存，
// 缓存规模被在途任务数限制，与输入总量无关。
func emit(w io.Writer, results <-chan result) (Stats, error) {
	var stats Stats
	bw := bufio.NewWriter(w)
	if _, err := bw.WriteString(header); err != nil {
		return stats, err
	}
	pending := make(map[int]result)
	next := 0
	flush := func() error {
		for {
			r, ok := pending[next]
			if !ok {
				return nil
			}
			delete(pending, next)
			if _, err := bw.WriteString(r.rows); err != nil {
				return err
			}
			stats.Total++
			switch {
			case r.failed:
				stats.Failed++
			case r.status == liftover.StatusMapped:
				stats.Mapped++
			case r.status == liftover.StatusPartial:
				stats.Partial++
			default:
				stats.Unmapped++
			}
			next++
		}
	}
	for r := range results {
		pending[r.seq] = r
		if err := flush(); err != nil {
			return stats, fmt.Errorf("batch: write output: %w", err)
		}
	}
	if err := bw.Flush(); err != nil {
		return stats, fmt.Errorf("batch: write output: %w", err)
	}
	return stats, nil
}

// renderRows 把一条记录的映射结果渲染为输出行：映射片段与未映射区间
// 按源坐标交错排列，部分映射记录的未映射部分以 unmapped 行显式给出。
func renderRows(rec record, res liftover.Result) string {
	var sb strings.Builder
	status := statusName(res.Status)
	si, ui := 0, 0
	emitSegment := func(s liftover.Segment) {
		tied := "."
		if len(s.Tied) > 0 {
			parts := make([]string, len(s.Tied))
			for i, id := range s.Tied {
				parts[i] = strconv.Itoa(id)
			}
			tied = strings.Join(parts, ",")
		}
		fmt.Fprintf(&sb, "%s\t%d\t%d\t%s\t%s\t%d\t%d\t%s\t%d\t%d\t%c\t%d\t%s\t%s\t.\n",
			rec.query.Name, rec.query.Start, rec.query.End, rec.name, status,
			s.SrcStart, s.SrcEnd, s.Target.Name, s.Target.Start, s.Target.End,
			s.Strand, s.ChainID, strconv.FormatFloat(s.Score, 'g', -1, 64), tied)
	}
	emitUnmapped := func(u liftover.Interval) {
		fmt.Fprintf(&sb, "%s\t%d\t%d\t%s\t%s\t%d\t%d\t.\t.\t.\t.\t.\t.\t.\t.\n",
			rec.query.Name, rec.query.Start, rec.query.End, rec.name, status,
			u.Start, u.End)
	}
	for si < len(res.Segments) || ui < len(res.Unmapped) {
		switch {
		case ui >= len(res.Unmapped):
			emitSegment(res.Segments[si])
			si++
		case si >= len(res.Segments):
			emitUnmapped(res.Unmapped[ui])
			ui++
		case res.Segments[si].SrcStart < res.Unmapped[ui].Start:
			emitSegment(res.Segments[si])
			si++
		default:
			emitUnmapped(res.Unmapped[ui])
			ui++
		}
	}
	if len(res.Segments) == 0 && len(res.Unmapped) == 0 {
		emitUnmapped(rec.query)
	}
	return sb.String()
}

func statusName(s liftover.Status) string {
	switch s {
	case liftover.StatusMapped:
		return "mapped"
	case liftover.StatusPartial:
		return "partial"
	default:
		return "unmapped"
	}
}

func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' {
			return ' '
		}
		return r
	}, s)
}
