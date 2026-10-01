package profile

import (
	"runtime"
	"sync"
	"time"

	"github.com/0xshikhar/go-hotpath/internal/rtm"
)

// QuietResult reports what a deliberate QuietGC cycle did.
type QuietResult struct {
	// Duration is the wall time runtime.GC() blocked the caller — includes
	// the mark setup/termination STW phases plus concurrent completion.
	Duration time.Duration

	// HeapLiveBefore/After bracket the cycle: the delta is roughly what the
	// cycle reclaimed (plus whatever the GC's own bookkeeping added).
	HeapLiveBefore int64
	HeapLiveAfter  int64
}

var heapLiveOnce = sync.OnceValue(func() *rtm.Set {
	return rtm.NewSet(rtm.MetricHeapLive)
})

// QuietGC runs a full collection now, at a boundary the caller chose — end
// of a batch, between market sessions, before opening a hot window.
//
// The point is control, not speed: a cycle forced here is a cycle that will
// not fire mid-window. Combine with SilentWindow (GOGC off + memory limit)
// and a periodic boundary call for the classic "GC is a scheduled event, not
// a surprise" setup.
//
// Cost: whatever a full cycle costs on your heap (typically tens of µs of
// STW plus marking). Call it on a cold path only.
func QuietGC() QuietResult {
	set := heapLiveOnce()
	setsMu.Lock()
	set.Read()
	before := int64(set.Value(0).Uint64())
	setsMu.Unlock()

	start := time.Now()
	runtime.GC()
	dur := time.Since(start)

	setsMu.Lock()
	set.Read()
	after := int64(set.Value(0).Uint64())
	setsMu.Unlock()

	return QuietResult{
		Duration:       dur,
		HeapLiveBefore: before,
		HeapLiveAfter:  after,
	}
}
