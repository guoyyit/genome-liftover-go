package chain

import (
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func parseAll(t *testing.T, data string) []*Chain {
	t.Helper()
	var out []*Chain
	err := Parse(strings.NewReader(data), func(c *Chain) error {
		out = append(out, c)
		return nil
	})
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	return out
}

func expectErrKind(t *testing.T, data string, kind ErrKind) {
	t.Helper()
	err := Parse(strings.NewReader(data), func(c *Chain) error { return nil })
	if err == nil {
		t.Fatalf("expected error kind %s, got nil", kind)
	}
	var pe *ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("expected *ParseError, got %T: %v", err, err)
	}
	if pe.Kind != kind {
		t.Fatalf("expected kind %s, got %s: %v", kind, pe.Kind, err)
	}
}

const simpleChain = "chain 1000 chr1 1000 + 100 200 chr2 2000 + 300 400 1\n" +
	"50\t10\t10\n" +
	"40\n" +
	"\n"

func TestParseSimpleChain(t *testing.T) {
	chains := parseAll(t, simpleChain)
	if len(chains) != 1 {
		t.Fatalf("expected 1 chain, got %d", len(chains))
	}
	c := chains[0]
	if c.Score != 1000 || c.TName != "chr1" || c.TSize != 1000 || c.TStrand != '+' ||
		c.TStart != 100 || c.TEnd != 200 || c.QName != "chr2" || c.QSize != 2000 ||
		c.QStrand != '+' || c.QStart != 300 || c.QEnd != 400 || c.ID != 1 {
		t.Fatalf("unexpected header: %+v", c.Header)
	}
	if len(c.Blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(c.Blocks))
	}
	b0 := c.Blocks[0]
	if b0.TStart != 100 || b0.TEnd != 150 || b0.QStart != 300 || b0.QEnd != 350 || b0.DT != 10 || b0.DQ != 10 {
		t.Fatalf("unexpected block0: %+v", b0)
	}
	b1 := c.Blocks[1]
	if b1.TStart != 160 || b1.TEnd != 200 || b1.QStart != 360 || b1.QEnd != 400 || b1.DT != 0 || b1.DQ != 0 {
		t.Fatalf("unexpected block1: %+v", b1)
	}
}

func TestParseMinusStrandChain(t *testing.T) {
	data := "chain 500 chr1 1000 + 0 100 chr2 2000 - 500 600 2\n100\n\n"
	chains := parseAll(t, data)
	if len(chains) != 1 {
		t.Fatalf("expected 1 chain, got %d", len(chains))
	}
	if chains[0].QStrand != '-' {
		t.Fatalf("expected qStrand '-', got %q", chains[0].QStrand)
	}
}

func TestParseMultipleChainsAndComments(t *testing.T) {
	data := simpleChain + "# comment\n\n" + simpleChain
	chains := parseAll(t, data)
	if len(chains) != 2 {
		t.Fatalf("expected 2 chains, got %d", len(chains))
	}
}

func TestParseEmptyInput(t *testing.T) {
	chains := parseAll(t, "")
	if len(chains) != 0 {
		t.Fatalf("expected 0 chains, got %d", len(chains))
	}
}

func TestParseTruncated(t *testing.T) {
	expectErrKind(t, "chain 1000 chr1 1000 + 100 200 chr2 2000 + 300 400 1\n50\t10\t5\n", ErrTruncated)
	expectErrKind(t, "chain 1000 chr1 1000 + 100 200 chr2 2000 + 300 400 1\n", ErrTruncated)
	expectErrKind(t, "chain 1000 chr1 1000 + 100 200 chr2 2000 + 300 400 1\n50\t10\t5\nchain 1 chr1 1000 + 0 1 chr2 2000 + 0 1 2\n", ErrTruncated)
}

func TestParseBadHeader(t *testing.T) {
	expectErrKind(t, "chain 1000 chr1 1000 + 100 200 chr2 2000 + 300 400\n100\n", ErrSyntax)
	expectErrKind(t, "chain 1000 chr1 1000 x 100 200 chr2 2000 + 300 400 1\n100\n", ErrSyntax)
	expectErrKind(t, "chain 1000 chr1 1000 - 100 200 chr2 2000 + 300 400 1\n100\n", ErrBadHeader)
	expectErrKind(t, "chain 1000 chr1 1000 + 100 2000 chr2 2000 + 300 400 1\n100\n", ErrBadHeader)
	expectErrKind(t, "chain 1000 chr1 1000 + 100 200 chr2 2000 + 300 3000 1\n100\n", ErrBadHeader)
	expectErrKind(t, "chain abc chr1 1000 + 100 200 chr2 2000 + 300 400 1\n100\n", ErrSyntax)
}

func TestParseBadBlock(t *testing.T) {
	expectErrKind(t, "chain 1000 chr1 1000 + 100 200 chr2 2000 + 300 400 1\n50\n", ErrBadBlock)
	expectErrKind(t, "chain 1000 chr1 1000 + 100 200 chr2 2000 + 300 400 1\n150\n", ErrBadBlock)
	expectErrKind(t, "chain 1000 chr1 1000 + 100 200 chr2 2000 + 300 400 1\n0\n", ErrBadBlock)
	expectErrKind(t, "chain 1000 chr1 1000 + 100 200 chr2 2000 + 300 400 1\n50\t60\t0\n50\n", ErrBadBlock)
}

func TestParseSyntaxErrors(t *testing.T) {
	expectErrKind(t, "100\n", ErrSyntax)
	expectErrKind(t, "chain 1000 chr1 1000 + 100 200 chr2 2000 + 300 400 1\n50\t1\n", ErrSyntax)
	expectErrKind(t, "chain 1000 chr1 1000 + 100 200 chr2 2000 + 300 400 1\n50\t-1\t0\n40\n", ErrSyntax)
	expectErrKind(t, "chain 1000 chr1 1000 + 100 200 chr2 2000 + 300 400 1\nabc\n", ErrSyntax)
}

func TestOpenPlainAndGzip(t *testing.T) {
	dir := t.TempDir()

	var gzBuf bytes.Buffer
	gw := gzip.NewWriter(&gzBuf)
	if _, err := gw.Write([]byte(simpleChain)); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}

	plainPath := filepath.Join(dir, "test.chain")
	gzPath := filepath.Join(dir, "test.chain.gz")
	if err := os.WriteFile(plainPath, []byte(simpleChain), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gzPath, gzBuf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{plainPath, gzPath} {
		rc, err := Open(path)
		if err != nil {
			t.Fatalf("Open(%s): %v", path, err)
		}
		var n int
		err = Parse(rc, func(c *Chain) error {
			n++
			if len(c.Blocks) != 2 {
				t.Fatalf("expected 2 blocks, got %d", len(c.Blocks))
			}
			return nil
		})
		if err != nil {
			t.Fatalf("Parse(%s): %v", path, err)
		}
		if err := rc.Close(); err != nil {
			t.Fatalf("Close(%s): %v", path, err)
		}
		if n != 1 {
			t.Fatalf("%s: expected 1 chain, got %d", path, n)
		}
	}
}
