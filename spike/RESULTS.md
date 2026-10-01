# Phase 0 Results

**Decision:** Continue — superseded. The next build is `guard`/`profile`/`bench`/`hotpathcheck`, not Phase 1 & 2 of the old roadmap. See `doc/research/architecture-and-plan.md`.

**Corrections to this summary (2026-10-06, re-reading the table below):**

1. Book C does **not** "eliminate GC cycles in steady-state". Every default-GC row for C recorded exactly 1 cycle. The strong result is that C's `Apply` allocates 0.00 objects/op (`TestAllocsPerRun` in `spike_test.go`). The flatter tail comes from no allocation and a smaller scannable heap, not from zero cycles.
2. `GOGC=off` is not a uniform win. It gave B the best p99.9 at 100k (152 µs) but made C worse at the same rate (312 µs vs 241 µs default) and made B much worse at 50k (2.55 ms p99.9, ~384 MB heap). `GOGC=off` must be paired with a memory limit and measured per workload — this is `profile`'s reason to exist.
3. E2 does not prove a GC mechanism. GC cycle count stayed at 1 with and without the neighbor. C's max moved 787 µs → 3.04 ms while A's *improved* (1.24 ms → 773 µs). CPU/cache contention is an equally good explanation. The `bench` rerun will separate the two using `/cpu/classes/gc/*` run-level counters.
4. **Ops more than 10 ms late are counted as dropped and are not recorded** in the latency histograms (`driver.go`). Every p99.9 and max below is therefore a lower bound — the true tails are at least this bad.
5. One run per cell. Any number quoted publicly must be re-measured with ≥3 repeats.
6. E3's 0.23 ns/op is below the cost of an L1 load — the compiler eliminated the work. Do not cite it.

**2026-10-06 post-restructure audit finding:**

7. E2's noisy-neighbor goroutines were **dead-code eliminated** — `buf := make([]byte, 1024); _ = buf` allocates nothing; the compiler removes the whole thing. E2's "neighbor" was a no-op; its max movement was scheduler noise. Fixed (`allocSink.Store(&buf)`), and the corrected rerun — E5, driven by `bench` — is in `../BENCHMARK.md`. The real E5 shows 338–354 GC cycles, ~10 GiB allocated by the neighbors, and Book C's p99 moving 0.9 µs → 16.5 µs despite allocating 0 B itself.

---

## Machine
- **Date:** 2026-10-06 00:50:38
- **Go version:** go1.26.0
- **GOOS / GOARCH:** darwin / arm64
- **CPU:** Apple M4 Pro
- **RAM:** 24 GB Unified Memory

---

## Workload
- **Mix:** 50% New Limit, 30% Cancel, 10% ReplaceDown, 10% MarketCross
- **Band:** 4096 ticks
- **Capacity:** 100,000 live orders
- **Driver:** Open-loop scheduler with Coordinated Omission correction (Gil Tene model)

---

## E1 — The Gap (Clean Run, No Noisy Neighbor)

| Impl | GC Config | Rate (target) | Drop% | p50 | p90 | p99 | p99.9 | max | GC Cycles | Heap Live MB |
|---|---|---|---|---|---|---|---|---|---|---|
| A (Idiomatic) | default | 50000 | 0.00% | 500ns | 4.084µs | 6.792µs | 185.625µs | 1.307042ms | 1 | 41.85 |
| B (Hand-tuned) | default | 50000 | 0.00% | 250ns | 792ns | 3.542µs | 30.542µs | 656.667µs | 1 | 28.93 |
| B (Hand-tuned) | GOGC=off | 50000 | 0.00% | 292ns | 834ns | 7.292µs | 2.554334ms | 5.900125ms | 0 | 383.68 |
| C (Pointer-free) | default | 50000 | 0.00% | 208ns | 750ns | 3.416µs | 59.25µs | 716.458µs | 1 | 29.63 |
| C (Pointer-free) | GOGC=off | 50000 | 0.00% | 208ns | 750ns | 3.291µs | 54.083µs | 1.010791ms | 0 | 419.35 |
| A (Idiomatic) | default | 100000 | 0.13% | 500ns | 4.708µs | 150.166µs | 8.447833ms | 12.651583ms | 1 | 38.50 |
| B (Hand-tuned) | default | 100000 | 0.00% | 292ns | 875ns | 46.875µs | 2.260209ms | 4.629875ms | 1 | 38.79 |
| B (Hand-tuned) | GOGC=off | 100000 | 0.00% | 250ns | 792ns | 5.584µs | 152.375µs | 1.221ms | 0 | 365.85 |
| C (Pointer-free) | default | 100000 | 0.00% | 250ns | 833ns | 9.958µs | 241.208µs | 1.092ms | 1 | 43.85 |
| C (Pointer-free) | GOGC=off | 100000 | 0.00% | 250ns | 834ns | 9.417µs | 312.292µs | 1.485084ms | 0 | 393.09 |
| A (Idiomatic) | default | 200000 | 0.00% | 458ns | 4.25µs | 6.166µs | 168.291µs | 1.712041ms | 2 | 52.06 |
| B (Hand-tuned) | default | 200000 | 0.03% | 208ns | 792ns | 18.25µs | 7.31825ms | 10.003667ms | 1 | 52.65 |
| B (Hand-tuned) | GOGC=off | 200000 | 0.00% | 250ns | 833ns | 6.583µs | 268.125µs | 1.336792ms | 0 | 403.35 |
| C (Pointer-free) | default | 200000 | 0.00% | 209ns | 750ns | 3.209µs | 256.5µs | 1.677834ms | 1 | 52.60 |
| C (Pointer-free) | GOGC=off | 200000 | 0.00% | 209ns | 834ns | 13.334µs | 1.701084ms | 3.104625ms | 0 | 440.06 |

## E2 — The Same-Process Hole (Noisy Neighbor Stress)

| Impl | Neighbor | Rate | Drop% | p50 | p90 | p99 | p99.9 | max | GC Cycles |
|---|---|---|---|---|---|---|---|---|---|
| A (Idiomatic) | None | 100000 | 0.00% | 459ns | 4.042µs | 5.75µs | 417µs | 1.243584ms | 1 |
| A (Idiomatic) | 4 allocators | 100000 | 0.00% | 459ns | 4.834µs | 7.959µs | 30.084µs | 772.917µs | 1 |
| C (Pointer-free) | None | 100000 | 0.00% | 208ns | 791ns | 5.166µs | 162.416µs | 787µs | 1 |
| C (Pointer-free) | 4 allocators | 100000 | 0.00% | 250ns | 917ns | 2.709µs | 432.875µs | 3.03725ms | 1 |

## E3 — Handle Generation Check Overhead (Microbenchmark)

- Bare Index lookup : 0.23 ns/op (11.673375ms total for 50000000 ops)
- Handle Gen check  : 0.23 ns/op (11.443542ms total for 50000000 ops)
- Delta overhead    : +-0.00 ns/op

