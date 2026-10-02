package bench

import (
	"math"
	"runtime/metrics"
	"time"

	"github.com/0xshikhar/go-hotpath/internal/rtm"
)

// runCapture snapshots the runtime counters and histograms a Report needs.
// All reads are cold-path: HistSet allocates its bucket slices.
//
// /sched/pauses/total/gc only exists on Go 1.23+; on older toolchains the
// GCPause* report fields stay zero (documented) rather than fail.
type runCapture struct {
	scalars   *rtm.Set
	cpu       *rtm.Set
	hists     *rtm.HistSet
	pauseIdx  int // index of MetricSchedPausesGC in hists, or -1
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

func newRunCapture() *runCapture {
	histNames := []string{rtm.MetricSchedLatencies}
	pauseIdx := -1
	if rtm.Available(rtm.MetricSchedPausesGC) {
		pauseIdx = len(histNames)
		histNames = append(histNames, rtm.MetricSchedPausesGC)
	}
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
		hists:    rtm.NewHistSet(histNames...),
		pauseIdx: pauseIdx,
	}
}

func (rc *runCapture) read() snap {
	rc.scalars.Read()
	rc.cpu.Read()
	rc.hists.Read()
	s := snap{
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
		schedLat:   cloneHist(rc.hists.Histogram(0)),
	}
	if rc.pauseIdx >= 0 {
		s.pauseGC = cloneHist(rc.hists.Histogram(rc.pauseIdx))
	}
	return s
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

func cloneHist(h *metrics.Float64Histogram) *metrics.Float64Histogram {
	c := make([]uint64, len(h.Counts))
	copy(c, h.Counts)
	return &metrics.Float64Histogram{Counts: c}
}

// histDelta returns after minus before counts, saturating per bucket (a
// counter reset between reads clamps to 0 rather than underflowing). Nil
// histograms — a metric unavailable on this toolchain — yield a nil delta.
func histDelta(before, after *metrics.Float64Histogram) []uint64 {
	if before == nil || after == nil {
		return nil
	}
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

// --- runtime timeHistogram bucket edges --------------------------------------
//
// The runtime's sched/pause histograms all share one bucket layout defined in
// runtime/histogram.go: min bucket bit 9, sub-bucket bits 2 (4 sub-buckets per
// power of two, ~25% max relative error), max bucket bit 48 (exclusive), plus
// underflow and overflow buckets. Edges reproduced here verbatim so a delta
// of Counts can be turned into percentiles.

const (
	thMinBits = 9
	thMaxBits = 48
	thSubBits = 2
	thSubN    = 1 << thSubBits
	thBuckets = (thMaxBits-thMinBits+1)*thSubN + 2 // 162
)

// timeHistEdgesNS returns the lower edge of each bucket, in nanoseconds,
// matching runtime timeHistogramMetricsBuckets. Length thBuckets+1; the last
// edge is the overflow bound 2^47.
func timeHistEdgesNS() []int64 {
	e := make([]int64, thBuckets+1)
	// First bucket: underflow (negative); edge[0] is -Inf conceptually.
	e[0] = -1 << 62
	for j := 0; j < thSubN; j++ {
		e[j+1] = int64(uint64(j) << (thMinBits - 1 - thSubBits))
	}
	for i := thMinBits; i < thMaxBits; i++ {
		for j := 0; j < thSubN; j++ {
			ns := uint64(1) << (i - 1)
			ns |= uint64(j) << (i - 1 - thSubBits)
			idx := (i-thMinBits+1)*thSubN + j + 1
			e[idx] = int64(ns)
		}
	}
	e[len(e)-2] = int64(uint64(1) << (thMaxBits - 1))
	e[len(e)-1] = 1<<62 - 1 // +Inf stand-in
	return e
}

// deltaQuantile reports the p-quantile of a bucket-count delta, in
// nanoseconds, using the runtime bucket's upper edge (conservative).
func deltaQuantile(delta []uint64, edges []int64, p float64) time.Duration {
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
		if cum >= target && i+1 < len(edges) {
			return time.Duration(edges[i+1])
		}
	}
	return 0
}

// deltaMax returns the upper edge of the largest non-empty delta bucket.
func deltaMax(delta []uint64, edges []int64) time.Duration {
	for i := len(delta) - 1; i >= 0; i-- {
		if delta[i] > 0 && i+1 < len(edges) {
			return time.Duration(edges[i+1])
		}
	}
	return 0
}
