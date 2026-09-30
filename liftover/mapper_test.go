package liftover

import (
	"strings"
	"testing"

	"github.com/guoyyit/genome-liftover-go/chain"
	"github.com/guoyyit/genome-liftover-go/index"
)

// plus chain on target [0,110) / query+ [0,90):
//
//	block1: t[0,20)  -> q[0,20)
//	target gap t[20,30); query gap q[20,60)
//	block2: t[30,60) -> q[60,90)
//	query gap q[90,100)
//	block3: t[60,110)-> q[100,150)... no: sizes must match:
//
// built as 20 (gap 10/40) 30 (gap 0/10) 50:
// query walk: 20 -> +40 -> 60; +30=90 -> +10 -> 100; +50=150 => qEnd 150.
const plusChains = `chain 100 chr1 200 + 0 110 chrA 200 + 0 150 1
20 10 40
30 0 10
50
`

// minus chain: target [0,120), query size 200, q-chain span [50,180):
// block1 t[0,20)  q-chain[50,70)   -> rev q[130,150)
// block2 t[40,90) q-chain[90,140)  -> rev q[60,110)
// block3 t[90,120)q-chain[150,180) -> rev q[20,50)
const minusChains = `chain 90 chr1 200 + 0 120 chrA 200 - 50 180 2
20 20 20
50 0 10
30
`

func parseIndex(t *testing.T, input string) *index.IntervalIndex {
	t.Helper()
	chains, err := chain.ParseReader(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	return index.New(chains)
}

func TestMapPlusStrandGapSplit(t *testing.T) {
	m := NewMapper(parseIndex(t, plusChains))

	// Query spans target gap [20,30): must split, middle segment unmapped.
	res, err := m.Map("chr1", 10, 50, TargetToQuery)
	if err != nil {
		t.Fatal(err)
	}
	best, ok := res.BestCandidate()
	if !ok {
		t.Fatal("want a candidate")
	}
	if len(best.Segments) != 3 {
		t.Fatalf("want 3 segments, got %d: %+v", len(best.Segments), best.Segments)
	}
	want := []Segment{
		{SourceStart: 10, SourceEnd: 20, DestStart: 10, DestEnd: 20, DestChrom: "chrA", Strand: chain.StrandPlus, Status: StatusMapped},
		{SourceStart: 20, SourceEnd: 30, Strand: chain.StrandPlus, Status: StatusUnmapped},
		{SourceStart: 30, SourceEnd: 50, DestStart: 60, DestEnd: 80, DestChrom: "chrA", Strand: chain.StrandPlus, Status: StatusMapped},
	}
	for i := range want {
		if best.Segments[i] != want[i] {
			t.Fatalf("segment %d mismatch:\n got %+v\nwant %+v", i, best.Segments[i], want[i])
		}
	}
	if res.FullyMapped() {
		t.Fatal("interval crossing a gap must not be fully mapped")
	}
}

func TestMapMinusStrandFlip(t *testing.T) {
	m := NewMapper(parseIndex(t, minusChains))

	res, err := m.Map("chr1", 0, 120, TargetToQuery)
	if err != nil {
		t.Fatal(err)
	}
	best, _ := res.BestCandidate()
	if len(best.Segments) != 4 {
		t.Fatalf("want 4 ordered segments, got %d: %+v", len(best.Segments), best.Segments)
	}

	// Block1 t[0,20) maps to reverse query [130,150).
	s0 := best.Segments[0]
	if s0.Status != StatusMapped || s0.DestStart != 130 || s0.DestEnd != 150 || s0.Strand != chain.StrandMinus {
		t.Fatalf("first flipped segment wrong: %+v", s0)
	}
	// Target gap t[20,40) is unmapped.
	s1 := best.Segments[1]
	if s1.Status != StatusUnmapped || s1.SourceStart != 20 || s1.SourceEnd != 40 {
		t.Fatalf("gap segment wrong: %+v", s1)
	}
	// Block2 t[40,90) maps to reverse query [60,110).
	s2 := best.Segments[2]
	if s2.Status != StatusMapped || s2.DestStart != 60 || s2.DestEnd != 110 {
		t.Fatalf("middle flipped segment wrong: %+v", s2)
	}
	// block3 t[90,120) is continuous on target, so it is one mapped piece
	// even though it contains a query-side gap; dest is reverse q[20,50).
	s3 := best.Segments[3]
	if s3.Status != StatusMapped || s3.SourceStart != 90 || s3.SourceEnd != 120 ||
		s3.DestStart != 20 || s3.DestEnd != 50 {
		t.Fatalf("last flipped segment wrong: %+v", s3)
	}
}

func TestMapOutsideChainUnmapped(t *testing.T) {
	m := NewMapper(parseIndex(t, plusChains))
	res, err := m.Map("chr1", 105, 160, TargetToQuery)
	if err != nil {
		t.Fatal(err)
	}
	best, ok := res.BestCandidate()
	if !ok {
		t.Fatal("interval overlapping chain edge should still produce a candidate")
	}
	if len(best.Segments) != 2 {
		t.Fatalf("want mapped + unmapped tail, got %+v", best.Segments)
	}
	if best.Segments[0].Status != StatusMapped || best.Segments[0].DestStart != 145 {
		t.Fatalf("edge mapped segment wrong: %+v", best.Segments[0])
	}
	if best.Segments[1].Status != StatusUnmapped || best.Segments[1].SourceStart != 110 {
		t.Fatalf("unmapped tail wrong: %+v", best.Segments[1])
	}

	res, err = m.Map("chrAbsent", 0, 10, TargetToQuery)
	if err != nil || len(res.Candidates) != 0 {
		t.Fatalf("unknown chrom must give zero candidates, got %v %+v", err, res)
	}
}

func TestInvalidInterval(t *testing.T) {
	m := NewMapper(parseIndex(t, plusChains))
	if _, err := m.Map("chr1", -1, 10, TargetToQuery); err != ErrInvalidInterval {
		t.Fatalf("want ErrInvalidInterval, got %v", err)
	}
	if _, err := m.Map("chr1", 10, 10, TargetToQuery); err != ErrInvalidInterval {
		t.Fatalf("want ErrInvalidInterval, got %v", err)
	}
}

func TestAmbiguousCandidates(t *testing.T) {
	input := `chain 100 chr1 1000 + 0 100 chrA 1000 + 0 100 1
100
chain 200 chr1 1000 + 0 100 chrB 1000 + 0 100 2
100
chain 150 chr1 1000 + 0 100 chrC 1000 + 0 100 3
100
`
	m := NewMapper(parseIndex(t, input))
	res, err := m.Map("chr1", 10, 40, TargetToQuery)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Candidates) != 3 {
		t.Fatalf("want 3 candidates, got %d", len(res.Candidates))
	}
	// Deterministic order: descending score, ties broken by ascending chain ID.
	wantIDs := []int64{2, 3, 1}
	for i, id := range wantIDs {
		if res.Candidates[i].ChainID != id {
			t.Fatalf("candidate %d: want id %d got %d", i, id, res.Candidates[i].ChainID)
		}
	}
	best, _ := res.BestCandidate()
	if best.ChainID != 2 {
		t.Fatalf("best candidate must be highest score, got %d", best.ChainID)
	}
}

