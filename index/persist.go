package index

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"

	"github.com/guoyyit/genome-liftover-go/chain"
)

const (
	indexFileName = "index.bin"
	indexMagic    = "LIFTIDX1"
	indexVersion  = uint16(1)
)

type hashedWriter struct {
	w io.Writer
	h hash.Hash
}

func (hw *hashedWriter) Write(p []byte) (int, error) {
	hw.h.Write(p)
	return hw.w.Write(p)
}

// Save atomically writes the index to a directory.
func (idx *IntervalIndex) Save(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create index directory: %w", err)
	}
	final := filepath.Join(dir, indexFileName)
	tmp, err := os.CreateTemp(dir, ".index-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp index file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }

	bw := bufio.NewWriterSize(tmp, 1<<20)
	if err := idx.writeTo(bw); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := bw.Flush(); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("flush index: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("sync index: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close index: %w", err)
	}
	if err := os.Rename(tmpName, final); err != nil {
		cleanup()
		return fmt.Errorf("commit index: %w", err)
	}
	if dirFd, err := os.Open(dir); err == nil {
		_ = dirFd.Sync()
		_ = dirFd.Close()
	}
	return nil
}

// Load reads an index previously written with Save.
func Load(dir string) (*IntervalIndex, error) {
	f, err := os.Open(filepath.Join(dir, indexFileName))
	if err != nil {
		return nil, fmt.Errorf("open index: %w", err)
	}
	defer f.Close()
	idx := &IntervalIndex{}
	if err := idx.readFrom(bufio.NewReaderSize(f, 1<<20)); err != nil {
		return nil, err
	}
	return idx, nil
}

// writeTo serializes the index; exported only for tests.
func (idx *IntervalIndex) writeTo(w io.Writer) error {
	hw := &hashedWriter{w: w, h: sha256.New()}
	buf := make([]byte, 8)

	if _, err := io.WriteString(hw, indexMagic); err != nil {
		return err
	}
	if err := binary.Write(hw, binary.LittleEndian, indexVersion); err != nil {
		return err
	}
	if err := binary.Write(hw, binary.LittleEndian, uint64(len(idx.chains))); err != nil {
		return err
	}
	for _, c := range idx.chains {
		if err := writeChain(hw, c, buf); err != nil {
			return err
		}
	}
	sum := hw.h.Sum(nil)
	if _, err := w.Write(sum); err != nil {
		return fmt.Errorf("write checksum: %w", err)
	}
	return nil
}

