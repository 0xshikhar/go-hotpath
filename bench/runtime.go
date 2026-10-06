package bench

import (
	"math"
	"runtime/metrics"
	"time"

	"github.com/0xshikhar/go-hotpath/internal/rtm"
)

// runCapture snapshots the runtime counters and histograms a Report needs.
// All reads are cold-path: HistSet allocates its bucket slices.
type runCapture struct {
	scalars *rtm.Set
	cpu     *rtm.Set
	hists   *rtm.HistSet
}

// Indexes into runCapture.scalars.
const (
	sCycles = iota
	sForced
	sAllocBytes
	sHeapLive
	sLimiter
)

// Indexes into runCapture.cpu.
const (
	cUser = iota
	cGCAssist
	cGCDedicated
	cGCPause
	cGCTotal
)

// snap holds one point-in-time read of a runCapture.
type snap struct {
	cycles, forced, allocBytes, heapLive, limiter uint64
	user, assist, dedicated, pause, gcTotal       float64 // cpu-seconds
	schedLat, pauseGC                             *metrics.Float64Histogram
}

// Indexes into runCapture.hists.
const (
	hSchedLat = iota
	hPauseGC
)

func newRunCapture() *runCapture {
	return &runCapture{
		scalars: rtm.NewSet(
			rtm.MetricGCCyclesTotal,
			rtm.MetricGCCyclesForced,
			rtm.MetricHeapAllocBytes,
			rtm.MetricHeapLive,
			rtm.MetricLimiterLast,
		),
		cpu: rtm.NewSet(
			rtm.MetricCPUUser,
			rtm.MetricCPUGCAssist,
			rtm.MetricCPUGCDedicated,
			rtm.MetricCPUGCPause,
			rtm.MetricCPUGCTotal,
		),
		hists: rtm.NewHistSet(rtm.MetricSchedLatencies, rtm.MetricSchedPausesGC),
	}
}

func (rc *runCapture) read() snap {
	rc.scalars.Read()
	rc.cpu.Read()
	rc.hists.Read()
	return snap{
		cycles:     rc.scalars.Value(sCycles).Uint64(),
		forced:     rc.scalars.Value(sForced).Uint64(),
		allocBytes: rc.scalars.Value(sAllocBytes).Uint64(),
		heapLive:   rc.scalars.Value(sHeapLive).Uint64(),
		limiter:    rc.scalars.Value(sLimiter).Uint64(),
		user:       rc.cpu.Value(cUser).Float64(),
		assist:     rc.cpu.Value(cGCAssist).Float64(),
		dedicated:  rc.cpu.Value(cGCDedicated).Float64(),
		pause:      rc.cpu.Value(cGCPause).Float64(),
		gcTotal:    rc.cpu.Value(cGCTotal).Float64(),
		schedLat:   cloneHist(rc.hists.Histogram(hSchedLat)),
		pauseGC:    cloneHist(rc.hists.Histogram(hPauseGC)),
	}
}

// readCPUOnly re-reads just the CPU classes, used after a final runtime.GC()
// flushes a partial cycle's stats at mark termination.
func (rc *runCapture) readCPUOnly(s *snap) {
	rc.cpu.Read()
	s.user = rc.cpu.Value(cUser).Float64()
	s.assist = rc.cpu.Value(cGCAssist).Float64()
	s.dedicated = rc.cpu.Value(cGCDedicated).Float64()
	s.pause = rc.cpu.Value(cGCPause).Float64()
	s.gcTotal = rc.cpu.Value(cGCTotal).Float64()
}

// cloneHist copies Counts. Buckets is shared: runtime/metrics guarantees a
// metric's bucket boundaries never change for the life of the process.
func cloneHist(h *metrics.Float64Histogram) *metrics.Float64Histogram {
	c := make([]uint64, len(h.Counts))
	copy(c, h.Counts)
	return &metrics.Float64Histogram{Counts: c, Buckets: h.Buckets}
}

// histDelta returns after minus before counts, saturating per bucket (a
// counter reset between reads clamps to 0 rather than underflowing).
func histDelta(before, after *metrics.Float64Histogram) []uint64 {
	n := len(before.Counts)
	if len(after.Counts) < n {
		n = len(after.Counts)
	}
	d := make([]uint64, n)
	for i := 0; i < n; i++ {
		if after.Counts[i] >= before.Counts[i] {
			d[i] = after.Counts[i] - before.Counts[i]
		}
	}
	return d
}

// bucketUpper returns bucket i's upper boundary from the runtime-provided
// edges (seconds). The open-ended overflow bucket reports its lower edge —
// the largest finite statement the histogram can make.
func bucketUpper(buckets []float64, i int) time.Duration {
	e := buckets[i+1]
	if math.IsInf(e, 1) {
		e = buckets[i]
	}
	return time.Duration(e * float64(time.Second))
}

// deltaQuantile reports the p-quantile of a bucket-count delta using each
// bucket's upper edge (conservative: can only overestimate).
func deltaQuantile(delta []uint64, buckets []float64, p float64) time.Duration {
	var total uint64
	for _, c := range delta {
		total += c
	}
	if total == 0 {
		return 0
	}
	target := uint64(math.Ceil(p * float64(total)))
	if target == 0 {
		target = 1
	}
	var cum uint64
	for i, c := range delta {
		cum += c
		if cum >= target {
			return bucketUpper(buckets, i)
		}
	}
	return 0
}

// deltaMax returns the upper edge of the largest non-empty delta bucket.
func deltaMax(delta []uint64, buckets []float64) time.Duration {
	for i := len(delta) - 1; i >= 0; i-- {
		if delta[i] > 0 {
			return bucketUpper(buckets, i)
		}
	}
	return 0
}
