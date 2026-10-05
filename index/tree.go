package index

// maxEndTree is a segment tree storing the maximum interval end over every
// index range. It supports two operations needed for correct overlap
// enumeration when interval ends are not monotone:
//
//   - firstAtLeast(lo, hi, v): leftmost index i in [lo,hi) whose end > v;
//   - pruning traversal that skips whole subtrees whose maximum end <= v.
//
// The layout is the standard heap layout over a power-of-two leaf count;
// leaf positions past the real data hold 0 (never exceed a valid start).
type maxEndTree struct {
	n    int // number of leaves (power of two)
	vals []int
}

func newMaxEndTree(ends []int) maxEndTree {
	n := 1
	for n < len(ends) {
		n <<= 1
	}
	t := maxEndTree{n: n, vals: make([]int, 2*n)}
	for i, e := range ends {
		t.vals[n+i] = e
	}
	for i := n - 1; i >= 1; i-- {
		l, r := t.vals[i<<1], t.vals[i<<1|1]
		if r > l {
			t.vals[i] = r
		} else {
			t.vals[i] = l
		}
	}
	return t
}

// firstAfter returns the leftmost index i in [lo, hi) with value > v, or -1.
func (t maxEndTree) firstAfter(lo, hi, v int) int {
	return t.firstAfterRec(1, 0, t.n, lo, hi, v)
}

func (t maxEndTree) firstAfterRec(node, nlo, nhi, lo, hi, v int) int {
	if nhi <= lo || nlo >= hi || t.vals[node] <= v {
		return -1
	}
	if nhi-nlo == 1 {
		return nlo
	}
	mid := nlo + (nhi-nlo)/2
	if i := t.firstAfterRec(node<<1, nlo, mid, lo, hi, v); i >= 0 {
		return i
	}
	return t.firstAfterRec(node<<1|1, mid, nhi, lo, hi, v)
}
