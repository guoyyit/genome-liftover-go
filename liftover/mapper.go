package liftover

import (
	"errors"
	"sort"

	"github.com/guoyyit/genome-liftover-go/chain"
	"github.com/guoyyit/genome-liftover-go/index"
)

// Direction is the mapping direction between reference versions.
type Direction int

const (
	TargetToQuery Direction = iota + 1
	QueryToTarget
)

// SegmentStatus marks how a query position was resolved.
type SegmentStatus int

const (
	StatusMapped SegmentStatus = iota + 1
	StatusUnmapped
)

// Segment is one ordered piece of a mapping result.
type Segment struct {
	SourceStart int
	SourceEnd   int
	DestStart   int
	DestEnd     int
	DestChrom   string
	Strand      chain.Strand
	Status      SegmentStatus
}

// Candidate is one candidate mapping for an ambiguous interval.
type Candidate struct {
	ChainID  int64
	Score    int64
	Segments []Segment
}

// Result is the mapping outcome for one source interval.
type Result struct {
	SourceChrom string
	Candidates  []Candidate
}

// Mapper maps intervals in either direction over a chain index.
type Mapper struct {
	idx *index.IntervalIndex
}

// ErrInvalidInterval is returned for malformed query coordinates.
var ErrInvalidInterval = errors.New("invalid interval: require 0 <= start < end")

// NewMapper creates a Mapper backed by the given index.
func NewMapper(idx *index.IntervalIndex) *Mapper { return &Mapper{idx: idx} }

// BestCandidate returns the deterministically chosen candidate (highest score,
// ties broken by lower chain ID). It returns false when no candidate exists.
func (r *Result) BestCandidate() (Candidate, bool) {
	if len(r.Candidates) == 0 {
		return Candidate{}, false
	}
	best := 0
	for i := 1; i < len(r.Candidates); i++ {
		if r.Candidates[i].Score > r.Candidates[best].Score ||
			(r.Candidates[i].Score == r.Candidates[best].Score &&
				r.Candidates[i].ChainID < r.Candidates[best].ChainID) {
			best = i
		}
	}
	return r.Candidates[best], true
}

// FullyMapped reports whether the chosen candidate covers the whole source
// interval with mapped segments.
func (r *Result) FullyMapped() bool {
	best, ok := r.BestCandidate()
	return ok && len(best.Segments) > 0 && allMapped(best.Segments)
}

func allMapped(segs []Segment) bool {
	for _, s := range segs {
		if s.Status != StatusMapped {
			return false
		}
	}
	return true
}

// Map maps one interval in the requested direction.
func (m *Mapper) Map(chrom string, start, end int, dir Direction) (*Result, error) {
	if start < 0 || end <= start {
		return nil, ErrInvalidInterval
	}
	var hits []index.ChainHit
	switch dir {
	case TargetToQuery:
		hits = m.idx.Query(chrom, start, end)
	case QueryToTarget:
		hits = m.idx.QueryQuery(chrom, start, end)
	default:
		return nil, errors.New("unknown mapping direction")
	}

	result := &Result{SourceChrom: chrom}
	for _, hit := range hits {
		segs := mapByChain(hit.Chain, start, end, dir)
		result.Candidates = append(result.Candidates, Candidate{
			ChainID:  hit.Chain.ID,
			Score:    hit.Chain.Score,
			Segments: segs,
		})
	}
	sort.SliceStable(result.Candidates, func(i, j int) bool {
		ci, cj := result.Candidates[i], result.Candidates[j]
		if ci.Score != cj.Score {
			return ci.Score > cj.Score
		}
		return ci.ChainID < cj.ChainID
	})
	return result, nil
}

