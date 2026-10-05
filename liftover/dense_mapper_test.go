package liftover

import (
	"math/rand"
	"sort"
	"testing"
)

// candidateIDs returns the ordered chain IDs of all candidates.
func candidateIDs(r *Result) []int64 {
	ids := make([]int64, len(r.Candidates))
	for i, c := range r.Candidates {
		ids[i] = c.ChainID
	}
	return ids
}

func findCandidate(r *Result, id int64) (Candidate, bool) {
	for _, c := range r.Candidates {
		if c.ChainID == id {
			return c, true
		}
	}
	return Candidate{}, false
}

// specByID indexes fixture specs.
func specByID(f *denseFixture) map[int64]denseChainSpec {
	m := make(map[int64]denseChainSpec, len(f.specs))
	for _, s := range f.specs {
		m[s.id] = s
	}
	return m
}

// overlapsTarget reports exact 0-based half-open overlap with target span.
func (s denseChainSpec) overlapsTarget(qs, qe int) bool {
	return s.tLo < qe && s.tHi > qs
}

// queryGenomeSpan returns the chain's query span in genome coordinates.
func (s denseChainSpec) queryGenomeSpan(qSize int) (int, int) {
	if s.strand == '+' {
		return s.qLo, s.qHi
	}
	return qSize - s.qHi, qSize - s.qLo
}

