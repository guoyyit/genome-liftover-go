package index

import (
	"math/rand"
	"sort"
	"testing"

	"github.com/guoyyit/genome-liftover-go/chain"
)

// hitIDs extracts chain IDs from index hits.
func hitIDs(hits []ChainHit) []int64 {
	ids := make([]int64, len(hits))
	for i, h := range hits {
		ids[i] = h.Chain.ID
	}
	return ids
}

// TestDenseOverlapMinimalRepro is the smallest fixture that reproduces the
// dense-overlap bug: chains are sorted by start but their ends are not
// monotone, so binary searching the ends array both skips the long early
// chain (missed candidate / false unmapped) and admits a short later chain
// that does not overlap (false candidate).
//
//	start-sorted spans: id1 [0,100), id2 [10,1000), id3 [20,30)
//	ends in that order: 100, 1000, 30  (not monotone)
//	query [50,60) truly overlaps id1 and id2 only.
func TestDenseOverlapMinimalRepro(t *testing.T) {
	chains := []*chain.Chain{
		singleBlockChain(1, 100, "chr1", 0, 100, "chrA", 100000, chain.StrandPlus, 0, 100),
		singleBlockChain(2, 90, "chr1", 10, 1000, "chrA", 100000, chain.StrandPlus, 10, 1000),
		singleBlockChain(3, 80, "chr1", 20, 30, "chrA", 100000, chain.StrandPlus, 20, 30),
	}
	idx := New(chains)

	got := hitIDs(idx.Query("chr1", 50, 60))
	want := []int64{1, 2}
	if !equalInt64(got, want) {
		t.Fatalf("dense overlap query: want candidates %v, got %v", want, got)
	}
}

// singleBlockChain builds a valid one-block chain with the given target and
// query chain-coordinate spans.
func singleBlockChain(id int64, score int64, tName string, tLo, tHi int,
	qName string, qSize int, qStrand chain.Strand, qLo, qHi int) *chain.Chain {
	c := &chain.Chain{
		Score:        score,
		TargetName:   tName,
		TargetSize:   10000000,
		TargetStrand: chain.StrandPlus,
		TargetStart:  tLo,
		TargetEnd:    tHi,
		QueryName:    qName,
		QuerySize:    qSize,
		QueryStrand:  qStrand,
		QueryStart:   qLo,
		QueryEnd:     qHi,
		ID:           id,
		Blocks: []chain.AlignBlock{{
			TStart: tLo,
			QStart: qLo,
			Size:   tHi - tLo,
		}},
	}
	if err := chain.Validate(c); err != nil {
		panic(err)
	}
	return c
}

