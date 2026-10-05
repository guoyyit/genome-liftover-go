package index

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// goldenV1Index is a LIFTIDX1 index file produced by the original release
// (two chains: chr1 plus-strand id=7, chr2 minus-strand id=8). The on-disk
// format is frozen; this blob guards backward compatibility so indexes
// written before the dense-overlap query fix keep loading.
const goldenV1Index = "4c49465449445831010002000000000000006400000000000000040000000000000063687231040000000000000063687241e80300000000000064000000000000002c0100000000000084030000000000003200000000000000fa0000000000000007000000000000002b000000000000002b000000000000000200000000000000640000000000000032000000000000000a0000000000000073000000000000004100000000000000b9000000000000005000000000000000040000000000000063687232040000000000000063687242f40100000000000000000000000000006400000000000000f4010000000000006400000000000000c80000000000000008000000000000002b000000000000002d0000000000000002000000000000000000000000000000640000000000000028000000000000003200000000000000960000000000000032000000000000008fa9e30a5cd4209edbc4d75b6b334ae3f526a6cef827a09c4f4fed2138f7f8f5"

func TestLoadGoldenV1Index(t *testing.T) {
	raw, err := hex.DecodeString(goldenV1Index)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, indexFileName), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	idx, err := Load(dir)
	if err != nil {
		t.Fatalf("legacy v1 index must remain loadable: %v", err)
	}
	if len(idx.Chains()) != 2 {
		t.Fatalf("want 2 chains, got %d", len(idx.Chains()))
	}
	hits := idx.Query("chr1", 110, 120)
	if len(hits) != 1 || hits[0].Chain.ID != 7 {
		t.Fatalf("legacy index target query wrong: %+v", hits)
	}
	// minus-strand chain id=8 flips to genome [300,400) on chrB.
	qhits := idx.QueryQuery("chrB", 300, 320)
	if len(qhits) != 1 || qhits[0].Chain.ID != 8 {
		t.Fatalf("legacy index flipped query wrong: %+v", qhits)
	}
}
