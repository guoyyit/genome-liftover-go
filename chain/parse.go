package chain

import (
	"bufio"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// ParseErrorKind enumerates distinguishable failure reasons.
type ParseErrorKind int

const (
	ErrMalformed ParseErrorKind = iota + 1
	ErrTruncated
	ErrCorrupt
)

// ParseError describes a rejected chain file with a specific reason.
type ParseError struct {
	Kind    ParseErrorKind
	Line    int
	Message string
}

func (k ParseErrorKind) String() string {
	switch k {
	case ErrMalformed:
		return "malformed"
	case ErrTruncated:
		return "truncated"
	case ErrCorrupt:
		return "corrupt"
	default:
		return "unknown"
	}
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("chain file %s at line %d: %s", e.Kind, e.Line, e.Message)
}

func parseError(kind ParseErrorKind, line int, format string, args ...any) error {
	return &ParseError{Kind: kind, Line: line, Message: fmt.Sprintf(format, args...)}
}

// ParseReader parses chain alignment data. The input may be gzip compressed;
// compression is detected from the gzip magic bytes.
func ParseReader(r io.Reader) ([]*Chain, error) {
	br := bufio.NewReaderSize(r, 1<<20)
	var reader io.Reader = br
	var gz *gzip.Reader

	head, peekErr := br.Peek(2)
	switch {
	case peekErr == nil && len(head) == 2 && head[0] == 0x1f && head[1] == 0x8b:
		g, err := gzip.NewReader(br)
		if err != nil {
			return nil, parseError(ErrCorrupt, 0, "invalid gzip stream: %v", err)
		}
		gz = g
		reader = g
	case peekErr != nil && !errors.Is(peekErr, io.EOF):
		return nil, parseError(ErrMalformed, 0, "cannot read input: %v", peekErr)
	}

	sc := bufio.NewScanner(reader)
	sc.Buffer(make([]byte, 64*1024), 64*1024*1024)

	var chains []*Chain
	var cur *Chain
	var nextT, nextQ int
	lineNo := 0

	finishScan := func() error {
		if err := sc.Err(); err != nil {
			if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
				return parseError(ErrTruncated, lineNo, "input ended in the middle of a record: %v", err)
			}
			return parseError(ErrCorrupt, lineNo, "read failure: %v", err)
		}
		if gz != nil {
			if err := gz.Close(); err != nil {
				if errors.Is(err, io.ErrUnexpectedEOF) {
					return parseError(ErrTruncated, lineNo, "gzip stream ended prematurely: %v", err)
				}
				return parseError(ErrCorrupt, lineNo, "gzip stream verification failed: %v", err)
			}
		}
		if cur != nil {
			return parseError(ErrTruncated, lineNo, "chain id=%d is missing its terminal block line", cur.ID)
		}
		return nil
	}

	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		isComment := strings.HasPrefix(line, "#")
		if line == "" || isComment {
			if cur != nil {
				return nil, parseError(ErrTruncated, lineNo,
					"blank/comment line before chain id=%d was terminated", cur.ID)
			}
			continue
		}

		if strings.HasPrefix(line, "chain") {
			if cur != nil {
				return nil, parseError(ErrMalformed, lineNo, "new header while chain id=%d is unterminated", cur.ID)
			}
			c, err := parseHeader(line, lineNo)
			if err != nil {
				return nil, err
			}
			cur = c
			nextT, nextQ = c.TargetStart, c.QueryStart
			continue
		}

		if cur == nil {
			return nil, parseError(ErrMalformed, lineNo, "data line without a chain header")
		}

		fields := strings.Fields(line)
		size, err := parseIntField(fields[0], lineNo, "block size")
		if err != nil {
			return nil, err
		}
		if size <= 0 {
			return nil, parseError(ErrMalformed, lineNo, "block size must be positive, got %d", size)
		}
		cur.Blocks = append(cur.Blocks, AlignBlock{TStart: nextT, QStart: nextQ, Size: size})
		nextT += size
		nextQ += size

		switch len(fields) {
		case 1:
			if nextT != cur.TargetEnd || nextQ != cur.QueryEnd {
				return nil, parseError(ErrCorrupt, lineNo,
					"terminal block ends at target %d (want %d) / query-chain %d (want %d)",
					nextT, cur.TargetEnd, nextQ, cur.QueryEnd)
			}
			if err := Validate(cur); err != nil {
				return nil, withLine(err, lineNo)
			}
			chains = append(chains, cur)
			cur = nil
		case 3:
			dt, err := parseIntField(fields[1], lineNo, "target gap")
			if err != nil {
				return nil, err
			}
			dq, err := parseIntField(fields[2], lineNo, "query gap")
			if err != nil {
				return nil, err
			}
			if dt < 0 || dq < 0 {
				return nil, parseError(ErrMalformed, lineNo, "gap lengths must be non-negative, got %d %d", dt, dq)
			}
			if dt == 0 && dq == 0 {
				return nil, parseError(ErrMalformed, lineNo, "zero gaps would merge adjacent blocks")
			}
			nextT += dt
			nextQ += dq
			if nextT > cur.TargetEnd || nextQ > cur.QueryEnd {
				return nil, parseError(ErrCorrupt, lineNo, "gap runs past declared chain end")
			}
		default:
			return nil, parseError(ErrMalformed, lineNo, "data line must have 1 or 3 fields, got %d", len(fields))
		}
	}
	if err := finishScan(); err != nil {
		return nil, err
	}
	return chains, nil
}

