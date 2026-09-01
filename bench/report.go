package bench

import (
	"fmt"
	"strings"
	"time"
)

// Report is the result of one Harness.Run.
//
// Latency is done−intended (corrected for coordinated omission). Service is
// done−actual (what a closed loop would print). Runtime fields are run-level
// deltas: /cpu/classes counters only advance at mark termination, so their
// values only cover completed cycles — see the doc comment on Run.
type Report struct {
	Label      string
	GoVersion  string
	GOOS       string
	GOARCH     string
	GOMAXPROCS int

	Rate     int
	Duration time.Duration
	Ops      uint64
	Late     uint64 // ops that started more than one interval after intended

	Latency Dist // done − intended
	Service Dist // done − actual

	// Runtime attribution, run-level.
	GCCycles       uint64
	GCForced       uint64 // subset of GCCycles started by runtime.GC
	AllocBytes     uint64 // process-wide delta; span-refill granularity
	LimiterEngaged bool   // GC CPU limiter engaged during the run

	UserCPU       time.Duration // CPU-seconds of user work, as a duration
	GCCPU         time.Duration // total GC CPU
	MarkAssistCPU time.Duration // GC work done inline by allocating goroutines
	GCDedCPU      time.Duration // background mark workers
	GCPauseCPU    time.Duration // CPU burned inside STW pauses

	GCPauseMax      time.Duration // largest GC STW pause (runtime histogram)
	GCPauseP99      time.Duration
	SchedLatencyP99 time.Duration // goroutine runnable-but-not-running p99

	HeapLiveStart uint64
	HeapLiveEnd   uint64
}

// String renders the report as an aligned block.
func (r Report) String() string {
	var b strings.Builder
	label := r.Label
	if label == "" {
		label = "run"
	}
	fmt.Fprintf(&b, "go-hotpath bench  label=%s rate=%d/s dur=%v ops=%d late=%d\n",
		label, r.Rate, r.Duration.Truncate(time.Millisecond), r.Ops, r.Late)
	fmt.Fprintf(&b, "  latency p50=%-10s p90=%-10s p99=%-10s p99.9=%-10s p99.99=%-10s max=%s\n",
		r.Latency.P50, r.Latency.P90, r.Latency.P99, r.Latency.P999, r.Latency.P9999, r.Latency.Max)
	fmt.Fprintf(&b, "  service p50=%-10s p90=%-10s p99=%-10s p99.9=%-10s p99.99=%-10s max=%s\n",
		r.Service.P50, r.Service.P90, r.Service.P99, r.Service.P999, r.Service.P9999, r.Service.Max)
	fmt.Fprintf(&b, "  gc      cycles=%d (forced %d) alloc=%s limiter=%v cpu=%s (assist %s, ded %s, pause %s)\n",
		r.GCCycles, r.GCForced, humanBytes(r.AllocBytes), r.LimiterEngaged,
		r.GCCPU.Truncate(time.Microsecond), r.MarkAssistCPU.Truncate(time.Microsecond),
		r.GCDedCPU.Truncate(time.Microsecond), r.GCPauseCPU.Truncate(time.Microsecond))
	fmt.Fprintf(&b, "  pauses  gcStwP99=%s gcStwMax=%s schedP99=%s\n",
		r.GCPauseP99, r.GCPauseMax, r.SchedLatencyP99)
	fmt.Fprintf(&b, "  env     %s %s/%s GOMAXPROCS=%d heapLive %s→%s\n",
		r.GoVersion, r.GOOS, r.GOARCH, r.GOMAXPROCS,
		humanBytes(r.HeapLiveStart), humanBytes(r.HeapLiveEnd))
	return b.String()
}

const mdHeader = "| Label | Rate | Late% | p50 | p90 | p99 | p99.9 | max | svc p99.9 | GC | alloc | assist CPU |\n|---|---|---|---|---|---|---|---|---|---|---|---|\n"

// Markdown returns a header plus one row — drop-in for a RESULTS.md table.
func (r Report) Markdown() string {
	return fmt.Sprintf("| %s | %d | %.2f%% | %v | %v | %v | %v | %v | %v | %d | %s | %v |\n",
		r.Label, r.Rate, float64(r.Late)*100/float64(r.Ops),
		r.Latency.P50, r.Latency.P90, r.Latency.P99, r.Latency.P999, r.Latency.Max,
		r.Service.P999, r.GCCycles, humanBytes(r.AllocBytes),
		r.MarkAssistCPU.Truncate(time.Microsecond))
}

// MarkdownHeader returns the table header matching Markdown rows.
func MarkdownHeader() string { return mdHeader }

// Compare renders base vs next with per-percentile deltas. A regression is
// flagged when the slowdown exceeds both 10% and 1 µs, so sub-microsecond
// jitter does not page anyone.
func Compare(base, next Report) string {
	var b strings.Builder
	bl, nl := base.Label, next.Label
	if bl == "" {
		bl = "base"
	}
	if nl == "" {
		nl = "next"
	}
	fmt.Fprintf(&b, "compare %s → %s (rate %d/s)\n", bl, nl, next.Rate)
	for _, row := range []struct {
		name string
		b, n time.Duration
	}{
		{"p50", base.Latency.P50, next.Latency.P50},
		{"p90", base.Latency.P90, next.Latency.P90},
		{"p99", base.Latency.P99, next.Latency.P99},
		{"p99.9", base.Latency.P999, next.Latency.P999},
		{"p99.99", base.Latency.P9999, next.Latency.P9999},
		{"max", base.Latency.Max, next.Latency.Max},
	} {
		mark := " "
		if row.n > row.b && row.n-row.b > time.Microsecond && float64(row.n) > 1.1*float64(row.b) {
			mark = " ← regression"
		}
		if row.b == 0 {
			fmt.Fprintf(&b, "  %-7s %10v → %-10v%s\n", row.name, row.b, row.n, mark)
			continue
		}
		pct := 100 * float64(row.n-row.b) / float64(row.b)
		fmt.Fprintf(&b, "  %-7s %10v → %-10v %+6.1f%%%s\n", row.name, row.b, row.n, pct, mark)
	}
	fmt.Fprintf(&b, "  gc      cycles %d→%d  allocBytes %s→%s\n",
		base.GCCycles, next.GCCycles, humanBytes(base.AllocBytes), humanBytes(next.AllocBytes))
	return b.String()
}

func humanBytes(n uint64) string {
	switch {
	case n < 1<<10:
		return fmt.Sprintf("%dB", n)
	case n < 1<<20:
		return fmt.Sprintf("%.1fKiB", float64(n)/1024)
	case n < 1<<30:
		return fmt.Sprintf("%.1fMiB", float64(n)/(1<<20))
	default:
		return fmt.Sprintf("%.1fGiB", float64(n)/(1<<30))
	}
}
