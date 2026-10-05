// Package testutil provides deterministic dense-overlap chain fixtures and a
// brute-force reference implementation used to cross-validate the index and
// mapper against exact per-chain comparison.
package testutil

import (
	"fmt"
	"io"
	"math/rand"
	"sort"

	"github.com/guoyyit/genome-liftover-go/chain"
)

// Spec controls dense fixture generation. All chains share one target
// chromosome and one query chromosome so they overlap heavily.
type Spec struct {
	TargetChrom string
	QueryChrom  string
	TargetSize  int
	QuerySize   int
	Chains      int   // number of chains to generate
	MinSpan     int   // minimum chain target walk (blocks + target gaps)
	MaxSpan     int   // maximum chain target walk
	MaxBlocks   int   // maximum alignment blocks per chain
	MinusEvery  int   // every Nth chain uses the minus strand (0 = none)
	ScoreSteps  int   // scores are drawn from this many distinct values (forces ties)
	Seed        int64 // deterministic RNG seed
}

// DenseChains deterministically generates heavily overlapping chains that all
// satisfy chain.Validate. The returned slice is shuffled (seeded) so tests do
// not depend on input order.
func DenseChains(spec Spec) []*chain.Chain {
	rng := rand.New(rand.NewSource(spec.Seed))
	chains := make([]*chain.Chain, 0, spec.Chains)
	for i := 0; i < spec.Chains; i++ {
		chains = append(chains, genChain(rng, spec, int64(i+1)))
	}
	rng.Shuffle(len(chains), func(i, j int) { chains[i], chains[j] = chains[j], chains[i] })
	return chains
}

func genChain(rng *rand.Rand, spec Spec, id int64) *chain.Chain {
	span := spec.MinSpan
	if spec.MaxSpan > spec.MinSpan {
		span += rng.Intn(spec.MaxSpan - spec.MinSpan)
	}

	// Walk blocks/gaps until the target walk reaches the chosen span.
	type gap struct{ size, dt, dq int }
	var blocks []gap
	tWalk, qWalk := 0, 0
	for {
		size := 200 + rng.Intn(2000)
		if tWalk+size > span && len(blocks) > 0 {
			size = span - tWalk
		}
		if size <= 0 {
			size = 1
		}
		blocks = append(blocks, gap{size: size})
		tWalk += size
		qWalk += size
		if tWalk >= span || len(blocks) >= spec.MaxBlocks {
			break
		}
		dt, dq := rng.Intn(800), rng.Intn(800)
		if dt == 0 && dq == 0 {
			dq = 1 + rng.Intn(50)
		}
		if tWalk+dt > span {
			dt = 0
			if dq == 0 {
				dq = 1
			}
		}
		blocks[len(blocks)-1].dt = dt
		blocks[len(blocks)-1].dq = dq
		tWalk += dt
		qWalk += dq
	}

	tStart := 0
	if spec.TargetSize > tWalk {
		tStart = rng.Intn(spec.TargetSize - tWalk)
	}
	qStart := 0
	if spec.QuerySize > qWalk {
		qStart = rng.Intn(spec.QuerySize - qWalk)
	}

	strand := chain.StrandPlus
	if spec.MinusEvery > 0 && id%int64(spec.MinusEvery) == 0 {
		strand = chain.StrandMinus
	}
	score := int64(100 * (1 + rng.Intn(spec.ScoreSteps)))

	c := &chain.Chain{
		Score:        score,
		TargetName:   spec.TargetChrom,
		TargetSize:   spec.TargetSize,
		TargetStrand: chain.StrandPlus,
		TargetStart:  tStart,
		TargetEnd:    tStart + tWalk,
		QueryName:    spec.QueryChrom,
		QuerySize:    spec.QuerySize,
		QueryStrand:  strand,
		QueryStart:   qStart,
		QueryEnd:     qStart + qWalk,
		ID:           id,
	}
	t, q := tStart, qStart
	for _, b := range blocks {
		c.Blocks = append(c.Blocks, chain.AlignBlock{TStart: t, QStart: q, Size: b.size})
		t += b.size + b.dt
		q += b.size + b.dq
	}
	return c
}

// WriteChains serializes chains in UCSC chain text format.
func WriteChains(w io.Writer, chains []*chain.Chain) error {
	for _, c := range chains {
		if _, err := fmt.Fprintf(w, "chain %d %s %d %c %d %d %s %d %c %d %d %d\n",
			c.Score, c.TargetName, c.TargetSize, byte(c.TargetStrand), c.TargetStart, c.TargetEnd,
			c.QueryName, c.QuerySize, byte(c.QueryStrand), c.QueryStart, c.QueryEnd, c.ID); err != nil {
			return err
		}
		for i, b := range c.Blocks {
			if i == len(c.Blocks)-1 {
				if _, err := fmt.Fprintf(w, "%d\n", b.Size); err != nil {
					return err
				}
				continue
			}
			n := c.Blocks[i+1]
			dt := n.TStart - (b.TStart + b.Size)
			dq := n.QStart - (b.QStart + b.Size)
			if _, err := fmt.Fprintf(w, "%d\t%d\t%d\n", b.Size, dt, dq); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}
	return nil
}

// Span returns the chromosome and half-open interval a chain occupies in the
// requested coordinate space. For the query side, minus-strand chain
// coordinates are flipped into genome coordinates with qSize.
func Span(c *chain.Chain, querySide bool) (chrom string, lo, hi int) {
	if !querySide {
		return c.TargetName, c.TargetStart, c.TargetEnd
	}
	lo, hi = c.QueryStart, c.QueryEnd
	if c.QueryStrand == chain.StrandMinus {
		lo, hi = c.QuerySize-c.QueryEnd, c.QuerySize-c.QueryStart
	}
	return c.QueryName, lo, hi
}

// Overlap is the brute-force reference: it returns the IDs of every chain
// whose span overlaps [start,end) on chrom, ordered by (span start, chain ID)
// — exactly the order the index documents for its hits.
func Overlap(chains []*chain.Chain, querySide bool, chrom string, start, end int) []int64 {
	type hit struct {
		lo int
		id int64
	}
	var hits []hit
	for _, c := range chains {
		name, lo, hi := Span(c, querySide)
		if name != chrom {
			continue
		}
		if lo < end && hi > start {
			hits = append(hits, hit{lo: lo, id: c.ID})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].lo != hits[j].lo {
			return hits[i].lo < hits[j].lo
		}
		return hits[i].id < hits[j].id
	})
	ids := make([]int64, len(hits))
	for i, h := range hits {
		ids[i] = h.id
	}
	return ids
}
