package profile

import (
	"runtime"

	"github.com/0xshikhar/go-hotpath/internal/rtm"
)

// Snapshot is a point-in-time read of the process's GC-relevant state.
// All values come from runtime/metrics scalars plus the cgroup memory limit
// where one is detectable. Reading a Snapshot is a cold-path operation —
// fine for a status endpoint or a pre/post profile check, not for a window.
type Snapshot struct {
	GoVersion string
	GOOS      string
	GOARCH    string

	// Effective knobs.
	GOGC       int64 // -1 means off
	MemLimit   int64 // math.MaxInt64 means unlimited
	GOMAXPROCS int64

	// Heap state.
	HeapLive int64 // /gc/heap/live — bytes the GC must keep
	HeapGoal int64 // /gc/heap/goal — bytes that trigger the next cycle
	ScanHeap int64 // /gc/scan/heap — pointer-containing bytes the GC walks

	// Process state.
	MemTotal   int64 // /memory/classes/total
	Goroutines int64

	// CgroupMemLimit is the container's memory limit in bytes, or -1 if the
	// process isn't in a cgroup with a limit (or the OS exposes none). The
	// runtime's MemLimit can exceed this — the kernel will OOM-kill first.
	CgroupMemLimit int64
}

var snapSet = rtm.NewSet(
	rtm.MetricGOGC,
	rtm.MetricGOMemLimit,
	rtm.MetricGOMAXPROCS,
	rtm.MetricHeapLive,
	rtm.MetricHeapGoal,
	rtm.MetricScanHeap,
	rtm.MetricMemTotal,
	rtm.MetricGoroutines,
)

// Read returns the process's current snapshot. The read takes the runtime
// metrics semaphore once (~300 ns typical); do not call it per-request on a
// contended process.
func Read() Snapshot {
	cgroup := CgroupMemoryLimit() // file I/O on Linux; keep it outside the lock
	setsMu.Lock()
	defer setsMu.Unlock()
	snapSet.Read()
	return Snapshot{
		GoVersion:      runtime.Version(),
		GOOS:           runtime.GOOS,
		GOARCH:         runtime.GOARCH,
		GOGC:           int64(snapSet.Value(0).Uint64()),
		MemLimit:       int64(snapSet.Value(1).Uint64()),
		GOMAXPROCS:     int64(snapSet.Value(2).Uint64()),
		HeapLive:       int64(snapSet.Value(3).Uint64()),
		HeapGoal:       int64(snapSet.Value(4).Uint64()),
		ScanHeap:       int64(snapSet.Value(5).Uint64()),
		MemTotal:       int64(snapSet.Value(6).Uint64()),
		Goroutines:     int64(snapSet.Value(7).Uint64()),
		CgroupMemLimit: cgroup,
	}
}
