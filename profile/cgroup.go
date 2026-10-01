package profile

import "strconv"

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
