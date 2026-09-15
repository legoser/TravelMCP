package jobs

import (
	"testing"
	"time"
)

func TestBackoffEdge(t *testing.T) {
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{0, 0},
		{1, 2 * time.Minute},
		{5, 10 * time.Minute},
		{15, 30 * time.Minute},
		{100, 30 * time.Minute},
		{-3, 0},
	}
	for _, tc := range cases {
		if got := Backoff(tc.attempt); got != tc.want {
			t.Errorf("Backoff(%d)=%v want %v", tc.attempt, got, tc.want)
		}
	}
}
