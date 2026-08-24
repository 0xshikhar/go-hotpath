package guard

import "github.com/0xshikhar/go-hotpath/internal/rtm"

// Guard holds a reusable metrics buffer so that Begin and End allocate
// nothing. Create it once on a cold path — init, setup, or warmup — never
// inside the code being measured.
//
// A Guard is owned by a single goroutine. Begin and End are not safe for
// concurrent use; give each watched goroutine its own Guard.
type Guard struct {
	set *rtm.Set
}

// Indexes into the set, in New's argument order.
const (
	iCycles = iota
	iForced
	iAllocBytes
	iAllocObjects
	iLimiter
)

// New creates a Guard. It allocates a small fixed buffer and validates that
// every metric it reads exists on this Go toolchain.
//
// It panics if the runtime lacks a required metric. Call Check first if you
// want to probe without panicking.
func New() *Guard {
	return &Guard{set: rtm.NewSet(
		rtm.MetricGCCyclesTotal,
		rtm.MetricGCCyclesForced,
		rtm.MetricHeapAllocBytes,
		rtm.MetricHeapAllocObjects,
		rtm.MetricLimiterLast,
	)}
}

// Check reports whether every runtime metric guard needs exists on this
// toolchain. A nil return means New will not panic.
func Check() error { return rtm.Check() }

// Window marks the start of a measured region. Create it with Begin and
// close it exactly once with End.
type Window struct {
	g            *Guard
	cycles       uint64
	forced       uint64
	allocBytes   uint64
	allocObjects uint64
	limiter      uint64
}

// Begin snapshots process-wide GC counters and returns a Window. It performs
// no heap allocations and does not stop the world.
//
// Cost: roughly 200–400 ns (a runtime/metrics read). It takes the runtime's
// global metrics semaphore, so a concurrent metrics reader — a Prometheus
// scrape, profile.Snapshot — can delay it. Guard batches, not per-request
// calls at very high rates.
func (g *Guard) Begin() Window {
	g.set.Read()
	return Window{
		g:            g,
		cycles:       g.set.Value(iCycles).Uint64(),
		forced:       g.set.Value(iForced).Uint64(),
		allocBytes:   g.set.Value(iAllocBytes).Uint64(),
		allocObjects: g.set.Value(iAllocObjects).Uint64(),
		limiter:      g.set.Value(iLimiter).Uint64(),
	}
}

// End closes the window and reports what happened inside it. Same cost and
// allocation profile as Begin.
func (w Window) End() Result {
	w.g.set.Read()
	return Result{
		GCCycles:             w.g.set.Value(iCycles).Uint64() - w.cycles,
		CyclesForced:         w.g.set.Value(iForced).Uint64() - w.forced,
		AllocBytesObserved:   w.g.set.Value(iAllocBytes).Uint64() - w.allocBytes,
		AllocObjectsObserved: w.g.set.Value(iAllocObjects).Uint64() - w.allocObjects,
		LimiterEngaged:       w.g.set.Value(iLimiter).Uint64() != w.limiter,
	}
}

// Result reports what the Go runtime did during a window. Read the
// precision comments before acting on any field: cycle fields are exact,
// allocation fields are a process-wide lower bound.
type Result struct {
	// GCCycles is the number of GC cycles that completed during the window.
	// Exact.
	GCCycles uint64

	// CyclesForced is how many of those cycles were started by an explicit
	// runtime.GC call rather than heap growth. Exact.
	CyclesForced uint64

	// AllocBytesObserved is the increase in the process-wide allocation
	// counter during the window. It is exact for large allocations and a
	// lower bound for small ones: the runtime only counts small objects when
	// a per-P span cache refills, so a window that allocated a few small
	// objects may report 0. Use Exact or Assert for an exact count.
	AllocBytesObserved uint64

	// AllocObjectsObserved is the object-count counterpart of
	// AllocBytesObserved. Same lower-bound caveat.
	AllocObjectsObserved uint64

	// LimiterEngaged reports that the GC CPU limiter engaged during the
	// window — the runtime was spending so much CPU on GC that it began
	// letting the heap grow instead. If a memory limit is configured, this
	// means the process is at its edge.
	LimiterEngaged bool
}

// Quiet reports whether no GC cycle completed during the window.
//
// Quiet deliberately does not include allocations: process-wide allocation
// counters are a lower bound, not a clean signal (see AllocBytesObserved).
// A non-quiet window is a GC event; nonzero observed allocations are context
// for why one might happen soon.
func (r Result) Quiet() bool { return r.GCCycles == 0 }

// CyclesAutomatic is the number of cycles the runtime started itself
// (heap-pressure-driven), as opposed to forced by runtime.GC.
func (r Result) CyclesAutomatic() uint64 { return r.GCCycles - r.CyclesForced }
