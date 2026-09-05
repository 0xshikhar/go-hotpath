// Package profile applies and reverses Go runtime GC settings as a unit.
//
// Turning GOGC off and setting a memory limit is a common recipe for
// latency-sensitive Go services: the GC trades "collect when the heap
// doubles" for "collect only when we approach a hard cap". Doing it by hand
// means remembering debug.SetGCPercent(-1) plus debug.SetMemoryLimit —
// and remembering to put them back. profile makes the pair (plus, sparingly,
// GOMAXPROCS) a tested, reversible operation:
//
//	s, err := profile.Apply(profile.SilentWindow(512 << 20))
//	if err != nil { log.Fatal(err) }
//	defer s.Undo()
//
// Apply reads back every knob it changed through runtime/metrics and returns
// an error if the effective value does not match the request. Session.Undo
// restores the values that were in effect before Apply.
//
// # The GOMAXPROCS one-way door
//
// Since Go 1.25 the runtime automatically re-detects GOMAXPROCS from cgroup
// CPU limits. Calling runtime.GOMAXPROCS — including through a Profile —
// permanently disables that automatic behavior for the process. Undo
// restores the numeric value but cannot restore auto-update mode. Set
// Profile.GOMAXPROCS only if you accept that.
//
// # Reading state, not just setting it
//
// Read returns a Snapshot: the effective knobs, live heap, heap goal,
// scannable heap, goroutine count, and the detected cgroup memory limit —
// the numbers you actually want when someone asks "is this process near its
// limit?"
//
// QuietGC runs a collection at a deliberate boundary (end of a batch,
// between sessions) so the cycle cost lands where you chose it, and reports
// wall duration plus the live-heap delta.
package profile
