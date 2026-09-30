package chain

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// Parse 以流式方式解析 chain 文件，每解析完一条链调用 fn。
// 任一记录损坏、截断或非法都会返回 *ParseError，不会静默跳过。
// 空行与以 '#' 开头的注释行会被显式忽略，其余非法内容一律报错。
func Parse(r io.Reader, fn func(*Chain) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	lineNo := 0
	var cur *Chain
	var tPos, qPos int

	flush := func() error {
		if tPos != cur.TEnd || qPos != cur.QEnd {
			return &ParseError{Kind: ErrBadBlock, Line: lineNo,
				Msg: fmt.Sprintf("blocks end at (%d,%d) but header declares (%d,%d)",
					tPos, qPos, cur.TEnd, cur.QEnd)}
		}
		c := cur
		cur = nil
		return fn(c)
	}

	for sc.Scan() {
		lineNo++
		trimmed := strings.TrimSpace(strings.TrimRight(sc.Text(), "\r"))
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		fields := strings.Fields(trimmed)
		if fields[0] == "chain" {
			if cur != nil {
				return &ParseError{Kind: ErrTruncated, Line: lineNo,
					Msg: "new chain header before previous chain completed"}
			}
			h, err := parseHeader(fields, lineNo)
			if err != nil {
				return err
			}
			cur = &Chain{Header: *h}
			tPos, qPos = h.TStart, h.QStart
			continue
		}
		if cur == nil {
			return &ParseError{Kind: ErrSyntax, Line: lineNo,
				Msg: "block data outside of any chain"}
		}
		nums := make([]int, len(fields))
		for i, f := range fields {
			n, err := strconv.Atoi(f)
			if err != nil || n < 0 {
				return &ParseError{Kind: ErrSyntax, Line: lineNo,
					Msg: fmt.Sprintf("invalid block number %q", f)}
			}
			nums[i] = n
		}
		if len(nums) != 1 && len(nums) != 3 {
			return &ParseError{Kind: ErrSyntax, Line: lineNo,
				Msg: fmt.Sprintf("block line must have 1 or 3 numbers, got %d", len(nums))}
		}
		size := nums[0]
		if size <= 0 {
			return &ParseError{Kind: ErrBadBlock, Line: lineNo, Msg: "block size must be positive"}
		}
		dt, dq := 0, 0
		if len(nums) == 3 {
			dt, dq = nums[1], nums[2]
		}
		blk := Block{TStart: tPos, TEnd: tPos + size, QStart: qPos, QEnd: qPos + size, DT: dt, DQ: dq}
		if blk.TEnd > cur.TEnd || blk.QEnd > cur.QEnd {
			return &ParseError{Kind: ErrBadBlock, Line: lineNo,
				Msg: "block exceeds chain bounds declared in header"}
		}
		cur.Blocks = append(cur.Blocks, blk)
		tPos += size + dt
		qPos += size + dq
		if len(nums) == 1 {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	if cur != nil {
		return &ParseError{Kind: ErrTruncated, Line: lineNo,
			Msg: "unexpected end of input inside chain"}
	}
	return nil
}

// Open 打开 chain 文件，自动按魔数识别 gzip 压缩。
func Open(path string) (io.ReadCloser, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	br := bufio.NewReader(f)
	magic, err := br.Peek(2)
	if err != nil && err != io.EOF {
		f.Close()
		return nil, err
	}
	fr := &fileReader{Reader: br, f: f}
	if len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		gz, err := gzip.NewReader(br)
		if err != nil {
			f.Close()
			return nil, err
		}
		fr.Reader = gz
		fr.gz = gz
	}
	return fr, nil
}

type fileReader struct {
	io.Reader
	f  *os.File
	gz *gzip.Reader
}

func (r *fileReader) Close() error {
	if r.gz != nil {
		r.gz.Close()
	}
	return r.f.Close()
}

func parseHeader(fields []string, lineNo int) (*Header, error) {
	if len(fields) != 13 {
		return nil, &ParseError{Kind: ErrSyntax, Line: lineNo,
			Msg: fmt.Sprintf("chain header must have 13 fields, got %d", len(fields))}
	}
	score, err := strconv.ParseFloat(fields[1], 64)
	if err != nil {
		return nil, &ParseError{Kind: ErrSyntax, Line: lineNo,
			Msg: fmt.Sprintf("invalid score %q", fields[1])}
	}
	nums := make([]int, 0, 8)
	for _, i := range []int{3, 5, 6, 8, 10, 11, 12} {
		n, err := strconv.Atoi(fields[i])
		if err != nil || n < 0 {
			return nil, &ParseError{Kind: ErrSyntax, Line: lineNo,
				Msg: fmt.Sprintf("invalid header number %q", fields[i])}
		}
		nums = append(nums, n)
	}
	tStrand, err := parseStrand(fields[4], lineNo)
	if err != nil {
		return nil, err
	}
	qStrand, err := parseStrand(fields[9], lineNo)
	if err != nil {
		return nil, err
	}
	if tStrand != '+' {
		return nil, &ParseError{Kind: ErrBadHeader, Line: lineNo,
			Msg: "negative target strand is not supported"}
	}
	h := &Header{
		Score:   score,
		TName:   fields[2],
		TSize:   nums[0],
		TStrand: tStrand,
		TStart:  nums[1],
		TEnd:    nums[2],
		QName:   fields[7],
		QSize:   nums[3],
		QStrand: qStrand,
		QStart:  nums[4],
		QEnd:    nums[5],
		ID:      nums[6],
	}
	if h.TStart >= h.TEnd || h.TEnd > h.TSize {
		return nil, &ParseError{Kind: ErrBadHeader, Line: lineNo,
			Msg: fmt.Sprintf("invalid target range [%d,%d) with size %d", h.TStart, h.TEnd, h.TSize)}
	}
	if h.QStart >= h.QEnd || h.QEnd > h.QSize {
		return nil, &ParseError{Kind: ErrBadHeader, Line: lineNo,
			Msg: fmt.Sprintf("invalid query range [%d,%d) with size %d", h.QStart, h.QEnd, h.QSize)}
	}
	return h, nil
}

func parseStrand(s string, lineNo int) (byte, error) {
	if len(s) != 1 || (s[0] != '+' && s[0] != '-') {
		return 0, &ParseError{Kind: ErrSyntax, Line: lineNo,
			Msg: fmt.Sprintf("invalid strand %q", s)}
	}
	return s[0], nil
}
