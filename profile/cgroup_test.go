package profile

import (
	"os"
	"testing"
)

func TestParseLimit(t *testing.T) {
	cases := []struct {
		in     string
		want   int64
		wantOK bool
	}{
		{"max", -1, true},
		{"536870912", 536870912, true},    // 512 MiB
		{"9223372036854771712", -1, true}, // v1 unlimited sentinel
		{"0", 0, true},
		{"", -1, true},
		{"garbage", -1, false},
		{"-5", -1, false},
	}
	for _, c := range cases {
		got, ok := parseLimit(c.in)
		if got != c.want || ok != c.wantOK {
			t.Errorf("parseLimit(%q) = %d,%v; want %d,%v", c.in, got, ok, c.want, c.wantOK)
		}
	}
}

func fakeFS(files map[string]string) func(string) ([]byte, error) {
	return func(p string) ([]byte, error) {
		if s, ok := files[p]; ok {
			return []byte(s), nil
		}
		return nil, os.ErrNotExist
	}
}

func TestCgroupMemoryLimitResolution(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  int64
	}{
		{"no cgroupfs", map[string]string{}, -1},
		{"v2 namespaced container", map[string]string{
			"/proc/self/cgroup":         "0::/\n",
			"/sys/fs/cgroup/memory.max": "536870912\n",
		}, 512 << 20},
		{"v2 nested, ancestor is tighter", map[string]string{
			"/proc/self/cgroup":                          "0::/kubepods/pod1/c1\n",
			"/sys/fs/cgroup/kubepods/pod1/c1/memory.max": "max\n",
			"/sys/fs/cgroup/kubepods/pod1/memory.max":    "268435456\n",
		}, 256 << 20},
		{"v2 unlimited", map[string]string{
			"/proc/self/cgroup":                    "0::/user.slice\n",
			"/sys/fs/cgroup/user.slice/memory.max": "max\n",
		}, -1},
		{"v1 memory controller", map[string]string{
			"/proc/self/cgroup":                           "12:cpu,cpuacct:/docker/x\n4:memory:/docker/x\n",
			"/sys/fs/cgroup/memory/memory.limit_in_bytes": "1073741824\n",
		}, 1 << 30},
	}
	for _, c := range cases {
		if got := cgroupMemoryLimit(fakeFS(c.files)); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
}
