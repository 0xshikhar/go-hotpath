// Package guard answers one question about a window of execution: did the
// garbage collector interfere?
//
// # Usage
//
// Production code wraps a region with a per-goroutine Guard:
//
//	g := guard.New() // once, on a cold path
//	for batch := range batches {
//		w := g.Begin()
//		process(batch)
//		if r := w.End(); !r.Quiet() {
//			log.Printf("guard: %s", r.Explain())
//		}
//	}
//
// Tests use Exact or Assert, which measure through runtime.ReadMemStats:
//
//	func TestApply(t *testing.T) {
//		guard.Assert(t, func() { book.Apply(&cmd, &ev) })
//	}
//
// # Precision
//
// Every field is one of three precision classes:
//
//	exact        — a complete count (cycle counters, ReadMemStats deltas)
//	lower bound  — small allocations may go uncounted until a span refill
//	run-level    — only meaningful across a long window (see package bench)
//
//	Result.GCCycles, CyclesForced        exact
//	Result.Alloc{Bytes,Objects}Observed  lower bound (process-wide)
//	Result.LimiterEngaged                exact
//	ExactResult.Mallocs/Frees/Bytes      exact
//
// # Cost
//
// Begin and End each read a fixed set of scalar runtime/metrics counters:
// zero allocations, no stop-the-world, roughly 200–400 ns per call on an
// Apple M4 Pro (go1.26). The read serializes on the runtime's metrics
// semaphore, so a concurrent histogram reader (a metrics exporter,
// profile.Snapshot, bench) can delay an End call — measured worst case
// ~143 µs under 4 concurrent histogram readers. Prefer batch or session
// windows over per-request windows at very high rates.
//
// # Limits
//
//   - All counters are process-wide. A JSON gateway in the same process can
//     make a quiet engine's window non-quiet. That is the point, not a bug:
//     a process is the unit of GC interference. If a neighbor you cannot
//     silence moves your tail, the fix is a separate process.
//   - Quiet() == true does not mean the tail was fine. Scheduler delay, OS
//     preemption, and CPU contention are not GC; package bench measures
//     those.
//   - Assert and Exact stop the world (~20 µs each call) — tests only.
package guard
