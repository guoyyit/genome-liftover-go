package liftover

import (
	"math/rand"
	"sort"
	"testing"

	"github.com/guoyyit/genome-liftover-go/chain"
	"github.com/guoyyit/genome-liftover-go/index"
	"github.com/guoyyit/genome-liftover-go/internal/testutil"
)

func denseMapperSpec() testutil.Spec {
	return testutil.Spec{
		TargetChrom: "chr1",
		QueryChrom:  "chrA",
		TargetSize:  1_000_000,
		QuerySize:   2_000_000,
		Chains:      3000,
		MinSpan:     40_000,
		MaxSpan:     160_000,
		MaxBlocks:   6,
		MinusEvery:  3,
		ScoreSteps:  8,
		Seed:        20261006,
	}
}

func buildDenseMapper(t *testing.T) (*Mapper, []*chain.Chain, testutil.Spec) {
	t.Helper()
	spec := denseMapperSpec()
	chains := testutil.DenseChains(spec)
	for _, c := range chains {
		if err := chain.Validate(c); err != nil {
			t.Fatalf("fixture chain id=%d invalid: %v", c.ID, err)
		}
	}
	return NewMapper(index.New(chains)), chains, spec
}

func candidateIDs(res *Result) []int64 {
	ids := make([]int64, len(res.Candidates))
	for i, c := range res.Candidates {
		ids[i] = c.ChainID
	}
	return ids
}

func sortedCopy(ids []int64) []int64 {
	cp := append([]int64(nil), ids...)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	return cp
}

