package rtm

import (
	"runtime"
	"testing"
)

func TestCheck(t *testing.T) {
	if err := Check(); err != nil {
		t.Fatalf("Check() = %v", err)
	}
}

func TestSetReadZeroAllocs(t *testing.T) {
	s := NewSet(MetricGCCyclesTotal, MetricHeapAllocBytes, MetricHeapAllocObjects)
	s.Read() // warm: first call may touch caches
	if n := testing.AllocsPerRun(1000, s.Read); n != 0 {
		t.Fatalf("Set.Read allocated %f times per call; want 0", n)
	}
}

func TestNewSetPanicsOnUnknown(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for unknown metric")
		}
	}()
	NewSet("/definitely/not/a/metric:nope")
}

func TestNewSetRejectsHistogram(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for histogram in scalar Set")
		}
	}()
	NewSet(MetricSchedLatencies)
}

func TestNewHistSetRejectsScalar(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for scalar in HistSet")
		}
	}()
	NewHistSet(MetricGCCyclesTotal)
}

func TestHistSetRead(t *testing.T) {
	h := NewHistSet(MetricSchedLatencies, MetricSchedPausesGC)
	h.Read()
	hist := h.Histogram(0)
	if hist == nil || len(hist.Counts) == 0 {
		t.Fatal("sched latencies histogram empty")
	}
	if got := h.Index(MetricSchedPausesGC); got != 1 {
		t.Fatalf("Index = %d, want 1", got)
	}
}

func TestCyclesCounterIncreases(t *testing.T) {
	s := NewSet(MetricGCCyclesTotal, MetricGCCyclesForced)
	s.Read()
	c0, f0 := s.Value(0).Uint64(), s.Value(1).Uint64()
	runtime.GC()
	s.Read()
	if s.Value(0).Uint64() <= c0 {
		t.Fatal("cycle counter did not increase after runtime.GC")
	}
	if s.Value(1).Uint64() <= f0 {
		t.Fatal("forced-cycle counter did not increase after runtime.GC")
	}
}

func TestValueKindPanics(t *testing.T) {
	s := NewSet(MetricGCCyclesTotal, MetricCPUGCAssist)
	s.Read()
	// Uint64 accessor on a float64 metric must panic, not return garbage.
	defer func() {
		if recover() == nil {
			t.Fatal("Uint64 on Float64 metric did not panic")
		}
	}()
	_ = s.Value(1).Uint64()
}

func TestIndex(t *testing.T) {
	s := NewSet(MetricGCCyclesTotal, MetricHeapLive)
	if s.Index(MetricHeapLive) != 1 {
		t.Fatal("Index wrong")
	}
	if s.Index("/nope") != -1 {
		t.Fatal("Index should be -1 for absent name")
	}
	if s.Len() != 2 {
		t.Fatal("Len wrong")
	}
}

func BenchmarkSetRead5(b *testing.B) {
	s := NewSet(
		MetricGCCyclesTotal,
		MetricGCCyclesForced,
		MetricHeapAllocBytes,
		MetricHeapAllocObjects,
		MetricLimiterLast,
	)
	b.ReportAllocs()
	for b.Loop() {
		s.Read()
	}
}

func BenchmarkSetRead5Parallel(b *testing.B) {
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		// One Set per goroutine, per the single-owner rule.
		s := NewSet(
			MetricGCCyclesTotal,
			MetricGCCyclesForced,
			MetricHeapAllocBytes,
			MetricHeapAllocObjects,
			MetricLimiterLast,
		)
		for pb.Next() {
			s.Read()
		}
	})
}

func BenchmarkHistSetRead(b *testing.B) {
	h := NewHistSet(MetricSchedLatencies, MetricSchedPausesGC)
	b.ReportAllocs()
	for b.Loop() {
		h.Read()
	}
}
