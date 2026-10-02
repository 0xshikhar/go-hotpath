# Benchmarks

Every number below is reproducible. Method first, numbers second.

## Environment & methodology

| | |
|---|---|
| Machine | Apple M4 Pro, 24 GB, macOS (darwin/arm64) |
| Go | go1.26.0 |
| Harness | `bench` — open-loop, intended-time pacing; **late ops recorded, never dropped** |
| Workload | order-book `Apply` per op: 50% new-limit / 30% cancel / 10% replace-down / 10% market-cross, 4096 ticks, 100k live-order capacity |
| Cells | 100k ops/s, 4 s measure + 1 s warmup per cell, 3 repeats (E4); 4 s single runs (E5) |
| Reproduce | `go run ./spike -e1=false -e2=false -e3=false -e4 -e5 -dur=4s -repeats=3` |

Percentiles report each histogram bucket's **upper bound** — they can only
overestimate a tail, never hide it. `Latency` is done−intended (corrected);
`svc` is done−actual (what a closed loop prints).

## 1. Instrumentation overhead — the product's own cost

| Operation | ns/op | allocs |
|---|---|---|
| `guard` `Begin`+`End` window | **452** | **0** |
| `guard` window, parallel (per-goroutine Guards) | 1,012 | 0 |
| `guard.Exact` (2× STW ReadMemStats — tests only) | ~51,000 | 4 |
| `rtm.Set.Read` (5 scalars, reused buffer) | 232 | 0 |
| `bench` histogram `Record` | **2.0** | **0** |
| `profile.Read` (Snapshot, 8 scalars) | ~350 | 0 |

Reproduce: `go test -run=^$ -bench=. -benchmem ./guard ./bench ./internal/rtm`.

A 452 ns guard around a ≥50 µs batch costs <1%. Around a 200 ns hot op it
costs ~3× — that's why the docs steer `guard` at batch/session windows.

## 2. E4 — the clean run, corrected harness

100k ops/s, 4 s cells, 3 repeats. **No GC cycle completed in any cell** — at
this duration and allocation rate, none of the books trigger the collector.
That is itself the honest result: at this scale the books' difference is
allocation-path cost, not GC interference.

| Impl | Config | rep | p50 | p99 | p99.9 | svc p99.9 | alloc |
|---|---|---|---|---|---|---|---|
| A | default | r3 | 333 ns | 5.46 µs | 13.5 µs | 7.09 µs | 11.7 MiB |
| A | GOGC=off | r3 | 333 ns | 5.67 µs | 13.2 µs | 7.30 µs | 11.7 MiB |
| A | silent(1GiB) | r3 | 333 ns | 5.38 µs | 15.9 µs | 7.54 µs | 11.8 MiB |
| B | default | r3 | 250 ns | 1.00 µs | 9.96 µs | 3.25 µs | 0 B |
| C | default | r3 | 250 ns | 1.25 µs | 15.1 µs | 3.29 µs | 0 B |
| C | silent(1GiB) | r3 | 250 ns | 0.88 µs | 8.00 µs | 1.08 µs | 0 B |

What it says, carefully:

- **Allocation cost is visible without any GC.** Book A pays ~3× at p50/p99
  purely for allocating ~3 objects per op — heap growth, cache misses,
  allocator bookkeeping — with zero cycles fired.
- **B and C are within noise of each other** at steady state. Pointer-freedom
  buys its advantage when the GC is *active*, not when it's absent — which is
  what E5 demonstrates.
- GC-config columns are interchangeable here because **nothing collected**.
  Quoting them as "GOGC=off didn't help" would be wrong — there was nothing
  to help.
- `max` per cell is a single sample and unstable (60–950 µs across rows);
  don't quote it.

## 3. E5 — the noisy neighbor, now actually allocating

4 allocating goroutines alongside the book. **Audit note:** the original
spike's neighbors were dead-code eliminated (`make` + `_ = buf`); this rerun
forces real allocations, so these numbers are the *first* honest noisy-run.

| Impl | Neighbor | p99 | p99.9 | max | svc p99.9 | GC cycles | alloc (proc-wide) | assist CPU |
|---|---|---|---|---|---|---|---|---|
| A | none | 5.3 µs | 18.3 µs | 220 µs | 7.4 µs | 0 | 11.8 MiB | 0 |
| A | 4 alloc goroutines | **27.1 µs** | **70.3 µs** | 134 µs | 12.8 µs | **354** | 10.0 GiB | 9.2 ms |
| C | none | 0.9 µs | 10.8 µs | 62 µs | 1.25 µs | 0 | 0 B | 0 |
| C | 4 alloc goroutines | **16.5 µs** | **62.7 µs** | 124 µs | 2.96 µs | **338** | 10.2 GiB | 8.3 ms |

The headline finding, stated precisely:

> **Book C allocates zero bytes, yet 338 GC cycles in the same process moved
> its p99 from 0.9 µs to 16.5 µs — an 18× tail regression caused entirely by
> another goroutine's allocation.**

And the columns now settle *which* mechanism did it — the thing E2 could
never prove:

- `GC=338` cycles ran in the window (exact count).
- `assist CPU = 8.25 ms` of mark work ran **inside the book's own goroutine**
  — the assist tax, directly attributed.
- `svc p99.9` stayed at 2.96 µs while `Latency p99.9` hit 62.7 µs — the
  tail is lateness (scheduler preemption + STW), not slower work. A
  closed-loop benchmark would have printed `p99.9 ≈ 3 µs` and missed it.
- `alloc` is process-wide: C's own share is 0; the 10.2 GiB is the
  neighbors' — the counter can't attribute it to them, which is exactly the
  same-process-isolation argument the docs make.

## 4. Known caveats — read before quoting

1. **3 repeats, one machine, one toolchain.** Single-run cells have ±2× max
   variance. p50/p90/p99 are stable; max is not.
2. **GOGC config rows are interchangeable in E4** because no cycle fired.
   The silent-window difference is only visible *during* GC activity — E5
   is the discriminating experiment.
3. **The benchmark process itself allocates** (pre-generated commands,
   harness state). `alloc` is process-wide — interpret it as "did the
   process allocate", not "did the book".
4. `rate × duration` beyond what the machine can sustain makes `Late%`
   climb — the harness couldn't keep up, which is a capacity signal, not a
   tail signal.

## 5. What changed vs the original spike

| Original `spike` | Corrected `bench` |
|---|---|
| Dropped ops >10 ms late without recording them | Records all; `Late%` column |
| Stored raw samples + sorted at end | Fixed log-linear histogram, 2 ns/record, 0 allocs |
| Cycle count only as GC evidence | cycles + forced split + alloc bytes + CPU-class attribution + pause/sched histograms |
| `buf := make(); _ = buf` neighbors (dead code — allocated nothing) | escape-forced neighbors; alloc column now shows ~10 GiB |
| E2 conclusion was unprovable (cycle=1, CPU contention equally plausible) | E5 attributes the mechanism: 338 cycles + 8.3 ms of mark-assist |
