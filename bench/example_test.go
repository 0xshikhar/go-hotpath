package bench_test

import (
	"fmt"
	"time"

	"github.com/0xshikhar/go-hotpath/bench"
)

// Drive a function at a fixed open-loop rate and read both latency series.
func ExampleHarness_Run() {
	work := make([]int, 1024)
	i := 0
	h := bench.New(10_000,
		bench.WithWarmup(100*time.Millisecond),
		bench.WithDuration(time.Second),
		bench.WithLabel("example"),
	)
	rep := h.Run(func() {
		work[i%len(work)]++ // one unit of work per call
		i++
	})
	fmt.Println(rep.String())
	fmt.Printf("p99 latency %v vs service %v; %d late ops\n",
		rep.Latency.P99, rep.Service.P99, rep.Late)
}

// Fail a CI job when a run regresses against a stored baseline.
func ExampleRegressed() {
	base := bench.Report{Label: "main"}
	next := bench.Report{Label: "pr"}
	base.Latency.P99 = 40 * time.Microsecond
	next.Latency.P99 = 95 * time.Microsecond

	if r := bench.Regressed(base, next); len(r) > 0 {
		fmt.Println("regressed:", r) // in CI: log.Fatalf and fail the job
	}
	// Output: regressed: [p99]
}
