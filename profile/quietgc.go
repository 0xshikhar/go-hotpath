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
	// the mark setup/termination STW phases, concurrent marking, and the
	// full sweep runtime.GC performs before returning.
	Duration time.Duration

	// HeapObjectsBefore/After are heap object bytes (/memory/classes/heap/
	// objects) around the cycle. Before includes dead objects not yet
	// swept; after the cycle and its sweep it is approximately the live
	// heap. Their difference is what the cycle reclaimed — see Reclaimed.
	HeapObjectsBefore int64
	HeapObjectsAfter  int64

	// HeapLiveBefore/After are /gc/heap/live: the live heap as marked by the
	// previous cycle and by this one. Their difference is how the live set
	// changed between the two cycles, not what was reclaimed.
	HeapLiveBefore int64
	HeapLiveAfter  int64
}

// Reclaimed is the number of heap bytes the cycle freed (never negative).
func (r QuietResult) Reclaimed() int64 {
	if d := r.HeapObjectsBefore - r.HeapObjectsAfter; d > 0 {
		return d
	}
	return 0
}

var quietSet = sync.OnceValue(func() *rtm.Set {
	return rtm.NewSet(rtm.MetricHeapLive, rtm.MetricMemHeapObjects)
})

func readHeap(set *rtm.Set) (live, objects int64) {
	setsMu.Lock()
	defer setsMu.Unlock()
	set.Read()
	return int64(set.Value(0).Uint64()), int64(set.Value(1).Uint64())
}

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
	set := quietSet()
	r := QuietResult{}
	r.HeapLiveBefore, r.HeapObjectsBefore = readHeap(set)

	start := time.Now()
	runtime.GC()
	r.Duration = time.Since(start)

	r.HeapLiveAfter, r.HeapObjectsAfter = readHeap(set)
	return r
}
