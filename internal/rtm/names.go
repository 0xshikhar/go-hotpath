// Package rtm is hotpath's single door into runtime/metrics. Every package in
// the module reads runtime counters through a Set or HistSet built here, and
// every metric name used anywhere is declared in this file. If a Go release
// renames, removes, or re-kinds a metric, Check fails loudly instead of
// letting a reader report a silent zero.
//
// Two measured facts shape this API:
//
//   - metrics.Read forces its sample buffer to the heap. A Set allocates its
//     buffer once at construction so that Read performs zero allocations.
//   - metrics.Read serializes on the runtime's metricsSema. Concurrent
//     readers contend; a histogram reader can delay a scalar reader. Keep
//     histogram reads on cold paths only.
package rtm

import "runtime/metrics"

// GC cycles.
const (
	MetricGCCyclesTotal  = "/gc/cycles/total:gc-cycles"
	MetricGCCyclesAuto   = "/gc/cycles/automatic:gc-cycles"
	MetricGCCyclesForced = "/gc/cycles/forced:gc-cycles"
	MetricLimiterLast    = "/gc/limiter/last-enabled:gc-cycle"
)

// Heap and allocation counters. Small allocations are only counted when a
// per-P span cache refills, so window-scale deltas are a lower bound.
// Large allocations (served directly from the heap) are counted immediately.
const (
	MetricHeapAllocBytes   = "/gc/heap/allocs:bytes"
	MetricHeapAllocObjects = "/gc/heap/allocs:objects"
	MetricHeapFreeBytes    = "/gc/heap/frees:bytes"
	MetricHeapFreeObjects  = "/gc/heap/frees:objects"
	MetricHeapLive         = "/gc/heap/live:bytes"
	MetricHeapGoal         = "/gc/heap/goal:bytes"
	MetricTinyAllocs       = "/gc/heap/tiny/allocs:objects"
)

// Scan surface: the bytes the GC must actually scan. A pointer-free heap
// keeps MetricScanHeap flat while MetricHeapLive grows.
const (
	MetricScanHeap    = "/gc/scan/heap:bytes"
	MetricScanStack   = "/gc/scan/stack:bytes"
	MetricScanGlobals = "/gc/scan/globals:bytes"
	MetricScanTotal   = "/gc/scan/total:bytes"
)

// Runtime knobs, readable back so callers can verify a setting took effect.
const (
	MetricGOGC       = "/gc/gogc:percent"
	MetricGOMemLimit = "/gc/gomemlimit:bytes"
	MetricGOMAXPROCS = "/sched/gomaxprocs:threads"
)

// Memory classes for profile.Snapshot.
const (
	MetricMemTotal        = "/memory/classes/total:bytes"
	MetricMemHeapObjects  = "/memory/classes/heap/objects:bytes"
	MetricMemHeapUnused   = "/memory/classes/heap/unused:bytes"
	MetricMemHeapFree     = "/memory/classes/heap/free:bytes"
	MetricMemHeapReleased = "/memory/classes/heap/released:bytes"
	MetricMemHeapStacks   = "/memory/classes/heap/stacks:bytes"
	MetricMemOther        = "/memory/classes/other:bytes"
)

// CPU classes. These update only at GC mark termination: they are flat
// between cycles, so they are run-level attribution for bench, never a
// per-window signal.
const (
	MetricCPUUser        = "/cpu/classes/user:cpu-seconds"
	MetricCPUIdle        = "/cpu/classes/idle:cpu-seconds"
	MetricCPUTotal       = "/cpu/classes/total:cpu-seconds"
	MetricCPUGCAssist    = "/cpu/classes/gc/mark/assist:cpu-seconds"
	MetricCPUGCDedicated = "/cpu/classes/gc/mark/dedicated:cpu-seconds"
	MetricCPUGCIdle      = "/cpu/classes/gc/mark/idle:cpu-seconds"
	MetricCPUGCPause     = "/cpu/classes/gc/pause:cpu-seconds"
	MetricCPUGCTotal     = "/cpu/classes/gc/total:cpu-seconds"
	MetricCPUScavAssist  = "/cpu/classes/scavenge/assist:cpu-seconds"
	MetricCPUScavBg      = "/cpu/classes/scavenge/background:cpu-seconds"
	MetricCPUScavTotal   = "/cpu/classes/scavenge/total:cpu-seconds"
)

// Scheduler state.
const (
	MetricGoroutines         = "/sched/goroutines:goroutines"
	MetricGoroutinesRunnable = "/sched/goroutines/runnable:goroutines"
	MetricGoroutinesRunning  = "/sched/goroutines/running:goroutines"
	MetricGoroutinesWaiting  = "/sched/goroutines/waiting:goroutines"
	MetricThreadsTotal       = "/sched/threads/total:threads"
)

