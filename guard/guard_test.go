package guard_test

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/0xshikhar/go-hotpath/guard"
)

var (
	sinkBytes  []byte
	sinkBox    *[16]byte
	sinkResult guard.Result
)

func TestCheck(t *testing.T) {
	if err := guard.Check(); err != nil {
		t.Fatalf("Check() = %v", err)
	}
	_ = guard.New() // must not panic
}

func TestBeginEndZeroAllocs(t *testing.T) {
	g := guard.New()
	// Warm the measurement.
	for i := 0; i < 100; i++ {
		w := g.Begin()
		sinkResult = w.End()
	}
	n := testing.AllocsPerRun(1000, func() {
		w := g.Begin()
		sinkResult = w.End()
	})
	if n != 0 {
		t.Fatalf("Begin+End allocated %f times per call; want 0", n)
	}
}

func TestEmptyWindowIsQuiet(t *testing.T) {
	g := guard.New()
	runtime.GC()
	w := g.Begin()
	r := w.End()
	if !r.Quiet() {
		t.Fatalf("empty window not quiet: %s", r.Explain())
	}
	if r.CyclesForced != 0 || r.LimiterEngaged {
		t.Fatalf("empty window: forced=%d limiter=%v, want 0/false", r.CyclesForced, r.LimiterEngaged)
	}
}

func TestForcedGCInsideWindow(t *testing.T) {
	g := guard.New()
	w := g.Begin()
	runtime.GC()
	r := w.End()
	if r.GCCycles < 1 || r.CyclesForced < 1 {
		t.Fatalf("runtime.GC in window: cycles=%d forced=%d, want >=1", r.GCCycles, r.CyclesForced)
	}
	if r.Quiet() {
		t.Fatal("window containing runtime.GC reported Quiet")
	}
	if r.CyclesAutomatic() != r.GCCycles-r.CyclesForced {
		t.Fatal("CyclesAutomatic inconsistent")
	}
}

func TestLargeAllocObserved(t *testing.T) {
	g := guard.New()
	w := g.Begin()
	sinkBytes = make([]byte, 64<<10) // served directly from the heap: exact
	r := w.End()
	if r.AllocObjectsObserved < 1 || r.AllocBytesObserved < 64<<10 {
		t.Fatalf("64KiB alloc in window: observed objects=%d bytes=%d",
			r.AllocObjectsObserved, r.AllocBytesObserved)
	}
}

func TestExactDetectsSingleAlloc(t *testing.T) {
	res := guard.Exact(func() {
		sinkBox = new([16]byte)
	})
	if res.Mallocs < 1 {
		t.Fatalf("Exact counted %d mallocs for one new(); want >=1", res.Mallocs)
	}
	if res.TotalAllocBytes < 16 {
		t.Fatalf("Exact counted %d bytes for one new([16]byte); want >=16", res.TotalAllocBytes)
	}
}

func TestExactEmpty(t *testing.T) {
	res := guard.Exact(func() {})
	if res.Mallocs != 0 || res.TotalAllocBytes != 0 || res.GCCycles != 0 {
		t.Fatalf("Exact of empty fn: %+v, want all zero", res)
	}
}

// fakeTB records Fatalf calls.
type fakeTB struct {
	calls int
	msg   string
}

func (f *fakeTB) Helper() {}
func (f *fakeTB) Fatalf(format string, args ...any) {
	f.calls++
	f.msg = fmt.Sprintf(format, args...)
}

func TestAssertPassesOnSilent(t *testing.T) {
	tb := &fakeTB{}
	guard.Assert(tb, func() {})
	if tb.calls != 0 {
		t.Fatalf("Assert on empty fn called Fatalf: %s", tb.msg)
	}
}

func TestAssertFailsOnAlloc(t *testing.T) {
	tb := &fakeTB{}
	guard.Assert(tb, func() {
		sinkBox = new([16]byte)
	})
	if tb.calls != 1 {
		t.Fatalf("Assert called Fatalf %d times, want 1", tb.calls)
	}
	for _, want := range []string{"guard:", "not quiet", "allocs", "budget"} {
		if !strings.Contains(tb.msg, want) {
			t.Fatalf("failure message %q missing %q", tb.msg, want)
		}
	}
}

func TestAssertWithinBudget(t *testing.T) {
	tb := &fakeTB{}
	alloc3 := func() {
		sinkBox = new([16]byte)
		sinkBox = new([16]byte)
		sinkBox = new([16]byte)
	}
	// 3 objects = 48 B. Budget fields are limits: a zero field allows zero.
	guard.AssertWithin(tb, guard.Budget{Allocs: 5, Bytes: 64}, alloc3)
	if tb.calls != 0 {
		t.Fatalf("3 allocs/48B within budget (5 allocs, 64B) failed: %s", tb.msg)
	}
	guard.AssertWithin(tb, guard.Budget{Allocs: 2, Bytes: 64}, alloc3)
	if tb.calls != 1 {
		t.Fatal("3 allocs over budget of 2 did not fail")
	}
	if !strings.Contains(tb.msg, "3 allocs") || !strings.Contains(tb.msg, "2 allocs") {
		t.Fatalf("message should report measured and budget: %q", tb.msg)
	}
	tb.calls = 0
	// Bytes limit enforced independently: 48 B over a 32 B budget fails even
	// with Allocs headroom.
	guard.AssertWithin(tb, guard.Budget{Allocs: 5, Bytes: 32}, alloc3)
	if tb.calls != 1 {
		t.Fatal("48 B over a 32 B budget did not fail")
	}
}

func TestAssertFailsOnCycle(t *testing.T) {
	tb := &fakeTB{}
	guard.AssertWithin(tb, guard.Budget{Allocs: 1 << 20, Bytes: 1 << 30}, func() {
		runtime.GC()
	})
	if tb.calls != 1 {
		t.Fatal("runtime.GC inside Assert did not fail")
	}
	if !strings.Contains(tb.msg, "cycle") {
		t.Fatalf("message should mention cycles: %q", tb.msg)
	}
}

func TestExplainSentences(t *testing.T) {
	cases := []struct {
		r    guard.Result
		want string
	}{
		{guard.Result{}, "quiet:"},
		{guard.Result{GCCycles: 1, CyclesForced: 1}, "runtime.GC"},
		{guard.Result{GCCycles: 2, CyclesForced: 1}, "automatic"},
		{guard.Result{LimiterEngaged: true}, "limiter"},
		{guard.Result{AllocObjectsObserved: 142, AllocBytesObserved: 18 << 10}, "pressure"},
	}
	for _, c := range cases {
		if got := c.r.Explain(); !strings.Contains(got, c.want) {
			t.Errorf("Explain(%+v) = %q, want substring %q", c.r, got, c.want)
		}
	}
}

func TestGuardsConcurrentOwnGuardPerGoroutine(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g := guard.New() // one Guard per goroutine
			var local guard.Result
			for j := 0; j < 1000; j++ {
				w := g.Begin()
				local = w.End()
			}
			_ = local
		}()
	}
	wg.Wait()
}

func BenchmarkWindow(b *testing.B) {
	g := guard.New()
	b.ReportAllocs()
	for b.Loop() {
		w := g.Begin()
		sinkResult = w.End()
	}
}

func BenchmarkWindowParallel(b *testing.B) {
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		g := guard.New() // per-goroutine, per the single-owner rule
		for pb.Next() {
			w := g.Begin()
			r := w.End()
			_ = r
		}
	})
}

func BenchmarkExact(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		guard.Exact(func() { sinkResult = guard.Result{} })
	}
}
