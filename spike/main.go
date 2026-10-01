// Command spike reruns the Phase 0 experiments and regenerates RESULTS.md.
// Run from the repository root: go run ./spike
package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"sync/atomic"
	"time"

	"spike/book"
	"spike/driver"
)

func runE1(rates []int, duration time.Duration) string {
	out := "## E1 — The Gap (Clean Run, No Noisy Neighbor)\n\n"
	out += "| Impl | GC Config | Rate (target) | Drop% | p50 | p90 | p99 | p99.9 | max | GC Cycles | Heap Live MB |\n"
	out += "|---|---|---|---|---|---|---|---|---|---|---|\n"

	configs := []struct {
		name     string
		gcOff    bool
		makeBook func() book.Book
	}{
		{"A (Idiomatic)", false, func() book.Book { return book.NewBookA() }},
		{"B (Hand-tuned)", false, func() book.Book { return book.NewBookB() }},
		{"B (Hand-tuned)", true, func() book.Book { return book.NewBookB() }},
		{"C (Pointer-free)", false, func() book.Book { return book.NewBookC() }},
		{"C (Pointer-free)", true, func() book.Book { return book.NewBookC() }},
	}

	for _, rate := range rates {
		fmt.Printf("--- Running E1 at rate %d cmds/s ---\n", rate)
		for _, cfg := range configs {
			gcLabel := "default"
			if cfg.gcOff {
				gcLabel = "GOGC=off"
				debug.SetGCPercent(-1)
			} else {
				debug.SetGCPercent(100)
			}

			runtime.GC()
			time.Sleep(100 * time.Millisecond)

			b := cfg.makeBook()
			res := driver.RunOpenLoop(b, rate, 1*time.Second, duration)

			heapMB := float64(res.LiveHeapBytes) / (1024 * 1024)
			row := fmt.Sprintf("| %s | %s | %d | %.2f%% | %v | %v | %v | %v | %v | %d | %.2f |\n",
				cfg.name, gcLabel, rate, res.DropPercent,
				res.IntendedHist.P50, res.IntendedHist.P90, res.IntendedHist.P99,
				res.IntendedHist.P999, res.IntendedHist.Max,
				res.GCCycles, heapMB)

			fmt.Print(row)
			out += row
		}
	}
	debug.SetGCPercent(100)
	out += "\n"
	return out
}

func runE2(rate int, duration time.Duration) string {
	out := "## E2 — The Same-Process Hole (Noisy Neighbor Stress)\n\n"
	out += "| Impl | Neighbor | Rate | Drop% | p50 | p90 | p99 | p99.9 | max | GC Cycles |\n"
	out += "|---|---|---|---|---|---|---|---|---|---|\n"

	targets := []struct {
		name     string
		makeBook func() book.Book
	}{
		{"A (Idiomatic)", func() book.Book { return book.NewBookA() }},
		{"C (Pointer-free)", func() book.Book { return book.NewBookC() }},
	}

	for _, target := range targets {
		// Clean run
		debug.SetGCPercent(100)
		runtime.GC()
		bClean := target.makeBook()
		resClean := driver.RunOpenLoop(bClean, rate, 1*time.Second, duration)

		rowClean := fmt.Sprintf("| %s | None | %d | %.2f%% | %v | %v | %v | %v | %v | %d |\n",
			target.name, rate, resClean.DropPercent,
			resClean.IntendedHist.P50, resClean.IntendedHist.P90, resClean.IntendedHist.P99,
			resClean.IntendedHist.P999, resClean.IntendedHist.Max,
			resClean.GCCycles)
		out += rowClean
		fmt.Print(rowClean)

		// Noisy run
		var stop int32
		for w := 0; w < 4; w++ {
			go func() {
				for atomic.LoadInt32(&stop) == 0 {
					// Write through the slice so the compiler cannot
					// dead-allocate it, then release it.
					buf := make([]byte, 1024)
					buf[0] = 1
					allocSink.Store(&buf) // forces a real heap alloc
					runtime.Gosched()
				}
			}()
		}

		time.Sleep(50 * time.Millisecond)
		bNoisy := target.makeBook()
		resNoisy := driver.RunOpenLoop(bNoisy, rate, 1*time.Second, duration)
		atomic.StoreInt32(&stop, 1)

		rowNoisy := fmt.Sprintf("| %s | 4 allocators | %d | %.2f%% | %v | %v | %v | %v | %v | %d |\n",
			target.name, rate, resNoisy.DropPercent,
			resNoisy.IntendedHist.P50, resNoisy.IntendedHist.P90, resNoisy.IntendedHist.P99,
			resNoisy.IntendedHist.P999, resNoisy.IntendedHist.Max,
			resNoisy.GCCycles)
		out += rowNoisy
		fmt.Print(rowNoisy)
	}
	out += "\n"
	return out
}

// slotC mirrors book.OrderC's layout; kept local so E3 does not depend on
// book internals.
type slotC struct {
	id    uint64
	price int64
	qty   int64
	side  book.Side
	prev  int32
	next  int32
}

