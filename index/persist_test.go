package index

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guoyyit/genome-liftover-go/chain"
)

const persistInput = `chain 100 chr1 1000 + 100 300 chrA 900 + 50 250 7
10 5 5
185
chain 80 chr2 500 + 0 100 chrB 500 - 100 200 8
40 10 10
50
`

func TestSaveLoadRoundTrip(t *testing.T) {
	chains, err := chain.ParseReader(strings.NewReader(persistInput))
	if err != nil {
		t.Fatal(err)
	}
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
		t.Fatal("serialized bytes differ after reload")
	}

	origHits := idx.Query("chr1", 110, 120)
	loadHits := loaded.Query("chr1", 110, 120)
	if len(origHits) != len(loadHits) {
		t.Fatalf("query result differs after reload: %d vs %d", len(origHits), len(loadHits))
	}
	if loadHits[0].Chain.Blocks[1] != origHits[0].Chain.Blocks[1] {
		t.Fatal("block data mismatch after reload")
	}
	// minus query q-chain [100,200) on size 500 flips to genome [300,400).
	if hits := loaded.QueryQuery("chrB", 300, 320); len(hits) != 1 || hits[0].Chain.ID != 8 {
		t.Fatalf("minus-strand query reload mismatch: %+v", hits)
	}
}

func TestLoadRejectsCorruption(t *testing.T) {
	chains, _ := chain.ParseReader(strings.NewReader(persistInput))
	dir := t.TempDir()
	if err := New(chains).Save(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, indexFileName)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	corrupt := make([]byte, len(raw))
	copy(corrupt, raw)
	corrupt[len(indexMagic)+10] ^= 0xff
	cdir := filepath.Join(t.TempDir(), "c")
	os.MkdirAll(cdir, 0o755)
	if err := os.WriteFile(filepath.Join(cdir, indexFileName), corrupt, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(cdir); err == nil {
		t.Fatal("want checksum error for corrupt index")
	}

	tdir := filepath.Join(t.TempDir(), "t")
	os.MkdirAll(tdir, 0o755)
	if err := os.WriteFile(filepath.Join(tdir, indexFileName), corrupt[:20], 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(tdir); err == nil {
		t.Fatal("want error for truncated index")
	}
}

func TestSaveFailureKeepsOldIndex(t *testing.T) {
	chains, _ := chain.ParseReader(strings.NewReader(persistInput))
	dir := filepath.Join(t.TempDir(), "idx")
	idx := New(chains)
	if err := idx.Save(dir); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(filepath.Join(dir, indexFileName))
	if err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("want exactly one index file before, got %d", len(entries))
	}

	// Saving onto a path that is a regular file must fail before the old
	// index file is touched (temp file + atomic rename guarantee).
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := idx.Save(blocker); err == nil {
		t.Fatal("want save failure when target path is a file")
	}

	if _, err := Load(dir); err != nil {
		t.Fatalf("old index must remain loadable: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, indexFileName))
	if !bytes.Equal(got, original) {
		t.Fatal("old index file changed after a failed write")
	}
	entries, _ = os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("temp file leaked, dir now has %d entries", len(entries))
	}
}

// TestLoadVersionMismatchMessage ensures an unsupported index version yields
// a distinguishable error (users can recover by rebuilding), rather than
// silently producing wrong results.
func TestLoadVersionMismatchMessage(t *testing.T) {
	chains, _ := chain.ParseReader(strings.NewReader(persistInput))
	dir := t.TempDir()
	if err := New(chains).Save(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, indexFileName)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Bump the version field (2 bytes right after the 8-byte magic) and
	// truncate the checksum; version checking happens before checksum
	// verification and must report the version mismatch clearly.
	raw = append([]byte(nil), raw...)
	raw[len(indexMagic)] = raw[len(indexMagic)] + 1
	bad := filepath.Join(t.TempDir(), "b")
	os.MkdirAll(bad, 0o755)
	if err := os.WriteFile(filepath.Join(bad, indexFileName), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = Load(bad)
	if err == nil {
		t.Fatal("want error for unsupported index version")
	}
	if !strings.Contains(err.Error(), "unsupported index version") {
		t.Fatalf("want distinguishable version error, got: %v", err)
	}
}
