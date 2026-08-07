package driver

import (
	"sort"
	"time"
)

type LatencyHistogram struct {
	samples []time.Duration
}

func NewLatencyHistogram(expectedCount int) *LatencyHistogram {
	return &LatencyHistogram{
		samples: make([]time.Duration, 0, expectedCount),
	}
}

func (h *LatencyHistogram) Record(d time.Duration) {
	h.samples = append(h.samples, d)
}

func (h *LatencyHistogram) Reset() {
	h.samples = h.samples[:0]
}

type Percentiles struct {
	Count int
	Min   time.Duration
	P50   time.Duration
	P90   time.Duration
	P99   time.Duration
	P999  time.Duration
	P9999 time.Duration
	Max   time.Duration
}

func (h *LatencyHistogram) Compute() Percentiles {
	n := len(h.samples)
	if n == 0 {
		return Percentiles{}
	}

	sort.Slice(h.samples, func(i, j int) bool {
		return h.samples[i] < h.samples[j]
	})

	return Percentiles{
		Count: n,
		Min:   h.samples[0],
		P50:   h.samples[n*50/100],
		P90:   h.samples[n*90/100],
		P99:   h.samples[n*99/100],
		P999:  h.samples[n*999/1000],
		P9999: h.samples[n*9999/10000],
		Max:   h.samples[n-1],
	}
}
