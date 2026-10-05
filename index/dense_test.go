package index

import (
	"math/rand"
	"testing"

	"github.com/guoyyit/genome-liftover-go/chain"
	"github.com/guoyyit/genome-liftover-go/internal/testutil"
)

// denseSpec builds thousands of heavily overlapping chains on one chromosome.
func denseSpec() testutil.Spec {
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

func denseChains(t *testing.T, spec testutil.Spec) []*chain.Chain {
	t.Helper()
	chains := testutil.DenseChains(spec)
	for _, c := range chains {
		if err := chain.Validate(c); err != nil {
			t.Fatalf("fixture chain id=%d invalid: %v", c.ID, err)
		}
	}
	return chains
}

func hitIDs(hits []ChainHit) []int64 {
	ids := make([]int64, len(hits))
	for i, h := range hits {
		ids[i] = h.Chain.ID
	}
	return ids
}

func equalIDs(a, b []int64) bool {
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

// TestDenseOverlapMatchesBruteForce cross-validates both query sides against
// exact per-chain comparison on thousands of mutually overlapping chains.
func TestDenseOverlapMatchesBruteForce(t *testing.T) {
	spec := denseSpec()
	chains := denseChains(t, spec)
	idx := New(chains)
	rng := rand.New(rand.NewSource(7))

	check := func(querySide bool, chrom string, start, end int) {
		t.Helper()
		want := testutil.Overlap(chains, querySide, chrom, start, end)
		var got []int64
		if querySide {
			got = hitIDs(idx.QueryQuery(chrom, start, end))
		} else {
			got = hitIDs(idx.Query(chrom, start, end))
		}
		if !equalIDs(got, want) {
			t.Fatalf("querySide=%v %s:[%d,%d): got %d hits, want %d\ngot  first IDs: %v\nwant first IDs: %v",
				querySide, chrom, start, end, len(got), len(want), firstN(got, 10), firstN(want, 10))
		}
	}

	// Random intervals over the whole chromosome, both sides.
	for i := 0; i < 400; i++ {
		start := rng.Intn(spec.TargetSize - 1)
		end := start + 1 + rng.Intn(50_000)
		if end > spec.TargetSize {
			end = spec.TargetSize
		}
		check(false, spec.TargetChrom, start, end)
	}
	for i := 0; i < 400; i++ {
		start := rng.Intn(spec.QuerySize - 1)
		end := start + 1 + rng.Intn(50_000)
		if end > spec.QuerySize {
			end = spec.QuerySize
		}
		check(true, spec.QueryChrom, start, end)
	}

	// Boundary queries derived from real chain spans: exact head, exact tail,
	// touching-but-not-overlapping neighbours, and full-span containment.
	for _, c := range chains[:200] {
		for _, side := range []bool{false, true} {
			chrom, lo, hi := testutil.Span(c, side)
			check(side, chrom, lo, lo+1)   // head base
			check(side, chrom, hi-1, hi)   // tail base
			check(side, chrom, hi, hi+1)   // touching end: no overlap
			check(side, chrom, lo-1, lo)   // touching start: no overlap
			check(side, chrom, lo, hi)     // exact span
			check(side, chrom, lo-1, hi+1) // spanning across the chain
		}
	}
}

func firstN(ids []int64, n int) []int64 {
	if len(ids) > n {
		return ids[:n]
	}
	return ids
}
