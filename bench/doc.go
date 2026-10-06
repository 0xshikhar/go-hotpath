// Package bench is an open-loop latency harness for hot paths.
//
// Standard Go benchmarks are closed-loop: they send a request, wait for it,
// then send the next. That hides tail latency through coordinated omission —
// if the system stalls for 10 ms, only one sample records the stall; the
// hundreds of requests that would have arrived during it are invisible.
//
// bench owns a clock. Every operation has an intended send time; latency is
// completion minus intended, so a stall is charged to every op it delayed.
// A second "service" series (completion minus actual start) shows the same
// run the way a closed loop would report it. The gap between the two is the
// lesson.
//
// The harness calls fn once per intended time, on one goroutine. fn must be
// synchronous. For a pipelined op (enqueue now, complete elsewhere), stamp
// the intended time into the message and measure on the far side — Run does
// not do that for you.
//
// Ops are never dropped: an op that starts late is recorded with its full
// lateness and counted in Report.Late. Dropping late samples would re-create
// the omission this harness exists to remove.
//
// Runtime attribution is captured at run level: GC cycles (forced vs
// automatic), allocated bytes, GC CPU split into mark-assist/dedicated/pause,
// and deltas of the runtime's /sched/latencies and /sched/pauses/total/gc
// histograms. CPU-class counters only advance at mark termination, so a run
// that completed any GC cycle is flushed with a final runtime.GC() before
// the CPU-class read; that extra cycle is excluded from GCCycles and
// GCForced.
//
// On Go versions before 1.23 the /sched/pauses histograms do not exist and
// Report.GCPauseMax/GCPauseP99 stay zero; everything else is reported.
//
// For CI gates, Regressed returns the percentiles that slowed down by more
// than 10% and 1 µs; Compare renders the full side-by-side.
package bench
