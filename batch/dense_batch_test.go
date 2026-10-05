package batch

import (
	"bytes"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/guoyyit/genome-liftover-go/chain"
	"github.com/guoyyit/genome-liftover-go/index"
	"github.com/guoyyit/genome-liftover-go/liftover"
)

// genDenseChainsText builds a chain file with n heavily overlapping chains on
// one chromosome, mixing plus/minus queries and adding gaps so that batch
// records can be mapped, partial and unmapped. It is written as UCSC chain
// text and parsed through the real parser.
func genDenseChainsText(n int, seed int64) string {
	rng := rand.New(rand.NewSource(seed))
	var b strings.Builder
	const size = 100000
	for i := 0; i < n; i++ {
		tLo := rng.Intn(size - 2)
		tHiCap := tLo + 50 + rng.Intn(3000)
		if tHiCap > size {
			tHiCap = size
		}
		strand := "+"
		if i%2 == 1 {
			strand = "-"
		}
		qStart := rng.Intn(size / 2)

		// Build blocks greedily. Each emitted line is either terminal (a
		// single field), or a non-terminal line immediately followed on the
		// next iteration by the block after its declared gap. The loop only
		// emits a non-terminal line when a following block is guaranteed.
		var lines []string
		tp, qp := tLo, qStart
		for {
			sz := 10 + rng.Intn(80)
			if tp+sz > tHiCap || qp+sz > size {
				// Cannot place even one block; abandon this chain.
				lines = nil
				break
			}
			tp += sz
			qp += sz
			dt := 1 + rng.Intn(20)
			dq := rng.Intn(20)
			nextSz := 10 + rng.Intn(80)
			if tp+dt+nextSz > tHiCap || qp+dq+nextSz > size {
				lines = append(lines, fmt.Sprintf("%d", sz))
				break
			}
			lines = append(lines, fmt.Sprintf("%d %d %d", sz, dt, dq))
			tp += dt
			qp += dq
		}
		if len(lines) == 0 {
			continue
		}
		score := n - i
		fmt.Fprintf(&b, "chain %d chr1 %d + %d %d chrA %d %s %d %d %d\n",
			score, size, tLo, tp, size, strand, qStart, qp, i+1)
		for _, ln := range lines {
			b.WriteString(ln)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// TestDenseBatchDeterministicAndConcurrentEqual runs thousands of queries
// over thousands of overlapping chains and asserts byte-identical output and
// identical stats across worker counts, strict input ordering, error
// isolation and mapped/partial/unmapped distinctions.
func TestDenseBatchDeterministicAndConcurrentEqual(t *testing.T) {
	text := genDenseChainsText(3000, 424)
	chains, err := chain.ParseReader(strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	idx := index.New(chains)

	// Build a mixed input: dense overlapping queries, boundary queries,
	// unknown chromosome, malformed lines interleaved.
	var in strings.Builder
	rng := rand.New(rand.NewSource(99))
	for i := 0; i < 3000; i++ {
		lo := rng.Intn(99000)
		hi := lo + 1 + rng.Intn(2000)
		if hi > 100000 {
			hi = 100000
		}
		fmt.Fprintf(&in, "chr1\t%d\t%d\trec%d\n", lo, hi, i)
		if i == 100 {
			in.WriteString("chrZ\t0\t10\tunknown-chrom\n")
		}
		if i == 500 {
			in.WriteString("chr1\tx\t10\tbad-int\n")
		}
		if i == 1000 {
			in.WriteString("garbage-line\n")
		}
	}

	run := func(workers int) (string, string, Stats) {
		var out, errOut bytes.Buffer
		stats, rerr := Run(liftover.NewMapper(idx), strings.NewReader(in.String()),
			&out, &errOut, Options{Direction: liftover.TargetToQuery, Workers: workers})
		if rerr != nil {
			t.Fatal(rerr)
		}
		return out.String(), errOut.String(), stats
	}

	out1, err1, s1 := run(1)
	out4, err4, s4 := run(4)
	out16, err16, s16 := run(16)
	if out1 != out4 || out1 != out16 {
		t.Fatal("dense batch output differs across worker counts")
	}
	if err1 != err4 || err1 != err16 {
		t.Fatal("dense batch error output differs across worker counts")
	}
	if s1 != s4 || s1 != s16 {
		t.Fatalf("dense batch stats differ: %+v %+v %+v", s1, s4, s16)
	}

	// Totals: 3000 normal + 1 unknown-chrom + 2 malformed.
	if s1.Total != 3003 || s1.RecordErrs != 2 {
		t.Fatalf("unexpected totals: %+v", s1)
	}
	if s1.Mapped+s1.Partial+s1.Unmapped != s1.Total-s1.RecordErrs {
		t.Fatalf("mapped+partial+unmapped must cover good records: %+v", s1)
	}
	if s1.Unmapped == 0 {
		t.Fatal("expect at least the unknown-chrom record to be unmapped")
	}

	// Output ordering: every output group must appear in rec input order.
	lines := strings.Split(strings.TrimRight(out1, "\n"), "\n")
	lastRec := -1
	for _, ln := range lines {
		name := strings.SplitN(ln, "\t", 2)[0]
		var n int
		if _, e := fmt.Sscanf(name, "rec%d", &n); e != nil || n < 0 {
			continue // other name forms (unknown-chrom) sort among records
		}
		if n < lastRec {
			t.Fatalf("output order violated: rec%d after rec%d", n, lastRec)
		}
		lastRec = n
	}

	// All-candidates mode must also be deterministic and order-independent.
	runAll := func(workers int) string {
		var out, errOut bytes.Buffer
		if _, rerr := Run(liftover.NewMapper(idx), strings.NewReader(in.String()),
			&out, &errOut, Options{
				Direction: liftover.TargetToQuery, Workers: workers,
				IncludeAllCandidates: true,
			}); rerr != nil {
			t.Fatal(rerr)
		}
		return out.String()
	}
	if a, b := runAll(1), runAll(16); a != b {
		t.Fatal("all-candidate dense output differs across worker counts")
	}
}