func writeChain(w io.Writer, c *chain.Chain, buf []byte) error {
	if err := writeUint64(w, uint64(c.Score), buf); err != nil {
		return err
	}
	if err := writeString(w, c.TargetName, buf); err != nil {
		return err
	}
	if err := writeString(w, c.QueryName, buf); err != nil {
		return err
	}
	ints := []int64{
		int64(c.TargetSize), int64(c.TargetStart), int64(c.TargetEnd),
		int64(c.QuerySize), int64(c.QueryStart), int64(c.QueryEnd),
		int64(c.ID), int64(c.TargetStrand), int64(c.QueryStrand),
		int64(len(c.Blocks)),
	}
	for _, v := range ints {
		if err := writeUint64(w, uint64(v), buf); err != nil {
			return err
		}
	}
	for _, b := range c.Blocks {
		for _, v := range []int64{int64(b.TStart), int64(b.QStart), int64(b.Size)} {
			if err := writeUint64(w, uint64(v), buf); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeString(w io.Writer, s string, buf []byte) error {
	if err := writeUint64(w, uint64(len(s)), buf); err != nil {
		return err
	}
	_, err := io.WriteString(w, s)
	return err
}

func writeUint64(w io.Writer, v uint64, buf []byte) error {
	binary.LittleEndian.PutUint64(buf, v)
	_, err := w.Write(buf)
	return err
}

// readFrom deserializes the index; exported only for tests.
func (idx *IntervalIndex) readFrom(r io.Reader) error {
	data, err := io.ReadAll(io.LimitReader(r, 8<<30))
	if err != nil {
		return fmt.Errorf("read index: %w", err)
	}
	if len(data) < len(indexMagic)+2+8+sha256.Size {
		return errors.New("index file is truncated or too small")
	}
	if string(data[:len(indexMagic)]) != indexMagic {
		return errors.New("index file has bad magic; not a liftover index")
	}
	pos := len(indexMagic)
	version := binary.LittleEndian.Uint16(data[pos : pos+2])
	if version != indexVersion {
		return fmt.Errorf("unsupported index version %d (want %d)", version, indexVersion)
	}

	payloadEnd := len(data) - sha256.Size
	wantSum := data[payloadEnd:]
	gotSum := sha256.Sum256(data[:payloadEnd])
	if !bytes.Equal(wantSum, gotSum[:]) {
		return errors.New("index checksum mismatch; index file is corrupt")
	}

	d := &decoder{data: data[:payloadEnd], pos: pos + 2}
	n := d.uint64()
	chains := make([]*chain.Chain, 0, n)
	for i := uint64(0); i < n; i++ {
		chains = append(chains, d.chain())
	}
	if d.err != nil {
		return d.err
	}
	if d.pos != payloadEnd {
		return errors.New("index payload has trailing bytes")
	}
	idx.chains = chains
	idx.target = make(map[string]*chromIndex)
	idx.query = make(map[string]*chromIndex)
	targetChains := make(map[string][]*chain.Chain)
	queryChains := make(map[string][]*chain.Chain)
	for _, c := range chains {
		if err := chain.Validate(c); err != nil {
			return fmt.Errorf("loaded chain id=%d is invalid: %w", c.ID, err)
		}
		targetChains[c.TargetName] = append(targetChains[c.TargetName], c)
		queryChains[c.QueryName] = append(queryChains[c.QueryName], c)
	}
	for chrom, list := range targetChains {
		idx.target[chrom] = buildChromIndex(list)
	}
	for chrom, list := range queryChains {
		idx.query[chrom] = buildQueryChromIndex(list)
	}
	return nil
}

type decoder struct {
	data []byte
	pos  int
	err  error
}

func (d *decoder) bytes(n int) []byte {
	if d.err != nil {
		return nil
	}
	if d.pos+n > len(d.data) || n < 0 {
		d.err = errors.New("index payload is truncated")
		return nil
	}
	b := d.data[d.pos : d.pos+n]
	d.pos += n
	return b
}

func (d *decoder) uint64() uint64 {
	b := d.bytes(8)
	if b == nil {
		return 0
	}
	return binary.LittleEndian.Uint64(b)
}

func (d *decoder) signed() int64 { return int64(d.uint64()) }

func (d *decoder) str() string {
	n := d.uint64()
	if n > uint64(len(d.data)) {
		d.err = errors.New("index string length is implausible")
		return ""
	}
	return string(d.bytes(int(n)))
}

func (d *decoder) chain() *chain.Chain {
	c := &chain.Chain{
		Score:        int64(d.uint64()),
		TargetName:   d.str(),
		QueryName:    d.str(),
		TargetSize:   int(d.signed()),
		TargetStart:  int(d.signed()),
		TargetEnd:    int(d.signed()),
		QuerySize:    int(d.signed()),
		QueryStart:   int(d.signed()),
		QueryEnd:     int(d.signed()),
		ID:           d.signed(),
		TargetStrand: chain.Strand(d.signed()),
		QueryStrand:  chain.Strand(d.signed()),
	}
	n := d.uint64()
	if n > uint64(len(d.data)) {
		d.err = errors.New("index block count is implausible")
		return c
	}
	c.Blocks = make([]chain.AlignBlock, n)
	for i := range c.Blocks {
		c.Blocks[i] = chain.AlignBlock{
			TStart: int(d.signed()),
			QStart: int(d.signed()),
			Size:   int(d.signed()),
		}
	}
	return c
}
