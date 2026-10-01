//go:build linux

package profile

import (
	"os"
	"strings"
)

// CgroupMemoryLimit returns the cgroup memory limit in bytes, or -1 when the
// process is not under one (bare metal, unlimited container, or unreadable
// cgroupfs).
//
// The runtime consults the same files to set its default memory limit on
// Go 1.25+; the difference is we expose the number so callers can compare it
// against a MemLimit they are about to request.
func CgroupMemoryLimit() int64 {
	// cgroup v2: unified hierarchy
	if b, err := os.ReadFile("/sys/fs/cgroup/memory.max"); err == nil {
		if n, ok := parseLimit(strings.TrimSpace(string(b))); ok {
			return n
		}
	}
	// cgroup v1: memory controller
	if b, err := os.ReadFile("/sys/fs/cgroup/memory/memory.limit_in_bytes"); err == nil {
		if n, ok := parseLimit(strings.TrimSpace(string(b))); ok {
			return n
		}
	}
	return -1
}
