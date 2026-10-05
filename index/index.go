package index

import (
	"sort"

	"github.com/guoyyit/genome-liftover-go/chain"
)

// IntervalIndex supports efficient bounded-memory interval queries.
// An index is immutable after construction, so concurrent Query calls are
// safe without external synchronization.
type IntervalIndex struct {
	chains []*chain.Chain

	target map[string]*chromIndex
	query  map[string]*chromIndex
}

type chromIndex struct {
	chains []*chain.Chain
	starts []int
	ends   []int
	// maxEnd is a segment tree over ends; it answers "leftmost index in a
	// range whose end exceeds a value" in O(log n), which is required to
	// find every overlap because ends are not monotone once many spans
	// overlap.
	tree maxEndTree
}

// New builds an index from parsed chains.
func New(chains []*chain.Chain) *IntervalIndex {
	cp := make([]*chain.Chain, len(chains))
	copy(cp, chains)
	sort.SliceStable(cp, func(i, j int) bool { return cp[i].ID < cp[j].ID })

	idx := &IntervalIndex{
		chains: cp,
		target: make(map[string]*chromIndex),
		query:  make(map[string]*chromIndex),
	}
	targetChains := make(map[string][]*chain.Chain)
	queryChains := make(map[string][]*chain.Chain)
	for _, c := range cp {
		targetChains[c.TargetName] = append(targetChains[c.TargetName], c)
		queryChains[c.QueryName] = append(queryChains[c.QueryName], c)
	}
	for chrom, list := range targetChains {
		idx.target[chrom] = buildChromIndex(list)
	}
	for chrom, list := range queryChains {
		idx.query[chrom] = buildQueryChromIndex(list)
	}
	return idx
}

// span pairs a chain with an interval in the queried coordinate space.
type span struct {
	c      *chain.Chain
	lo, hi int
}

func buildChromIndex(list []*chain.Chain) *chromIndex {
	spans := make([]span, len(list))
	for i, c := range list {
		spans[i] = span{c: c, lo: c.TargetStart, hi: c.TargetEnd}
	}
	return buildSpanIndex(spans)
}

// buildQueryChromIndex indexes chains by their query span in *genome*
// coordinates: for minus-strand queries the header's qStart/qEnd are chain
// coordinates and must be flipped with qSize before users can query them.
func buildQueryChromIndex(list []*chain.Chain) *chromIndex {
	spans := make([]span, len(list))
	for i, c := range list {
		lo, hi := c.QueryStart, c.QueryEnd
		if c.QueryStrand == chain.StrandMinus {
			lo, hi = c.QuerySize-c.QueryEnd, c.QuerySize-c.QueryStart
		}
		spans[i] = span{c: c, lo: lo, hi: hi}
	}
	return buildSpanIndex(spans)
}

func buildSpanIndex(spans []span) *chromIndex {
	sort.SliceStable(spans, func(i, j int) bool {
		if spans[i].lo != spans[j].lo {
			return spans[i].lo < spans[j].lo
		}
		return spans[i].c.ID < spans[j].c.ID
	})
	ci := &chromIndex{
		chains: make([]*chain.Chain, len(spans)),
		starts: make([]int, len(spans)),
		ends:   make([]int, len(spans)),
	}
	for i, s := range spans {
		ci.chains[i] = s.c
		ci.starts[i] = s.lo
		ci.ends[i] = s.hi
	}
	ci.tree = newMaxEndTree(ci.ends)
	return ci
}

// ChainHit is an index lookup result.
type ChainHit struct {
	Chain *chain.Chain
}

// Query returns chains whose target span overlaps [start,end) on chrom,
// ordered by target start and then chain ID.
func (idx *IntervalIndex) Query(chrom string, start, end int) []ChainHit {
	return idx.querySide(idx.target, chrom, start, end)
}

// QueryQuery returns chains whose query span overlaps [start,end) on chrom,
// ordered by query start and then chain ID.
func (idx *IntervalIndex) QueryQuery(chrom string, start, end int) []ChainHit {
	return idx.querySide(idx.query, chrom, start, end)
}

func (idx *IntervalIndex) querySide(side map[string]*chromIndex, chrom string, start, end int) []ChainHit {
	if start < 0 || end < start {
		return nil
	}
	ci := side[chrom]
	if ci == nil {
		return nil
	}
	n := len(ci.starts)
	// Only spans with start < end can overlap; this bound is exact because
	// starts are sorted. Empty queries (start == end) never overlap.
	limit := sort.Search(n, func(i int) bool { return ci.starts[i] >= end })
	if start == end || limit == 0 {
		return nil
	}
	// Among indices [0,limit), report exactly those with end > start. Ends
	// are not monotone under heavy overlap, so the max-end tree locates each
	// matching index in O(log n) while skipping ranges that contain none.
	var hits []ChainHit
	i := ci.tree.firstAfter(0, limit, start)
	for i >= 0 {
		hits = append(hits, ChainHit{Chain: ci.chains[i]})
		i = ci.tree.firstAfter(i+1, limit, start)
	}
	return hits
}

// Chains returns all indexed chains in deterministic order.
func (idx *IntervalIndex) Chains() []*chain.Chain {
	return idx.chains
}