func TestRoundTrip(t *testing.T) {
	m := NewMapper(parseIndex(t, minusChains))

	// Use a region fully covered by blocks (avoiding both gaps).
	srcStart, srcEnd := 40, 90
	fwd, err := m.Map("chr1", srcStart, srcEnd, TargetToQuery)
	if err != nil {
		t.Fatal(err)
	}
	best, _ := fwd.BestCandidate()
	if len(best.Segments) != 1 || best.Segments[0].Status != StatusMapped {
		t.Fatalf("forward should be one mapped segment: %+v", best.Segments)
	}
	back, err := m.Map("chrA", best.Segments[0].DestStart, best.Segments[0].DestEnd, QueryToTarget)
	if err != nil {
		t.Fatal(err)
	}
	bb, _ := back.BestCandidate()
	if len(bb.Segments) != 1 || bb.Segments[0].Status != StatusMapped {
		t.Fatalf("reverse should be one mapped segment: %+v", bb.Segments)
	}
	if bb.Segments[0].DestStart != srcStart || bb.Segments[0].DestEnd != srcEnd {
		t.Fatalf("round trip mismatch: got [%d,%d) want [%d,%d)",
			bb.Segments[0].DestStart, bb.Segments[0].DestEnd, srcStart, srcEnd)
	}
}

func TestReverseMinusFlipCoordinates(t *testing.T) {
	m := NewMapper(parseIndex(t, minusChains))
	// Forward map of t[10,15) is reverse q[135,140).
	res, err := m.Map("chrA", 135, 140, QueryToTarget)
	if err != nil {
		t.Fatal(err)
	}
	best, _ := res.BestCandidate()
	if len(best.Segments) != 1 {
		t.Fatalf("want single segment, got %+v", best.Segments)
	}
	s := best.Segments[0]
	if s.DestStart != 10 || s.DestEnd != 15 || s.Strand != chain.StrandMinus {
		t.Fatalf("reverse minus mapping wrong: %+v", s)
	}
}

func TestReverseMinusAcrossQueryGapRoundTrip(t *testing.T) {
	// minus chain: target [0,100), block t[0,40) q-chain[0,40);
	// query gap q-chain[40,60) (genome reverse q[140,160));
	// block t[40,100) q-chain[60,120); query size 200, qStart 0 qEnd 120.
	input := `chain 50 chr1 1000 + 0 100 chrA 200 - 0 120 9
40 0 20
60
`
	m := NewMapper(parseIndex(t, input))

	// Genome interval q[130,170) corresponds to q-chain [30,70):
	// mapped 30-40, unmapped 40-60, mapped 60-70. In genome order the
	// pieces appear as 60-70 first and 30-40 last.
	res, err := m.Map("chrA", 130, 170, QueryToTarget)
	if err != nil {
		t.Fatal(err)
	}
	best, _ := res.BestCandidate()
	if len(best.Segments) != 3 {
		t.Fatalf("want 3 ordered segments, got %+v", best.Segments)
	}
	want := []Segment{
		{SourceStart: 130, SourceEnd: 140, DestStart: 40, DestEnd: 50, DestChrom: "chr1", Strand: chain.StrandMinus, Status: StatusMapped},
		{SourceStart: 140, SourceEnd: 160, Strand: chain.StrandMinus, Status: StatusUnmapped},
		{SourceStart: 160, SourceEnd: 170, DestStart: 30, DestEnd: 40, DestChrom: "chr1", Strand: chain.StrandMinus, Status: StatusMapped},
	}
	for i := range want {
		if best.Segments[i] != want[i] {
			t.Fatalf("reverse-gap segment %d:\n got %+v\nwant %+v", i, best.Segments[i], want[i])
		}
	}

	// Round trip of the fully-covered first piece must restore coordinates.
	back, err := m.Map("chr1", 40, 50, TargetToQuery)
	if err != nil {
		t.Fatal(err)
	}
	bb, _ := back.BestCandidate()
	if len(bb.Segments) != 1 || bb.Segments[0].Status != StatusMapped ||
		bb.Segments[0].DestStart != 130 || bb.Segments[0].DestEnd != 140 {
		t.Fatalf("forward roundtrip mismatch: %+v", bb.Segments)
	}
}
