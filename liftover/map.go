package liftover

import (
	"sort"

	"github.com/guoyyit/genome-liftover-go/chain"
)

// piece 是一条链的某个块与查询区间的交集片段。
// 坐标均为源/目标染色体的正链坐标；strand 记录比对方向。
type piece struct {
	srcStart  int
	srcEnd    int
	destStart int // srcStart 对应的目标正链坐标
	destName  string
	strand    byte
	chainID   int
	score     float64
}

// mapInterval 是 Map 的内部实现。
//
// 算法：收集所有候选链与查询区间相交的块片段，对查询区间做扫描线切分，
// 每个基本分段内按（分数降序、链 ID 升序）的确定规则选出胜出链，
// 同分的其他链记入 Segment.Tied 作为并列候选；随后把相邻且连续的
// 同链片段合并，剩余未被覆盖的区间作为 Unmapped 显式报告。
func (ix *Index) mapInterval(q Interval, dir Direction) Result {
	res := Result{Query: q, Status: StatusUnmapped}
	if q.Name == "" || q.Start < 0 || q.End <= q.Start {
		return res
	}
	entries := ix.lookup(q.Name, q.Start, q.End, dir)
	var pieces []piece
	for _, e := range entries {
		pieces = appendChainPieces(pieces, e.chain, dir, q.Start, q.End)
	}
	if len(pieces) == 0 {
		res.Unmapped = []Interval{q}
		return res
	}

	cuts := make([]int, 0, 2*len(pieces)+2)
	cuts = append(cuts, q.Start, q.End)
	for _, p := range pieces {
		cuts = append(cuts, p.srcStart, p.srcEnd)
	}
	sort.Ints(cuts)
	cuts = dedupeInts(cuts)

	for i := 0; i+1 < len(cuts); i++ {
		a, b := cuts[i], cuts[i+1]
		best := -1
		var tied []int
		for j := range pieces {
			p := &pieces[j]
			if p.srcStart > a || p.srcEnd < b {
				continue
			}
			if best == -1 || p.score > pieces[best].score {
				best = j
				tied = tied[:0]
				continue
			}
			if p.score == pieces[best].score {
				if p.chainID < pieces[best].chainID {
					tied = appendTied(tied, pieces[best].chainID)
					best = j
				} else {
					tied = appendTied(tied, p.chainID)
				}
			}
		}
		if best == -1 {
			continue
		}
		p := pieces[best]
		seg := Segment{
			Target: Interval{
				Name:  p.destName,
				Start: p.destStart + (a - p.srcStart),
				End:   p.destStart + (b - p.srcStart),
			},
			Strand:   p.strand,
			ChainID:  p.chainID,
			Score:    p.score,
			SrcStart: a,
			SrcEnd:   b,
			Tied:     append([]int(nil), tied...),
		}
		res.Segments = appendSegment(res.Segments, seg)
	}

	pos := q.Start
	for _, seg := range res.Segments {
		if seg.SrcStart > pos {
			res.Unmapped = append(res.Unmapped, Interval{Name: q.Name, Start: pos, End: seg.SrcStart})
		}
		pos = seg.SrcEnd
	}
	if pos < q.End {
		res.Unmapped = append(res.Unmapped, Interval{Name: q.Name, Start: pos, End: q.End})
	}

	switch {
	case len(res.Segments) == 0:
		res.Status = StatusUnmapped
	case len(res.Unmapped) > 0:
		res.Status = StatusPartial
	default:
		res.Status = StatusMapped
	}
	return res
}

// appendChainPieces 把链 c 与查询区间 [qs,qe) 相交的块追加为 piece。
// 反向链的坐标在此翻转为正链坐标，映射方向记录到 strand。
func appendChainPieces(pieces []piece, c *chain.Chain, dir Direction, qs, qe int) []piece {
	for _, b := range c.Blocks {
		var s, e, destStart int
		var destName string
		var strand byte
		if dir == Forward {
			s, e = b.TStart, b.TEnd
			destName = c.QName
			if c.QStrand == '+' {
				destStart = b.QStart
				strand = '+'
			} else {
				destStart = c.QSize - b.QEnd
				strand = '-'
			}
		} else {
			destName = c.TName
			if c.QStrand == '+' {
				s, e = b.QStart, b.QEnd
				destStart = b.TStart
				strand = '+'
			} else {
				s, e = c.QSize-b.QEnd, c.QSize-b.QStart
				destStart = b.TStart
				strand = '-'
			}
		}
		i0, i1 := maxInt(s, qs), minInt(e, qe)
		if i0 >= i1 {
			continue
		}
		pieces = append(pieces, piece{
			srcStart:  i0,
			srcEnd:    i1,
			destStart: destStart + (i0 - s),
			destName:  destName,
			strand:    strand,
			chainID:   c.ID,
			score:     c.Score,
		})
	}
	return pieces
}

// appendSegment 追加片段；若与末位片段同源且源、目标两侧都连续则合并。
func appendSegment(list []Segment, seg Segment) []Segment {
	if n := len(list); n > 0 {
		last := &list[n-1]
		if last.ChainID == seg.ChainID && last.Strand == seg.Strand &&
			last.Target.Name == seg.Target.Name &&
			last.SrcEnd == seg.SrcStart && last.Target.End == seg.Target.Start &&
			equalInts(last.Tied, seg.Tied) {
			last.SrcEnd = seg.SrcEnd
			last.Target.End = seg.Target.End
			return list
		}
	}
	return append(list, seg)
}

func appendTied(tied []int, id int) []int {
	for _, v := range tied {
		if v == id {
			return tied
		}
	}
	return append(tied, id)
}

func dedupeInts(in []int) []int {
	out := in[:0]
	last := -1
	for i, v := range in {
		if i == 0 || v != last {
			out = append(out, v)
			last = v
		}
	}
	return out
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
