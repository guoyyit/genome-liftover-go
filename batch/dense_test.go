package batch

import (
	"bytes"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/guoyyit/genome-liftover-go/chain"
	"github.com/guoyyit/genome-liftover-go/index"
	"github.com/guoyyit/genome-liftover-go/internal/testutil"
	"github.com/guoyyit/genome-liftover-go/liftover"
)

func denseBatchFixture(t *testing.T) (*liftover.Mapper, testutil.Spec) {
	t.Helper()
	spec := testutil.Spec{
		TargetChrom: "chr1",
		QueryChrom:  "chrA",
		TargetSize:  1_000_000,
		QuerySize:   2_000_000,
		Chains:      2000,
		MinSpan:     40_000,
		MaxSpan:     160_000,
		MaxBlocks:   6,
		MinusEvery:  3,
		ScoreSteps:  8,
		Seed:        99,
	}
	chains := testutil.DenseChains(spec)
	for _, c := range chains {
		if err := chain.Validate(c); err != nil {
			t.Fatalf("fixture chain id=%d invalid: %v", c.ID, err)
		}
	}
	return liftover.NewMapper(index.New(chains)), spec
}

// TestDenseBatchConcurrencyEquivalence runs a dense batch with 1 and 8
// workers and requires byte-identical output, identical stats, input-order
// output rows, and stats that match a direct per-record mapper reference.
func TestDenseBatchConcurrencyEquivalence(t *testing.T) {
	m, spec := denseBatchFixture(t)
	rng := rand.New(rand.NewSource(5))

	var input strings.Builder
	type rec struct {
		chrom      string
		start, end int
		malformed  bool
	}
	var recs []rec
	for i := 0; i < 600; i++ {
		s := rng.Intn(spec.TargetSize - 20_000)
		e := s + 1 + rng.Intn(20_000)
		fmt.Fprintf(&input, "%s\t%d\t%d\tr%d\n", spec.TargetChrom, s, e, i)
		recs = append(recs, rec{chrom: spec.TargetChrom, start: s, end: e})
	}
	// Records that must not abort the batch.
	fmt.Fprintf(&input, "chrZ\t0\t100\tno-chain\n")
	recs = append(recs, rec{chrom: "chrZ", start: 0, end: 100})
	fmt.Fprintf(&input, "chr1\tnotanumber\t100\n")
	recs = append(recs, rec{malformed: true})
	fmt.Fprintf(&input, "onlyonefield\n")
	recs = append(recs, rec{malformed: true})

	run := func(workers int) (string, string, Stats) {
		var out, errs bytes.Buffer
		stats, err := Run(m, strings.NewReader(input.String()), &out, &errs, Options{
			Direction: liftover.TargetToQuery,
			Workers:   workers,
		})
		if err != nil {
			t.Fatal(err)
		}
		return out.String(), errs.String(), stats
	}

	out1, err1, stats1 := run(1)
	out8, err8, stats8 := run(8)
	if out1 != out8 {
		t.Fatal("8-worker output differs from single-worker output on dense data")
	}
	if err1 != err8 {
		t.Fatal("8-worker error stream differs from single-worker")
	}
	if stats1 != stats8 {
		t.Fatalf("stats differ between worker counts: %+v vs %+v", stats1, stats8)
	}

	// Reference classification straight from the mapper, in input order.
	var want Stats
	for _, r := range recs {
		if r.malformed {
			want.RecordErrs++
			want.Total++
			continue
		}
		res, err := m.Map(r.chrom, r.start, r.end, liftover.TargetToQuery)
		if err != nil {
			t.Fatal(err)
		}
		want.Total++
		switch classify(res) {
		case "mapped":
			want.Mapped++
		case "partial":
			want.Partial++
		default:
			want.Unmapped++
		}
	}
	if stats1 != want {
		t.Fatalf("batch stats %+v do not match mapper reference %+v", stats1, want)
	}
	if stats1.Unmapped == 0 || stats1.Partial == 0 || stats1.Mapped == 0 {
		t.Fatalf("dense batch should exercise mapped/partial/unmapped, got %+v", stats1)
	}

	// Output rows must appear in input record order: r0's rows first, then
	// r1's, and so on (bad records produce no output rows).
	next := 0
	for _, line := range strings.Split(strings.TrimRight(out1, "\n"), "\n") {
		name := strings.SplitN(line, "\t", 2)[0]
		if name == "no-chain" {
			next = 600
		}
		var got int
		if name != "no-chain" {
			if _, err := fmt.Sscanf(name, "r%d", &got); err != nil {
				t.Fatalf("unexpected output row name %q", name)
			}
			if got < next {
				t.Fatalf("output order broken: record r%d appeared after r%d", got, next)
			}
			next = got
		}
	}
}