func runE3() string {
	out := "## E3 — Handle Generation Check Overhead (Microbenchmark)\n\n"

	const iterations = 50_000_000
	slots := make([]slotC, 1024)
	generations := make([]uint32, 1024)
	for i := range generations {
		generations[i] = 1
	}

	// 1. Bare Index
	t0 := time.Now()
	var sum1 int64
	for i := 0; i < iterations; i++ {
		idx := i & 1023
		sum1 += slots[idx].qty
	}
	dBare := time.Since(t0)

	// 2. Generation Check Handle
	type MockHandle struct {
		Index uint32
		Gen   uint32
	}
	h := MockHandle{Index: 0, Gen: 1}

	t0 = time.Now()
	var sum2 int64
	for i := 0; i < iterations; i++ {
		h.Index = uint32(i & 1023)
		if generations[h.Index] == h.Gen {
			sum2 += slots[h.Index].qty
		}
	}
	dGen := time.Since(t0)

	nsBare := float64(dBare.Nanoseconds()) / float64(iterations)
	nsGen := float64(dGen.Nanoseconds()) / float64(iterations)

	out += fmt.Sprintf("- Bare Index lookup : %.2f ns/op (%v total for %d ops)\n", nsBare, dBare, iterations)
	out += fmt.Sprintf("- Handle Gen check  : %.2f ns/op (%v total for %d ops)\n", nsGen, dGen, iterations)
	out += fmt.Sprintf("- Delta overhead    : +%.2f ns/op\n\n", nsGen-nsBare)

	fmt.Print(out)
	return out
}

func main() {
	var (
		e1      = flag.Bool("e1", true, "run E1 (original driver, rate matrix)")
		e2      = flag.Bool("e2", true, "run E2 (original driver, noisy neighbor)")
		e3      = flag.Bool("e3", true, "run E3 (handle check microbenchmark)")
		e4      = flag.Bool("e4", false, "run E4 (bench harness rerun of E1)")
		e5      = flag.Bool("e5", false, "run E5 (bench harness rerun of E2)")
		dur     = flag.Duration("dur", 4*time.Second, "measure duration per cell")
		warmup  = flag.Duration("warmup", 1*time.Second, "warmup duration per cell")
		repeats = flag.Int("repeats", 1, "repeats per E4 cell")
		write   = flag.Bool("write", false, "rewrite RESULTS.md (off by default: the header is curated)")
	)
	flag.Parse()

	fmt.Println("=== Phase 0 Benchmark Spike ===")
	fmt.Println("Warming up system...")
	runtime.GC()

	rates := []int{50_000, 100_000, 200_000}
	duration := *dur

	var e1MD, e2MD, e3MD string
	if *e1 {
		e1MD = runE1(rates, duration)
	}
	if *e2 {
		e2MD = runE2(100_000, duration)
	}
	if *e3 {
		e3MD = runE3()
	}
	if *e4 {
		fmt.Println(runE4(100_000, duration, *warmup, *repeats))
	}
	if *e5 {
		fmt.Println(runE5(100_000, duration, *warmup))
	}
	if !*write {
		return
	}

	// Assemble RESULTS.md. The header states the caveats up front; see
	// doc/research/spec-review.md §6 for the reasoning.
	results := `# Phase 0 Results

**Decision:** Continue — superseded. The next build is ` + "`guard`/`profile`/`bench`/`hotpathcheck`" + `, not Phase 1 & 2 of the old roadmap. See ` + "`doc/research/architecture-and-plan.md`" + `.

**Read this before quoting any number below:**

1. Book C's ` + "`Apply`" + ` allocates 0.00 objects/op; that is the strong result. Every default-GC row still records 1 GC cycle — pointer-free layout does not stop cycles, it makes them cheap.
2. ` + "`GOGC=off`" + ` is not a uniform win. It must be paired with a memory limit and measured per workload.
3. E2 shows the tail moving without extra GC cycles; the mechanism (GC work vs CPU/cache contention) is undetermined by this table.
4. Ops more than 10 ms late are counted as dropped and are not recorded in the histograms. Every p99.9 and max below is a lower bound.
5. One run per cell. Re-measure with >=3 repeats before quoting publicly.
6. E3 is below the cost of an L1 load; if it shows ~0 ns/op the compiler eliminated the work.

---

## Machine
- **Date:** ` + time.Now().Format("2006-01-02 15:04:05") + `
- **Go version:** ` + runtime.Version() + `
- **GOOS / GOARCH:** ` + runtime.GOOS + ` / ` + runtime.GOARCH + `
- **CPU:** Apple M4 Pro
- **RAM:** 24 GB Unified Memory

---

## Workload
- **Mix:** 50% New Limit, 30% Cancel, 10% ReplaceDown, 10% MarketCross
- **Band:** 4096 ticks
- **Capacity:** 100,000 live orders
- **Driver:** Open-loop scheduler with Coordinated Omission correction (Gil Tene model)

---

` + e1MD + e2MD + e3MD

	path := "spike/RESULTS.md"
	if _, err := os.Stat("spike"); os.IsNotExist(err) {
		path = "RESULTS.md"
	}
	if err := os.WriteFile(path, []byte(results), 0644); err != nil {
		fmt.Printf("Error writing RESULTS.md: %v\n", err)
	} else {
		fmt.Printf("RESULTS.md successfully generated at %s!\n", path)
	}
}

// allocSink forces neighbor-goroutine allocations to escape. Stored as a
// pointer so concurrent writers are race-clean.
var allocSink atomic.Pointer[[]byte]
