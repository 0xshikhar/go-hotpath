package profile

import (
	"fmt"
	"math"
	"runtime"
	"runtime/debug"
	"sync"

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

// setsMu serializes all reads of the package-level metric sets. rtm.Set is
// single-goroutine (a read writes into the sample buffer), and a status
// endpoint calling Read while Apply is mid-verify would race without it.
var setsMu sync.Mutex

var knobSet = rtm.NewSet(
	rtm.MetricGOGC,
	rtm.MetricGOMemLimit,
	rtm.MetricGOMAXPROCS,
)

// readKnobs is a variable so tests can simulate a read-back mismatch.
var readKnobs = readKnobsRuntime

func readKnobsRuntime() Knobs {
	setsMu.Lock()
	defer setsMu.Unlock()
	knobSet.Read()
	return Knobs{
		GOGC:       int64(knobSet.Value(0).Uint64()), // off reports -1 as uint64
		MemLimit:   int64(knobSet.Value(1).Uint64()),
		GOMAXPROCS: int64(knobSet.Value(2).Uint64()),
	}
}

// Session is an active profile application. Not safe for concurrent use;
// sessions are expected to be created and undone by a single owner. Undo
// overlapping sessions in reverse order of Apply.
type Session struct {
	profile   Profile
	mask      knobMask
	before    Knobs
	effective Knobs
	active    bool
}

// knobMask records which knobs a profile touches. Restores go through the
// mask rather than the zero-means-keep sentinel, so a prior GOGC=0 is
// restored faithfully.
type knobMask struct{ gogc, memLimit, procs bool }

func (p Profile) mask() knobMask {
	return knobMask{gogc: p.GOGC != 0, memLimit: p.MemLimit != 0, procs: p.GOMAXPROCS != 0}
}

func (p Profile) knobs() Knobs {
	return Knobs{GOGC: int64(p.GOGC), MemLimit: p.MemLimit, GOMAXPROCS: int64(p.GOMAXPROCS)}
}

// set applies the masked knobs of k. A memory limit that is being lowered is
// set before GOGC, so there is never an instant with the collector off and
// no ceiling.
func set(m knobMask, k, current Knobs) {
	limitFirst := m.memLimit && k.MemLimit < current.MemLimit
	if limitFirst {
		debug.SetMemoryLimit(k.MemLimit)
	}
	if m.gogc {
		debug.SetGCPercent(int(k.GOGC))
	}
	if m.memLimit && !limitFirst {
		debug.SetMemoryLimit(k.MemLimit)
	}
	if m.procs {
		runtime.GOMAXPROCS(int(k.GOMAXPROCS))
	}
}

// mismatch reports the first masked knob whose read-back value differs.
func mismatch(name, what string, m knobMask, got, want Knobs) error {
	switch {
	case m.gogc && got.GOGC != want.GOGC:
		return fmt.Errorf("profile %q: GOGC %s %d, want %d", name, what, got.GOGC, want.GOGC)
	case m.memLimit && got.MemLimit != want.MemLimit:
		return fmt.Errorf("profile %q: MemLimit %s %d, want %d", name, what, got.MemLimit, want.MemLimit)
	case m.procs && got.GOMAXPROCS != want.GOMAXPROCS:
		return fmt.Errorf("profile %q: GOMAXPROCS %s %d, want %d", name, what, got.GOMAXPROCS, want.GOMAXPROCS)
	}
	return nil
}

// Apply sets the profile's knobs, verifies the effective values through
// runtime/metrics, and returns a Session that can undo them.
//
// If read-back does not match the request, Apply restores the previous
// values before returning the error: a failed Apply leaves the runtime as it
// found it.
func Apply(p Profile) (*Session, error) {
	if err := p.validate(); err != nil {
		return nil, err
	}
	s := &Session{profile: p, mask: p.mask(), before: readKnobs(), active: true}

	set(s.mask, p.knobs(), s.before)
	s.effective = readKnobs()
	if err := mismatch(p.Name, "readback", s.mask, s.effective, p.knobs()); err != nil {
		set(s.mask, s.before, s.effective)
		return nil, err
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

// Undo restores the pre-Apply values of the knobs this profile changed and
// marks the session inactive. Calling Undo on an inactive session returns an
// error.
//
// If the profile pinned GOMAXPROCS, Undo restores the numeric value but
// cannot re-enable automatic cgroup detection — see the package doc.
func (s *Session) Undo() error {
	if !s.active {
		return fmt.Errorf("profile %q: session is not active", s.profile.Name)
	}
	s.active = false
	set(s.mask, s.before, readKnobs())
	return mismatch(s.profile.Name, "restore readback", s.mask, readKnobs(), s.before)
}
