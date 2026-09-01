package bench

import (
	"math"
	"math/bits"
	"time"
)

// histogram is a fixed-size log-linear duration histogram.
//
// Layout: exact for values below 2048 ns; above that, one bucket per value
// of the top 10 mantissa bits within each power of two. That gives a maximum
// relative error of ~0.1% across the whole range (finer than the runtime's
// own timeHistogram, which uses 2 mantissa bits for ~25%), with no
// allocation or sorting on Record.
//
// bucket(v), v >= 2048: e = position of MSB; sub = top-10-mantissa - 1024.
const (
	histExact = 2048 // values below this are exact
	histSub   = 10   // mantissa bits used for sub-bucketing
	histSubN  = 1 << histSub
	histMaxE  = 63
	histN     = histExact + (histMaxE-10)*histSubN // 56320 buckets, ~440 KiB
)

type histogram struct {
	counts [histN]uint64
	total  uint64
	maxIdx int
}

func histBucket(v uint64) uint32 {
	if v < histExact {
		return uint32(v)
	}
	e := bits.Len64(v) - 1 // 11..63 for v >= 2048
	sub := (v >> uint(e-histSub)) - histSubN
	return uint32(histExact + (e-11)*histSubN + int(sub))
}

// histUpper returns the largest value that can fall into bucket idx.
// Percentiles report the bucket's upper bound: a conservative read that can
// only overestimate a percentile, never hide a tail.
func histUpper(idx int) uint64 {
	if idx < histExact {
		return uint64(idx)
	}
	k := idx - histExact
	e := 11 + k/histSubN
	j := k % histSubN
	lo := uint64(histSubN+j) << uint(e-histSub)
	return lo + (1 << uint(e-histSub)) - 1
}

// Record stores one duration. Negative durations clamp to 0. It performs no
// allocation.
func (h *histogram) Record(d time.Duration) {
	v := uint64(d)
	if d < 0 {
		v = 0
	}
	idx := histBucket(v)
	h.counts[idx]++
	h.total++
	if int(idx) > h.maxIdx {
		h.maxIdx = int(idx)
	}
}

func (h *histogram) quantile(p float64) time.Duration {
	if h.total == 0 {
		return 0
	}
	// Rank of the p-quantile sample. p*total can be exact, so use ceil —
	// +1 would overshoot to the next bucket whenever p*N lands on a boundary.
	target := uint64(math.Ceil(p * float64(h.total)))
	if target == 0 {
		target = 1
	}
	var cum uint64
	for i := 0; i <= h.maxIdx; i++ {
		cum += h.counts[i]
		if cum >= target {
			return time.Duration(histUpper(i))
		}
	}
	return time.Duration(histUpper(h.maxIdx))
}

// Dist is a percentile summary of one latency series.
type Dist struct {
	Count                           uint64
	P50, P90, P99, P999, P9999, Max time.Duration
}

func (h *histogram) dist() Dist {
	return Dist{
		Count: h.total,
		P50:   h.quantile(0.50),
		P90:   h.quantile(0.90),
		P99:   h.quantile(0.99),
		P999:  h.quantile(0.999),
		P9999: h.quantile(0.9999),
		Max:   time.Duration(histUpper(h.maxIdx)),
	}
}
