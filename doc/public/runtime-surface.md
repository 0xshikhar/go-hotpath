# The Go runtime surface — what hotpath can actually measure and control

**Date:** 2026-10-06
**Verified on:** `go1.26.0 darwin/arm64` (floor-verified against go1.22.12;
version-dated metrics called out inline). Every metric name in §2 was printed
by `metrics.All()` on this machine; update timing and cost were measured with
a dedicated lab harness and checked against `$GOROOT/src/runtime/metrics.go`
and `mgc.go`.

go-hotpath reads the Go runtime through `runtime/metrics`. The obvious
counters are `/gc/cycles/total`, `/gc/heap/allocs:{bytes,objects}`, and the
`/sched/latencies` histogram, but the runtime exposes much more. This document
audits the full surface, maps each GC mechanism to the counter that catches it,
and states **when each counter actually updates**. That last part decides whether
a counter is usable inside a short window.

### Three runtime facts that shape every design below

1. **`metrics.Read` takes a global semaphore** (`metricsSema`, `runtime/metrics.go`).
   It is not a lock-free atomic read. A 3-counter read costs ~223 ns alone and
   ~553 ns/op with 14 parallel readers. Its worst case was 143 µs while 4 goroutines
   read histograms.
2. **The sample buffer always escapes.** `metrics.Read` hands it to a linkname'd
   runtime function, so even `var s [3]metrics.Sample` is "moved to heap". That is
   1 alloc per call. Zero-alloc reads need a buffer allocated once and reused.
3. **Counters update at different times:**

| Counter family | Updates | Usable in a window? |
|---|---|---|
| `/gc/cycles/*` | At every cycle end | Yes, exact |
| `/gc/heap/allocs:*` (large objects) | Immediately | Yes, exact |
| `/gc/heap/allocs:*` (small objects) | When a P's span cache refills or flushes | Lower bound only. 100 allocs read as 0 |
| `/cpu/classes/*` | Only at GC mark termination (`work.cpuStats.accumulate`, `mgc.go`) | No. Flat between cycles. Run-level only |
| `/gc/limiter/last-enabled` | When the GC CPU limiter engages | Yes, as "did it change" |
| Histograms | Cumulative; reading allocates `Buckets` | No. Cold path only |

---

## 1. What Rust gives that Go lacks

Rust's determinism is three separate guarantees. hotpath can close each to a
different degree:

| Rust guarantee | Mechanism | What Go can offer | Residual gap |
|---|---|---|---|
| No runtime memory work | No GC; ownership frees deterministically | Detect (`guard`), minimize (`profile`), enforce (`hotpathcheck`) | STW, assists, write barriers are process-wide and cannot be disabled per-goroutine |
| Compile-time allocation proof | Borrow checker; `Box` is explicit | `//hotpath:noalloc` flags syntactic sites; `AllocsPerRun` is ground truth | Analyzer is pre-optimization — it sees `make()`, not whether it escapes |
| Deterministic teardown | `Drop` | Preallocated pools + `profile.QuietGC` between sessions | No per-object destructor; finalizers/`runtime.AddCleanup` are GC-timed, never hot-path-safe |

Two Rust habits worth importing as *conventions*, since the language won't
enforce them:

