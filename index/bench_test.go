package index

import (
	"fmt"
	"testing"

	"github.com/guoyyit/genome-liftover-go/chain"
)

// benchmarkChains builds n single-block overlapping chains covering a fixed
// coordinate space, so density scales with n.
func benchmarkChains(b *testing.B, n int) *IntervalIndex {
	spans := denseSpans(n, 100000, 2000, int64(n))
	chains := make([]*chain.Chain, 0, n)
	for _, s := range spans {
		chains = append(chains, singleBlockChain(s.id, s.score,
			"chr1", s.lo, s.hi, "chrA", 100000, chain.StrandPlus, s.lo, s.hi))
	}
	return New(chains)
}

func BenchmarkQueryDense(b *testing.B) {
	for _, n := range []int{1000, 4000, 16000} {
		idx := benchmarkChains(b, n)
		b.Run(fmt.Sprintf("chains=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				lo := (i * 37) % 99000
				_ = idx.Query("chr1", lo, lo+100)
			}
		})
	}
}

// BenchmarkQuerySparse measures a window that overlaps only a handful of
// chains even when the chromosome holds many; its time must stay ~O(log n).
func BenchmarkQuerySparse(b *testing.B) {
	for _, n := range []int{1000, 4000, 16000} {
		spans := denseSpans(n, 1000000, 50, int64(n)+1)
		chains := make([]*chain.Chain, 0, n)
		for _, s := range spans {
			chains = append(chains, singleBlockChain(s.id, s.score,
				"chr1", s.lo, s.hi, "chrA", 1000000, chain.StrandPlus, s.lo, s.hi))
		}
		idx := New(chains)
		b.Run(fmt.Sprintf("chains=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				lo := (i * 7919) % 999900
				_ = idx.Query("chr1", lo, lo+5)
			}
		})
	}
}

func BenchmarkBuildDense(b *testing.B) {
	for _, n := range []int{1000, 4000, 16000} {
		spans := denseSpans(n, 100000, 2000, int64(n)+3)
		chains := make([]*chain.Chain, 0, n)
		for _, s := range spans {
			chains = append(chains, singleBlockChain(s.id, s.score,
				"chr1", s.lo, s.hi, "chrA", 100000, chain.StrandPlus, s.lo, s.hi))
		}
		b.Run(fmt.Sprintf("chains=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = New(chains)
			}
		})
	}
}