func mapByChain(c *chain.Chain, qStart, qEnd int, dir Direction) []Segment {
	minus := c.QueryStrand == chain.StrandMinus
	if dir == QueryToTarget && minus {
		return mapReverseMinus(c, qStart, qEnd)
	}

	type sBlock struct{ srcLo, srcHi, dstLo, dstHi int }
	blocks := make([]sBlock, 0, len(c.Blocks))
	for _, b := range c.Blocks {
		sLo, sHi := b.TStart, b.TStart+b.Size
		dLo, dHi := b.QStart, b.QStart+b.Size
		if dir == QueryToTarget {
			sLo, sHi = b.QStart, b.QStart+b.Size
			dLo, dHi = b.TStart, b.TStart+b.Size
		} else if minus {
			// Reverse complement flip of the destination coordinates.
			dLo, dHi = c.QuerySize-b.QStart-b.Size, c.QuerySize-b.QStart
		}
		if sHi <= qStart || sLo >= qEnd {
			continue
		}
		lo := sLo
		if qStart > lo {
			lo = qStart
		}
		hi := sHi
		if qEnd < hi {
			hi = qEnd
		}
		off := lo - sLo
		dstLo := dLo + off
		dstHi := dLo + off + (hi - lo)
		if minus && dir == TargetToQuery {
			dstLo = dHi - (hi - sLo)
			dstHi = dstLo + (hi - lo)
		}
		blocks = append(blocks, sBlock{srcLo: lo, srcHi: hi, dstLo: dstLo, dstHi: dstHi})
	}

	destChrom := c.QueryName
	if dir == QueryToTarget {
		destChrom = c.TargetName
	}
	var segs []Segment
	cursor := qStart
	emitUnmapped := func(to int) {
		if to > cursor {
			segs = append(segs, Segment{
				SourceStart: cursor, SourceEnd: to,
				Strand: strandFor(c, dir), Status: StatusUnmapped,
			})
		}
	}
	for i := 0; i < len(blocks); {
		if cursor < blocks[i].srcLo {
			emitUnmapped(blocks[i].srcLo)
			cursor = blocks[i].srcLo
		}
		// Merge blocks continuous in source coordinates into one segment.
		j := i
		mergeOK := !(minus && dir == TargetToQuery)
		for mergeOK && j+1 < len(blocks) && blocks[j+1].srcLo == blocks[j].srcHi {
			j++
		}
		segs = append(segs, Segment{
			SourceStart: blocks[i].srcLo,
			SourceEnd:   blocks[j].srcHi,
			DestStart:   blocks[i].dstLo,
			DestEnd:     blocks[j].dstHi,
			DestChrom:   destChrom,
			Strand:      strandFor(c, dir),
			Status:      StatusMapped,
		})
		cursor = blocks[j].srcHi
		i = j + 1
	}
	emitUnmapped(qEnd)
	return segs
}

// mapReverseMinus maps a genome interval on a minus-strand query back to
// target coordinates. Genome coordinates are first converted to query-chain
// coordinates, segments are built in chain order and then reversed so the
// output stays ordered by ascending source coordinates.
func mapReverseMinus(c *chain.Chain, gStart, gEnd int) []Segment {
	sLo := c.QuerySize - gEnd
	sHi := c.QuerySize - gStart

	type piece struct {
		sourceLo, sourceHi int
		mapped             bool
		dLo, dHi           int
	}
	var pieces []piece
	cursor := sLo
	flushUnmapped := func(to int) {
		if to > cursor {
			pieces = append(pieces, piece{sourceLo: cursor, sourceHi: to})
		}
		cursor = to
	}
	for _, b := range c.Blocks {
		bLo, bHi := b.QStart, b.QStart+b.Size
		ovLo := sLo
		if bLo > ovLo {
			ovLo = bLo
		}
		ovHi := sHi
		if bHi < ovHi {
			ovHi = bHi
		}
		if ovLo >= ovHi {
			continue
		}
		if cursor < bLo {
			flushUnmapped(bLo)
		}
		if cursor > ovLo {
			ovLo = cursor
		}
		if ovLo >= ovHi {
			continue
		}
		tLo := b.TStart + (ovLo - bLo)
		pieces = append(pieces, piece{
			sourceLo: ovLo,
			sourceHi: ovHi,
			mapped:   true,
			dLo:      tLo,
			dHi:      tLo + (ovHi - ovLo),
		})
		cursor = ovHi
	}
	flushUnmapped(sHi)

	segs := make([]Segment, 0, len(pieces))
	i := len(pieces) - 1
	for i >= 0 {
		j := i
		// Merge mapped pieces adjacent in q-chain order; they are also
		// contiguous in genome order with contiguous target destinations.
		for j > 0 && pieces[j].mapped && pieces[j-1].mapped &&
			pieces[j-1].sourceLo == pieces[j].sourceHi {
			j--
		}
		first, last := pieces[j], pieces[i]
		s := Segment{
			SourceStart: c.QuerySize - last.sourceHi,
			SourceEnd:   c.QuerySize - first.sourceLo,
			Strand:      chain.StrandMinus,
			Status:      StatusUnmapped,
		}
		if first.mapped {
			s.Status = StatusMapped
			s.DestStart = first.dLo
			s.DestEnd = last.dHi
			s.DestChrom = c.TargetName
		}
		segs = append(segs, s)
		i = j - 1
	}
	return segs
}

func strandFor(c *chain.Chain, dir Direction) chain.Strand {
	if dir == QueryToTarget {
		if c.QueryStrand == chain.StrandMinus {
			return chain.StrandMinus
		}
		return chain.StrandPlus
	}
	return c.QueryStrand
}
