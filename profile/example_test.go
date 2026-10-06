package profile_test

import (
	"fmt"
	"log"

	"github.com/0xshikhar/go-hotpath/profile"
)

// Turn the GC off behind a memory ceiling, collect only at boundaries you
// choose, and put the previous settings back when done.
func ExampleApply() {
	s, err := profile.Apply(profile.SilentWindow(512 << 20))
	if err != nil {
		log.Fatal(err) // a failed Apply has already restored the old settings
	}
	defer s.Undo()

	for batch := 0; batch < 3; batch++ {
		// ... process a batch on the near-allocation-free hot path ...
		res := profile.QuietGC() // the cycle lands here, between batches
		log.Printf("batch %d: GC took %v, reclaimed %d bytes", batch, res.Duration, res.Reclaimed())
	}
}

// Read is cheap enough for a status endpoint: one scalar metrics read plus
// the cgroup lookup.
func ExampleRead() {
	s := profile.Read()
	headroom := s.HeapGoal - s.HeapLive
	fmt.Printf("GOGC=%d next GC in %d bytes, cgroup limit %d\n", s.GOGC, headroom, s.CgroupMemLimit)
}

// Check a SilentWindow ceiling against the container before applying it.
func ExampleCgroupMemoryLimit() {
	limit := int64(1 << 30)
	if cg := profile.CgroupMemoryLimit(); cg > 0 && limit > cg*8/10 {
		limit = cg * 8 / 10 // leave headroom: the kernel OOM-kills without asking the GC
	}
	fmt.Println(profile.SilentWindow(limit).MemLimit > 0)
	// Output: true
}
