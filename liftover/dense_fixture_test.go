package liftover

import (
	"math/rand"

	"github.com/guoyyit/genome-liftover-go/chain"
	"github.com/guoyyit/genome-liftover-go/index"
)

// denseFixture holds a set of thousands of heavily overlapping chains on one
// target/query chromosome pair, plus the raw spec needed by the brute-force
// reference implementation.
type denseChainSpec struct {
	id       int64
	score    int64
	tLo, tHi int
	qLo, qHi int // query-chain coordinates
	strand   chain.Strand
	blocks   []chain.AlignBlock
}

type denseFixture struct {
	chains []*chain.Chain
	specs  []denseChainSpec
	qSize  int
	tSize  int
}

// genDenseFixture builds n overlapping chains, each consisting of several
// alignment blocks separated by valid gaps. Minus chains use flipped query
// chain coordinates. One long chain carries the highest score so that
// best-candidate round trips are unambiguous.
func genDenseFixture(n, coordRange int, seed int64) *denseFixture {
	rng := rand.New(rand.NewSource(seed))
	const qSize = 100000
	f := &denseFixture{qSize: qSize, tSize: coordRange}
	for i := 0; i < n; i++ {
		tLo := rng.Intn(coordRange - 2)
		tHi := tLo + 50 + rng.Intn(4000)
		if tHi > coordRange {
			tHi = coordRange
		}
		strand := chain.StrandPlus
		if i%2 == 1 {
			strand = chain.StrandMinus
		}

		var blocks []chain.AlignBlock
		tp, qp := tLo, rng.Intn(qSize/4)
		for tp < tHi {
			size := 10 + rng.Intn(120)
			if tp+size > tHi {
				size = tHi - tp
			}
			if size <= 0 {
				break
			}
			if qp+size > qSize {
				// Start a chain whose query coordinates cannot fit; instead
				// just terminate the current chain at the last block.
				break
			}
			blocks = append(blocks, chain.AlignBlock{TStart: tp, QStart: qp, Size: size})
			tp += size
			qp += size
			if tp >= tHi {
				break
			}
			dt := 1 + rng.Intn(40)
			dq := rng.Intn(40) // may be zero: query-only gap merges source blocks
			if tp+dt > tHi {
				break
			}
			tp += dt
			qp += dq
			if qp > qSize {
				break
			}
		}
		if len(blocks) == 0 {
			continue
		}
		final := blocks[len(blocks)-1]
		realTHi := final.TStart + final.Size
		realQHi := final.QStart + final.Size
		qStart := blocks[0].QStart
		spec := denseChainSpec{
			id:     int64(i + 1),
			tLo:    tLo,
			tHi:    realTHi,
			qLo:    qStart,
			qHi:    realQHi,
			strand: strand,
			blocks: blocks,
		}
		c := &chain.Chain{
			Score:        int64(n - i),
			TargetName:   "chrT",
			TargetSize:   coordRange,
			TargetStrand: chain.StrandPlus,
			TargetStart:  tLo,
			TargetEnd:    realTHi,
			QueryName:    "chrQ",
			QuerySize:    qSize,
			QueryStrand:  strand,
			QueryStart:   qStart,
			QueryEnd:     realQHi,
			ID:           spec.id,
			Blocks:       blocks,
		}
		if err := chain.Validate(c); err != nil {
			// Generated coordinates must always be valid; fail loudly if not.
			panic(err)
		}
		spec.score = c.Score
		f.specs = append(f.specs, spec)
		f.chains = append(f.chains, c)
	}
	return f
}

func (f *denseFixture) index() *index.IntervalIndex { return index.New(f.chains) }
