package liftover

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

var persistQueries = []struct {
	q   Interval
	dir Direction
}{
	{Interval{Name: "chr1", Start: 100, End: 200}, Forward},
	{Interval{Name: "chr1", Start: 100, End: 150}, Forward},
	{Interval{Name: "chr1", Start: 150, End: 160}, Forward},
	{Interval{Name: "chr1", Start: 0, End: 1000}, Forward},
	{Interval{Name: "chr2", Start: 300, End: 400}, Reverse},
	{Interval{Name: "chr2", Start: 1600, End: 1700}, Reverse},
	{Interval{Name: "chrX", Start: 0, End: 10}, Forward},
}

func TestSaveLoadRoundTrip(t *testing.T) {
	data := plusChain + minusChain +
		"chain 1000 chr1 1000 + 100 200 chr3 3000 + 500 600 7\n100\n\n"
	ix := buildIndex(t, data)

	path := filepath.Join(t.TempDir(), "index.gli")
	if err := ix.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := LoadIndex(path)
	if err != nil {
		t.Fatalf("LoadIndex: %v", err)
	}
	for _, tc := range persistQueries {
		want := ix.Map(tc.q, tc.dir)
		got := loaded.Map(tc.q, tc.dir)
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("query %+v dir=%v:\n got %+v\nwant %+v", tc.q, tc.dir, got, want)
		}
	}
}

func TestLoadIndexRejectsBadFiles(t *testing.T) {
	dir := t.TempDir()

	garbage := filepath.Join(dir, "garbage.gli")
	if err := os.WriteFile(garbage, []byte("not an index at all........."), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadIndex(garbage); !errors.Is(err, ErrNotIndex) {
		t.Fatalf("expected ErrNotIndex, got %v", err)
	}

	empty := filepath.Join(dir, "empty.gli")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadIndex(empty); !errors.Is(err, ErrNotIndex) {
		t.Fatalf("expected ErrNotIndex for empty file, got %v", err)
	}

	truncated := filepath.Join(dir, "truncated.gli")
	if err := os.WriteFile(truncated, []byte(indexMagic+"\x01\x02"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadIndex(truncated); err == nil || errors.Is(err, ErrNotIndex) {
		t.Fatalf("expected decode error, got %v", err)
	}
}

func TestSaveFailureKeepsExistingIndex(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "index.gli")

	ix1 := buildIndex(t, plusChain)
	if err := ix1.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// 目标目录不存在：写入失败，已有索引必须保持原样
	ix2 := buildIndex(t, minusChain)
	badPath := filepath.Join(dir, "no-such-dir", "index.gli")
	if err := ix2.Save(badPath); err == nil {
		t.Fatal("expected save error")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("existing index was modified by failed save")
	}

	// 正常覆盖写：加载结果应与新索引一致
	if err := ix2.Save(path); err != nil {
		t.Fatalf("Save overwrite: %v", err)
	}
	loaded, err := LoadIndex(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range persistQueries {
		want := ix2.Map(tc.q, tc.dir)
		got := loaded.Map(tc.q, tc.dir)
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("query %+v: got %+v want %+v", tc.q, got, want)
		}
	}
}
