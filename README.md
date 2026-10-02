# go-hotpath

Go garbage collector evidence and control for latency-sensitive systems —
a small library that answers *"did the GC interfere with my hot path, and
can I prevent it?"* without a runtime fork or a rewrite.

```go
g := guard.New()              // once, at init
w := g.Begin()                // ~226 ns, zero allocations
process(batch)
if r := w.End(); !r.Quiet() { // a GC cycle completed inside the window
	log.Print(r.Explain())
}
```

## The four pieces

| Package | What it does | One-liner |
|---|---|---|
| [`guard`](./guard) | Per-goroutine, zero-alloc GC-interference windows | `Begin`/`End` ≈ 450 ns, 0 allocs; `Exact`/`Assert` give exact counts in tests |
| [`profile`](./profile) | Reversible GOGC/GOMEMLIMIT control | `profile.Apply(profile.SilentWindow(512<<20))` → `s.Undo()` |
| [`bench`](./bench) | Open-loop latency harness | corrects coordinated omission; dual `Latency`/`Service` series + run-level GC attribution |
| [`hotpathcheck`](./cmd/hotpathcheck) | `go vet` analyzer | `//hotpath:noalloc` fails CI if the annotated function has an allocation site |

## Why

Go has excellent GC — but a trading engine, a matcher, a hot-path decoder,
or a real-time system needs to *know* when it ran, and to be able to prove a
function can't allocate. Today that's tribal knowledge plus ad-hoc
benchmarks that lie about tails (closed loops omit the stall's victims). This
library makes the answer a first-class, tested API:

- **detect** — `guard` wraps a window and reports exactly which GC
  mechanisms fired (cycles, forced/auto split, limiter, lower-bound allocs).
- **measure** — `bench` runs open-loop so a 10 ms stall records ~N delayed
  samples, not one; GC CPU attribution (assist vs dedicated vs pause) is in
  the report.
- **prevent** — `hotpathcheck` gates `//hotpath:noalloc` functions in CI;
  `profile.SilentWindow` + `QuietGC` makes GC a scheduled event.

## Quick start

```bash
go get github.com/0xshikhar/go-hotpath/guard
go get github.com/0xshikhar/go-hotpath/profile
go get github.com/0xshikhar/go-hotpath/bench
go install github.com/0xshikhar/go-hotpath/cmd/hotpathcheck@latest
```

```go
// Test-time: exact allocation proof
func TestApply(t *testing.T) {
	guard.Assert(t, func() { book.Apply(&cmd, &ev) })
}

// Source-time: CI gate
//hotpath:noalloc
func (e *Engine) apply(c *Command) { ... }
//   go vet -vettool=$(which hotpathcheck) ./...

// Run-time: reversible control
s, _ := profile.Apply(profile.SilentWindow(512 << 20))
defer s.Undo()
```

## Measured evidence

See [BENCHMARK.md](BENCHMARK.md) for the full methodology. The headline:

> A zero-allocation order book (Book C) ran **clean** in isolation — then a
> noisy neighbor goroutine triggered 338 GC cycles and its p99 went from
> **0.9 µs to 16.5 µs** (18×) despite allocating zero bytes itself. The
> process is the interference boundary — `guard` is how you see it.

## Honest scope

- Per-goroutine GC exemption is not possible in Go — the GC is process-wide.
  `guard` measures it; it cannot prevent it.
- `//hotpath:noalloc` detects *syntactic* allocation sites — a `make` the
  compiler would stack-promote is still flagged. `testing.AllocsPerRun` and
  `guard.Exact` are the ground truth.
- Observed allocation counters are process-wide lower bounds (span-refill
  granularity); exact counts need `ReadMemStats`, which is STW — that's why
  `Exact`/`Assert` exist for tests.
- `profile` restores knob *values*; a `GOMAXPROCS` pin is one-way (it
  permanently disables Go 1.25+ cgroup auto-detection).

## Docs

- [INTEGRATIONS.md](INTEGRATIONS.md) — use cases: matching engine, HTTP
  service, CI gate, benchmark recipe
- [BENCHMARK.md](BENCHMARK.md) — methodology + all measured numbers
- `doc/research/` — product spec, architecture plan, runtime audit
- `doc/learn/build/` — implementation deep-dives per phase (Go internals)

## Requirements

- Go 1.26+ (CI runs 1.26 and 1.27 on linux/macOS)
- No external dependencies for the library; `cmd/hotpathcheck` uses
  `golang.org/x/tools` in its own module

## License

Apache-2.0 — see [LICENSE](LICENSE).
