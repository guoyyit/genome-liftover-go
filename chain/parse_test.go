package chain

import (
	"bytes"
	"compress/gzip"
	"errors"
	"strings"
	"testing"
)

const validPlusChain = `chain 100 chr1 1000 + 100 300 chrA 900 + 50 250 1
10 5 5
185

chain 80 chr2 500 + 0 100 chrB 500 - 100 200 2
40 10 10
50
`

func TestParseReaderValid(t *testing.T) {
	chains, err := ParseReader(strings.NewReader(validPlusChain))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chains) != 2 {
		t.Fatalf("want 2 chains, got %d", len(chains))
	}
	c := chains[0]
	if c.ID != 1 || c.Score != 100 || c.TargetName != "chr1" || c.QueryName != "chrA" {
		t.Fatalf("header fields wrong: %+v", c)
	}
	if len(c.Blocks) != 2 || c.Blocks[0] != (AlignBlock{TStart: 100, QStart: 50, Size: 10}) {
		t.Fatalf("blocks wrong: %+v", c.Blocks)
	}
	if c.Blocks[1] != (AlignBlock{TStart: 115, QStart: 65, Size: 185}) {
		t.Fatalf("second block wrong: %+v", c.Blocks[1])
	}
	m := chains[1]
	if m.QueryStrand != StrandMinus || m.QueryStart != 100 || m.QueryEnd != 200 {
		t.Fatalf("minus strand header wrong: %+v", m)
	}
	if len(m.Blocks) != 2 || m.Blocks[1] != (AlignBlock{TStart: 50, QStart: 150, Size: 50}) {
		t.Fatalf("minus strand blocks wrong: %+v", m.Blocks)
	}
}

func TestParseReaderGzip(t *testing.T) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	if _, err := gw.Write([]byte(validPlusChain)); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	chains, err := ParseReader(&buf)
	if err != nil {
		t.Fatalf("gzip parse failed: %v", err)
	}
	if len(chains) != 2 {
		t.Fatalf("want 2 chains from gzip, got %d", len(chains))
	}
}

func TestParseReaderCommentLines(t *testing.T) {
	input := "# top comment\n\n" + validPlusChain + "# trailing\n"
	chains, err := ParseReader(strings.NewReader(input))
	if err != nil {
		t.Fatalf("comments must be accepted: %v", err)
	}
	if len(chains) != 2 {
		t.Fatalf("want 2 chains, got %d", len(chains))
	}
}

func TestParseReaderErrors(t *testing.T) {
	cases := map[string]struct {
		input string
		kind  ParseErrorKind
	}{
		"malformed header field count": {
			"chain 100 chr1 1000 + 100 300 chrA 900 + 50 250\n10\n",
			ErrMalformed,
		},
		"malformed integer": {
			"chain 10x chr1 1000 + 100 300 chrA 900 + 50 250 1\n10\n",
			ErrMalformed,
		},
		"minus target strand": {
			"chain 1 chr1 1000 - 100 300 chrA 900 + 50 250 1\n10\n",
			ErrMalformed,
		},
		"start >= end": {
			"chain 1 chr1 1000 + 300 300 chrA 900 + 50 250 1\n",
			ErrMalformed,
		},
		"truncated chain": {
			"chain 1 chr1 1000 + 100 300 chrA 900 + 50 250 1\n10 5 5\n",
			ErrTruncated,
		},
		"data line without header": {
			"10\n",
			ErrMalformed,
		},
		"bad data field count": {
			"chain 1 chr1 1000 + 100 300 chrA 900 + 50 250 1\n10 5\n",
			ErrMalformed,
		},
		"terminal ends short": {
			"chain 1 chr1 1000 + 100 300 chrA 900 + 50 250 1\n15\n",
			ErrCorrupt,
		},
		"end exceeds size": {
			"chain 1 chr1 1000 + 100 1300 chrA 900 + 50 250 1\n10\n",
			ErrCorrupt,
		},
		"gap past end": {
			"chain 1 chr1 1000 + 100 200 chrA 900 + 50 150 1\n10 200 0\n10\n",
			ErrCorrupt,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseReader(strings.NewReader(tc.input))
			var pe *ParseError
			if !errors.As(err, &pe) {
				t.Fatalf("want ParseError, got %v", err)
			}
			if pe.Kind != tc.kind {
				t.Fatalf("want kind %s, got %s (%v)", tc.kind, pe.Kind, err)
			}
		})
	}
}

func TestParseReaderGzipTruncated(t *testing.T) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	gw.Write([]byte(strings.Repeat(validPlusChain, 200)))
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()
	_, err := ParseReader(bytes.NewReader(raw[:len(raw)-8]))
	var pe *ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("want ParseError for truncated gzip, got %v", err)
	}
	if pe.Kind != ErrTruncated && pe.Kind != ErrCorrupt {
		t.Fatalf("want truncated/corrupt gzip error, got %s: %v", pe.Kind, err)
	}
}
