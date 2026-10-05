package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/guoyyit/genome-liftover-go/chain"
	"github.com/guoyyit/genome-liftover-go/index"
	"github.com/guoyyit/genome-liftover-go/internal/testutil"
	"github.com/guoyyit/genome-liftover-go/liftover"
)

// TestDenseCLIEndToEnd generates a dense chain file (thousands of overlapping
// chains on one chromosome), then checks that the CLI produces identical
// batch output whether it reads the chain file or a persisted index, and that
// lift exit codes reflect real mapping outcomes.
func TestDenseCLIEndToEnd(t *testing.T) {
	spec := testutil.Spec{
		TargetChrom: "chr1",
		QueryChrom:  "chrA",
		TargetSize:  1_000_000,
		QuerySize:   2_000_000,
		Chains:      1500,
		MinSpan:     40_000,
		MaxSpan:     160_000,
		MaxBlocks:   6,
		MinusEvery:  3,
		ScoreSteps:  8,
		Seed:        777,
	}
	chains := testutil.DenseChains(spec)

	root := t.TempDir()
	chainPath := filepath.Join(root, "dense.chain")
	f, err := os.Create(chainPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := testutil.WriteChains(f, chains); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	// The generated file must parse back to the same chains.
	parsed, err := chain.ParseFile(chainPath)
	if err != nil {
		t.Fatalf("generated chain file does not parse: %v", err)
	}
	if len(parsed) != len(chains) {
		t.Fatalf("parsed %d chains, wrote %d", len(parsed), len(chains))
	}

	// Build a persisted index from the dense file.
	idxDir := filepath.Join(root, "idx")
	if code := run([]string{"index", "build", "--chain", chainPath, "--out", idxDir}); code != 0 {
		t.Fatalf("index build exit code %d", code)
	}

	// Batch input: intervals across the dense chromosome.
	inPath := filepath.Join(root, "in.tsv")
	var inBytes []byte
	for i := 0; i < 200; i++ {
		s := (i * 4999) % (spec.TargetSize - 20_000)
		inBytes = append(inBytes, []byte(
			fmt.Sprintf("chr1\t%d\t%d\tq%d\n", s, s+10_000, i))...)
	}
	if err := os.WriteFile(inPath, inBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	outChain := filepath.Join(root, "out-chain.tsv")
	outIdx := filepath.Join(root, "out-idx.tsv")
	if code := run([]string{"batch", "--chain", chainPath, "--in", inPath,
		"--out", outChain, "--workers", "4", "--all-candidates"}); code != 0 {
		t.Fatalf("batch from chain file exit code %d", code)
	}
	if code := run([]string{"batch", "--index", idxDir, "--in", inPath,
		"--out", outIdx, "--workers", "4", "--all-candidates"}); code != 0 {
		t.Fatalf("batch from index exit code %d", code)
	}
	b1, err := os.ReadFile(outChain)
	if err != nil {
		t.Fatal(err)
	}
	b2, err := os.ReadFile(outIdx)
	if err != nil {
		t.Fatal(err)
	}
	if string(b1) != string(b2) {
		t.Fatal("batch output from persisted index differs from chain-file output on dense data")
	}

	// Find an interval that maps fully on the dense data and check lift codes.
	m := liftover.NewMapper(index.New(parsed))
	fully := -1
	for s := 0; s < spec.TargetSize-2000 && fully < 0; s += 3000 {
		res, err := m.Map("chr1", s, s+1000, liftover.TargetToQuery)
		if err != nil {
			t.Fatal(err)
		}
		if res.FullyMapped() {
			fully = s
		}
	}
	if fully < 0 {
		t.Fatal("no fully mapped interval found in dense fixture")
	}
	if code := run([]string{"lift", "--index", idxDir, "--chrom", "chr1",
		"--start", strconv.Itoa(fully), "--end", strconv.Itoa(fully + 1000)}); code != 0 {
		t.Fatalf("lift on fully mapped dense interval exit code %d", code)
	}
	if code := run([]string{"lift", "--index", idxDir, "--chrom", "chrZ",
		"--start", "0", "--end", "10"}); code != 1 {
		t.Fatalf("lift on absent chrom must exit 1, got %d", code)
	}
}
