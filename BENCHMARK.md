# Benchmarks

Every number below is reproducible with the commands in each section. Method
first, numbers second.

## Environment & method

| | |
|---|---|
| Date | 2026-10-06 |
| Machine | Apple M4 Pro (10 performance + 4 efficiency cores), 24 GB, macOS, on AC power |
| Toolchains | **go1.22.12** (library floor), **go1.26.8** (recommended), **go1.27.1** (latest) — selected with `GOTOOLCHAIN` |
| Harness | `bench` — open-loop, intended-time pacing; late ops recorded, never dropped |
| Workload | order-book `Apply` per op: 50% new-limit / 30% cancel / 10% replace-down / 10% market-cross, 4096 ticks, 100k live-order capacity |
| Cells | 100k ops/s, 1 s warmup + 4 s measured per cell, **3 repeats**, median reported |

Reading rules:

- Percentiles are histogram bucket **upper bounds** — they can only overestimate.
  `Latency` (`p99`, `p99.9`) is done−intended, corrected for coordinated
  omission; `svc` is done−actual, what a closed-loop benchmark would print.
- The machine was a working laptop (browser, IDE, a disk scanner running;
  load average 5–15 on 14 cores). That noise lands in **p99.9 and max**,
  which vary run to run by 10–100×; **p50, p99 and service time are stable**
  across repeats and toolchains. Quote those, not the extremes.
- E4 cells complete **zero** GC cycles, so a late op there is machine
  interference by definition: E4 repeats with more than 1.5% late ops are
  discarded, and the table says how many survived. E5 is not filtered — its
  lateness is the effect being measured.

## 1. Instrumentation overhead — the library's own cost

`go test -run=^$ -bench=. -benchmem -count=3 ./guard ./internal/rtm ./bench`,
median of 3:

| Operation | go1.22.12 | go1.26.8 | go1.27.1 | allocs |
|---|---|---|---|---|
| `guard` `Begin`+`End` window | 471 ns | 443 ns | 438 ns | 0 |
| `guard` window, parallel (one Guard per goroutine) | 1,205 ns | 964 ns | 898 ns | 0 |
| `guard.Exact` (2× stop-the-world `ReadMemStats`; tests only) | 50.5 µs | 50.3 µs | 48.6 µs | 4 |
| `rtm.Set.Read` (5 scalars, reused buffer) | 233 ns | 215 ns | 211 ns | 0 |
| `rtm.Set.Read`, parallel | 576 ns | 480 ns | 447 ns | 0 |
| `rtm.HistSet.Read` (2 histograms, reused) | 271 ns | 260 ns | 263 ns | 0 |
| `bench` histogram `Record` | 1.8 ns | 1.9 ns | 1.8 ns | 0 |

The zero-allocation guarantees hold on every supported toolchain. Contended
(parallel) reads improve on newer Go — the `runtime/metrics` read path
serializes on a runtime semaphore, and that path got cheaper. A ~450 ns
window around a ≥50 µs batch costs under 1%; around a 200 ns operation it
would cost more than the operation, which is why `guard` belongs on batch or
session boundaries.

## 2. E5 — a zero-allocation book next to allocating neighbors

Four neighbor goroutines run alongside the book in three modes: **clean**
(none), **+4spin** (busy, never allocate — the control), and **+4alloc**
(each allocates 1 KiB per iteration). p99 latency, median of 3 runs:

| Book | Neighbors | go1.22.12 | go1.26.8 | go1.27.1 | GC cycles (1.26.8) |
|---|---|---|---|---|---|
| **C** (0 allocs/op) | clean | 3.1 µs | 3.2 µs | 3.3 µs | 0 |
| **C** | +4spin | 2.5 µs | 8.5 µs | 6.5 µs | 0 |
| **C** | **+4alloc** | **30 µs** | **33 µs** | **35 µs** | **354** |
| A (~3 allocs/op) | clean | 10 µs | 6.7 µs | 6.8 µs | 0 |
| A | +4spin | 11 µs | 8.7 µs | 7.8 µs | 0 |
| A | +4alloc | 40 µs | 34 µs | 42 µs | 363 |

> **Book C allocates nothing, yet allocating neighbors in the same process
> move its p99 ~10× (3.2 µs → 33 µs on go1.26.8), on every toolchain from 1.22
> to 1.27. Equally busy neighbors that don't allocate move it far less (to
> 2.5–8.5 µs). The cause is the garbage collector, not CPU contention.**

Attribution for the go1.26.8 runs (median of 3):

| Cell | GC cycles | GC CPU (process) | GC STW p99 | GC STW max | svc p99.9 | late ops |
|---|---|---|---|---|---|---|
| C clean | 0 | 0 | — | — | 3.3 µs | 0.42% |
| C +4spin | 0 | 0 | — | — | 2.8 µs | 0.87% |
| C +4alloc | 354 | 271 ms | 98 µs | 229 µs | 3.1 µs | 1.71% |
| A +4alloc | 363 | 1,334 ms | 98 µs | 164 µs | 15 µs | 1.85% |

What the columns establish:

- **Service time does not move** (C: 3.3 µs clean, 3.1 µs with allocating
  neighbors). The book does the same work; ops start late. A closed-loop
  benchmark would report no regression at all.
- **The mechanism is process-wide GC activity**: ~350 cycles in 4 s, two
  stop-the-world pauses each (p99 ~100 µs), plus background mark workers
  taking cores. Book C cannot be charged mark assists — it never allocates;
  the GC CPU column is the whole process, almost all of it caused by the
  neighbors. That is the point: the process is the interference boundary.
