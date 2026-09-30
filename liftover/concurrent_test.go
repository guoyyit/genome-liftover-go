package liftover

import (
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/guoyyit/genome-liftover-go/chain"
)

// mustChain 解析文本中的全部链并逐个加入 Builder 的辅助函数。
func mustChain(t *testing.T, data string) *chain.Chain {
	t.Helper()
	var first *chain.Chain
	err := chain.Parse(strings.NewReader(data), func(c *chain.Chain) error {
		if first == nil {
			first = c
		}
		return nil
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return first
}

// TestConcurrentQueriesConsistent 验证索引可并发查询且结果与单线程一致；
// 并发期间其他 Builder 的构建活动不影响已建成的索引。
func TestConcurrentQueriesConsistent(t *testing.T) {
	data := plusChain + minusChain +
		"chain 1000 chr1 1000 + 100 200 chr3 3000 + 500 600 7\n100\n\n"
	ix := buildIndex(t, data)

	queries := []Interval{
		{Name: "chr1", Start: 100, End: 200},
		{Name: "chr1", Start: 50, End: 250},
		{Name: "chr1", Start: 150, End: 160},
		{Name: "chr2", Start: 300, End: 400},
		{Name: "chr2", Start: 1600, End: 1700},
	}
	want := make([][]Result, len(queries))
	for i, q := range queries {
		want[i] = []Result{ix.Map(q, Forward), ix.Map(q, Reverse)}
	}

	var wg sync.WaitGroup
	// 后台并发构建新索引，模拟构建与查询并发的场景
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			b := NewBuilder()
			_ = b.Add(mustChain(t, data))
			_ = b.Build()
		}
	}()

	const workers = 8
	const rounds = 200
	errCh := make(chan string, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for r := 0; r < rounds; r++ {
				q := queries[(id+r)%len(queries)]
				for dir := 0; dir < 2; dir++ {
					got := ix.Map(q, Direction(dir))
					if !reflect.DeepEqual(got, want[(id+r)%len(queries)][dir]) {
						errCh <- "inconsistent result"
						return
					}
				}
			}
		}(w)
	}
	wg.Wait()
	close(errCh)
	for msg := range errCh {
		t.Fatal(msg)
	}
}

// TestRoundTripSweep 对链覆盖区做系统扫描：正向映射的每个片段反向映射后
// 必须精确恢复其源子区间。
func TestRoundTripSweep(t *testing.T) {
	for _, data := range []string{plusChain, minusChain} {
		ix := buildIndex(t, data)
		for start := 0; start < 1000; start += 7 {
			q := Interval{Name: "chr1", Start: start, End: start + 13}
			fwd := ix.Map(q, Forward)
			for _, s := range fwd.Segments {
				rev := ix.Map(s.Target, Reverse)
				if rev.Status != StatusMapped || len(rev.Segments) != 1 {
					t.Fatalf("query %+v segment %+v: reverse = %+v", q, s, rev)
				}
				rs := rev.Segments[0]
				if rs.Target.Name != "chr1" || rs.Target.Start != s.SrcStart || rs.Target.End != s.SrcEnd {
					t.Fatalf("round trip mismatch: query %+v segment %+v -> %+v", q, s, rs)
				}
			}
		}
	}
}
