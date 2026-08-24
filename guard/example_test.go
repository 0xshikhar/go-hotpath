package guard_test

import (
	"fmt"
	"runtime"

	"github.com/0xshikhar/go-hotpath/guard"
)

// Production: one Guard per watched goroutine, created once. Wrap the work
// you suspect, then act on the Result.
func ExampleGuard() {
	g := guard.New()

	runtime.GC() // settle the runtime before the measured window

	w := g.Begin()
	// ... hot work: match orders, apply commands, decode frames ...
	r := w.End()

	fmt.Println("quiet:", r.Quiet())
	// Output: quiet: true
}

// Constructed results are useful to see what Explain reports.
func ExampleResult_Explain() {
	fmt.Println(guard.Result{GCCycles: 1, CyclesForced: 1}.Explain())
	fmt.Println(guard.Result{}.Explain())
	// Output:
	// 1 forced GC cycle(s) completed (runtime.GC was called during the window)
	// quiet: no GC cycle completed and no allocation was observed during the window
}

// Tests: Assert fails the test if fn allocates or a GC cycle completes.
// AssertWithin relaxes it with a Budget.
//
//	func TestApplyIsQuiet(t *testing.T) {
//		guard.Assert(t, func() { book.Apply(&cmd, &ev) })
//	}
func ExampleExact() {
	res := guard.Exact(func() {
		buf := make([]byte, 0, 1<<20)
		_ = buf
	})
	fmt.Println(res.Mallocs > 0)
	// Output: true
}
