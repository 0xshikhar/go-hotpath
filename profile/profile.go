package profile

import (
	"fmt"
	"math"
	"runtime"
	"runtime/debug"

	"github.com/0xshikhar/go-hotpath/internal/rtm"
)

// Profile is a set of runtime GC knobs applied together. The zero value
// changes nothing: 0 in every field means "keep the current value".
type Profile struct {
	Name string

	// GOGC is the GC trigger percentage. 0 keeps the current value;
	// -1 disables the GC (use only with a memory limit); 100 is the default.
	GOGC int

	// MemLimit is the soft memory limit in bytes. 0 keeps the current
	// value; math.MaxInt64 means unlimited.
	MemLimit int64

	// GOMAXPROCS pins the scheduler width. 0 keeps the current value.
	//
	// Warning: since Go 1.25 the runtime periodically re-derives GOMAXPROCS
	// from the cgroup CPU quota. Any explicit call — including one made by
	// Apply — permanently disables that automatic update for the process;
	// Undo can restore the number but not the auto-detection.
	GOMAXPROCS int
}

// Default returns a profile that restores the runtime's standard behavior:
// GOGC=100 and no memory limit.
func Default() Profile {
	return Profile{Name: "default", GOGC: 100, MemLimit: math.MaxInt64}
}

// SilentWindow returns the "GC off, hard ceiling on" profile: the collector
// only runs when live heap approaches memLimit bytes. Good for services with
// a nearly-allocation-free hot path and periodic quiet points.
//
// The limit must be real memory the process can afford to keep — under a
// cgroup memory limit, the kernel OOM-kills you without asking the GC first.
// Read CgroupMemoryLimit and leave headroom.
func SilentWindow(memLimit int64) Profile {
	return Profile{Name: "silent-window", GOGC: -1, MemLimit: memLimit}
}

func (p Profile) validate() error {
	if p.GOGC < -1 {
		return fmt.Errorf("profile %q: GOGC %d invalid (use -1 for off, or a percentage)", p.Name, p.GOGC)
	}
	if p.MemLimit < 0 {
		return fmt.Errorf("profile %q: MemLimit %d invalid", p.Name, p.MemLimit)
	}
	if p.GOMAXPROCS < 0 {
		return fmt.Errorf("profile %q: GOMAXPROCS %d invalid", p.Name, p.GOMAXPROCS)
	}
	return nil
}

// Knobs are the effective runtime settings, read back via runtime/metrics.
// GOGC == -1 means the collector is off; MemLimit == math.MaxInt64 means no
// soft limit.
type Knobs struct {
	GOGC       int64
	MemLimit   int64
	GOMAXPROCS int64
}

var knobSet = rtm.NewSet(
	rtm.MetricGOGC,
	rtm.MetricGOMemLimit,
	rtm.MetricGOMAXPROCS,
)

func readKnobs() Knobs {
	knobSet.Read()
	return Knobs{
		GOGC:       int64(knobSet.Value(0).Uint64()), // off reports -1 as uint64
		MemLimit:   int64(knobSet.Value(1).Uint64()),
		GOMAXPROCS: int64(knobSet.Value(2).Uint64()),
	}
}

// Session is an active profile application. Not safe for concurrent use;
// sessions are expected to be created and undone by a single owner.
type Session struct {
	profile     Profile
	before      Knobs
	effective   Knobs
	procsPinned bool
	active      bool
}

// Apply sets the profile's knobs, verifies the effective values through
// runtime/metrics, and returns a Session that can undo them.
func Apply(p Profile) (*Session, error) {
	if err := p.validate(); err != nil {
		return nil, err
	}
	s := &Session{profile: p, active: true}
	s.before = readKnobs()

	if p.GOGC != 0 {
		debug.SetGCPercent(p.GOGC)
	}
	if p.MemLimit != 0 {
		debug.SetMemoryLimit(p.MemLimit)
	}
	if p.GOMAXPROCS != 0 {
		s.procsPinned = true
		runtime.GOMAXPROCS(p.GOMAXPROCS)
	}

	s.effective = readKnobs()
	if p.GOGC != 0 && s.effective.GOGC != int64(p.GOGC) {
		return nil, fmt.Errorf("profile %q: GOGC readback %d, want %d", p.Name, s.effective.GOGC, p.GOGC)
	}
	if p.MemLimit != 0 && s.effective.MemLimit != p.MemLimit {
		return nil, fmt.Errorf("profile %q: MemLimit readback %d, want %d", p.Name, s.effective.MemLimit, p.MemLimit)
	}
	if p.GOMAXPROCS != 0 && s.effective.GOMAXPROCS != int64(p.GOMAXPROCS) {
		return nil, fmt.Errorf("profile %q: GOMAXPROCS readback %d, want %d", p.Name, s.effective.GOMAXPROCS, p.GOMAXPROCS)
	}
	return s, nil
}

// Before returns the knobs as they were before Apply.
func (s *Session) Before() Knobs { return s.before }

// Effective returns the knobs as read back after Apply.
func (s *Session) Effective() Knobs { return s.effective }

// Active reports whether the session's settings are currently applied
// (i.e., Undo has not run).
func (s *Session) Active() bool { return s.active }

// Undo restores the pre-Apply values and marks the session inactive.
// Calling Undo on an inactive session returns an error.
//
// If the profile pinned GOMAXPROCS, Undo restores the numeric value but
// cannot re-enable automatic cgroup detection — see the package doc.
func (s *Session) Undo() error {
	if !s.active {
		return fmt.Errorf("profile %q: session is not active", s.profile.Name)
	}
	s.active = false

	// Restore in the same order Apply set them.
	if s.profile.GOGC != 0 {
		debug.SetGCPercent(int(s.before.GOGC))
	}
	if s.profile.MemLimit != 0 {
		debug.SetMemoryLimit(s.before.MemLimit)
	}
	if s.profile.GOMAXPROCS != 0 {
		runtime.GOMAXPROCS(int(s.before.GOMAXPROCS))
	}

	got := readKnobs()
	if s.profile.GOGC != 0 && got.GOGC != s.before.GOGC {
		return fmt.Errorf("profile %q: GOGC restore readback %d, want %d", s.profile.Name, got.GOGC, s.before.GOGC)
	}
	if s.profile.MemLimit != 0 && got.MemLimit != s.before.MemLimit {
		return fmt.Errorf("profile %q: MemLimit restore readback %d, want %d", s.profile.Name, got.MemLimit, s.before.MemLimit)
	}
	if s.profile.GOMAXPROCS != 0 && got.GOMAXPROCS != s.before.GOMAXPROCS {
		return fmt.Errorf("profile %q: GOMAXPROCS restore readback %d, want %d", s.profile.Name, got.GOMAXPROCS, s.before.GOMAXPROCS)
	}
	return nil
}
