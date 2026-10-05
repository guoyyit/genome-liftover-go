package liftover

import "testing"

// minus chain with a QUERY-ONLY gap (dt=0, dq>0): the two blocks are adjacent
// in target coordinates. Under target->query with a minus query these are
// emitted per block by design (dest coordinates cross), and each block's
// flipped destination must be computed against the block's own coordinates.
//
//	target [100,160):
//	  block A t[100,130) q-chain[100,130)
//	  query-only gap q-chain[130,140) (dt=0,dq=10)
//	  block B t[130,160) q-chain[140,170)
//	query size 200, q-chain span [100,170).
//
// In minus genome coordinates:
//
//	block A t[100,130) -> q-genome [70,100)
//	block B t[130,160) -> q-genome [30,60)
const minusQueryOnlyGap = `chain 50 chrT 1000 + 100 160 chrQ 200 - 100 170 7
30 0 10
30
`

func TestMinusPerBlockFlipWhenClipped(t *testing.T) {
	m := NewMapper(parseIndex(t, minusQueryOnlyGap))

	// Query clips the START of block A: t[110,130). The old implementation
	// flipped relative to the chain span instead of the clipped block end
	// and produced [60,80); correct flipped coordinates are [70,90).
	res, err := m.Map("chrT", 110, 130, TargetToQuery)
	if err != nil {
		t.Fatal(err)
	}
	best, ok := res.BestCandidate()
	if !ok {
		t.Fatal("want candidate")
	}
	var mapped []Segment
	for _, sg := range best.Segments {
		if sg.Status == StatusMapped {
			mapped = append(mapped, sg)
		}
	}
	if len(mapped) != 1 {
		t.Fatalf("want one mapped piece, got %+v", best.Segments)
	}
	if mapped[0].DestStart != 70 || mapped[0].DestEnd != 90 {
		t.Fatalf("clipped minus flip wrong: got [%d,%d), want [70,90): %+v",
			mapped[0].DestStart, mapped[0].DestEnd, mapped[0])
	}

	// Across the query-only gap: per-block pieces with a flip each.
	// t[120,140): A piece -> [70,80); B piece -> [50,60).
	res2, err := m.Map("chrT", 120, 140, TargetToQuery)
	if err != nil {
		t.Fatal(err)
	}
	best2, _ := res2.BestCandidate()
	var mp2 []Segment
	for _, sg := range best2.Segments {
		if sg.Status == StatusMapped {
			mp2 = append(mp2, sg)
		}
	}
	if len(mp2) != 2 {
		t.Fatalf("minus T->Q emits per-block pieces, want 2 got %+v", best2.Segments)
	}
	if mp2[0].DestStart != 70 || mp2[0].DestEnd != 80 {
		t.Fatalf("A piece wrong: %+v", mp2[0])
	}
	if mp2[1].DestStart != 50 || mp2[1].DestEnd != 60 {
		t.Fatalf("B piece wrong: %+v", mp2[1])
	}
}

// minus chain with a TARGET-ONLY gap (dt>0, dq=0): blocks are adjacent in
// q-chain coordinates but separated in target. Querying query->target the
// pieces must NOT be merged, otherwise the destination interval is wrong.
//
//	block A t[100,130) q-chain[100,130)
//	target-only gap t[130,140) (dt=10,dq=0)
//	block B t[140,170) q-chain[130,160)
//	query size 200, q-chain span [100,160).
//
// Genome query q[40,100) corresponds to q-chain [100,160), covering both
// blocks with an unmapped middle gap (in source genome order the gap sits
// between the two mapped pieces).
const minusTargetOnlyGap = `chain 50 chrT 1000 + 100 170 chrQ 200 - 100 160 8
30 10 0
30
`

func TestMinusSplitAcrossTargetOnlyGap(t *testing.T) {
	m := NewMapper(parseIndex(t, minusTargetOnlyGap))

	res, err := m.Map("chrQ", 40, 100, QueryToTarget)
	if err != nil {
		t.Fatal(err)
	}
	best, ok := res.BestCandidate()
	if !ok {
		t.Fatal("want candidate")
	}
	var mapped []Segment
	var unmapped []Segment
	for _, sg := range best.Segments {
		switch sg.Status {
		case StatusMapped:
			mapped = append(mapped, sg)
		case StatusUnmapped:
			unmapped = append(unmapped, sg)
		}
	}
	// In genome (reverse) order:
	//   q-genome [40,70) = q-chain[130,160) -> block B -> t[140,170)
	//   q-genome [70,100)= q-chain[100,130) -> block A -> t[100,130)
	if len(mapped) != 2 {
		t.Fatalf("want 2 mapped pieces (must not merge across target gap), got %d: %+v",
			len(mapped), best.Segments)
	}
	if mapped[0].SourceStart != 40 || mapped[0].DestStart != 140 || mapped[0].DestEnd != 170 {
		t.Fatalf("first reverse piece wrong: %+v", mapped[0])
	}
	if mapped[1].SourceStart != 70 || mapped[1].DestStart != 100 || mapped[1].DestEnd != 130 {
		t.Fatalf("second reverse piece wrong: %+v", mapped[1])
	}
	_ = unmapped
}
