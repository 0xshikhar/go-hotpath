//go:build linux

package profile

import "testing"

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
