package liftover

import (
	"strings"
	"testing"

	"github.com/guoyyit/genome-liftover-go/chain"
)

func buildIndex(t *testing.T, data string) *Index {
	t.Helper()
	b := NewBuilder()
	err := chain.Parse(strings.NewReader(data), func(c *chain.Chain) error {
		return b.Add(c)
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return b.Build()
}

// 正向链：chr1[100,200) -> chr2[300,400)，中间有 dt=10,dq=10 的缺口。
const plusChain = "chain 1000 chr1 1000 + 100 200 chr2 2000 + 300 400 1\n" +
	"50\t10\t10\n" +
	"40\n" +
	"\n"

// 反向链：chr1[100,200) -> chr2 反链（文件坐标 [300,400)，qSize=2000）。
const minusChain = "chain 1000 chr1 1000 + 100 200 chr2 2000 - 300 400 1\n" +
	"100\n" +
	"\n"

func TestMapForwardPlus(t *testing.T) {
	ix := buildIndex(t, plusChain)
	res := ix.Map(Interval{Name: "chr1", Start: 100, End: 150}, Forward)
	if res.Status != StatusMapped {
		t.Fatalf("expected mapped, got %v", res.Status)
	}
	if len(res.Segments) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(res.Segments))
	}
	s := res.Segments[0]
	if s.Target.Name != "chr2" || s.Target.Start != 300 || s.Target.End != 350 || s.Strand != '+' {
		t.Fatalf("unexpected segment: %+v", s)
	}
}

func TestMapForwardMinusStrand(t *testing.T) {
	ix := buildIndex(t, minusChain)
	res := ix.Map(Interval{Name: "chr1", Start: 100, End: 110}, Forward)
	if res.Status != StatusMapped || len(res.Segments) != 1 {
		t.Fatalf("unexpected result: %+v", res)
	}
	s := res.Segments[0]
	// t=100 对应文件坐标 q=399，正链坐标 2000-1-399=1600。
	if s.Target.Name != "chr2" || s.Target.Start != 1600 || s.Target.End != 1610 {
		t.Fatalf("unexpected target: %+v", s.Target)
	}
	if s.Strand != '-' {
		t.Fatalf("expected strand '-', got %q", s.Strand)
	}
}

func TestMapGapSplitsSegments(t *testing.T) {
	ix := buildIndex(t, plusChain)
	res := ix.Map(Interval{Name: "chr1", Start: 100, End: 200}, Forward)
	if res.Status != StatusPartial {
		t.Fatalf("expected partial, got %v", res.Status)
	}
	if len(res.Segments) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(res.Segments))
	}
	s0, s1 := res.Segments[0], res.Segments[1]
	if s0.Target.Start != 300 || s0.Target.End != 350 || s0.SrcStart != 100 || s0.SrcEnd != 150 {
		t.Fatalf("unexpected segment0: %+v", s0)
	}
	if s1.Target.Start != 360 || s1.Target.End != 400 || s1.SrcStart != 160 || s1.SrcEnd != 200 {
		t.Fatalf("unexpected segment1: %+v", s1)
	}
	if len(res.Unmapped) != 1 || res.Unmapped[0].Start != 150 || res.Unmapped[0].End != 160 {
		t.Fatalf("unexpected unmapped: %+v", res.Unmapped)
	}
}

func TestMapUnmappedRegions(t *testing.T) {
	ix := buildIndex(t, plusChain)
	// 完全落在缺口里
	res := ix.Map(Interval{Name: "chr1", Start: 150, End: 160}, Forward)
	if res.Status != StatusUnmapped || len(res.Segments) != 0 {
		t.Fatalf("expected unmapped, got %+v", res)
	}
	if len(res.Unmapped) != 1 || res.Unmapped[0].Start != 150 || res.Unmapped[0].End != 160 {
		t.Fatalf("unexpected unmapped: %+v", res.Unmapped)
	}
	// 染色体不存在
	res = ix.Map(Interval{Name: "chrX", Start: 0, End: 10}, Forward)
	if res.Status != StatusUnmapped {
		t.Fatalf("expected unmapped, got %+v", res)
	}
	// 部分越界
	res = ix.Map(Interval{Name: "chr1", Start: 50, End: 120}, Forward)
	if res.Status != StatusPartial {
		t.Fatalf("expected partial, got %+v", res)
	}
	if len(res.Unmapped) != 1 || res.Unmapped[0].Start != 50 || res.Unmapped[0].End != 100 {
		t.Fatalf("unexpected unmapped: %+v", res.Unmapped)
	}
}

