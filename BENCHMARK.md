# Benchmarks

Every number below is reproducible. Method first, numbers second.

## Environment & methodology

| | |
|---|---|
| Machine | Apple M4 Pro, 24 GB, macOS (darwin/arm64) |
| Toolchains | **go1.22.12** (library floor) and **go1.26.0** (dev/default); `hotpathcheck` itself is built on 1.26 but vets any version's code |
| Harness | `bench` — open-loop, intended-time pacing; **late ops recorded, never dropped** |
| Workload | order-book `Apply` per op: 50% new-limit / 30% cancel / 10% replace-down / 10% market-cross, 4096 ticks, 100k live-order capacity |
| Cells | 100k ops/s, 4 s measure + 1 s warmup per cell, 3 repeats (E4); 4 s single runs (E5) |
| Reproduce | `go run ./spike -e1=false -e2=false -e3=false -e4 -e5 -dur=4s -repeats=3` |

Percentiles report each histogram bucket's **upper bound** — they can only
overestimate a tail, never hide it. `Latency` is done−intended (corrected);
`svc` is done−actual (what a closed loop prints).

## 1. Instrumentation overhead — the product's own cost

Measured on **both supported toolchain generations** (same machine):

| Operation | go1.22.12 | go1.26.0 | allocs (both) |
|---|---|---|---|
| `guard` `Begin`+`End` window | 502 ns | **452 ns** | 0 |
| `guard` window, parallel (per-goroutine Guards) | 1,312 ns | 1,012 ns | 0 |
| `guard.Exact` (2× STW ReadMemStats — tests only) | 75.8 µs | ~51 µs | 4 |
| `rtm.Set.Read` (5 scalars, reused buffer) | 239 ns | 232 ns | 0 |
| `bench` histogram `Record` | **1.97 ns** | **2.0 ns** | 0 |

Reproduce: `go test -run=^$ -bench=. -benchmem ./guard ./bench ./internal/rtm`
(under go1.22: `GOTOOLCHAIN=go1.22.12 GOWORK=off go test ...`).

Two findings in the version delta: the **zero-alloc guarantees hold on both**
toolchains (the reuse-buffer trick predates and outlives any single GC), and
`Exact` is ~50% slower on go1.22 — the pre-Green-Tea collector spends longer
in its stop-the-world sweep that `ReadMemStats` triggers. Steady-state reads
(`Set`, `HistSet`, `Record`) are toolchain-flat: they never touch the GC.

A ~450–500 ns guard around a ≥50 µs batch costs <1%. Around a 200 ns hot op
it costs ~2.5× — that's why the docs steer `guard` at batch/session windows.

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

## 5. Toolchain comparison — same workload, go1.22 vs go1.26

E4 rerun on **go1.22.12** (the library's minimum: pre-Green-Tea collector,
pre-`/sched/pauses` metrics) vs **go1.26.0**. Same flags:
`-e4 -dur=2s -warmup=500ms -repeats=1` — one rep, so treat as directional,
not quotable.

| Cell | 1.22 p99 | 1.26 p99 | 1.22 p99.9 | 1.26 p99.9 | 1.22 svc p99.9 | 1.26 svc p99.9 |
|---|---|---|---|---|---|---|
| A default | 9.8 µs | 8.0 µs | 21.3 µs | 35.0 µs | 13.9 µs | 14.1 µs |
| A GOGC=off | 9.9 µs | 6.1 µs | 19.3 µs | 13.3 µs | 13.1 µs | 9.6 µs |
| A silent(1GiB) | 9.3 µs | 6.9 µs | 15.6 µs | 32.2 µs | 12.2 µs | 11.3 µs |
| B default | 3.2 µs | 3.2 µs | 10.7 µs | 16.4 µs | 3.6 µs | 3.4 µs |
| C default | 3.3 µs | **1.2 µs** | 13.0 µs | 10.9 µs | 4.0 µs | 1.1 µs |
| C silent(1GiB) | 3.8 µs | 3.0 µs | 12.8 µs | 15.8 µs | 3.2 µs | 3.1 µs |

What to read and not read into it:

- **No GC cycle completed on either toolchain** (all `GC=0`) — the
  differences are allocation-path and scheduler noise, not collection.
- `svc p99.9` is nearly toolchain-flat — as it should be: service time is
  dominated by the machine and the data structure, not the GC version.
- Book C's p99 looks better on 1.26 (1.2 vs 3.3 µs) — consistent with the
  Green Tea direction (1.25+) — but a single rep can't prove it; the
  multi-repeat table above on 1.26 is the quotable number set.
- **Metric coverage differs by version**: on go1.22 the `GCPause*` report
  fields are silently 0 (the histogram doesn't exist pre-1.23). `bench`
  degrades rather than fails — intentional.
- Version support reality: `guard`/`profile`/`bench` run on go1.22+;
  `hotpathcheck` is *built* with 1.26 but vets code targeting any version
  (verified: the 1.26-built binary produces the same 4 diagnostics when
  driven by go1.22's `go vet`).

## 6. What changed vs the original spike

| Original `spike` | Corrected `bench` |
|---|---|
| Dropped ops >10 ms late without recording them | Records all; `Late%` column |
| Stored raw samples + sorted at end | Fixed log-linear histogram, 2 ns/record, 0 allocs |
| Cycle count only as GC evidence | cycles + forced split + alloc bytes + CPU-class attribution + pause/sched histograms |
| `buf := make(); _ = buf` neighbors (dead code — allocated nothing) | escape-forced neighbors; alloc column now shows ~10 GiB |
| E2 conclusion was unprovable (cycle=1, CPU contention equally plausible) | E5 attributes the mechanism: 338 cycles + 8.3 ms of mark-assist |
