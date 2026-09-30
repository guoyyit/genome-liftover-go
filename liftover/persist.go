package liftover

import (
	"bytes"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/guoyyit/genome-liftover-go/chain"
)

// indexMagic 标识索引文件格式与版本。
const indexMagic = "GLIFIDX1"

// ErrNotIndex 表示文件不是本工具的索引文件。
var ErrNotIndex = errors.New("liftover: not a liftover index file")

type indexPayload struct {
	Chains []*chain.Chain
}

// Save 把索引持久化到 path。写入先落到临时文件再原子改名，
// 中途失败不会损坏目标路径上已有的索引文件。
func (ix *Index) Save(path string) error {
	var buf bytes.Buffer
	buf.WriteString(indexMagic)
	if err := gob.NewEncoder(&buf).Encode(indexPayload{Chains: ix.chains}); err != nil {
		return fmt.Errorf("liftover: encode index: %w", err)
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".liftover-*.tmp")
	if err != nil {
		return fmt.Errorf("liftover: create temp file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func(err error) error {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		return cleanup(fmt.Errorf("liftover: write index: %w", err))
	}
	if err := tmp.Sync(); err != nil {
		return cleanup(fmt.Errorf("liftover: sync index: %w", err))
	}
	if err := tmp.Close(); err != nil {
		return cleanup(fmt.Errorf("liftover: close index: %w", err))
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("liftover: rename index: %w", err)
	}
	return nil
}

// LoadIndex 从 path 加载索引，加载结果与新建索引行为一致。
func LoadIndex(path string) (*Index, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	magic := make([]byte, len(indexMagic))
	if _, err := io.ReadFull(f, magic); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, ErrNotIndex
		}
		return nil, err
	}
	if string(magic) != indexMagic {
		return nil, ErrNotIndex
	}
	var payload indexPayload
	if err := gob.NewDecoder(f).Decode(&payload); err != nil {
		return nil, fmt.Errorf("liftover: decode index: %w", err)
	}
	b := NewBuilder()
	for _, c := range payload.Chains {
		if err := b.Add(c); err != nil {
			return nil, fmt.Errorf("liftover: corrupt index: %w", err)
		}
	}
	return b.Build(), nil
}