- **Single writer** (Rust's `&mut` exclusivity) → matching engines already do
  this; document it as the ownership rule for any structure annotated
  `//hotpath:noalloc`.
- **Drop = region reset** → a slab/arena's bulk free approximates `Drop` for a
  whole object graph.

## 2. The audited metric surface (go1.26.0)

Grouped by what the product can do with it. Scalar counters can be read inside
`Begin`/`End` if the buffer is reused. Histograms allocate on read
(`metrics.Read` fills `Buckets`) and belong in `bench`/`Snapshot`, never in the
hot window.

### 2.1 GC cycles — finer than "a cycle ran"

| Metric | Kind | What it tells you |
|---|---|---|
| `/gc/cycles/total:gc-cycles` | uint64 | Any completed cycle — the `Quiet` signal |
| `/gc/cycles/automatic:gc-cycles` | uint64 | Heap-pressure-driven cycles — "your process created this" |
| `/gc/cycles/forced:gc-cycles` | uint64 | `runtime.GC()` calls — "a human (or a library) did this" |

The forced/automatic split is free attribution: a cycle during a window that was
*forced* points at a `runtime.GC()` call inside the window (a warmup helper, a
library, a misplaced `profile.QuietGC`). An *automatic* cycle points at
allocation pressure. Different causes, different fixes.

### 2.2 Allocation pressure

| Metric | Kind | What it tells you |
|---|---|---|
| `/gc/heap/allocs:{bytes,objects}` | uint64 | Process-wide allocations. Small objects counted at span-refill granularity, so in a window this is a lower bound |
| `/gc/heap/frees:{bytes,objects}` | uint64 | Churn rate; alloc-without-free during a window means growth |
| `/gc/heap/live:bytes` | uint64 | Live heap after last cycle |
| `/gc/heap/goal:bytes` | uint64 | What the pacer is aiming for — exposes the effective GOGC/GOMEMLIMIT interaction |
| `/gc/heap/tiny/allocs:objects` | uint64 | Tiny-allocation churn (sub-16-byte, no-pointer objects) |
| `/gc/heap/allocs-by-size:bytes` | histogram | Size distribution — `bench`-level, not hot-path |

### 2.3 Scan surface — the metric nobody surfaces

| Metric | Kind | What it tells you |
|---|---|---|
| `/gc/scan/heap:bytes` | uint64 | Bytes of heap the GC must actually scan (pointer-bearing) |
| `/gc/scan/stack:bytes` | uint64 | Scannable stack bytes |
| `/gc/scan/globals:bytes` | uint64 | Scannable globals |
| `/gc/scan/total:bytes` | uint64 | Sum — the real GC surface area |

This is the most underused counter in the catalog for this product's thesis.
A pointer-free slab keeps `/gc/scan/heap` **flat** while `/gc/heap/live` grows —
the spike's whole premise, measurable on the user's own heap. `Snapshot` ships
`ScanHeap` next to `HeapLive` for exactly this: it converts "pointer-free is
better" from a claim into a number the user can watch drop.

### 2.4 GC CPU cost: run-level attribution only

| Metric | Kind | What it tells you |
|---|---|---|
| `/cpu/classes/gc/mark/assist:cpu-seconds` | float64 | **Mark assists — goroutines taxed inline.** The direct measure of "your goroutine did GC work before returning" |
| `/cpu/classes/gc/mark/dedicated:cpu-seconds` | float64 | Background mark workers on dedicated Ps — CPU stolen from the app |
| `/cpu/classes/gc/mark/idle:cpu-seconds` | float64 | GC work done on otherwise-idle Ps — mostly harmless |
| `/cpu/classes/gc/pause:cpu-seconds` | float64 | CPU burned inside STW phases |
| `/cpu/classes/gc/total:cpu-seconds` | float64 | All of the above |
| `/cpu/classes/user:cpu-seconds` | float64 | App CPU — denominator for "what fraction of the window was GC" |
| `/cpu/classes/scavenge/*:cpu-seconds` | float64 | Heap-return work — mostly cold path, worth a `Snapshot` field |

**Correction to the first draft of this file.** These counters are snapshots taken
at the STW of mark termination. `cpuStatsAggregate.compute` copies `work.cpuStats`,
and the runtime source has a TODO about refreshing it between cycles. Lab 03
busy-looped for 200 ms with no GC: `user` and `assist` both moved by exactly 0.
After a `runtime.GC()`, `user` jumped by 0.201 s at once.

So they are **not** a per-window signal. Inside a window they can only jump when a
cycle ends, and `GCCycles` already reports that event exactly. They are still the
right **run-level** tool. Across a `bench` run with several cycles, "8.1 of 12.3
GC CPU-seconds were mark assist" says which mechanism dominated. This is how the
noisy-neighbor experiment (E5 in BENCHMARK.md) separates GC work from plain
CPU contention. Treat every value as the
runtime's documented "overestimate, compare only with other /cpu/classes".

### 2.5 Pauses and scheduler

| Metric | Kind | What it tells you |
|---|---|---|
| `/gc/pauses:seconds` | histogram | **Deprecated**: "Prefer the identical /sched/pauses/total/gc:seconds". Do not use |
| `/sched/pauses/stopping/gc:seconds` | histogram | Time to *reach* STW — measures preemption delay, distinct from pause length |
| `/sched/pauses/total/gc:seconds` | histogram | Total per-goroutine pause time |
| `/sched/latencies:seconds` | histogram | Time runnable-but-not-running — scheduler jitter; allocates on read |
| `/sched/gomaxprocs:threads` | uint64 | Effective GOMAXPROCS — self-check for `profile` |
| `/sched/goroutines/runnable:goroutines` | uint64 | Run-queue depth — instantaneous contention signal |

All histograms here are `bench`- or `Snapshot`-grade. None belong in `Begin`.

### 2.6 The knobs, readable back

| Metric | Kind | What it tells you |
|---|---|---|
| `/gc/gogc:percent` | uint64 | Effective GOGC — verify `Apply` took effect |
| `/gc/gomemlimit:bytes` | uint64 | Effective memory limit |
| `/gc/limiter/last-enabled:gc-cycle` | uint64 | **The GC cycle in which the GC CPU limiter last engaged** (0 = never) |

**Correction.** In the first draft I said this counter means "the memory limit
forced a collection". It does not. It tracks the GC **CPU** limiter. That
limiter engages when GC CPU use gets too high (the runtime caps it around 50%),
and it trades memory for CPU to stop a death spiral. The runtime's own
description says it is "most likely to occur with use of SetMemoryLimit" and
calls it the key metric for diagnosing an OOM.

For `SilentWindow` it is still the tripwire. In that mode `GOMEMLIMIT` is the only
GC trigger. If the process keeps allocating near the limit, collections run back
to back. The limiter engages, and then the heap is allowed past the limit. If
this value changes, the "almost nothing allocates" assumption has failed and an
OOM is on the way. The value is a cycle number, not a count, so `guard` reports
`LimiterEngaged bool` (the value changed), not a number of trips.

Also worth reading once at `Apply` time: `/godebug/non-default-behavior/
{containermaxprocs,updatemaxprocs}:events` — whether Go 1.25+'s cgroup-aware
GOMAXPROCS is live. If it is and the user also sets `Profile.GOMAXPROCS`, the manual
set *disables* the runtime's periodic cgroup re-check. `profile` warns on this
in its package and field docs.

### 2.7 Memory classes — for `Snapshot`

`/memory/classes/{total,heap/objects,heap/stacks,heap/free,heap/released,
heap/unused,other}:bytes` — everything `Snapshot` needs for the `Headroom`
computation without `ReadMemStats`. `heap/stacks` matters for trading services
(high goroutine counts); `other` catches runtime metadata the naive "heap vs
limit" math misses.

### 2.8 GC-adjacent costs worth documenting

| Metric | Kind | Relevance |
|---|---|---|
| `/gc/finalizers/{queued,executed}` | uint64 | `SetFinalizer` objects make GC work — flag in `noalloc` docs |
| `/gc/cleanups/{queued,executed}` | uint64 | Same for `runtime.AddCleanup` (Go 1.24+) — the modern API, same cost model |
| `/sched/threads/total:threads` | uint64 | Thread-bloat watch for long-running engines |

## 3. What `guard` reads

*Shipped* as `guard`/`Result` + `rtm.Set`.

These counters are usable in a window. Each is a scalar read from a buffer
allocated once in `guard.New()`. Measured: 3 counters ~223 ns, 7 counters
~275 ns. Begin+End with a reused buffer is ~450 ns and 0 allocs (measured,
go1.26.0 — see the root BENCHMARK.md).

```go
type Result struct {
    GCCycles             uint64 // exact
    CyclesForced         uint64 // exact: someone called runtime.GC()
    AllocBytesObserved   uint64 // process-wide, lower bound for small objects
    AllocObjectsObserved uint64 // process-wide, lower bound for small objects
    LimiterEngaged       bool   // /gc/limiter/last-enabled changed
    Quiet                bool   // GCCycles == 0
}
```

Automatic cycles are `GCCycles - CyclesForced`. Reading one counter fewer keeps
Begin cheaper. The per-window diagnosis:

- `CyclesForced > 0`: a `runtime.GC()` ran inside the window. Find the caller.
- `GCCycles > CyclesForced`: the runtime triggered the cycle. Some goroutine in
  the process allocated enough to reach the heap goal.
- `LimiterEngaged`: the GC was CPU-capped during the window. With a memory limit
  set, the heap is at the edge.
- `GCCycles == 0 && AllocBytesObserved > 0`: no cycle, but the process allocated
  during the window. That is not a fault. It is pressure that leads to the next
  cycle.

The cases my first draft listed for `MarkAssistCPU` and `GCPauseCPU` cannot
happen per window, because those counters only move at cycle end. They are gone
from `Result`. Test mode (`guard.Exact`) adds an exact `Mallocs` count from
`runtime.ReadMemStats`.

## 4. What `profile` adds

`profile.Read` returns a `Snapshot` built from one scalar read:

| `Snapshot` field | Source |
|---|---|
| `GOGC`, `MemLimit`, `GOMAXPROCS` | `/gc/gogc:percent`, `/gc/gomemlimit:bytes`, `/sched/gomaxprocs:threads` — the effective values, also used by `Apply` to verify itself |
| `HeapLive`, `HeapGoal` | `/gc/heap/live:bytes`, `/gc/heap/goal:bytes` — headroom to the next cycle |
| `ScanHeap` | `/gc/scan/heap:bytes` — the GC's work surface |
| `MemTotal` | `/memory/classes/total:bytes` |
| `Goroutines` | `/sched/goroutines:goroutines` |
| `CgroupMemLimit` | the effective cgroup v2/v1 memory limit (own cgroup or any ancestor), or -1 |

Two behavioral notes — both shipped in the package docs:

1. `Profile.GOMAXPROCS` on Go 1.25+ disables `updatemaxprocs` — the runtime
   stops re-reading cgroup CPU limits. On Kubernetes with autoscaling limits
   this can leave a process permanently misconfigured. The package warns in
   the doc comments; it does not prevent it.
2. `QuietGC` returns `QuietResult` — duration, heap object bytes before and
   after (`Reclaimed()` is the difference), and `/gc/heap/live` before and
   after. Note that `/gc/heap/live` only updates at the end of a cycle, so it
   describes how the live set changed between cycles, not what one cycle
   freed. `runtime.GC()` itself allocates: a forced GC inside a `guard`
   window reports ~18–23 objects.

## 5. What `bench` adds

*Shipped.* Every item below is in `bench`/`Report` today.

- Dual series: `Latency` (corrected, `done − intended`)
  and `Service` (`done − actual`). The gap between them is the coordinated-
  omission lesson in one number pair.
  and `P99Service` (`done − actual`). The gap between them is the coordinated-
  omission lesson in one number pair.
- Run-level deltas of the §2.4 CPU-class counters: `MarkAssistCPU`,
  `GCTotalCPU`, plus `GCCycles` split into forced and automatic. These are valid
  only at this level, and only when at least one cycle completed in the run.
  When a run completed a cycle, `bench` calls `runtime.GC()` after the run
  and re-reads the CPU classes so the last partial cycle is counted; that
  extra cycle is excluded from `GCCycles`. Per-op tagging stays out.
- `/sched/pauses/total/gc:seconds` (not the deprecated `/gc/pauses`; Go
  1.22+) and `/sched/latencies:seconds` histograms, read once before and once
  after the run, with percentiles computed against the bucket boundaries the
  runtime reports. Their deltas are the two distributions that explain the remaining tail
  once allocations are zero.

## 6. What `hotpathcheck` could also flag (not implemented)

Beyond `noalloc`'s allocation sites — none of these are in the shipped
analyzer; listed as considered future extensions:

- `runtime.SetFinalizer` / `runtime.AddCleanup` calls in an annotated
  function's callee tree — queued cleanups make every future cycle heavier.
- `defer` inside a `//hotpath:noalloc` loop body — legal, cheap in 1.14+, but
  deferred-call chains in hot loops are a classic regression; flag with a
  warning severity, not a failure.
- A `//hotpath:pointerfree` comment on a type declaration — walks fields,
  rejects `*`, `string`, slice, map, chan,
  interface, `unsafe.Pointer`. No constructor required, works on the user's own
  `Order` struct today.

## 7. Ideas beyond the shipped packages (not implemented)

Patterns that build on this surface. None of them ship in go-hotpath today:

**Flight recorder evidence.** Go 1.25 shipped
`runtime/trace.FlightRecorder`: a rolling window of the execution trace,
`WriteTo(w)` on demand. Wire it so a `!Quiet` result dumps the last
2 seconds of trace to a file — the user opens it in `go tool trace` and *sees*
the mark-assist on their goroutine. The runtime allows one flight recorder
per process, so this has to be a process-wide singleton.

**Warmup recipe (`profile` docs or `profile.Warmup`).** Preallocate structures
→ run synthetic traffic → `profile.QuietGC()` → `profile.Apply(...)`. Answers the
universal "first 10 seconds are noisy" complaint. ~15 lines.

**GODEBUG preset documentation.** `disablethp=1` (Linux THP stalls),
`gctrace=1` in staging, `asyncpreemptoff` for differential experiments. A
`profile.GODEBUG() string` that prints the recommended string is documentation
with teeth.

**The `explain` idea.** A `bench`/`guard` post-pass that reads the counters
and emits "dominant mechanism: mark assist (8.1 CPU-s of 12.3 total GC CPU-s);
lever: reduce allocations or raise GOGC." Diagnosis, not detection — the
strongest UX improvement available.

**Topology guidance.** E5 in BENCHMARK.md measured the in-process hole: a
zero-allocation book's p99 moved ~3 µs → ~33 µs from neighbor allocation,
while equally busy non-allocating neighbors barely moved it. When `guard` reports
scheduler/GC noise that `profile` cannot fix, the answer is a separate process
with a shared-memory ring. Document the
decision boundary now so users don't expect `SilentWindow` to do process
isolation.

## 8. What remains unreachable — keep saying so

- No per-goroutine GC exemption. `LockOSThread` pins a thread; STW still halts
  it. Only process isolation fixes this.
- No way to suppress mark assists selectively — they follow from allocating
  during a cycle. The fix is not allocating, which is why `noalloc` +
  `AllocsPerRun` matter more than any runtime knob.
- No user-space control over write barriers — they are active while marking is
  active, on every goroutine. Pointer-free hot paths reduce *both* the number
  of barrier invocations and the scannable heap that drives cycle frequency.
- The analyzer sees syntax, not escape decisions — `AllocsPerRun` remains the
  ground truth, forever.

The product's credibility lives in the gap between §7 (what it can deliver) and
§8 (what it never will). A README that states both columns is a better
portfolio piece than one that states only the first.
