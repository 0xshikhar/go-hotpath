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
| [`bench`](./bench) | Open-loop latency harness — coordinated-omission-safe, dual Latency/Service series, GC CPU attribution | 2.0 ns per record |
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

Measured on a real order-book workload — Apple M4 Pro, go1.26.0, open-loop
100k ops/s. A zero-allocation book (Book C) ran clean in isolation; adding 4
allocating neighbor goroutines triggered 338 GC cycles and moved its p99 from
**0.9 µs to 16.5 µs** (18×) despite C allocating zero bytes itself — the
process is the interference boundary.

| Book (alloc/op) | Neighbor | p99 | p99.9 | GC cycles | Mark-assist CPU |
|---|---|---|---|---|---|
| C (0 objs) | none | 0.9 µs | 10.8 µs | 0 | 0 |
| C (0 objs) | 4 allocating | 16.5 µs | 62.7 µs | 338 | 8.25 ms |
| A (~3 objs) | none | 5.3 µs | 18.3 µs | 0 | 0 |
| A (~3 objs) | 4 allocating | 27.1 µs | 70.3 µs | 354 | 9.19 ms |

Full methodology, per-toolchain results (1.22 vs 1.26), and caveats:
**[BENCHMARK.md](BENCHMARK.md)**. Reproduce:
`go run ./spike -e1=false -e2=false -e3=false -e4 -e5 -dur=4s -repeats=3`

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

- **Go 1.26+ recommended** (developed and measured here); **minimum Go 1.22** —
  on <1.23, `bench`'s `GCPause*` fields read 0 (`/sched/pauses` doesn't exist).
  Everything else works.
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
