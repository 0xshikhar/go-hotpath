package main

import (
	"fmt"
	"math"
	"runtime"
	"runtime/debug"
	"sync/atomic"
	"time"

	"spike/book"
	"spike/driver"

	"github.com/0xshikhar/go-hotpath/bench"
)

// E4 reruns the Phase 0 matrix through the bench harness: pre-generated
// commands (generation cost out of the timed region), late ops recorded
// instead of dropped, and run-level GC CPU attribution.
func runE4(rate int, dur, warmup time.Duration, repeats int) string {
	out := "## E4 — Rerun through bench (corrected harness)\n\n" + bench.MarkdownHeader()

	type gcCfg struct {
		name   string
		gogc   int   // -1 = off
		memlim int64 // bytes; math.MaxInt64 = none
	}
	gcCfgs := []gcCfg{
		{"default", 100, math.MaxInt64},
		{"GOGC=off", -1, math.MaxInt64},
		{"silent(1GiB)", -1, 1 << 30},
	}
	books := []struct {
		name string
		mk   func() book.Book
	}{
		{"A", func() book.Book { return book.NewBookA() }},
		{"B", func() book.Book { return book.NewBookB() }},
		{"C", func() book.Book { return book.NewBookC() }},
	}

	for rep := 0; rep < repeats; rep++ {
		for _, bk := range books {
			for _, gc := range gcCfgs {
				debug.SetGCPercent(gc.gogc)
				debug.SetMemoryLimit(gc.memlim)
				runtime.GC()

				b := bk.mk()
				n := rate*int((warmup+dur).Seconds()) + rate // margin
				cmds := make([]book.Command, n)
				gen := driver.NewWorkloadGenerator(0x12345678)
				for i := range cmds {
					cmds[i] = gen.NextCommand()
				}

				var ev book.Event
				pos := 0
				h := bench.New(rate,
					bench.WithDuration(dur),
					bench.WithWarmup(warmup),
					bench.WithGCBefore(false), // already settled above
					bench.WithLabel(fmt.Sprintf("%s %s r%d", bk.name, gc.name, rep+1)),
				)
				rep_ := h.Run(func() {
					b.Apply(&cmds[pos], &ev)
					pos++
				})
				row := rep_.Markdown()
				out += row
				fmt.Print(row)
			}
		}
	}
	debug.SetGCPercent(100)
	debug.SetMemoryLimit(math.MaxInt64)
	out += "\n"
	return out
}

// E5 is the E2 neighbor-stress rerun through bench: does a quiet book's tail
// still move when 4 goroutines allocate in the same process? The run-level
// GC CPU columns are what settle whether the interference is GC work or CPU
// contention.
func runE5(rate int, dur, warmup time.Duration) string {
	out := "## E5 — Noisy neighbor through bench\n\n" + bench.MarkdownHeader()
	attr := "\nAttribution (process-wide GC CPU; GC STW pauses; goroutine scheduling delay):\n\n" +
		"| Label | GC CPU | mark-assist CPU | GC STW p99 | GC STW max | sched-delay p99 |\n|---|---|---|---|---|---|\n"

	targets := []struct {
		name string
		mk   func() book.Book
	}{
		{"A", func() book.Book { return book.NewBookA() }},
		{"C", func() book.Book { return book.NewBookC() }},
	}

	for _, target := range targets {
		// "+4spin" is the control: the same four busy neighbors, but they never
		// allocate. If it moves the tail as much as "+4alloc", the cause is CPU
		// contention, not the garbage collector.
		for _, mode := range []string{"clean", "+4alloc", "+4spin"} {
			noisy := mode != "clean"
			b := target.mk()
			n := rate*int((warmup+dur).Seconds()) + rate
			cmds := make([]book.Command, n)
			gen := driver.NewWorkloadGenerator(0x12345678)
			for i := range cmds {
				cmds[i] = gen.NextCommand()
			}

			var stop int32
			if noisy {
				for w := 0; w < 4; w++ {
					go func() {
						var spin uint64
						for atomic.LoadInt32(&stop) == 0 {
							if mode == "+4spin" {
								for i := 0; i < 64; i++ { // ~ the cost of one 1 KiB alloc
									spin = spin*6364136223846793005 + 1442695040888963407
								}
								spinSink.Add(spin)
							} else {
								// Write through the slice so the compiler
								// cannot dead-allocate it, then release it.
								buf := make([]byte, 1024)
								buf[0] = 1
								allocSink.Store(&buf) // forces a real heap alloc
							}
							runtime.Gosched()
						}
					}()
				}
				time.Sleep(50 * time.Millisecond)
			}

			label := target.name + " " + mode
			var ev book.Event
			pos := 0
			h := bench.New(rate,
				bench.WithDuration(dur),
				bench.WithWarmup(warmup),
				bench.WithLabel(label),
			)
			rep := h.Run(func() {
				b.Apply(&cmds[pos], &ev)
				pos++
			})
			if noisy {
				atomic.StoreInt32(&stop, 1)
			}
			row := rep.Markdown()
			out += row
			fmt.Print(row)
			attr += fmt.Sprintf("| %s | %v | %v | %v | %v | %v |\n", rep.Label,
				rep.GCCPU.Truncate(time.Microsecond), rep.MarkAssistCPU.Truncate(time.Microsecond),
				rep.GCPauseP99, rep.GCPauseMax, rep.SchedLatencyP99)
		}
	}
	fmt.Print(attr)
	out += attr + "\n"
	return out
}
