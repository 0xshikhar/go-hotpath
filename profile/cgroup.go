package profile

import (
	"path"
	"strconv"
	"strings"
)

// cgroupMemoryLimit resolves the effective cgroup memory limit: the
// smallest limit on the process's own cgroup or any ancestor, cgroup v2
// first, then v1. read is injected so the logic is testable off Linux.
// Returns -1 when no limit applies.
func cgroupMemoryLimit(read func(string) ([]byte, error)) int64 {
	self, _ := read("/proc/self/cgroup")
	v2, v1 := cgroupPaths(string(self))
	if n, ok := minLimit(read, "/sys/fs/cgroup", v2, "memory.max"); ok {
		return n
	}
	if n, ok := minLimit(read, "/sys/fs/cgroup/memory", v1, "memory.limit_in_bytes"); ok {
		return n
	}
	return -1
}

// cgroupPaths extracts the v2 path ("0::/path") and the v1 memory
// controller path from /proc/self/cgroup. Missing entries default to "/",
// which is what a container with its own cgroup namespace reports anyway.
func cgroupPaths(self string) (v2, v1 string) {
	v2, v1 = "/", "/"
	for _, line := range strings.Split(self, "\n") {
		f := strings.SplitN(line, ":", 3)
		if len(f) != 3 {
			continue
		}
		if f[0] == "0" && f[1] == "" {
			v2 = f[2]
			continue
		}
		for _, c := range strings.Split(f[1], ",") {
			if c == "memory" {
				v1 = f[2]
			}
		}
	}
	return v2, v1
}

// minLimit walks dir and its ancestors under root, returning the smallest
// limit found in file. ok is false when no level had a readable file.
func minLimit(read func(string) ([]byte, error), root, dir, file string) (limit int64, ok bool) {
	limit = -1
	for d := path.Clean("/" + dir); ; d = path.Dir(d) {
		if b, err := read(path.Join(root, d, file)); err == nil {
			if n, valid := parseLimit(strings.TrimSpace(string(b))); valid {
				ok = true
				if n >= 0 && (limit < 0 || n < limit) {
					limit = n
				}
			}
		}
		if d == "/" {
			return limit, ok
		}
	}
}

// parseLimit interprets a cgroup memory limit field. "max" (v2), empty, and
// absurd v1 sentinels all mean unlimited → (-1, true). A real byte count
// parses to the limit. Garbage → (-1, false).
func parseLimit(s string) (int64, bool) {
	if s == "" || s == "max" {
		return -1, true
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return -1, false
	}
	// v1 unlimited is a huge sentinel near MaxInt64; anything >= 1 EiB is
	// effectively "no limit" in practice.
	if n >= 1<<60 {
		return -1, true
	}
	return n, true
}
