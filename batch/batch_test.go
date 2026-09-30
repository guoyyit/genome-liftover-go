package batch

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/guoyyit/genome-liftover-go/chain"
	"github.com/guoyyit/genome-liftover-go/index"
	"github.com/guoyyit/genome-liftover-go/liftover"
)

// target [0,200) with target gap [50,100); query+ [0,150) gapless.
const batchChains = `chain 100 chr1 1000 + 0 200 chrA 1000 + 0 150 1
50 50 0
100
chain 90 chr2 1000 + 0 100 chrB 1000 - 100 200 2
100
`

func buildIdx(t *testing.T) *index.IntervalIndex {
	t.Helper()
	chains, err := chain.ParseReader(strings.NewReader(batchChains))
	if err != nil {
		t.Fatal(err)
	}
	return index.New(chains)
}

func mustParse(t *testing.T, input string) []*chain.Chain {
	t.Helper()
	chains, err := chain.ParseReader(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	return chains
}

func TestRunDeterministicAndConcurrentEqual(t *testing.T) {
	var input strings.Builder
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&input, "chr1\t%d\t%d\tname%d\n", i, i+60, i)
	}
	input.WriteString("chr1\tnope\t10\tbad-int\n")
	input.WriteString("chr1\t10\t50\toverlaps-gap\n")
	input.WriteString("chrZ\t0\t10\tno-chain\n")
	input.WriteString("onlyonefield\n")
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&input, "chr2\t%d\t%d\trev%d\n", i, i+30, i)
	}

	run := func(workers int) (string, string, Stats) {
		m := liftover.NewMapper(buildIdx(t))
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
	outN, errN, statsN := run(8)
	if out1 != outN {
		t.Fatal("concurrent output differs from single-threaded output")
	}
	if err1 != errN {
		t.Fatal("concurrent error output differs")
	}
	if stats1 != statsN {
		t.Fatalf("stats differ: %+v vs %+v", stats1, statsN)
	}

	// 400 normal + 2 malformed + 1 unmapped-chrom + 1 partial = 404 total.
	if stats1.Total != 404 || stats1.RecordErrs != 2 {
		t.Fatalf("unexpected stats: %+v", stats1)
	}
	if stats1.Mapped == 0 || stats1.Partial == 0 || stats1.Unmapped == 0 {
		t.Fatalf("expect mapped/partial/unmapped all present: %+v", stats1)
	}

	// Output rows must be grouped per record in input order: first row must
	// belong to name0.
	firstLine := strings.SplitN(out1, "\n", 2)[0]
	if !strings.HasPrefix(firstLine, "name0\t") {
		t.Fatalf("output order broken, first row: %q", firstLine)
	}
	if !strings.Contains(err1, "need at least 3") || !strings.Contains(err1, "start/end must be integers") {
		t.Fatalf("error output missing expected messages:\n%s", err1)
	}
}

func TestRunAllCandidates(t *testing.T) {
	const chainsTwo = `chain 100 chr1 1000 + 0 100 chrA 1000 + 0 100 1
100
chain 200 chr1 1000 + 0 100 chrB 1000 + 0 100 2
100
`
	idx := index.New(mustParse(t, chainsTwo))
	var out, errs bytes.Buffer
	stats, err := Run(liftover.NewMapper(idx), strings.NewReader("chr1\t10\t20\tq\n"),
		&out, &errs, Options{Direction: liftover.TargetToQuery, Workers: 2, IncludeAllCandidates: true})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Mapped != 1 || !strings.Contains(out.String(), "candidate=1") || !strings.Contains(out.String(), "candidate=2") {
		t.Fatalf("expected both candidates emitted, stats=%+v out=%s", stats, out.String())
	}
}
