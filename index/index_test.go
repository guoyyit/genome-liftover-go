package index

import (
	"strings"
	"testing"

	"github.com/guoyyit/genome-liftover-go/chain"
)

func TestQuerySides(t *testing.T) {
	input := `chain 1 chr1 1000 + 0 100 chrA 900 + 0 100 1
100
chain 2 chr1 1000 + 50 150 chrA 900 - 100 200 2
100
chain 3 chr1 1000 + 300 400 chrB 900 + 0 100 3
100
`
	chains, err := chain.ParseReader(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	idx := New(chains)

	hits := idx.Query("chr1", 40, 60)
	if len(hits) != 2 {
		t.Fatalf("want 2 overlapping target chains, got %d", len(hits))
	}
	if hits[0].Chain.ID != 1 || hits[1].Chain.ID != 2 {
		t.Fatalf("overlap order wrong: %d %d", hits[0].Chain.ID, hits[1].Chain.ID)
	}

	if hits := idx.Query("chr1", 200, 300); len(hits) != 0 {
		t.Fatalf("want no overlap for gap interval, got %d", len(hits))
	}

	// Chain 2 is minus-strand: q-chain [100,200) on size 500 flips to the
	// genome interval [700,800). Chain 1 (plus) stays at [0,100).
	qHits := idx.QueryQuery("chrA", 0, 50)
	if len(qHits) != 1 || qHits[0].Chain.ID != 1 {
		t.Fatalf("query-side overlap wrong: %+v", qHits)
	}
	qHits = idx.QueryQuery("chrA", 750, 760)
	if len(qHits) != 1 || qHits[0].Chain.ID != 2 {
		t.Fatalf("want flipped minus chain near boundary, got %+v", qHits)
	}

	if len(idx.Chains()) != 3 {
		t.Fatal("chains slice mismatch")
	}
}
