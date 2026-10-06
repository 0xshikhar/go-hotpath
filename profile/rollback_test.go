package profile

import (
	"runtime/debug"
	"testing"
)

func TestApplyRollsBackOnReadbackMismatch(t *testing.T) {
	prev := debug.SetGCPercent(100)
	defer debug.SetGCPercent(prev)

	calls := 0
	readKnobs = func() Knobs {
		calls++
		k := readKnobsRuntime()
		if calls == 2 { // the post-set verification read
			k.GOGC = 7
		}
		return k
	}
	defer func() { readKnobs = readKnobsRuntime }()

	if _, err := Apply(Profile{Name: "t", GOGC: 50}); err == nil {
		t.Fatal("Apply succeeded despite a read-back mismatch")
	}
	if got := readKnobsRuntime().GOGC; got != 100 {
		t.Fatalf("GOGC after failed Apply = %d, want 100 (rolled back)", got)
	}
}