- **A's own allocations add GC CPU** (1,334 ms vs 271 ms with the same
  neighbors) — the allocating book makes every cycle more expensive for
  itself.
- An earlier run on a quieter machine (go1.26.0) measured C at 0.9 µs clean
  and 16.5 µs with allocating neighbors (18×). The baseline is
  load-sensitive; the degraded p99 and the cycle counts are not.

Reproduce (repeat 3×, take medians):

```bash
cd spike && GOTOOLCHAIN=go1.26.8 go run . -e1=false -e2=false -e3=false -e5 -dur=4s -warmup=1s
```

## 3. E4 — the books without GC pressure

The full matrix — books × {default, `GOGC=off`, `silent(1GiB)`} — through
`bench`. **No GC cycle completed in any cell on any toolchain**, so the three
GC configurations are interchangeable here and only the default rows are
shown. Medians of the repeats that passed the late-op filter:

| Book | Toolchain | kept | p50 | p99 | svc p99.9 | process alloc / run |
|---|---|---|---|---|---|---|
| A | go1.22.12 | 3/3 | 334 ns | 10 µs | 16 µs | 12.7 MiB |
| A | go1.26.8 | 3/3 | 375 ns | 6.3 µs | 12 µs | 11.7 MiB |
| A | go1.27.1 | 1/3 | 334 ns | 11 µs | 19 µs | 11.7 MiB |
| B | go1.22.12 | 3/3 | 250 ns | 4.3 µs | 3.9 µs | 223 KiB |
| B | go1.26.8 | 3/3 | 250 ns | 3.3 µs | 3.5 µs | 0 B |
| B | go1.27.1 | 3/3 | 208 ns | 3.2 µs | 3.5 µs | 0 B |
| C | go1.22.12 | 3/3 | 250 ns | 3.5 µs | 3.7 µs | 0 B |
| C | go1.26.8 | 3/3 | 250 ns | 3.3 µs | 3.4 µs | 0 B |
| C | go1.27.1 | 3/3 | 208 ns | 3.3 µs | 3.8 µs | 0 B |

- **Allocation costs latency before the GC ever runs.** With zero cycles,
  Book A is 2–3× slower than B and C at p99 and 3.5–5× slower in service
  time: heap growth, allocator work, and cache misses on pointer-chasing.
- **B and C are equivalent without GC pressure.** Pointer-freedom pays off
  when the collector is active (E5), not when it's idle.
- Book B allocates ~220 KiB per run on go1.22 and nothing on 1.26+; the hand-
  tuned book hit an allocation the older compiler or runtime didn't avoid.
- The go1.27.1 A row kept only one repeat: a disk scan on the machine
  overlapped those runs (one discarded repeat had 7% late ops and a 16 ms p99
  with no GC at all).

Reproduce:

```bash
cd spike && GOTOOLCHAIN=go1.26.8 go run . -e1=false -e2=false -e3=false -e4 -dur=4s -warmup=1s -repeats=3
```

## 4. E3 — the cost of a generation check

A handle with a generation counter (to detect use-after-free in a slab)
versus a bare index, 50M lookups into a 40-byte slot array:

| | go1.22.12 | go1.26.8 | go1.27.1 |
|---|---|---|---|
| bare index | 0.46 ns | 0.57 ns | 0.40 ns |
| index + generation check | 0.54 ns | 0.61 ns | 0.56 ns |

The check costs a fraction of a nanosecond once the branch is predicted —
generational handles are effectively free. (The original spike reported
0.23 ns: its loop results were never read and the compiler deleted the
loops. Fixed.)

## 5. Version notes

- Every package behaves identically on 1.22, 1.26 and 1.27, including the
  `GCPause*` report fields: the `/sched/pauses` histograms exist from Go 1.22.
  Newer metrics the library can use when present (cgroup GOMAXPROCS counters,
  1.25+; finalizer/cleanup and goroutine-state counters, 1.26+) are probed,
  never required.
- `hotpathcheck` builds with Go 1.26 (pinned `toolchain go1.26.8`) and vets
  code for any version; the same binary reports the same diagnostics when
  driven by go1.22's `go vet`.

## 6. Known caveats

1. One machine, one architecture (darwin/arm64). Linux/amd64 numbers will
   differ in absolute terms; the E5 effect is a property of the Go runtime,
   not of macOS.
2. p99.9 and max are dominated by machine noise on a working laptop. For
   numbers you intend to quote at p99.9, run on an isolated host.
3. `alloc` columns are process-wide, including the harness's own setup — read
   them as "did the process allocate", not "did the book".
4. If `Late%` climbs in a cell with zero GC cycles, the machine couldn't keep
   the rate; that's a capacity or interference signal, not a tail result.

## 7. What changed vs the original spike

| Original `spike` | Corrected `bench` / spike |
|---|---|
| Dropped ops >10 ms late without recording them | Records all; `Late%` column |
| Stored raw samples and sorted at the end | Fixed log-linear histogram, ~2 ns/record, 0 allocs |
| Cycle count as the only GC evidence | Cycles, forced split, process alloc, GC CPU classes, STW pause and scheduler histograms |
| `buf := make(); _ = buf` neighbors (dead code — allocated nothing) | Escape-forced neighbors (~10 GiB per run) plus a non-allocating control |
| E2 conclusion unprovable (cycle count 1, CPU contention equally plausible) | E5 separates them: busy-but-non-allocating neighbors don't reproduce the regression |
| E3 loops deleted by the compiler (0.23 ns) | Results kept live; real cost measured |