// expectedCandidateIDs is the brute-force reference: scan every chain and
// test exact half-open overlap in the queried coordinate space.
func expectedCandidateIDs(f *denseFixture, qs, qe int, dir Direction) []int64 {
	var ids []int64
	for _, s := range f.specs {
		var lo, hi int
		if dir == TargetToQuery {
			lo, hi = s.tLo, s.tHi
		} else {
			lo, hi = s.queryGenomeSpan(f.qSize)
		}
		if lo < qe && hi > qs {
			ids = append(ids, s.id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// TestDenseCandidatesCompleteBothDirections verifies on thousands of heavily
// overlapping chains that every truly overlapping chain is returned as a
// candidate (no missed candidates / false unmapped), in both mapping
// directions, with mixed plus/minus strands, and that candidate order is the
// deterministic score-then-ID order.
func TestDenseCandidatesCompleteBothDirections(t *testing.T) {
	f := genDenseFixture(3000, 100000, 1234)
	m := NewMapper(f.index())

	// Deterministic query set: random intervals plus every chain's head/tail
	// boundaries on both sides.
	type q struct {
		chrom  string
		lo, hi int
	}
	var queries []q
	rng := rand.New(rand.NewSource(4242))
	for i := 0; i < 1500; i++ {
		lo := rng.Intn(99000)
		hi := lo + 1 + rng.Intn(3000)
		queries = append(queries, q{"chrT", lo, hi})
		queries = append(queries, q{"chrQ", lo, hi})
	}
	for i, s := range f.specs {
		if i%5 != 0 {
			continue
		}
		queries = append(queries, q{"chrT", s.tLo, s.tHi})
		queries = append(queries, q{"chrT", s.tHi, s.tHi + 10})
		qlo, qhi := s.queryGenomeSpan(f.qSize)
		queries = append(queries, q{"chrQ", qlo, qhi})
		queries = append(queries, q{"chrQ", qhi, qhi + 10})
	}

	// Run queries twice in shuffled order to prove query-order independence
	// and repeat stability.
	shuffled := make([]q, len(queries))
	copy(shuffled, queries)
	rand.New(rand.NewSource(7)).Shuffle(len(shuffled), func(i, j int) {
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	})
	first := make(map[q][]int64)

	for round := 0; round < 2; round++ {
		list := queries
		if round == 1 {
			list = shuffled
		}
		for _, qq := range list {
			dir := TargetToQuery
			chrom := "chrT"
			if qq.chrom == "chrQ" {
				dir = QueryToTarget
				chrom = "chrQ"
			}
			res, err := m.Map(qq.chrom, qq.lo, qq.hi, dir)
			if err != nil {
				t.Fatalf("map %s [%d,%d): %v", qq.chrom, qq.lo, qq.hi, err)
			}
			got := candidateIDs(res)
			want := expectedCandidateIDs(f, qq.lo, qq.hi, dir)
			if gotSet, wantSet := sortedCopy(got), sortedCopy(want); !equalInt64Slice(gotSet, wantSet) {
				t.Fatalf("%s [%d,%d): candidate set mismatch: got %d want %d",
					chrom, qq.lo, qq.hi, len(gotSet), len(wantSet))
			}
			// Deterministic order: descending score, ties by ascending ID.
			if !candidateOrderValid(res) {
				t.Fatalf("%s [%d,%d): candidate order not score/id sorted", chrom, qq.lo, qq.hi)
			}
			if prev, ok := first[qq]; ok && !equalInt64Slice(prev, got) {
				t.Fatalf("%s [%d,%d): non-deterministic results across runs", chrom, qq.lo, qq.hi)
			} else if !ok {
				first[qq] = got
			}
		}
	}
}

func sortedCopy(in []int64) []int64 {
	out := append([]int64(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func equalInt64Slice(a, b []int64) bool {
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

func candidateOrderValid(r *Result) bool {
	for i := 1; i < len(r.Candidates); i++ {
		a, b := r.Candidates[i-1], r.Candidates[i]
		if a.Score < b.Score {
			return false
		}
		if a.Score == b.Score && a.ChainID > b.ChainID {
			return false
		}
	}
	return true
}

// ival is a half-open interval in the *source* coordinate space of a query.
type ival struct{ lo, hi int }

func unionIntervals(xs []ival) []ival {
	if len(xs) == 0 {
		return nil
	}
	cp := append([]ival(nil), xs...)
	sort.Slice(cp, func(i, j int) bool {
		if cp[i].lo != cp[j].lo {
			return cp[i].lo < cp[j].lo
		}
		return cp[i].hi < cp[j].hi
	})
	var out []ival
	cur := cp[0]
	for _, x := range cp[1:] {
		if x.lo <= cur.hi {
			if x.hi > cur.hi {
				cur.hi = x.hi
			}
			continue
		}
		out = append(out, cur)
		cur = x
	}
	return append(out, cur)
}

// blockSourceIntervals returns the aligned block coverage of one chain in the
// query source coordinate space: target coords for T->Q; q-chain coords for
// Q->T minus (the caller converts the query); genome q coords for plus.
func blockSourceIntervals(s denseChainSpec, dir Direction) []ival {
	out := make([]ival, 0, len(s.blocks))
	for _, b := range s.blocks {
		if dir == TargetToQuery {
			out = append(out, ival{b.TStart, b.TStart + b.Size})
		} else {
			out = append(out, ival{b.QStart, b.QStart + b.Size})
		}
	}
	return out
}

// TestDenseSegmentTilingAndProjection checks, for every candidate of dense
// queries, that (a) segments tile the full source interval exactly,
// (b) mapped source coverage equals the intersection with the chain's aligned
// blocks (so gaps/unaligned regions are reported unmapped, never dropped),
// and (c) mapped destination coordinates match an independent projection.
func TestDenseSegmentTilingAndProjection(t *testing.T) {
	f := genDenseFixture(3000, 100000, 2222)
	m := NewMapper(f.index())
	specs := specByID(f)

	rng := rand.New(rand.NewSource(5151))
	for iter := 0; iter < 1200; iter++ {
		dir := TargetToQuery
		chrom := "chrT"
		if iter%2 == 1 {
			dir = QueryToTarget
			chrom = "chrQ"
		}
		qs := rng.Intn(98000)
		qe := qs + 1 + rng.Intn(2000)

		res, err := m.Map(chrom, qs, qe, dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, cand := range res.Candidates {
			s := specs[cand.ChainID]
			assertSegmentsTile(t, cand, qs, qe)
			assertMappedCoverage(t, f, s, cand, qs, qe, dir)
			assertProjection(t, f, s, cand, qs, qe, dir)
		}
	}
}

// assertSegmentsTile verifies ordered segments partition [qs,qe) with no
// overlap or gap.
func assertSegmentsTile(t *testing.T, cand Candidate, qs, qe int) {
	t.Helper()
	segs := cand.Segments
	if len(segs) == 0 {
		t.Fatalf("chain %d candidate produced no segments", cand.ChainID)
	}
	cursor := qs
	for _, sg := range segs {
		if sg.SourceStart != cursor || sg.SourceEnd <= sg.SourceStart {
			t.Fatalf("chain %d segments do not tile [%d,%d): %+v", cand.ChainID, qs, qe, segs)
		}
		cursor = sg.SourceEnd
	}
	if cursor != qe {
		t.Fatalf("chain %d segments end at %d, want %d", cand.ChainID, cursor, qe)
	}
}

// expectedMappedSourceCoverage intersects the query with aligned blocks,
// expressed in the query's source coordinate space.
func expectedMappedSourceCoverage(f *denseFixture, s denseChainSpec, qs, qe int, dir Direction) []ival {
	var span []ival
	if dir == QueryToTarget && s.strand == '-' {
		// Work in q-chain coordinates; caller converts back for comparison.
		cs, ce := f.qSize-qe, f.qSize-qs
		for _, b := range s.blocks {
			lo, hi := max2(b.QStart, cs), min2(b.QStart+b.Size, ce)
			if lo < hi {
				// back to genome source coords
				span = append(span, ival{f.qSize - hi, f.qSize - lo})
			}
		}
	} else {
		for _, iv := range blockSourceIntervals(s, dir) {
			lo, hi := max2(iv.lo, qs), min2(iv.hi, qe)
			if lo < hi {
				span = append(span, ival{lo, hi})
			}
		}
	}
	return unionIntervals(span)
}

func assertMappedCoverage(t *testing.T, f *denseFixture, s denseChainSpec, cand Candidate, qs, qe int, dir Direction) {
	t.Helper()
	var got []ival
	for _, sg := range cand.Segments {
		if sg.Status == StatusMapped {
			got = append(got, ival{sg.SourceStart, sg.SourceEnd})
		}
	}
	want := expectedMappedSourceCoverage(f, s, qs, qe, dir)
	gotU := unionIntervals(got)
	if len(gotU) != len(want) {
		t.Fatalf("chain %d [%d,%d) dir %d: mapped coverage %v want %v",
			cand.ChainID, qs, qe, dir, gotU, want)
	}
	for i := range want {
		if gotU[i] != want[i] {
			t.Fatalf("chain %d [%d,%d) dir %d: mapped coverage %v want %v",
				cand.ChainID, qs, qe, dir, gotU, want)
		}
	}
}

// assertProjection independently recomputes the mapped coordinates of both
// endpoints of every mapped segment from the chain's blocks and compares.
func assertProjection(t *testing.T, f *denseFixture, s denseChainSpec, cand Candidate, qs, qe int, dir Direction) {
	t.Helper()
	for _, sg := range cand.Segments {
		if sg.Status != StatusMapped {
			continue
		}
		dlo, dhi, ok := project(f, s, sg.SourceStart, sg.SourceEnd, dir)
		if !ok {
			t.Fatalf("chain %d: segment [%d,%d) marked mapped but is outside blocks",
				cand.ChainID, sg.SourceStart, sg.SourceEnd)
		}
		if dlo != sg.DestStart || dhi != sg.DestEnd {
			t.Fatalf("chain %d: projection [%d,%d)->[%d,%d), segment says [%d,%d)",
				cand.ChainID, sg.SourceStart, sg.SourceEnd, dlo, dhi, sg.DestStart, sg.DestEnd)
		}
		if dir == TargetToQuery {
			if sg.DestChrom != "chrQ" {
				t.Fatalf("dest chrom: got %q want chrQ", sg.DestChrom)
			}
			if byte(sg.Strand) != byte(s.strand) {
				t.Fatalf("strand mismatch: %c vs %c", sg.Strand, s.strand)
			}
		} else {
			if sg.DestChrom != "chrT" {
				t.Fatalf("dest chrom: got %q want chrT", sg.DestChrom)
			}
		}
	}
}

// project maps a fully-aligned source interval [lo,hi) via one chain and
// returns destination coordinates. The interval may span source-adjacent
// blocks (merged across a destination-only gap); the result is the envelope
// of the per-block projections.
func project(f *denseFixture, s denseChainSpec, lo, hi int, dir Direction) (int, int, bool) {
	if dir == TargetToQuery {
		dMin, dMax := int(1<<62), 0
		covered := false
		for _, b := range s.blocks {
			tbLo, tbHi := b.TStart, b.TStart+b.Size
			ovLo, ovHi := max2(tbLo, lo), min2(tbHi, hi)
			if ovLo >= ovHi {
				continue
			}
			off := ovLo - tbLo
			length := ovHi - ovLo
			var a, c int
			if s.strand == '+' {
				a = b.QStart + off
				c = a + length
			} else {
				// minus: per-block chain coords flipped to genome coords.
				a = f.qSize - (b.QStart + off + length)
				c = f.qSize - (b.QStart + off)
			}
			if a < dMin {
				dMin = a
			}
			if c > dMax {
				dMax = c
			}
			covered = true
		}
		return dMin, dMax, covered
	}
	// QueryToTarget.
	cLo, cHi := lo, hi
	if s.strand == '-' {
		cLo, cHi = f.qSize-hi, f.qSize-lo
	}
	dMin, dMax := int(1<<62), 0
	covered := false
	for _, b := range s.blocks {
		qbLo, qbHi := b.QStart, b.QStart+b.Size
		ovLo, ovHi := max2(qbLo, cLo), min2(qbHi, cHi)
		if ovLo >= ovHi {
			continue
		}
		a := b.TStart + (ovLo - qbLo)
		c := b.TStart + (ovLo - qbLo) + (ovHi - ovLo)
		if a < dMin {
			dMin = a
		}
		if c > dMax {
			dMax = c
		}
		covered = true
	}
	return dMin, dMax, covered
}

func max2(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func min2(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestDenseRoundTripInsideBlocks maps an interval fully contained in one
// alignment block forward and backward through the SAME chain (plus and
// minus), on the dense fixture, and asserts the original interval is
// restored.
func TestDenseRoundTripInsideBlocks(t *testing.T) {
	f := genDenseFixture(3000, 100000, 31337)
	m := NewMapper(f.index())
	rng := rand.New(rand.NewSource(909))

	sampled := 0
	for si, s := range f.specs {
		if si%3 != 0 {
			continue
		}
		for _, b := range s.blocks {
			if b.Size < 8 {
				continue
			}
			margin := 1 + rng.Intn(3)
			tLo := b.TStart + margin
			tHi := b.TStart + b.Size - margin
			if tHi <= tLo {
				continue
			}
			fwd, err := m.Map("chrT", tLo, tHi, TargetToQuery)
			if err != nil {
				t.Fatal(err)
			}
			fc, ok := findCandidate(fwd, s.id)
			if !ok {
				t.Fatalf("chain %d missing from forward candidates", s.id)
			}
			fMapped := mappedOnly(fc)
			if len(fMapped) != 1 || fMapped[0].lo != tLo || fMapped[0].hi != tHi {
				t.Fatalf("chain %d: interior interval not one mapped segment: %+v", s.id, fc.Segments)
			}
			// Map back starting from THIS chain's own destination; the
			// destination is the block's q interval (flipped to genome
			// coordinates for minus chains), not the target coordinates.
			dlo, dhi := fMapped[0].dstLo, fMapped[0].dstHi
			back, err := m.Map("chrQ", dlo, dhi, QueryToTarget)
			if err != nil {
				t.Fatal(err)
			}
			bc, ok := findCandidate(back, s.id)
			if !ok {
				t.Fatalf("chain %d missing from backward candidates", s.id)
			}
			bMapped := mappedOnly(bc)
			if len(bMapped) != 1 || bMapped[0].dstLo != tLo || bMapped[0].dstHi != tHi {
				t.Fatalf("chain %d (%c) round trip [%d,%d)->[%d,%d): got %+v want target [%d,%d)",
					s.id, s.strand, tLo, tHi, dlo, dhi, bc.Segments, tLo, tHi)
			}
			sampled++
		}
	}
	if sampled < 1000 {
		t.Fatalf("round-trip sample too small: %d", sampled)
	}
}

type dstInterval struct{ lo, hi, dstLo, dstHi int }

func mappedOnly(c Candidate) []dstInterval {
	var out []dstInterval
	for _, sg := range c.Segments {
		if sg.Status == StatusMapped {
			out = append(out, dstInterval{sg.SourceStart, sg.SourceEnd, sg.DestStart, sg.DestEnd})
		}
	}
	return out
}
