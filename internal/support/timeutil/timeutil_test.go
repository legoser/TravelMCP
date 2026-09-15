package timeutil

import "testing"

func TestParseTimeMinutesEdge(t *testing.T) {
	cases := []struct {
		in   string
		want int
		ok   bool
	}{
		{"06:00", 360, true},
		{"6:00", 360, true},
		{" 07:05 ", 425, true},
		{"00:00", 0, true},
		{"23:59", 1439, true},
		{"24:00", 0, false},
		{"06:60", 0, false},
		{"", 0, false},
		{"noon", 0, false},
		{"7:5", 0, false},
		{"-1:00", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, ok := ParseTimeMinutes(tc.in)
			if ok != tc.ok || (ok && got != tc.want) {
				t.Fatalf("ParseTimeMinutes(%q)=(%d,%v), want (%d,%v)", tc.in, got, ok, tc.want, tc.ok)
			}
		})
	}
}
