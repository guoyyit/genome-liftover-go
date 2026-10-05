package index

import (
	"bytes"
	"math/rand"
	"path/filepath"
	"testing"

	"github.com/guoyyit/genome-liftover-go/internal/testutil"
)

// TestDenseSaveLoadIdentical verifies that a dense index (thousands of
// overlapping chains on one chromosome) behaves exactly the same after a
// save/load round trip: identical serialized bytes and identical hit lists
// for random and boundary queries on both query sides.
func TestDenseSaveLoadIdentical(t *testing.T) {
	spec := testutil.Spec{
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
		Seed:        4242,
	}
	chains := denseChains(t, spec)
	idx := New(chains)

	dir := filepath.Join(t.TempDir(), "idx")
	if err := idx.Save(dir); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}

	var a, b bytes.Buffer
	if err := idx.writeTo(&a); err != nil {
		t.Fatal(err)
	}
	if err := loaded.writeTo(&b); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Fatal("dense index serialized bytes differ after reload")
	}

	rng := rand.New(rand.NewSource(13))
	compare := func(querySide bool, chrom string, start, end int) {
		t.Helper()
		var got, want []ChainHit
		if querySide {
			got, want = loaded.QueryQuery(chrom, start, end), idx.QueryQuery(chrom, start, end)
		} else {
			got, want = loaded.Query(chrom, start, end), idx.Query(chrom, start, end)
		}
		if !equalIDs(hitIDs(got), hitIDs(want)) {
			t.Fatalf("querySide=%v %s:[%d,%d): reloaded index hits differ (%d vs %d)",
				querySide, chrom, start, end, len(got), len(want))
		}
	}
	for i := 0; i < 300; i++ {
		s := rng.Intn(spec.TargetSize - 1)
		e := s + 1 + rng.Intn(50_000)
		if e > spec.TargetSize {
			e = spec.TargetSize
		}
		compare(false, spec.TargetChrom, s, e)
	}
	for i := 0; i < 300; i++ {
		s := rng.Intn(spec.QuerySize - 1)
		e := s + 1 + rng.Intn(50_000)
		if e > spec.QuerySize {
			e = spec.QuerySize
		}
		compare(true, spec.QueryChrom, s, e)
	}
	for _, c := range chains[:100] {
		tChrom, tLo, tHi := testutil.Span(c, false)
		compare(false, tChrom, tLo, tLo+1)
		compare(false, tChrom, tHi-1, tHi)
		qChrom, qLo, qHi := testutil.Span(c, true)
		compare(true, qChrom, qLo, qLo+1)
		compare(true, qChrom, qHi-1, qHi)
	}
}
