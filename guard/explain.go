package guard

import (
	"fmt"
	"strings"
)

// Explain renders the result as one greppable sentence. It is a cold-path
// convenience for logging — it allocates.
func (r Result) Explain() string {
	if r.Quiet() && r.AllocObjectsObserved == 0 && !r.LimiterEngaged {
		return "quiet: no GC cycle completed and no allocation was observed during the window"
	}

	var findings []string

	if r.CyclesForced > 0 {
		findings = append(findings, fmt.Sprintf(
			"%d forced GC cycle(s) completed (runtime.GC was called during the window)", r.CyclesForced))
	}
	if auto := r.CyclesAutomatic(); auto > 0 {
		findings = append(findings, fmt.Sprintf(
			"%d automatic GC cycle(s) completed (the process reached its heap goal)", auto))
	}
	if r.LimiterEngaged {
		findings = append(findings,
			"the GC CPU limiter engaged (the runtime was spending too much CPU on GC; with a memory limit set, the heap is at its edge)")
	}
	if r.GCCycles == 0 && r.AllocObjectsObserved > 0 {
		findings = append(findings, fmt.Sprintf(
			"no GC cycle, but the process allocated >=%d objects (%s) during the window (pressure, not a fault)",
			r.AllocObjectsObserved, humanBytes(r.AllocBytesObserved)))
	}

	return strings.Join(findings, "; ")
}
