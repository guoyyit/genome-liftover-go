package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCLIEndToEnd(t *testing.T) {
	root := t.TempDir()
	chainPath := filepath.Join(root, "demo.chain")
	if err := os.WriteFile(chainPath, []byte(`chain 100 chr1 1000 + 0 100 chrA 1000 + 0 100 1
100
`), 0o644); err != nil {
		t.Fatal(err)
	}

	if code := run([]string{"lift", "--chain", chainPath, "--chrom", "chr1", "--start", "10", "--end", "20"}); code != 0 {
		t.Fatalf("lift exit code %d", code)
	}
	if code := run([]string{"lift", "--chain", chainPath, "--chrom", "chrX", "--start", "0", "--end", "10"}); code != 1 {
		t.Fatalf("unmapped lift must exit 1, got %d", code)
	}

	inPath := filepath.Join(root, "in.tsv")
	if err := os.WriteFile(inPath, []byte("chr1\t0\t50\ta\nchr1\t50\t100\tb\nbad\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"batch", "--chain", chainPath, "--in", inPath, "--workers", "4"}); code != 0 {
		t.Fatalf("batch exit code %d", code)
	}

	idxDir := filepath.Join(root, "idx")
	if code := run([]string{"index", "build", "--chain", chainPath, "--out", idxDir}); code != 0 {
		t.Fatalf("index build exit code %d", code)
	}
	if code := run([]string{"lift", "--index", idxDir, "--chrom", "chr1", "--start", "0", "--end", "10"}); code != 0 {
		t.Fatalf("lift from index exit code %d", code)
	}
	if code := run([]string{}); code != 2 {
		t.Fatalf("usage exit code %d, want 2", code)
	}
}