func TestMapAmbiguityByScore(t *testing.T) {
	data := plusChain +
		"chain 500 chr1 1000 + 100 200 chr3 3000 + 500 600 2\n100\n\n"
	ix := buildIndex(t, data)
	// 两条链都覆盖的区域：高分链 1 胜出
	res := ix.Map(Interval{Name: "chr1", Start: 100, End: 150}, Forward)
	if len(res.Segments) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(res.Segments))
	}
	s := res.Segments[0]
	if s.ChainID != 1 || s.Target.Name != "chr2" {
		t.Fatalf("expected chain 1 to win, got %+v", s)
	}
	if len(s.Tied) != 0 {
		t.Fatalf("expected no tie, got %v", s.Tied)
	}
	// 链 1 的缺口区 [150,160) 只有低分链 2 覆盖，由链 2 映射
	res = ix.Map(Interval{Name: "chr1", Start: 150, End: 160}, Forward)
	if len(res.Segments) != 1 || res.Segments[0].ChainID != 2 {
		t.Fatalf("expected chain 2 to cover the gap, got %+v", res.Segments)
	}
	// 全区间：缺口外由链 1 映射，缺口内由链 2 映射
	res = ix.Map(Interval{Name: "chr1", Start: 100, End: 200}, Forward)
	if res.Status != StatusMapped {
		t.Fatalf("expected fully mapped, got %+v", res)
	}
	var got []int
	for _, s := range res.Segments {
		got = append(got, s.ChainID)
	}
	if len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 1 {
		t.Fatalf("expected chain sequence [1 2 1], got %v", got)
	}
}

func TestMapAmbiguityTie(t *testing.T) {
	data := plusChain +
		"chain 1000 chr1 1000 + 100 200 chr3 3000 + 500 600 7\n100\n\n"
	ix := buildIndex(t, data)
	// 两条链同分且都覆盖 [100,150)：链 ID 小者胜出，另一方列入 Tied
	res := ix.Map(Interval{Name: "chr1", Start: 100, End: 150}, Forward)
	if len(res.Segments) == 0 {
		t.Fatal("expected segments")
	}
	for _, s := range res.Segments {
		if s.ChainID != 1 {
			t.Fatalf("expected lower chain id 1 to win, got %d", s.ChainID)
		}
		if len(s.Tied) != 1 || s.Tied[0] != 7 {
			t.Fatalf("expected tied [7], got %v", s.Tied)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	for _, data := range []string{plusChain, minusChain} {
		ix := buildIndex(t, data)
		q := Interval{Name: "chr1", Start: 100, End: 200}
		fwd := ix.Map(q, Forward)
		if len(fwd.Segments) == 0 {
			t.Fatal("expected forward segments")
		}
		var back []Interval
		for _, s := range fwd.Segments {
			rev := ix.Map(s.Target, Reverse)
			if rev.Status != StatusMapped {
				t.Fatalf("reverse not fully mapped: %+v", rev)
			}
			for _, rs := range rev.Segments {
				back = append(back, Interval{Name: rs.Target.Name, Start: rs.Target.Start, End: rs.Target.End})
			}
		}
		// 往返后应恢复为原区间被映射的部分（缺口除外）
		if len(back) != len(fwd.Segments) {
			t.Fatalf("expected %d back segments, got %d", len(fwd.Segments), len(back))
		}
		for i, s := range fwd.Segments {
			if back[i].Name != "chr1" || back[i].Start != s.SrcStart || back[i].End != s.SrcEnd {
				t.Fatalf("round trip mismatch: got %+v, want [%d,%d)", back[i], s.SrcStart, s.SrcEnd)
			}
		}
	}
}
