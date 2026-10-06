package bench

import (
	"math"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestHistogramExact(t *testing.T) {
	for v := uint64(0); v < 2048; v++ {
		if got := histBucket(v); got != uint32(v) {
			t.Fatalf("bucket(%d) = %d", v, got)
		}
	}
}

func TestHistogramAccuracy(t *testing.T) {
	var h histogram
	vals := []time.Duration{
		100 * time.Nanosecond,
		1 * time.Microsecond,
		1500 * time.Microsecond,
		1 * time.Millisecond,
		250 * time.Millisecond,
		3 * time.Second,
	}
	for _, v := range vals {
		h.counts = [histN]uint64{}
		h.total = 0
		h.maxIdx = 0
		for i := 0; i < 1000; i++ {
			h.Record(v)
		}
		got := h.quantile(0.99)
		// Upper-bound reporting: within one bucket width (~0.1%) above v.
		if got < v || float64(got) > float64(v)*1.002+2 {
			t.Errorf("quantile(0.99) for %v = %v, want within bucket width", v, got)
		}
	}
}

func TestHistogramPercentileSplit(t *testing.T) {
	var h histogram
	for i := 0; i < 900; i++ {
		h.Record(1 * time.Microsecond)
	}
	for i := 0; i < 100; i++ {
		h.Record(1 * time.Millisecond)
	}
	if got := h.quantile(0.90); got > 100*time.Microsecond {
		t.Errorf("p90 = %v, want ~1µs", got)
	}
	if got := h.quantile(0.99); got < 500*time.Microsecond {
		t.Errorf("p99 = %v, want ~1ms", got)
	}
}

func TestHistogramZeroAlloc(t *testing.T) {
	var h histogram
	h.Record(time.Second) // warm
	n := testing.AllocsPerRun(1000, func() {
		h.Record(time.Duration(12345))
	})
	if n != 0 {
		t.Fatalf("Record allocated %f times; want 0", n)
	}
}

func TestRunBasic(t *testing.T) {
	h := New(10_000, WithDuration(200*time.Millisecond), WithWarmup(50*time.Millisecond))
	var n int
	rep := h.Run(func() { n++ })
	if rep.Ops != 2000 {
		t.Fatalf("Ops = %d, want 2000", rep.Ops)
	}
	if rep.Latency.Count != 2000 || rep.Service.Count != 2000 {
		t.Fatal("series counts wrong")
	}
	if rep.Latency.P50 <= 0 || rep.Service.P50 <= 0 {
		t.Fatal("empty fn should still have positive measured latencies")
	}
	// Sanity: service time for a near-empty fn should be far below latency
	// (latency includes pacing jitter up to the intended time).
	t.Logf("\n%s", rep.String())
}

func TestRunCoordinatedOmission(t *testing.T) {
	// fn sleeps 5 ms once every 500 ops. At 20k ops/s (50 µs interval), each
	// sleep delays ~100 ops that all record lateness in the Latency series,
	// while Service only sees the sleeper itself.
	h := New(20_000, WithDuration(500*time.Millisecond), WithWarmup(0), WithGCBefore(false))
	var i int
	rep := h.Run(func() {
		i++
		if i%500 == 0 {
			time.Sleep(5 * time.Millisecond)
		}
	})
	if rep.Late == 0 {
		t.Fatal("expected late ops to be counted")
	}
	if rep.Latency.P90 <= rep.Service.P99 {
		t.Fatalf("coordinated omission not visible: latency p90=%v service p99=%v",
			rep.Latency.P90, rep.Service.P99)
	}
	t.Logf("latency p90=%v p99=%v max=%v | service p99=%v | late=%d",
		rep.Latency.P90, rep.Latency.P99, rep.Latency.Max, rep.Service.P99, rep.Late)
}

func TestRunGCAttribution(t *testing.T) {
	h := New(1_000, WithDuration(300*time.Millisecond), WithWarmup(0), WithGCBefore(false))
	var once bool
	rep := h.Run(func() {
		if !once {
			once = true
			runtime.GC()
		}
	})
	if rep.GCCycles < 1 || rep.GCForced < 1 {
		t.Fatalf("runtime.GC in workload: cycles=%d forced=%d", rep.GCCycles, rep.GCForced)
	}
	if rep.GCPauseMax <= 0 {
		t.Fatal("expected positive GC pause max")
	}
	t.Logf("\n%s", rep.String())
}

func TestRegressed(t *testing.T) {
	base := Report{Label: "a", Rate: 1000}
	next := Report{Label: "b", Rate: 1000}
	base.Latency.P50 = 500 * time.Nanosecond
	next.Latency.P50 = 900 * time.Nanosecond // +80% but < 1 µs → jitter, not a regression
	base.Latency.P99 = 100 * time.Microsecond
	next.Latency.P99 = 200 * time.Microsecond // +100% → regression
	base.Latency.Max = time.Millisecond
	next.Latency.Max = 1100 * time.Microsecond // exactly +10% → not over the bar

	got := Regressed(base, next)
	if len(got) != 1 || got[0] != "p99" {
		t.Fatalf("Regressed = %v, want [p99]", got)
	}
	if out := Compare(base, next); strings.Count(out, "regression") != 1 {
		t.Fatalf("Compare should mark exactly one row:\n%s", out)
	}
	if r := Regressed(base, base); len(r) != 0 {
		t.Fatalf("identical reports regressed: %v", r)
	}
}

func TestRuntimeBucketUpper(t *testing.T) {
	b := []float64{math.Inf(-1), 0, 1e-6, 2e-6, math.Inf(1)}
	if got := deltaQuantile([]uint64{0, 0, 10, 0}, b, 0.99); got != 2*time.Microsecond {
		t.Fatalf("p99 = %v, want 2µs (upper edge)", got)
	}
	if got := deltaMax([]uint64{0, 0, 0, 3}, b); got != 2*time.Microsecond {
		t.Fatalf("overflow max = %v, want its finite lower edge 2µs", got)
	}
}

func TestNewRejectsBadRate(t *testing.T) {
	for _, r := range []int{0, -1, int(time.Second) + 1} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("New(%d) did not panic", r)
				}
			}()
			New(r)
		}()
	}
}

func BenchmarkHistogramRecord(b *testing.B) {
	var h histogram
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		h.Record(time.Duration(i & 0xFFFFF))
	}
}
