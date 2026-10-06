//go:build linux

package profile

import "os"

// CgroupMemoryLimit returns the effective cgroup memory limit in bytes — the
// smallest limit on the process's cgroup or any ancestor (v2, then v1) — or
// -1 when none applies (bare metal, unlimited container, unreadable cgroupfs).
//
// The Go runtime does not derive its memory limit from the cgroup; compare
// this number against any MemLimit you are about to request, and leave
// headroom — the kernel OOM-kills without asking the GC first.
func CgroupMemoryLimit() int64 { return cgroupMemoryLimit(os.ReadFile) }