func parseHeader(line string, lineNo int) (*Chain, error) {
	fields := strings.Fields(line)
	if len(fields) != 13 {
		return nil, parseError(ErrMalformed, lineNo, "header must have 13 fields, got %d", len(fields))
	}
	if fields[0] != "chain" {
		return nil, parseError(ErrMalformed, lineNo, "header must start with the 'chain' keyword")
	}
	score, err := parseInt64Field(fields[1], lineNo, "score")
	if err != nil {
		return nil, err
	}
	tSize, err := parseIntField(fields[3], lineNo, "target size")
	if err != nil {
		return nil, err
	}
	tStrand, err := parseStrand(fields[4], lineNo)
	if err != nil {
		return nil, err
	}
	tStart, err := parseIntField(fields[5], lineNo, "target start")
	if err != nil {
		return nil, err
	}
	tEnd, err := parseIntField(fields[6], lineNo, "target end")
	if err != nil {
		return nil, err
	}
	qSize, err := parseIntField(fields[8], lineNo, "query size")
	if err != nil {
		return nil, err
	}
	qStrand, err := parseStrand(fields[9], lineNo)
	if err != nil {
		return nil, err
	}
	qStart, err := parseIntField(fields[10], lineNo, "query start")
	if err != nil {
		return nil, err
	}
	qEnd, err := parseIntField(fields[11], lineNo, "query end")
	if err != nil {
		return nil, err
	}
	id, err := parseInt64Field(fields[12], lineNo, "chain id")
	if err != nil {
		return nil, err
	}
	c := &Chain{
		Score:        score,
		TargetName:   fields[2],
		TargetSize:   tSize,
		TargetStrand: tStrand,
		TargetStart:  tStart,
		TargetEnd:    tEnd,
		QueryName:    fields[7],
		QuerySize:    qSize,
		QueryStrand:  qStrand,
		QueryStart:   qStart,
		QueryEnd:     qEnd,
		ID:           id,
	}
	if err := validateHeader(c, lineNo); err != nil {
		return nil, err
	}
	return c, nil
}