func sameIDSet(a, b []int64) bool {
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

func findCandidate(res *Result, id int64) *Candidate {
	for i := range res.Candidates {
		if res.Candidates[i].ChainID == id {
			return &res.Candidates[i]
		}
	}
	return nil
}

// TestDenseCandidateCompleteness verifies, on dense overlapping data, that the
// mapper returns exactly the brute-force candidate set in both directions and
// never reports an overlapping interval as unmapped.
func TestDenseCandidateCompleteness(t *testing.T) {
	m, chains, spec := buildDenseMapper(t)
	rng := rand.New(rand.NewSource(11))

	check := func(dir Direction, querySide bool, chrom string, start, end int) {
		t.Helper()
		res, err := m.Map(chrom, start, end, dir)
		if err != nil {
			t.Fatalf("map %s:[%d,%d): %v", chrom, start, end, err)
		}
		want := testutil.Overlap(chains, querySide, chrom, start, end)
		got := candidateIDs(res)
		if !sameIDSet(sortedCopy(got), sortedCopy(want)) {
			t.Fatalf("dir=%v %s:[%d,%d): got %d candidates, want %d",
				dir, chrom, start, end, len(got), len(want))
		}
		if len(want) > 0 && len(res.Candidates) == 0 {
			t.Fatalf("dir=%v %s:[%d,%d): overlapping interval reported unmapped", dir, chrom, start, end)
		}
		// Candidate order must be deterministic: score desc, chain ID asc.
		for i := 1; i < len(res.Candidates); i++ {
			a, b := res.Candidates[i-1], res.Candidates[i]
			if a.Score < b.Score || (a.Score == b.Score && a.ChainID > b.ChainID) {
				t.Fatalf("candidate order not deterministic at %d: %+v then %+v", i, a, b)
			}
		}
	}

	for i := 0; i < 300; i++ {
		start := rng.Intn(spec.TargetSize - 1)
		end := start + 1 + rng.Intn(40_000)
		if end > spec.TargetSize {
			end = spec.TargetSize
		}
		check(TargetToQuery, false, spec.TargetChrom, start, end)
	}
	for i := 0; i < 300; i++ {
		start := rng.Intn(spec.QuerySize - 1)
		end := start + 1 + rng.Intn(40_000)
		if end > spec.QuerySize {
			end = spec.QuerySize
		}
		check(QueryToTarget, true, spec.QueryChrom, start, end)
	}
	// Boundary queries at real chain edges, both directions.
	for _, c := range chains[:150] {
		tChrom, tLo, tHi := testutil.Span(c, false)
		check(TargetToQuery, false, tChrom, tLo, tLo+1)
		check(TargetToQuery, false, tChrom, tHi-1, tHi)
		check(TargetToQuery, false, tChrom, tHi, tHi+1)
		qChrom, qLo, qHi := testutil.Span(c, true)
		check(QueryToTarget, true, qChrom, qLo, qLo+1)
		check(QueryToTarget, true, qChrom, qHi-1, qHi)
		check(QueryToTarget, true, qChrom, qHi, qHi+1)
	}
}

// TestDenseRoundTrip maps intervals fully inside one alignment block through
// the same chain there and back on dense data; the original interval must be
// restored for both strands.
func TestDenseRoundTrip(t *testing.T) {
	m, chains, spec := buildDenseMapper(t)
	plus, minus := 0, 0
	for _, c := range chains {
		if c.QueryStrand == chain.StrandMinus {
			if minus >= 150 {
				continue
			}
			minus++
		} else {
			if plus >= 150 {
				continue
			}
			plus++
		}
		for _, b := range c.Blocks {
			srcStart, srcEnd := b.TStart, b.TStart+b.Size
			fwd, err := m.Map(spec.TargetChrom, srcStart, srcEnd, TargetToQuery)
			if err != nil {
				t.Fatal(err)
			}
			fc := findCandidate(fwd, c.ID)
			if fc == nil {
				t.Fatalf("chain %d missing from forward candidates for [%d,%d)", c.ID, srcStart, srcEnd)
			}
			if len(fc.Segments) != 1 || fc.Segments[0].Status != StatusMapped {
				t.Fatalf("chain %d block should map as one segment, got %+v", c.ID, fc.Segments)
			}
			d := fc.Segments[0]
			back, err := m.Map(d.DestChrom, d.DestStart, d.DestEnd, QueryToTarget)
			if err != nil {
				t.Fatal(err)
			}
			bc := findCandidate(back, c.ID)
			if bc == nil {
				t.Fatalf("chain %d missing from reverse candidates for %s:[%d,%d)",
					c.ID, d.DestChrom, d.DestStart, d.DestEnd)
			}
			if len(bc.Segments) != 1 || bc.Segments[0].Status != StatusMapped {
				t.Fatalf("chain %d reverse should be one mapped segment, got %+v", c.ID, bc.Segments)
			}
			rb := bc.Segments[0]
			if rb.DestChrom != spec.TargetChrom || rb.DestStart != srcStart || rb.DestEnd != srcEnd {
				t.Fatalf("chain %d round trip [%d,%d) -> [%d,%d) -> [%d,%d)",
					c.ID, srcStart, srcEnd, d.DestStart, d.DestEnd, rb.DestStart, rb.DestEnd)
			}
		}
	}
	if plus == 0 || minus == 0 {
		t.Fatalf("fixture must contain both strands, got plus=%d minus=%d", plus, minus)
	}
}

// TestDenseGapSplit checks that intervals crossing a chain's target gap are
// split into mapped/unmapped pieces on dense data, for the chain's candidate.
func TestDenseGapSplit(t *testing.T) {
	m, chains, spec := buildDenseMapper(t)
	tested := 0
	for _, c := range chains {
		if len(c.Blocks) < 2 {
			continue
		}
		b1, b2 := c.Blocks[0], c.Blocks[1]
		gapStart := b1.TStart + b1.Size
		gapEnd := b2.TStart
		if gapEnd <= gapStart {
			continue // no target gap between these blocks
		}
		start, end := gapStart-10, gapEnd+10
		res, err := m.Map(spec.TargetChrom, start, end, TargetToQuery)
		if err != nil {
			t.Fatal(err)
		}
		cand := findCandidate(res, c.ID)
		if cand == nil {
			t.Fatalf("chain %d missing for gap-spanning interval", c.ID)
		}
		var sawMapped, sawUnmapped bool
		covered := 0
		for _, s := range cand.Segments {
			covered += s.SourceEnd - s.SourceStart
			if s.Status == StatusMapped {
				sawMapped = true
			} else {
				sawUnmapped = true
				if s.SourceStart != gapStart || s.SourceEnd != gapEnd {
					t.Fatalf("chain %d unmapped piece [%d,%d), want the gap [%d,%d)",
						c.ID, s.SourceStart, s.SourceEnd, gapStart, gapEnd)
				}
			}
		}
		if !sawMapped || !sawUnmapped {
			t.Fatalf("chain %d gap interval must split into mapped+unmapped: %+v", c.ID, cand.Segments)
		}
		if covered != end-start {
			t.Fatalf("chain %d segments cover %d bases, want %d (nothing dropped, nothing double-counted)",
				c.ID, covered, end-start)
		}
		if res.FullyMapped() && findBestID(res) == c.ID {
			t.Fatalf("chain %d crosses a gap and cannot be fully mapped", c.ID)
		}
		tested++
		if tested >= 100 {
			break
		}
	}
	if tested == 0 {
		t.Fatal("fixture produced no chain with a target gap")
	}
}

func findBestID(res *Result) int64 {
	best, ok := res.BestCandidate()
	if !ok {
		return -1
	}
	return best.ChainID
}

// TestDenseDeterminism repeats queries and rebuilds the index from shuffled
// input; candidate sequences must be identical.
func TestDenseDeterminism(t *testing.T) {
	m, chains, spec := buildDenseMapper(t)
	rng := rand.New(rand.NewSource(3))
	queries := make([][2]int, 50)
	for i := range queries {
		s := rng.Intn(spec.TargetSize - 10_000)
		queries[i] = [2]int{s, s + 5000}
	}
	snapshot := make([][]int64, len(queries))
	for i, q := range queries {
		res, err := m.Map(spec.TargetChrom, q[0], q[1], TargetToQuery)
		if err != nil {
			t.Fatal(err)
		}
		snapshot[i] = candidateIDs(res)
	}

	// Rebuild from a differently shuffled input order.
	shuffled := append([]*chain.Chain(nil), chains...)
	rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
	m2 := NewMapper(index.New(shuffled))
	for i, q := range queries {
		res, err := m2.Map(spec.TargetChrom, q[0], q[1], TargetToQuery)
		if err != nil {
			t.Fatal(err)
		}
		got := candidateIDs(res)
		if !orderedSame(got, snapshot[i]) {
			t.Fatalf("query %v: candidate sequence differs across rebuilds", q)
		}
	}
}

func orderedSame(a, b []int64) bool {
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
