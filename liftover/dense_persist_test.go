package liftover

import (
	"bytes"
	"math/rand"
	"path/filepath"
	"sync"
	"testing"

	"github.com/guoyyit/genome-liftover-go/index"
)

// snapshot is a deterministic fingerprint of one Map result.
type snapshot struct {
	chainID  int64
	segments string
}

// fingerprint renders a result to comparable tuples without relying on map
// iteration order (candidates are already score/id ordered).
func fingerprint(r *Result) []snapshot {
	out := make([]snapshot, 0, len(r.Candidates))
	var b bytes.Buffer
	for _, c := range r.Candidates {
		b.Reset()
		for _, s := range c.Segments {
			status := "M"
			if s.Status == StatusUnmapped {
				status = "U"
			}
			b.WriteString(status)
			b.WriteByte(' ')
			writeInt(&b, s.SourceStart)
			b.WriteByte('-')
			writeInt(&b, s.SourceEnd)
			b.WriteByte(' ')
			writeInt(&b, s.DestStart)
			b.WriteByte('-')
			writeInt(&b, s.DestEnd)
			b.WriteByte(' ')
			b.WriteString(s.DestChrom)
			b.WriteByte(' ')
			b.WriteByte(byte(s.Strand))
			b.WriteByte(';')
		}
		out = append(out, snapshot{chainID: c.ChainID, segments: b.String()})
	}
	return out
}

func writeInt(b *bytes.Buffer, n int) {
	const digits = "0123456789"
	if n == 0 {
		b.WriteByte('0')
		return
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = digits[n%10]
		n /= 10
	}
	b.Write(buf[i:])
}

// TestDensePersistReloadEquivalence verifies on dense overlapping chains that
// save -> load produces an index whose Map results are identical to a freshly
// built index: candidate sets, mapped/unmapped segments, strand and
// destination, in both directions.
func TestDensePersistReloadEquivalence(t *testing.T) {
	f := genDenseFixture(2500, 100000, 2024)
	fresh := f.index()
	dir := filepath.Join(t.TempDir(), "idx")
	if err := fresh.Save(dir); err != nil {
		t.Fatal(err)
	}
	loaded, err := index.Load(dir)
	if err := err; err != nil {
		t.Fatal(err)
	}
	mf := NewMapper(fresh)
	ml := NewMapper(loaded)

	compare := func(chrom string, lo, hi int, dir Direction) {
		t.Helper()
		rf, err := mf.Map(chrom, lo, hi, dir)
		if err != nil {
			t.Fatal(err)
		}
		rl, err := ml.Map(chrom, lo, hi, dir)
		if err != nil {
			t.Fatal(err)
		}
		sf, sl := fingerprint(rf), fingerprint(rl)
		if len(sf) != len(sl) {
			t.Fatalf("[%d,%d) dir %d: candidate count %d vs %d",
				lo, hi, dir, len(sf), len(sl))
		}
		for i := range sf {
			if sf[i] != sl[i] {
				t.Fatalf("[%d,%d) dir %d candidate %d:\n fresh %s\nloaded %s",
					lo, hi, dir, i, sf[i].segments, sl[i].segments)
			}
		}
	}

	rng := rand.New(rand.NewSource(13))
	for i := 0; i < 800; i++ {
		lo := rng.Intn(98000)
		hi := lo + 1 + rng.Intn(2500)
		compare("chrT", lo, hi, TargetToQuery)
		compare("chrQ", lo, hi, QueryToTarget)
	}
	for i, s := range f.specs {
		if i%7 != 0 {
			continue
		}
		compare("chrT", s.tLo, s.tHi, TargetToQuery)
		qlo, qhi := s.queryGenomeSpan(f.qSize)
		compare("chrQ", qlo, qhi, QueryToTarget)
	}
}

// TestDenseReloadConcurrentConsistency hammers a freshly built and a reloaded
// index concurrently and asserts results never tear or drift.
func TestDenseReloadConcurrentConsistency(t *testing.T) {
	f := genDenseFixture(2500, 100000, 2025)
	fresh := f.index()
	dir := filepath.Join(t.TempDir(), "idx")
	if err := fresh.Save(dir); err != nil {
		t.Fatal(err)
	}
	loaded, err := index.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	mf := NewMapper(fresh)
	ml := NewMapper(loaded)

	type query struct {
		chrom  string
		lo, hi int
		dir    Direction
	}
	var queries []query
	rng := rand.New(rand.NewSource(77))
	for i := 0; i < 400; i++ {
		lo := rng.Intn(98000)
		hi := lo + 1 + rng.Intn(2500)
		queries = append(queries,
			query{"chrT", lo, hi, TargetToQuery},
			query{"chrQ", lo, hi, QueryToTarget})
	}

	var wg sync.WaitGroup
	var failMu sync.Mutex
	var firstFail string
	fail := func(msg string) {
		failMu.Lock()
		if firstFail == "" {
			firstFail = msg
		}
		failMu.Unlock()
	}
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			// Each worker maps with both mappers twice and compares both to
			// each other and to a repeat, proving no torn state.
			for _, q := range queries {
				r1, err := mf.Map(q.chrom, q.lo, q.hi, q.dir)
				if err != nil {
					fail(err.Error())
					return
				}
				r2, err := ml.Map(q.chrom, q.lo, q.hi, q.dir)
				if err != nil {
					fail(err.Error())
					return
				}
				s1, s2 := fingerprint(r1), fingerprint(r2)
				if !snapshotsEqual(s1, s2) {
					fail("fresh vs loaded differ")
					return
				}
				r3, _ := mf.Map(q.chrom, q.lo, q.hi, q.dir)
				if !snapshotsEqual(s1, fingerprint(r3)) {
					fail("repeat query on fresh differs (torn state)")
					return
				}
			}
		}(w)
	}
	wg.Wait()
	if firstFail != "" {
		t.Fatal(firstFail)
	}
}

func snapshotsEqual(a, b []snapshot) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
