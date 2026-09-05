package profile_test

import (
	"math"
	"runtime"
	"testing"

	"github.com/0xshikhar/go-hotpath/profile"
)

func TestApplySilentWindow(t *testing.T) {
	before := profile.Read()
	if before.GOGC != 100 || before.MemLimit != math.MaxInt64 {
		t.Fatalf("unexpected defaults: %+v", before)
	}

	s, err := profile.Apply(profile.SilentWindow(512 << 20))
	if err != nil {
		t.Fatal(err)
	}
	eff := s.Effective()
	if eff.GOGC != -1 {
		t.Fatalf("GOGC effective %d, want -1", eff.GOGC)
	}
	if eff.MemLimit != 512<<20 {
		t.Fatalf("MemLimit effective %d, want %d", eff.MemLimit, 512<<20)
	}
	if !s.Active() {
		t.Fatal("session should be active")
	}
	// Verify through an independent read, not just session state.
	if snap := profile.Read(); snap.GOGC != -1 {
		t.Fatalf("snapshot GOGC %d, want -1", snap.GOGC)
	}

	if err := s.Undo(); err != nil {
		t.Fatal(err)
	}
	if s.Active() {
		t.Fatal("session should be inactive after Undo")
	}
	snap := profile.Read()
	if snap.GOGC != before.GOGC || snap.MemLimit != before.MemLimit {
		t.Fatalf("undo failed: got gogc=%d memlimit=%d, want %d/%d",
			snap.GOGC, snap.MemLimit, before.GOGC, before.MemLimit)
	}
}

func TestApplyDefault(t *testing.T) {
	s, err := profile.Apply(profile.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Undo()
	if s.Effective().GOGC != 100 || s.Effective().MemLimit != math.MaxInt64 {
		t.Fatalf("default profile effective = %+v", s.Effective())
	}
}

func TestKeepZeroValue(t *testing.T) {
	s, err := profile.Apply(profile.Profile{Name: "noop"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Undo()
	if s.Effective() != s.Before() {
		t.Fatalf("no-op profile changed knobs: %+v → %+v", s.Before(), s.Effective())
	}
}

func TestValidation(t *testing.T) {
	for _, p := range []profile.Profile{
		{Name: "bad-gogc", GOGC: -5},
		{Name: "bad-mem", MemLimit: -1},
		{Name: "bad-procs", GOMAXPROCS: -1},
	} {
		if _, err := profile.Apply(p); err == nil {
			t.Errorf("Apply(%+v) should fail", p)
		}
	}
}

func TestUndoTwice(t *testing.T) {
	s, err := profile.Apply(profile.Default())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Undo(); err != nil {
		t.Fatal(err)
	}
	if err := s.Undo(); err == nil {
		t.Fatal("second Undo should return an error")
	}
}

func TestGOMAXPROCSPin(t *testing.T) {
	// Pinning in a test binary is harmless (process-local). The point is
	// verifying apply/undo readback — and documenting the one-way door.
	n := runtime.GOMAXPROCS(0)
	s, err := profile.Apply(profile.Profile{Name: "pin", GOMAXPROCS: 2})
	if err != nil {
		t.Fatal(err)
	}
	if s.Effective().GOMAXPROCS != 2 || runtime.GOMAXPROCS(0) != 2 {
		t.Fatalf("GOMAXPROCS effective %d, want 2", s.Effective().GOMAXPROCS)
	}
	if err := s.Undo(); err != nil {
		t.Fatal(err)
	}
	if runtime.GOMAXPROCS(0) != n {
		t.Fatalf("GOMAXPROCS after undo = %d, want %d", runtime.GOMAXPROCS(0), n)
	}
}

func TestQuietGCReclaims(t *testing.T) {
	// Make garbage: allocate and drop ~8 MiB.
	junk := make([][]byte, 0, 64)
	for i := 0; i < 64; i++ {
		junk = append(junk, make([]byte, 128<<10))
	}
	junk = nil

	res := profile.QuietGC()
	if res.Duration <= 0 {
		t.Fatal("QuietGC reported zero duration")
	}
	if res.HeapLiveAfter > res.HeapLiveBefore {
		t.Fatalf("live heap grew across QuietGC: %d → %d", res.HeapLiveBefore, res.HeapLiveAfter)
	}
	t.Logf("QuietGC: %v, live %d → %d", res.Duration, res.HeapLiveBefore, res.HeapLiveAfter)
}

func TestCgroupMemoryLimit(t *testing.T) {
	// On darwin this must be -1; on linux CI it may read a real limit.
	if got := profile.CgroupMemoryLimit(); got != -1 && got <= 0 {
		t.Fatalf("CgroupMemoryLimit = %d; want -1 or a positive limit", got)
	}
}

func TestSnapshotFields(t *testing.T) {
	s := profile.Read()
	if s.GOMAXPROCS != int64(runtime.GOMAXPROCS(0)) {
		t.Fatalf("GOMAXPROCS %d != runtime %d", s.GOMAXPROCS, runtime.GOMAXPROCS(0))
	}
	if s.Goroutines <= 0 || s.HeapLive <= 0 || s.HeapGoal <= 0 || s.MemTotal <= 0 {
		t.Fatalf("snapshot has zero fields: %+v", s)
	}
	if s.GoVersion != runtime.Version() {
		t.Fatalf("GoVersion %q != %q", s.GoVersion, runtime.Version())
	}
}
