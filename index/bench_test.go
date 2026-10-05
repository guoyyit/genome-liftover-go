package index

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/guoyyit/genome-liftover-go/internal/testutil"
)

// BenchmarkDenseQueryScaling measures point-query cost as the number of
// heavily overlapping chains on one chromosome grows. The chromosome length
// grows with the chain count so the overlap depth (and thus the output size)
// stays constant: a flat ns/op curve demonstrates that query time does not
// grow linearly with the number of chains. Run:
//
//	go test ./index/ -run=NONE -bench=BenchmarkDenseQueryScaling -benchtime=200x
func BenchmarkDenseQueryScaling(b *testing.B) {
	for _, n := range []int{1000, 2000, 4000, 8000, 16000} {
		b.Run(fmt.Sprintf("chains=%d", n), func(b *testing.B) {
			spec := testutil.Spec{
				TargetChrom: "chr1",
				QueryChrom:  "chrA",
				TargetSize:  n * 400, // constant overlap depth (~25x)
				QuerySize:   n * 800,
				Chains:      n,
				MinSpan:     4_000,
				MaxSpan:     16_000,
				MaxBlocks:   6,
				MinusEvery:  3,
				ScoreSteps:  8,
				Seed:        1,
			}
			idx := New(testutil.DenseChains(spec))
			rng := rand.New(rand.NewSource(1))
			starts := make([]int, 100)
			for i := range starts {
				starts[i] = rng.Intn(spec.TargetSize - 1000)
			}
			var total int
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				s := starts[i%len(starts)]
				total += len(idx.Query("chr1", s, s+500))
			}
			if total == 0 {
				b.Fatal("dense fixture must produce hits")
			}
		})
	}
}