func validateHeader(c *Chain, lineNo int) error {
	if c.TargetStrand != StrandPlus {
		return parseError(ErrMalformed, lineNo, "target strand must be '+', got %q", c.TargetStrand)
	}
	if c.QueryStrand != StrandPlus && c.QueryStrand != StrandMinus {
		return parseError(ErrMalformed, lineNo, "query strand must be '+' or '-'")
	}
	if c.TargetName == "" || c.QueryName == "" {
		return parseError(ErrMalformed, lineNo, "target and query chromosome names must be non-empty")
	}
	check := func(name string, lo, hi, size int) error {
		if lo < 0 || hi < 0 || size <= 0 {
			return parseError(ErrMalformed, lineNo, "%s coordinates and size must be non-negative (size positive)", name)
		}
		if lo >= hi {
			return parseError(ErrMalformed, lineNo, "%s start %d must be smaller than end %d", name, lo, hi)
		}
		if hi > size {
			return parseError(ErrCorrupt, lineNo, "%s interval [%d,%d) exceeds chromosome size %d", name, lo, hi, size)
		}
		return nil
	}
	if err := check("target", c.TargetStart, c.TargetEnd, c.TargetSize); err != nil {
		return err
	}
	return check("query", c.QueryStart, c.QueryEnd, c.QuerySize)
}

func parseStrand(s string, lineNo int) (Strand, error) {
	if len(s) != 1 || (s[0] != byte(StrandPlus) && s[0] != byte(StrandMinus)) {
		return 0, parseError(ErrMalformed, lineNo, "invalid strand %q", s)
	}
	return Strand(s[0]), nil
}

func parseIntField(s string, lineNo int, what string) (int, error) {
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, parseError(ErrMalformed, lineNo, "invalid integer for %s: %q", what, s)
	}
	return v, nil
}

func parseInt64Field(s string, lineNo int, what string) (int64, error) {
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, parseError(ErrMalformed, lineNo, "invalid integer for %s: %q", what, s)
	}
	return v, nil
}

func withLine(err error, lineNo int) error {
	var pe *ParseError
	if errors.As(err, &pe) {
		if pe.Line == 0 {
			pe.Line = lineNo
		}
		return pe
	}
	return parseError(ErrCorrupt, lineNo, "%v", err)
}

// Validate performs structural and semantic checks on a parsed chain:
// strand legality, coordinate bounds and exact block reconstruction.
func Validate(c *Chain) error {
	if err := validateHeader(c, 0); err != nil {
		return err
	}
	if len(c.Blocks) == 0 {
		return parseError(ErrCorrupt, 0, "chain id=%d has no alignment blocks", c.ID)
	}
	t := c.TargetStart
	q := c.QueryStart
	for i, b := range c.Blocks {
		if b.TStart != t || b.QStart != q {
			return parseError(ErrCorrupt, 0,
				"chain id=%d block %d is not contiguous: got target/query start %d/%d want %d/%d",
				c.ID, i, b.TStart, b.QStart, t, q)
		}
		if b.Size <= 0 {
			return parseError(ErrCorrupt, 0, "chain id=%d block %d has non-positive size", c.ID, i)
		}
		t += b.Size
		q += b.Size
		if i < len(c.Blocks)-1 {
			next := c.Blocks[i+1]
			dt, dq := next.TStart-t, next.QStart-q
			if dt < 0 || dq < 0 || dt+dq == 0 {
				return parseError(ErrCorrupt, 0, "chain id=%d gap before block %d is invalid: %d/%d", c.ID, i+1, dt, dq)
			}
			t, q = next.TStart, next.QStart
		}
	}
	if t != c.TargetEnd || q != c.QueryEnd {
		return parseError(ErrCorrupt, 0,
			"chain id=%d blocks reconstruct ends %d/%d, header declares %d/%d",
			c.ID, t, q, c.TargetEnd, c.QueryEnd)
	}
	return nil
}

// ParseFile parses a chain file. Files ending in .gz or carrying the gzip
// magic bytes are transparently decompressed.
func ParseFile(path string) ([]*Chain, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open chain file %q: %w", path, err)
	}
	defer f.Close()
	chains, err := ParseReader(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return chains, nil
}
