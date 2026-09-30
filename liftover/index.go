package liftover

import (
	"fmt"
	"sort"

	"github.com/guoyyit/genome-liftover-go/chain"
)

// Builder 累积比对链并构建不可变索引。Builder 不可并发使用；
// 构建出的 Index 与 Builder 互不影响。
type Builder struct {
	chains []*chain.Chain
}

// NewBuilder 创建一个空的索引构建器。
func NewBuilder() *Builder {
	return &Builder{}
}

// Add 向构建器加入一条链。链必须已通过 chain.Parse 的完整性校验。
func (b *Builder) Add(c *chain.Chain) error {
	if c == nil || len(c.Blocks) == 0 {
		return fmt.Errorf("liftover: cannot add empty chain")
	}
	b.chains = append(b.chains, c)
	return nil
}

// Build 冻结当前链集合，返回可并发安全查询的不可变索引。
// 返回的 Index 不持有 Builder 的任何可变状态。
func (b *Builder) Build() *Index {
	chains := make([]*chain.Chain, len(b.chains))
	copy(chains, b.chains)
	ix := &Index{
		chains: chains,
		fwd:    make(map[string]*chromIndex),
		rev:    make(map[string]*chromIndex),
	}
	for _, c := range chains {
		if name, start, end := srcInterval(c, Forward); true {
			ix.fwd[name] = appendEntry(ix.fwd[name], chainEntry{chain: c, srcStart: start, srcEnd: end})
		}
		if name, start, end := srcInterval(c, Reverse); true {
			ix.rev[name] = appendEntry(ix.rev[name], chainEntry{chain: c, srcStart: start, srcEnd: end})
		}
	}
	for _, ci := range ix.fwd {
		ci.finish()
	}
	for _, ci := range ix.rev {
		ci.finish()
	}
	return ix
}

// Index 是不可变的区间映射索引，构建完成后可并发查询。
// 所有查询只读取构建期确定的数据，不存在撕裂状态。
type Index struct {
	chains []*chain.Chain
	fwd    map[string]*chromIndex // 以 target 染色体名称为键
	rev    map[string]*chromIndex // 以 query 染色体名称为键
}

// Map 把查询区间映射到目标版本坐标。
func (ix *Index) Map(q Interval, dir Direction) Result {
	return ix.mapInterval(q, dir)
}

// lookup 返回与 [start,end) 重叠的候选链，按 srcStart 升序。
func (ix *Index) lookup(name string, start, end int, dir Direction) []chainEntry {
	m := ix.fwd
	if dir == Reverse {
		m = ix.rev
	}
	ci, ok := m[name]
	if !ok {
		return nil
	}
	return ci.query(start, end)
}

// srcInterval 计算链在指定方向上的源侧区间（源染色体正链坐标）。
func srcInterval(c *chain.Chain, dir Direction) (name string, start, end int) {
	if dir == Forward {
		return c.TName, c.TStart, c.TEnd
	}
	if c.QStrand == '+' {
		return c.QName, c.QStart, c.QEnd
	}
	return c.QName, c.QSize - c.QEnd, c.QSize - c.QStart
}

// chainEntry 是索引中按源侧区间排序的链条目。
type chainEntry struct {
	chain    *chain.Chain
	srcStart int
	srcEnd   int
}

// chromIndex 是单条染色体的链索引：按 srcStart 排序，
// maxEnd 为 srcEnd 的前缀最大值，用于二分剪枝。
type chromIndex struct {
	entries []chainEntry
	maxEnd  []int
}

func appendEntry(ci *chromIndex, e chainEntry) *chromIndex {
	if ci == nil {
		ci = &chromIndex{}
	}
	ci.entries = append(ci.entries, e)
	return ci
}

func (ci *chromIndex) finish() {
	sort.Slice(ci.entries, func(i, j int) bool {
		a, b := ci.entries[i], ci.entries[j]
		if a.srcStart != b.srcStart {
			return a.srcStart < b.srcStart
		}
		if a.srcEnd != b.srcEnd {
			return a.srcEnd < b.srcEnd
		}
		return a.chain.ID < b.chain.ID
	})
	ci.maxEnd = make([]int, len(ci.entries))
	m := 0
	for i, e := range ci.entries {
		if e.srcEnd > m {
			m = e.srcEnd
		}
		ci.maxEnd[i] = m
	}
}

// query 返回所有与 [start,end) 重叠的条目，时间复杂度 O(log n + k)。
func (ci *chromIndex) query(start, end int) []chainEntry {
	n := len(ci.entries)
	// lo：首个 maxEnd > start 的下标，之前的条目 srcEnd 都 <= start。
	lo := sort.Search(n, func(i int) bool { return ci.maxEnd[i] > start })
	// hi：首个 srcStart >= end 的下标，之后的条目不与查询重叠。
	hi := sort.Search(n, func(i int) bool { return ci.entries[i].srcStart >= end })
	if lo > hi {
		lo = hi
	}
	var out []chainEntry
	for _, e := range ci.entries[lo:hi] {
		if e.srcEnd > start && e.srcStart < end {
			out = append(out, e)
		}
	}
	return out
}
