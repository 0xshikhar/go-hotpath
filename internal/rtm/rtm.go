package rtm

import (
	"fmt"
	"runtime"
	"runtime/metrics"
	"slices"
	"sync"
)

// UnsupportedError reports runtime/metrics names that are missing or have an
// unexpected kind on the running toolchain.
type UnsupportedError struct {
	Names []string // sorted
}

func (e *UnsupportedError) Error() string {
	return fmt.Sprintf("rtm: unsupported metrics on %s: %v", runtime.Version(), e.Names)
}

var catalog = sync.OnceValue(func() map[string]metrics.Description {
	all := metrics.All()
	m := make(map[string]metrics.Description, len(all))
	for _, d := range all {
		m[d.Name] = d
	}
	return m
})

// Check verifies that every metric name the module depends on exists with its
// expected kind on the running toolchain. Call it early (init or main) to
// fail fast; the Set constructors perform the same check lazily.
func Check() error {
	cat := catalog()
	var bad []string
	for name, want := range registry {
		d, ok := cat[name]
		if !ok || d.Kind != want {
			bad = append(bad, name)
		}
	}
	if len(bad) == 0 {
		return nil
	}
	slices.Sort(bad)
	return &UnsupportedError{Names: bad}
}

// Available reports whether the named metric exists on this toolchain with
// the kind this module expects. Use it to degrade gracefully on older Go
// releases — for example the /gc/finalizers/* counters only exist on Go
// 1.26+. A name not
// declared in names.go is never available.
func Available(name string) bool {
	want, ok := registry[name]
	if !ok {
		if want, ok = optional[name]; !ok {
			return false
		}
	}
	d, ok := catalog()[name]
	return ok && d.Kind == want
}

func require(name string, scalar bool) metrics.ValueKind {
	d, ok := catalog()[name]
	if !ok {
		panic(fmt.Sprintf("rtm: metric %q does not exist on %s", name, runtime.Version()))
	}
	isScalar := d.Kind == metrics.KindUint64 || d.Kind == metrics.KindFloat64
	if scalar && !isScalar {
		panic(fmt.Sprintf("rtm: metric %q is a histogram; use NewHistSet", name))
	}
	if !scalar && d.Kind != metrics.KindFloat64Histogram {
		panic(fmt.Sprintf("rtm: metric %q is scalar; use NewSet", name))
	}
	return d.Kind
}

// Set is a reusable group of scalar metric samples. The backing slice is
// allocated once in NewSet, so Read performs no heap allocations.
//
// A Set is owned by a single goroutine: concurrent calls to Read race on the
// sample Values. metrics.Read itself serializes on a runtime semaphore, so a
// Set shared with a histogram-reading goroutine would add latency, not just
// races.
type Set struct {
	samples []metrics.Sample
	index   map[string]int
}

// NewSet builds a Set for the given metric names. It panics if a name does
// not exist on this toolchain or names a histogram metric — use Check to
// probe without panicking.
func NewSet(names ...string) *Set {
	s := &Set{
		samples: make([]metrics.Sample, len(names)),
		index:   make(map[string]int, len(names)),
	}
	for i, n := range names {
		require(n, true)
		s.samples[i].Name = n
		s.index[n] = i
	}
	return s
}

// Read refreshes every sample in the set. It performs no heap allocations.
func (s *Set) Read() {
	metrics.Read(s.samples)
}

// Value returns the value of the i-th metric, in NewSet argument order.
func (s *Set) Value(i int) metrics.Value {
	return s.samples[i].Value
}

// Index returns the position of name in the set, or -1.
func (s *Set) Index(name string) int {
	i, ok := s.index[name]
	if !ok {
		return -1
	}
	return i
}

// Len returns the number of metrics in the set.
func (s *Set) Len() int { return len(s.samples) }

// HistSet is a reusable group of histogram metrics. Reading fills the
// per-histogram Buckets slice and may allocate, so HistSet is for cold paths:
// bench run boundaries, profile snapshots, diagnostics.
type HistSet struct {
	samples []metrics.Sample
	index   map[string]int
}

// NewHistSet builds a HistSet. It panics if a name does not exist or is not a
// histogram metric.
func NewHistSet(names ...string) *HistSet {
	h := &HistSet{
		samples: make([]metrics.Sample, len(names)),
		index:   make(map[string]int, len(names)),
	}
	for i, n := range names {
		require(n, false)
		h.samples[i].Name = n
		h.index[n] = i
	}
	return h
}

// Read refreshes every histogram. It may allocate; do not call it inside a
// guarded hot path.
func (h *HistSet) Read() {
	metrics.Read(h.samples)
}

// Histogram returns the i-th histogram, in NewHistSet argument order. The
// returned pointer aliases the sample; copy before another Read.
func (h *HistSet) Histogram(i int) *metrics.Float64Histogram {
	return h.samples[i].Value.Float64Histogram()
}

// Index returns the position of name in the set, or -1.
func (h *HistSet) Index(name string) int {
	i, ok := h.index[name]
	if !ok {
		return -1
	}
	return i
}

// Len returns the number of metrics in the set.
func (h *HistSet) Len() int { return len(h.samples) }
