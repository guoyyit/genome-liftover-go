package batch

import (
	"fmt"
	"strings"
	"testing"

	"github.com/guoyyit/genome-liftover-go/chain"
	"github.com/guoyyit/genome-liftover-go/liftover"
)

const testChains = "chain 1000 chr1 1000 + 100 200 chr2 2000 + 300 400 1\n" +
	"50\t10\t10\n" +
	"40\n" +
	"\n" +
	"chain 800 chr1 1000 + 500 600 chr2 2000 - 1000 1100 2\n" +
	"100\n" +
	"\n"

func testIndex(t *testing.T) *liftover.Index {
	t.Helper()
	b := liftover.NewBuilder()
	err := chain.Parse(strings.NewReader(testChains), func(c *chain.Chain) error {
		return b.Add(c)
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return b.Build()
}

func TestRunBasic(t *testing.T) {
	ix := testIndex(t)
	input := "# comment\n" +
		"\n" +
		"chr1\t100\t150\tfull\n" +
		"chr1\t100\t200\tgapped\n" +
		"chr1\t900\t950\tnowhere\n" +
		"chr1\t500\t600\tminus\n" +
		"bad\tline\n" +
		"chr1\t100\t110\n"
	var out strings.Builder
	stats, err := Run(strings.NewReader(input), &out, ix, liftover.Forward, 1)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.Total != 6 {
		t.Fatalf("expected total 6, got %d", stats.Total)
	}
	if stats.Mapped != 3 || stats.Partial != 1 || stats.Unmapped != 1 || stats.Failed != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if !strings.HasPrefix(lines[0], "#src_name") {
		t.Fatalf("missing header: %q", lines[0])
	}
	body := lines[1:]
	// full: 1 行；gapped: 2 片段 + 1 未映射；nowhere: 1 行；
	// minus: 1 行；bad: 1 error 行；最后一条: 1 行
	if len(body) != 8 {
		t.Fatalf("expected 8 body rows, got %d:\n%s", len(body), out.String())
	}
	// 顺序必须与输入一致
	if !strings.Contains(body[0], "\tfull\tmapped\t") {
		t.Fatalf("row0: %q", body[0])
	}
	if !strings.Contains(body[1], "\tgapped\tpartial\t100\t150\tchr2\t300\t350\t+") {
		t.Fatalf("row1: %q", body[1])
	}
	if !strings.Contains(body[2], "\tgapped\tpartial\t150\t160\t.\t.\t.\t.") {
		t.Fatalf("row2 (unmapped piece): %q", body[2])
	}
	if !strings.Contains(body[3], "\tgapped\tpartial\t160\t200\tchr2\t360\t400\t+") {
		t.Fatalf("row3: %q", body[3])
	}
	if !strings.Contains(body[4], "\tnowhere\tunmapped\t900\t950\t.") {
		t.Fatalf("row4: %q", body[4])
	}
	if !strings.Contains(body[5], "\tminus\tmapped\t500\t600\tchr2\t900\t1000\t-") {
		t.Fatalf("row5: %q", body[5])
	}
	if !strings.Contains(body[6], "\terror\t") {
		t.Fatalf("row6 should be error row: %q", body[6])
	}
	if !strings.Contains(body[7], "chr1\t100\t110\t.\tmapped\t100\t110\tchr2\t300\t310\t+") {
		t.Fatalf("row7: %q", body[7])
	}
}

func TestRunRecordErrorDoesNotAbort(t *testing.T) {
	ix := testIndex(t)
	input := "chr1\tnot-a-number\t150\n" +
		"chr1\t200\t100\n" +
		"chr1\t-5\t10\n" +
		"chr1\t100\t150\tok\n"
	var out strings.Builder
	stats, err := Run(strings.NewReader(input), &out, ix, liftover.Forward, 4)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.Failed != 3 || stats.Mapped != 1 || stats.Total != 4 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	if !strings.Contains(out.String(), "\tok\tmapped\t") {
		t.Fatalf("last record should be mapped:\n%s", out.String())
	}
}

func TestRunConcurrentDeterministic(t *testing.T) {
	ix := testIndex(t)
	var sb strings.Builder
	for i := 0; i < 2000; i++ {
		fmt.Fprintf(&sb, "chr1\t%d\t%d\tr%d\n", i%500, i%500+50, i)
	}
	input := sb.String()

	var single strings.Builder
	stats1, err := Run(strings.NewReader(input), &single, ix, liftover.Forward, 1)
	if err != nil {
		t.Fatalf("Run workers=1: %v", err)
	}
	for _, workers := range []int{2, 4, 16} {
		var multi strings.Builder
		statsN, err := Run(strings.NewReader(input), &multi, ix, liftover.Forward, workers)
		if err != nil {
			t.Fatalf("Run workers=%d: %v", workers, err)
		}
		if single.String() != multi.String() {
			t.Fatalf("workers=%d output differs from workers=1", workers)
		}
		if stats1 != statsN {
			t.Fatalf("workers=%d stats differ: %+v vs %+v", workers, statsN, stats1)
		}
	}
	if stats1.Total != 2000 {
		t.Fatalf("expected 2000 records, got %d", stats1.Total)
	}
}

func TestRunReverseDirection(t *testing.T) {
	ix := testIndex(t)
	input := "chr2\t300\t350\tq\n"
	var out strings.Builder
	stats, err := Run(strings.NewReader(input), &out, ix, liftover.Reverse, 2)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.Mapped != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	if !strings.Contains(out.String(), "\tq\tmapped\t300\t350\tchr1\t100\t150\t+") {
		t.Fatalf("unexpected output:\n%s", out.String())
	}
}
