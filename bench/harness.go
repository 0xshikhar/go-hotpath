package bench

import (
	"runtime"
	"time"
)

const spinCutoff = 50 * time.Microsecond

// Harness drives an open-loop benchmark at a fixed op rate.
type Harness struct {
	rate     int
	interval time.Duration
	warmup   time.Duration
	duration time.Duration
	gcBefore bool
	label    string
}

type config struct {
	warmup, duration time.Duration
	gcBefore         bool
	label            string
}

// Option configures a Harness.
type Option func(*config)

// WithDuration sets the measured window length. Default 10 s.
func WithDuration(d time.Duration) Option { return func(c *config) { c.duration = d } }

// WithWarmup runs fn at the target rate for d before measuring. Warmup ops
// are paced identically and discarded. Default 2 s.
func WithWarmup(d time.Duration) Option { return func(c *config) { c.warmup = d } }

// WithGCBefore controls whether a runtime.GC runs before the measured window
// so each run starts on a settled heap. Default true.
func WithGCBefore(on bool) Option { return func(c *config) { c.gcBefore = on } }

// WithLabel sets the row label used by Report.String and Report.Markdown.
func WithLabel(s string) Option { return func(c *config) { c.label = s } }

// New creates a harness at the given target rate (ops/sec, 1 to 1e9).
//
// The contract for fn: the harness calls it once per intended send time, on a
// single goroutine. fn must do one complete unit of work — the harness owns
// the clock, so a duration loop inside fn breaks the intended-time
// accounting.
func New(rate int, opts ...Option) *Harness {
	if rate <= 0 || rate > int(time.Second) {
		panic("bench: rate must be between 1 and 1e9 ops/sec")
	}
	c := config{
		warmup:   2 * time.Second,
		duration: 10 * time.Second,
		gcBefore: true,
	}
	for _, o := range opts {
		o(&c)
	}
	return &Harness{
		rate:     rate,
		interval: time.Second / time.Duration(rate),
		warmup:   c.warmup,
		duration: c.duration,
		gcBefore: c.gcBefore,
		label:    c.label,
	}
}

// pace blocks until t: sleeps while the gap is large, then spins for the last
// ~50 µs where timer granularity would dominate.
func pace(t time.Time) {
	for {
		gap := time.Until(t)
		if gap <= 0 {
			return
		}
		if gap > spinCutoff {
			time.Sleep(gap - spinCutoff)
			continue
		}
		for time.Now().Before(t) {
		}
		return
	}
}

// Run executes fn once per intended send time for the configured duration and
// returns the report. The measured window runs on the calling goroutine.
func (h *Harness) Run(fn func()) Report {
	if h.gcBefore {
		runtime.GC()
	}

	rc := newRunCapture()

	// Warmup: same pacing, nothing recorded.
	if h.warmup > 0 {
		deadline := time.Now().Add(h.warmup)
		t := time.Now()
		for time.Now().Before(deadline) {
			t = t.Add(h.interval)
			pace(t)
			fn()
		}
	}

	total := int(h.duration.Seconds() * float64(h.rate)) // no int overflow on 32-bit
	if total < 1 {
		total = 1
	}
	var latHist, svcHist histogram
	var late uint64

	before := rc.read()
	start := time.Now()

	for i := 0; i < total; i++ {
		intended := start.Add(h.interval * time.Duration(i+1))
		pace(intended)

		actual := time.Now()
		fn()
		done := time.Now()

		if actual.Sub(intended) > h.interval {
			late++ // started more than one interval late; still recorded
		}
		latHist.Record(done.Sub(intended))
		svcHist.Record(done.Sub(actual))
	}
	elapsed := time.Since(start)

	after := rc.read()

	// CPU-class counters only advance at mark termination. If the window
	// completed a cycle mid-run, a final GC flushes the tail of it so the
	// run-level attribution reflects the whole window. The extra forced cycle
	// is not counted: after.forced was already read.
	if after.cycles-before.cycles > 0 {
		runtime.GC()
		rc.readCPUOnly(&after)
	}

	latDelta := histDelta(before.schedLat, after.schedLat)
	pauseDelta := histDelta(before.pauseGC, after.pauseGC)

	return Report{
		Label:      h.label,
		GoVersion:  runtime.Version(),
		GOOS:       runtime.GOOS,
		GOARCH:     runtime.GOARCH,
		GOMAXPROCS: runtime.GOMAXPROCS(0),
		Rate:       h.rate,
		Duration:   elapsed,
		Ops:        uint64(total),
		Late:       late,
		Latency:    latHist.dist(),
		Service:    svcHist.dist(),

		GCCycles:       after.cycles - before.cycles,
		GCForced:       after.forced - before.forced,
		AllocBytes:     after.allocBytes - before.allocBytes,
		LimiterEngaged: after.limiter != before.limiter,

		UserCPU:       secToDur(after.user - before.user),
		GCCPU:         secToDur(after.gcTotal - before.gcTotal),
		MarkAssistCPU: secToDur(after.assist - before.assist),
		GCDedCPU:      secToDur(after.dedicated - before.dedicated),
		GCPauseCPU:    secToDur(after.pause - before.pause),

		GCPauseMax:      deltaMax(pauseDelta, after.pauseGC.Buckets),
		GCPauseP99:      deltaQuantile(pauseDelta, after.pauseGC.Buckets, 0.99),
		SchedLatencyP99: deltaQuantile(latDelta, after.schedLat.Buckets, 0.99),

		HeapLiveStart: before.heapLive,
		HeapLiveEnd:   after.heapLive,
	}
}

func secToDur(f float64) time.Duration {
	return time.Duration(f * float64(time.Second))
}
