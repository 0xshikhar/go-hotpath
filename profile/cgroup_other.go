//go:build !linux

package profile

// CgroupMemoryLimit returns -1 on non-Linux platforms: there is no cgroup
// memory limit to read.
func CgroupMemoryLimit() int64 { return -1 }