// GC-adjacent costs: queued work that makes future cycles heavier.
const (
	MetricFinalizersQueued   = "/gc/finalizers/queued:finalizers"
	MetricFinalizersExecuted = "/gc/finalizers/executed:finalizers"
	MetricCleanupsQueued     = "/gc/cleanups/queued:cleanups"
	MetricCleanupsExecuted   = "/gc/cleanups/executed:cleanups"
)

// GODEBUG non-default-behavior events. profile reads these to learn whether
// Go 1.25+ container-aware GOMAXPROCS is active.
const (
	MetricGodebugContainerMaxProcs = "/godebug/non-default-behavior/containermaxprocs:events"
	MetricGodebugUpdateMaxProcs    = "/godebug/non-default-behavior/updatemaxprocs:events"
)

// Histograms. Reading one allocates (the runtime fills Buckets), so these are
// for HistSet on cold paths only — bench reports and profile snapshots.
const (
	MetricSchedLatencies   = "/sched/latencies:seconds"
	MetricSchedPausesGC    = "/sched/pauses/total/gc:seconds"
	MetricSchedPausesOther = "/sched/pauses/total/other:seconds"
	MetricHeapAllocsBySize = "/gc/heap/allocs-by-size:bytes"
	MetricHeapFreesBySize  = "/gc/heap/frees-by-size:bytes"
)

// registry maps every name this module depends on to its expected kind.
// Check and the tests iterate this table; the Set constructors consult the
// runtime catalog directly.
var registry = map[string]metrics.ValueKind{
	MetricGCCyclesTotal:  metrics.KindUint64,
	MetricGCCyclesAuto:   metrics.KindUint64,
	MetricGCCyclesForced: metrics.KindUint64,
	MetricLimiterLast:    metrics.KindUint64,

	MetricHeapAllocBytes:   metrics.KindUint64,
	MetricHeapAllocObjects: metrics.KindUint64,
	MetricHeapFreeBytes:    metrics.KindUint64,
	MetricHeapFreeObjects:  metrics.KindUint64,
	MetricHeapLive:         metrics.KindUint64,
	MetricHeapGoal:         metrics.KindUint64,
	MetricTinyAllocs:       metrics.KindUint64,

	MetricScanHeap:    metrics.KindUint64,
	MetricScanStack:   metrics.KindUint64,
	MetricScanGlobals: metrics.KindUint64,
	MetricScanTotal:   metrics.KindUint64,

	MetricGOGC:       metrics.KindUint64,
	MetricGOMemLimit: metrics.KindUint64,
	MetricGOMAXPROCS: metrics.KindUint64,

	MetricMemTotal:        metrics.KindUint64,
	MetricMemHeapObjects:  metrics.KindUint64,
	MetricMemHeapUnused:   metrics.KindUint64,
	MetricMemHeapFree:     metrics.KindUint64,
	MetricMemHeapReleased: metrics.KindUint64,
	MetricMemHeapStacks:   metrics.KindUint64,
	MetricMemOther:        metrics.KindUint64,

	MetricCPUUser:        metrics.KindFloat64,
	MetricCPUIdle:        metrics.KindFloat64,
	MetricCPUTotal:       metrics.KindFloat64,
	MetricCPUGCAssist:    metrics.KindFloat64,
	MetricCPUGCDedicated: metrics.KindFloat64,
	MetricCPUGCIdle:      metrics.KindFloat64,
	MetricCPUGCPause:     metrics.KindFloat64,
	MetricCPUGCTotal:     metrics.KindFloat64,
	MetricCPUScavAssist:  metrics.KindFloat64,
	MetricCPUScavBg:      metrics.KindFloat64,
	MetricCPUScavTotal:   metrics.KindFloat64,

	MetricGoroutines: metrics.KindUint64,

	MetricSchedLatencies:   metrics.KindFloat64Histogram,
	MetricHeapAllocsBySize: metrics.KindFloat64Histogram,
	MetricHeapFreesBySize:  metrics.KindFloat64Histogram,
}

// optional metrics exist only on newer toolchains. They are excluded from
// Check — probe them with Available before building a Set/HistSet that
// reads them. Verified absent on go1.21: all of these.
var optional = map[string]metrics.ValueKind{
	// Go 1.23+
	MetricSchedPausesGC:      metrics.KindFloat64Histogram,
	MetricSchedPausesOther:   metrics.KindFloat64Histogram,
	MetricGoroutinesRunnable: metrics.KindUint64,
	MetricGoroutinesRunning:  metrics.KindUint64,
	MetricGoroutinesWaiting:  metrics.KindUint64,
	// Go 1.24+
	MetricThreadsTotal:       metrics.KindUint64,
	MetricFinalizersQueued:   metrics.KindUint64,
	MetricFinalizersExecuted: metrics.KindUint64,
	MetricCleanupsQueued:     metrics.KindUint64,
	MetricCleanupsExecuted:   metrics.KindUint64,
	// Go 1.25+ (cgroup-aware scheduling godebug counters)
	MetricGodebugContainerMaxProcs: metrics.KindUint64,
	MetricGodebugUpdateMaxProcs:    metrics.KindUint64,
}
