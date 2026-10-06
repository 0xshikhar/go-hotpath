# Integrations — using go-hotpath in real services

Four packages, one question each:

| Package | Question | Where it runs |
|---|---|---|
| `guard` | Did the GC interfere with this window? | production hot path (0 alloc, ~450 ns) |
| `profile` | Can I change GC behavior safely and reversibly? | startup, config changes |
| `bench` | What does my tail actually look like? | benchmarks, CI perf gates |
| `hotpathcheck` | Did a code change introduce allocations? | `go vet` / CI |

Library: Go 1.22+ (1.26+ recommended). `hotpathcheck` builds with Go 1.26+
and vets code targeting any version.

---

## 1. A matching engine / order book — the headline use case

```go
type Engine struct {
	g     *guard.Guard   // one per event loop goroutine, created at init
	books map[uint32]*Book
}

func NewEngine() *Engine {
	if err := guard.Check(); err != nil {
		log.Fatal(err) // toolchain doesn't expose the metrics — fail loudly
	}
	return &Engine{g: guard.New()}
}

func (e *Engine) applyBatch(batch []Command) {
	w := e.g.Begin()          // ~226 ns, zero allocations
	for i := range batch {
		e.apply(&batch[i])
	}
	if r := w.End(); !r.Quiet() {
		gcInterference.Inc() // your metrics counter
		log.Printf("guard: %s", r.Explain())
	}
}
```

Rules that make this work:

- **Guard batches, not orders.** Per-order is too chatty (false positives from
  the process's other goroutines); per-batch gives actionable signal.
- **`Quiet()==true` means no cycle completed** — not "no allocation". If
  your window allocates, you'll see it in `AllocObjectsObserved` eventually
  (span-refill granularity) or in the limiter counter.
- **One `Guard` per goroutine.** The struct is single-owner by design.

### Prove the hot path can't allocate — at write time

```go
//hotpath:noalloc
func (e *Engine) apply(c *Command) {
	lvl := e.books[c.Symbol].level(c.Price) // calls annotated helpers only
	//hotpath:allow freelist is preallocated to MaxOrders at boot
	lvl.pushFree(...)
}
```

```bash
go install github.com/0xshikhar/go-hotpath/cmd/hotpathcheck@latest
go vet -vettool=$(which hotpathcheck) ./...
```

Every `new`/`make`/`append`/`&T{}`/conversion/closure/string
concatenation/map write/`go`/`defer`, and every call to an unannotated
function, gets flagged (`math`, `math/bits`, and `sync/atomic` are
pre-verified). `//hotpath:allow` is the audited exception — trailing on the
line, or alone on the line above. The annotation is transitive, including
into other packages via analysis facts.

### Prove it at run time — in tests

```go
func TestApplyIsAllocFree(t *testing.T) {
	e := NewEngine()
	e.warmup(10_000) // prime freelists/pools first
	guard.Assert(t, func() { e.apply(&cmd) })
	// or with a budget: guard.AssertWithin(t, guard.Budget{Allocs: 2, Bytes: 64}, fn)
}
```

`Assert` uses `runtime.ReadMemStats` — exact, ~20 µs STW, tests only. The
counts are process-wide, so don't run it under `t.Parallel` or alongside
allocating background goroutines. `hotpathcheck` is the write-time gate;
`Assert` is the run-time ground truth.

## 2. A latency-sensitive HTTP/RPC service

An allocation-heavy service shouldn't go silent — measure instead. Expose
the GC state on a debug endpoint (`profile.Read` is cold-path, ~300 ns):

```go
http.HandleFunc("/debug/hotpath", func(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(profile.Read())
})
```

`Snapshot` answers the operational questions:

- `HeapLive / HeapGoal` — headroom to the next GC trigger
- `ScanHeap` — bytes the GC must actually walk (pointer-bearing)
- `CgroupMemLimit` — the container's effective memory cap (own cgroup or
  any ancestor; the Go runtime does not read it for you); compare to
  `MemLimit` before trusting a `SilentWindow`

### When to go silent

`profile.SilentWindow(n)` = `GOGC=off` + `GOMEMLIMIT=n`. Use it when **all**
of these hold:

1. The hot path is near-allocation-free (verify with `hotpathcheck` first).
2. A quiet point exists per unit of work (batch boundary, session end) for
   `profile.QuietGC()`.
3. `n < CgroupMemoryLimit()` with headroom (~80% is sane).
4. You watch `guard`'s `LimiterEngaged` — if it flips, the window broke.

```go
s, err := profile.Apply(profile.SilentWindow(512 << 20))
if err != nil {
	log.Fatal(err) // a failed Apply has already restored the old settings
}
defer s.Undo()

// at each batch boundary:
res := profile.QuietGC()
log.Printf("quietGC %v reclaimed %d bytes", res.Duration, res.Reclaimed())
```

## 3. CI / regression gate

Two layers — write-time and run-time:

```yaml
# .github/workflows/ci.yml
- run: go install github.com/0xshikhar/go-hotpath/cmd/hotpathcheck@latest
- run: go vet -vettool=$(go env GOPATH)/bin/hotpathcheck ./...  # exits non-zero on new alloc sites
- run: go test -race -count=1 ./...                             # guard.Assert tests
```

For a latency gate, a small program that runs `bench` against a stored
baseline and fails when a percentile slows by more than 10% and 1 µs:

```go
base := loadBaseline() // a Report you stored earlier (e.g. as JSON)
next := bench.New(100_000, bench.WithDuration(10*time.Second)).Run(engineOp)
fmt.Print(bench.Compare(base, next))
if r := bench.Regressed(base, next); len(r) > 0 {
	log.Fatalf("latency regression at %v", r)
}
```

Shared CI runners are noisy; run latency gates on dedicated hardware.

## 4. Benchmarking your own hot path

```go
cmds := pregenerate() // generation outside the measured region
var ev Event
pos := 0
h := bench.New(100_000,
	bench.WithDuration(10*time.Second),
	bench.WithWarmup(2*time.Second),
	bench.WithLabel("book-apply"),
)
rep := h.Run(func() {
	e.apply(&cmds[pos%len(cmds)]) // the harness may run more ops than you pre-generated
	pos++
})
fmt.Println(rep.String()) // dual series + GC CPU attribution
fmt.Print(bench.MarkdownHeader() + rep.Markdown())
```

Rules: `fn` does exactly one unit of work; pre-generate inputs so the
generator cost stays out of the measured region (warmup ops call `fn` too);
always read both `Latency` (done−intended) and `Service` (done−actual) — the
gap *is* the finding.

## 5. The four-line integration summary

```
write-time:  //hotpath:noalloc + go vet -vettool   → no regressions merged
run-time:    guard.Assert in tests                 → exact alloc counts
production:  g.Begin()/End() per batch             → "was this window quiet"
control:     profile.SilentWindow + QuietGC        → GC on your schedule
```

## What this does NOT give you

- Per-goroutine GC exemption — the process is the interference unit; an
  allocating neighbor goroutine can still move your tail (measured: E5).
- Compile-time guarantees — `noalloc` is heuristic; `AllocsPerRun` is truth.
- Process isolation — if a shared process is the problem, the fix is a
  separate process for the hot path.
