<div align="center">

# go-hotpath

**Answer the question every latency-sensitive Go system can't: did the GC interfere with my hot path?**

[![Go Version](https://img.shields.io/badge/Go-1.22%2B-00ADD8.svg?style=flat-square&logo=go)](https://go.dev/)
[![Go Reference](https://img.shields.io/badge/Go%20Reference-pkg.go.dev-00ADD8.svg?style=flat-square&logo=go)](https://pkg.go.dev/github.com/0xshikhar/go-hotpath)
[![CI](https://img.shields.io/github/actions/workflow/status/0xshikhar/go-hotpath/ci.yml?branch=master&style=flat-square&logo=github-actions)](https://github.com/0xshikhar/go-hotpath/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg?style=flat-square)](LICENSE)

[Quick Start](#quick-start) · [Benchmarks](BENCHMARK.md) · [Integrations](INTEGRATIONS.md) · [Docs](#documentation) · [Releases](https://github.com/0xshikhar/go-hotpath/releases)

</div>

---

A small, dependency-free Go toolkit for detecting, measuring, and controlling
garbage-collector interference in latency-sensitive systems — trading engines,
exchange infrastructure, feed decoders, real-time services.

Go's GC is good — but a hot path needs to *know* when it ran and be able to
*prove* a function can't allocate. Today that's tribal knowledge plus
closed-loop benchmarks that hide the tail. `go-hotpath` makes it a first-class
API.

## Packages

| Package | What it does | Cost |
|---|---|---|
| [`guard`](./guard) | Zero-allocation GC-interference windows (`Begin`/`End`) + exact `ReadMemStats` assertions for tests | ~450 ns, 0 allocs |
| [`bench`](./bench) | Open-loop latency harness — coordinated-omission-safe, dual Latency/Service series, GC CPU attribution | ~2 ns per record |
| [`profile`](./profile) | Reversible GOGC/GOMEMLIMIT/GOMAXPROCS control — `Apply`/`Session`, `SilentWindow`, `QuietGC`, `Snapshot` | startup/boundary |
| [`hotpathcheck`](./cmd/hotpathcheck) | `go vet` analyzer enforcing `//hotpath:noalloc` across packages | CI gate |

## Quick start

```bash
go get github.com/0xshikhar/go-hotpath/guard
go get github.com/0xshikhar/go-hotpath/profile
go get github.com/0xshikhar/go-hotpath/bench
go install github.com/0xshikhar/go-hotpath/cmd/hotpathcheck@latest
```

Production hot path — detect interference in a bounded window:

```go
g := guard.New()                       // once, at init — one per goroutine
w := g.Begin()                         // ~226 ns, zero allocations
processBatch(batch)
if r := w.End(); !r.Quiet() {          // a GC cycle completed inside
	log.Print(r.Explain())             // "2 automatic GC cycle(s) completed…"
}
```

Tests — exact allocation proof:

```go
guard.Assert(t, func() { book.Apply(&cmd, &ev) })          // STW — tests only
guard.AssertWithin(t, guard.Budget{Allocs: 2, Bytes: 64}, fn)
```

CI — write-time allocation gate:

```go
//hotpath:noalloc
func (e *Engine) apply(c *Command) { /* annotated helpers only */ }
```
```bash
go vet -vettool=$(which hotpathcheck) ./...   # fails on new alloc sites
```

Reversible GC control:

```go
s, err := profile.Apply(profile.SilentWindow(512 << 20)) // GOGC=off + limit
if err != nil { log.Fatal(err) }
res := profile.QuietGC()   // collect once at a boundary of your choosing
defer s.Undo()             // restore verified prior settings
```

Tail-latency measurement:

```go
rep := bench.New(100_000, bench.WithDuration(10*time.Second)).Run(fn)
fmt.Println(rep.String())   // dual Latency/Service series + GC attribution
```

## Benchmarks

An order-book workload at 100k ops/s, open-loop, on an Apple M4 Pro; p99,
median of 3 runs. Book C allocates nothing. Four neighbor goroutines in the
same process either do nothing, spin without allocating (the control), or
allocate:

| Book | Neighbors | go1.22.12 | go1.26.8 | go1.27.1 | GC cycles |
|---|---|---|---|---|---|
| C (0 allocs/op) | none | 3.1 µs | 3.2 µs | 3.3 µs | 0 |
| C | 4 busy, non-allocating | 2.5 µs | 8.5 µs | 6.5 µs | 0 |
| C | 4 allocating | **30 µs** | **33 µs** | **35 µs** | ~354 |
| A (~3 allocs/op) | 4 allocating | 40 µs | 34 µs | 42 µs | ~363 |

A book that never allocates still takes a ~10× p99 hit from the GC work its
neighbors cause, on every toolchain from 1.22 to 1.27, while its service
time stays at ~3 µs. Busy neighbors that don't allocate don't reproduce
it. The process is the interference boundary, and this is what `guard` and
`bench` make visible.

Methodology, the instrumentation-overhead table (`guard` window ~440 ns,
`bench` record ~2 ns, both 0 allocs on all three toolchains), the
no-GC-pressure matrix, and caveats: **[BENCHMARK.md](BENCHMARK.md)**.

## Scope — what this does not do

- Per-goroutine GC exemption is not possible in Go — the GC is process-wide.
  `guard` measures interference; it cannot prevent it.
- `//hotpath:noalloc` detects *syntactic* alloc sites — a `make` the compiler
  stack-promotes is still flagged, and implicit interface conversions or calls
  through function values are not seen. `testing.AllocsPerRun`/`guard.Exact`
  is ground truth; the linter is the regression gate.
- Observed allocation counters are process-wide *lower bounds* (span-refill
  granularity) — that's why `Exact`/`Assert` exist for tests. Those are exact
  but still process-wide: don't use them under `t.Parallel`.
- `profile` restores knob *values*; a `GOMAXPROCS` pin is one-way (it disables
  Go 1.25+ cgroup auto-detection permanently).

## Requirements

- **Go 1.26+ recommended**; **minimum Go 1.22**. Every package works the same
  on 1.22–1.27 (benchmarked on 1.22.12, 1.26.8 and 1.27.1 — see
  [BENCHMARK.md](BENCHMARK.md)).
- `hotpathcheck` builds with Go 1.26+ but vets code of *any* version —
  `go install` auto-fetches the toolchain (Go 1.21+).
- CI: 1.22 / 1.26 / 1.27 × linux/macOS. Zero library dependencies.

## Documentation

| Doc | What |
|---|---|
| [INTEGRATIONS.md](INTEGRATIONS.md) | Use cases: matching engine, HTTP service, CI gate, bench recipe |
| [BENCHMARK.md](BENCHMARK.md) | Methodology, all measured numbers, per-toolchain results, caveats |
| [`doc/public/runtime-surface.md`](doc/public/runtime-surface.md) | Audited `runtime/metrics` catalog — what each counter measures and when it updates |
| Package docs | `go doc` / [pkg.go.dev](https://pkg.go.dev/github.com/0xshikhar/go-hotpath) — the API reference |

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Commit style is conventional
(`feat:`, `fix:`, `docs:`…); the zero-alloc benchmarks and the Go 1.22 floor
are hard gates.

## License

Apache-2.0 — permissive, safe for closed-source integration.
Copyright 2026 0xShikhar (https://shikhar.xyz). See [LICENSE](LICENSE).
