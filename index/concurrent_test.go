package index

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/guoyyit/genome-liftover-go/chain"
)

func TestConcurrentBuildQueryAndSave(t *testing.T) {
	chains, err := chain.ParseReader(strings.NewReader(persistInput))
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	var firstHits []ChainHit
	var mu sync.Mutex

	// Concurrent builders: each goroutine builds an independent index.
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			idx := New(chains)
			hits := idx.Query("chr1", 100, 200)
			mu.Lock()
			if firstHits == nil {
				firstHits = hits
			} else if len(hits) != len(firstHits) {
				t.Errorf("builder %d hit count drift: %d vs %d", g, len(hits), len(firstHits))
			}
			dir := filepath.Join(t.TempDir(), "idx")
			if err := idx.Save(dir); err != nil {
				t.Errorf("concurrent save: %v", err)
				return
			}
			loaded, err := Load(dir)
			if err != nil {
				t.Errorf("concurrent load: %v", err)
				return
			}
			if len(loaded.Query("chr1", 100, 200)) != len(hits) {
				t.Errorf("reloaded index mismatch in builder %d", g)
			}
			mu.Unlock()
		}(g)
	}

	// Concurrent readers on a shared index.
	shared := New(chains)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				hits := shared.Query("chr1", 100+(i%50), 200)
				if len(hits) != 1 || hits[0].Chain.ID != 7 {
					t.Errorf("reader %d iteration %d unexpected hits %+v", g, i, hits)
					return
				}
			}
		}(g)
	}
	wg.Wait()
}
