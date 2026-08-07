package driver

import (
	"runtime/metrics"
	"time"

	"spike/book"
)

type RunResult struct {
	TotalCommands int
	Dropped       int
	DropPercent   float64
	GCCycles      uint64
	LiveHeapBytes uint64
	IntendedHist  Percentiles
	RawHist       Percentiles
}

func readMetricsSnapshot() (gcCycles uint64, liveHeapBytes uint64) {
	const (
		metricCycles = "/gc/cycles/total:gc-cycles"
		metricLive   = "/memory/classes/heap/objects:bytes"
	)
	samples := []metrics.Sample{
		{Name: metricCycles},
		{Name: metricLive},
	}
	metrics.Read(samples)
	for _, s := range samples {
		switch s.Name {
		case metricCycles:
			if s.Value.Kind() == metrics.KindUint64 {
				gcCycles = s.Value.Uint64()
			}
		case metricLive:
			if s.Value.Kind() == metrics.KindUint64 {
				liveHeapBytes = s.Value.Uint64()
			}
		}
	}
	return
}

// RunOpenLoop drives b at a fixed rate with coordinated-omission-corrected
// latency (done - intended_send_time).
//
// Known limitation: ops more than 10 ms behind their intended time are
// counted in Dropped and are NOT recorded in the histograms, so tail
// percentiles are lower bounds. bench replaces this driver in Phase 3.
func RunOpenLoop(
	b book.Book,
	targetRate int, // commands per second
	warmupDuration time.Duration,
	measureDuration time.Duration,
) RunResult {
	gen := NewWorkloadGenerator(0x12345678)
	var ev book.Event

	// Pre-fill / warmup book
	warmupStart := time.Now()
	for time.Since(warmupStart) < warmupDuration {
		cmd := gen.NextCommand()
		b.Apply(&cmd, &ev)
	}

	spacing := time.Duration(float64(time.Second) / float64(targetRate))
	expectedCommands := int(float64(targetRate) * measureDuration.Seconds())

	intendedHist := NewLatencyHistogram(expectedCommands)
	rawHist := NewLatencyHistogram(expectedCommands)

	gcStart, _ := readMetricsSnapshot()

	measureStart := time.Now()
	intendedTime := measureStart
	dropped := 0
	total := 0

	for total < expectedCommands {
		intendedTime = intendedTime.Add(spacing)

		// Wait until intended time
		for time.Now().Before(intendedTime) {
			// Spin wait for high precision under 100us spacing
		}

		actualSendTime := time.Now()

		// Coordinated omission check: if we are falling behind by > 10ms, count overrun
		if actualSendTime.Sub(intendedTime) > 10*time.Millisecond {
			dropped++
			total++
			continue
		}

		cmd := gen.NextCommand()
		cmd.Timestamp = actualSendTime.UnixNano()

		b.Apply(&cmd, &ev)
		doneTime := time.Now()

		intendedLat := doneTime.Sub(intendedTime)
		rawLat := doneTime.Sub(actualSendTime)

		intendedHist.Record(intendedLat)
		rawHist.Record(rawLat)
		total++
	}

	gcEnd, liveHeap := readMetricsSnapshot()

	return RunResult{
		TotalCommands: total,
		Dropped:       dropped,
		DropPercent:   float64(dropped) * 100.0 / float64(total),
		GCCycles:      gcEnd - gcStart,
		LiveHeapBytes: liveHeap,
		IntendedHist:  intendedHist.Compute(),
		RawHist:       rawHist.Compute(),
	}
}