func equalInt64(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// genSpan is one chain span in the queried genome coordinate space.
type genSpan struct {
	id    int64
	lo    int
	hi    int
	score int64
}

// TestDenseOverlapBruteForceTarget cross-validates target-side queries on a
// fixture of thousands of heavily overlapping chains against an exhaustive
// per-chain comparison, including endpoint-touching and boundary-spanning
// intervals.
func TestDenseOverlapBruteForceTarget(t *testing.T) {
	const n = 4000
	spans := denseSpans(n, 100000, 2000, 1)
	var chains []*chain.Chain
	for _, s := range spans {
		chains = append(chains, singleBlockChain(s.id, s.score,
			"chr1", s.lo, s.hi, "chrA", 100000, chain.StrandPlus, s.lo, s.hi))
	}
	idx := New(chains)
	crossValidate(t, spans, func(lo, hi int) []ChainHit { return idx.Query("chr1", lo, hi) })
}

// TestDenseOverlapBruteForceQuery mixes plus and minus chains on one query
// chromosome; minus-chain qStart/qEnd are chain coordinates and the index
// must flip them to genome coordinates.
func TestDenseOverlapBruteForceQuery(t *testing.T) {
	const n = 4000
	const qSize = 100000
	spans := denseSpans(n, qSize, 2000, 7)
	var chains []*chain.Chain
	for i, s := range spans {
		strand := chain.StrandPlus
		if i%2 == 1 {
			strand = chain.StrandMinus
		}
		qLo, qHi := s.lo, s.hi
		if strand == chain.StrandMinus {
			// Genome span [s.lo,s.hi) <-> chain coords flipped.
			qLo, qHi = qSize-s.hi, qSize-s.lo
		}
		chains = append(chains, singleBlockChain(s.id, s.score,
			"trg", s.lo, s.hi, "chrA", qSize, strand, qLo, qHi))
	}
	idx := New(chains)
	crossValidate(t, spans, func(lo, hi int) []ChainHit { return idx.QueryQuery("chrA", lo, hi) })
}

// denseSpans deterministically generates n heavily overlapping spans inside
// [0,coordRange).
func denseSpans(n, coordRange, maxLen int, seed int64) []genSpan {
	rng := rand.New(rand.NewSource(seed))
	spans := make([]genSpan, n)
	for i := 0; i < n; i++ {
		lo := rng.Intn(coordRange - 1)
		hi := lo + 1 + rng.Intn(maxLen-1)
		if hi > coordRange {
			hi = coordRange
		}
		spans[i] = genSpan{id: int64(i + 1), lo: lo, hi: hi, score: int64(n - i)}
	}
	return spans
}

func crossValidate(t *testing.T, spans []genSpan, query func(lo, hi int) []ChainHit) {
	t.Helper()

	// Brute-force reference: exact 0-based half-open overlap; touching ends
	// do not count.
	reference := func(qs, qe int) []int64 {
		var ids []int64
		for _, s := range spans {
			if s.lo < qe && s.hi > qs {
				ids = append(ids, s.id)
			}
		}
		return ids
	}

	check := func(qs, qe int) {
		t.Helper()
		gotHits := query(qs, qe)
		got := hitIDs(gotHits)
		want := reference(qs, qe)
		// Reference order: ascending (lo, id), same as index contract.
		byLo := make(map[int64]genSpan)
		for _, s := range spans {
			byLo[s.id] = s
		}
		sort.Slice(want, func(i, j int) bool {
			a, b := byLo[want[i]], byLo[want[j]]
			if a.lo != b.lo {
				return a.lo < b.lo
			}
			return a.id < b.id
		})
		if !equalInt64(got, want) {
			t.Fatalf("query [%d,%d): want %d candidates, got %d", qs, qe, len(want), len(got))
		}
	}

	endpointPool := map[int]bool{0: true, 100000: true}
	for _, s := range spans {
		endpointPool[s.lo] = true
		endpointPool[s.hi] = true
	}
	var endpoints []int
	for e := range endpointPool {
		endpoints = append(endpoints, e)
	}
	sort.Ints(endpoints)

	// Exhaustive boundary queries on a deterministic sample of spans: the
	// sample still touches thousands of distinct endpoints, while keeping the
	// brute-force reference cheap.
	for i, s := range spans {
		if i%8 != 0 {
			continue
		}
		w := 50
		headBack := s.lo - w
		if headBack < 0 {
			headBack = 0
		}
		straddleLo := s.lo - w/2
		if straddleLo < 0 {
			straddleLo = 0
		}
		tailFwd := s.hi + w
		if tailFwd > 100000 {
			tailFwd = 100000
		}
		straddleHi := s.hi + w/2
		if straddleHi > 100000 {
			straddleHi = 100000
		}
		check(s.lo, s.hi)   // exact chain span
		check(s.lo, s.lo+w) // starts exactly at head
		if s.hi >= w {
			check(s.hi-w, s.hi) // ends exactly at tail
		}
		check(s.hi, tailFwd)        // only touches the tail: no overlap
		check(headBack, s.lo)       // only touches the head: no overlap
		check(straddleLo, s.lo+w/2) // straddles head boundary
		if s.hi >= w/2 {
			check(s.hi-w/2, straddleHi) // straddles tail boundary
		}
	}

	// Random intervals across the coordinate space.
	rng := rand.New(rand.NewSource(99))
	for i := 0; i < 2000; i++ {
		a := rng.Intn(99000)
		b := a + 1 + rng.Intn(3000)
		check(a, b)
	}
}
