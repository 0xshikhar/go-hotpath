package guard

import (
	"fmt"
	"runtime"
)

// ExactResult extends Result with exact allocation counts measured by
// runtime.ReadMemStats.
type ExactResult struct {
	Result

	// Mallocs is the exact number of objects allocated while fn ran.
	Mallocs uint64
	// Frees is the exact number of objects freed while fn ran.
	Frees uint64
	// TotalAllocBytes is the exact number of bytes allocated while fn ran.
	TotalAllocBytes uint64
}

// Exact runs fn once and returns an exact measurement of what it did.
//
// It uses runtime.ReadMemStats, which stops the world to flush per-P
// allocation caches — the only way to count small allocations exactly. Each
// ReadMemStats call costs roughly 20 µs of stop-the-world time. Use Exact in
// tests and diagnostics, never on a hot path.
//
// The counts are process-wide: allocations by other goroutines while fn runs
// are included, so measure from a quiet test (no t.Parallel).
//
// fn is run exactly once. Warm it up yourself if the first call would do
// lazy initialization (map growth, pool priming) that shouldn't be measured.
func Exact(fn func()) ExactResult {
	g := New()
	var before, after runtime.MemStats

	runtime.ReadMemStats(&before)
	w := g.Begin()
	fn()
	r := w.End()
	runtime.ReadMemStats(&after)

	return ExactResult{
		Result:          r,
		Mallocs:         after.Mallocs - before.Mallocs,
		Frees:           after.Frees - before.Frees,
		TotalAllocBytes: after.TotalAlloc - before.TotalAlloc,
	}
}

// TB is the part of testing.TB that Assert needs. *testing.T, *testing.B,
// and *testing.F all satisfy it. Defining it here keeps the testing package
// out of production binaries.
type TB interface {
	Helper()
	Fatalf(format string, args ...any)
}

// Budget is the allowance for AssertWithin. The zero value is strict: no
// allocations, no bytes, no GC cycles.
//
// Every field is an enforced limit — a zero field allows zero. If you allow
// allocations, also allow bytes; Budget{Allocs: 5} alone will fail on the
// first alloc because the byte limit is 0.
type Budget struct {
	// Allocs is the maximum number of object allocations allowed (exact count).
	Allocs uint64
	// Bytes is the maximum allocated bytes allowed (exact count).
	Bytes uint64
	// Cycles is the maximum number of completed GC cycles allowed.
	Cycles uint64
}

// Assert runs fn once and fails the test if it allocated anything or a GC
// cycle completed. Equivalent to AssertWithin(t, Budget{}, fn).
func Assert(t TB, fn func()) {
	t.Helper()
	AssertWithin(t, Budget{}, fn)
}

// AssertWithin runs fn once and fails the test if it exceeded b. It uses
// Exact: exact counts via runtime.ReadMemStats, at the cost of two
// stop-the-world reads. The failure message is fixed-format and greppable.
func AssertWithin(t TB, b Budget, fn func()) {
	t.Helper()
	res := Exact(fn)
	if res.Mallocs <= b.Allocs && res.TotalAllocBytes <= b.Bytes && res.GCCycles <= b.Cycles {
		return
	}
	t.Fatalf("guard: window not quiet: %d GC cycle(s) (%d forced); %d allocs (%s) [budget: %d allocs, %s, %d cycles]",
		res.GCCycles, res.CyclesForced, res.Mallocs, humanBytes(res.TotalAllocBytes),
		b.Allocs, humanBytes(b.Bytes), b.Cycles)
}

func humanBytes(n uint64) string {
	switch {
	case n < 1<<10:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		return fmt.Sprintf("%.1f KiB", float64(n)/1024)
	case n < 1<<30:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	default:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	}
}
