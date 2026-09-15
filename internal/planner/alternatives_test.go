package planner

import "testing"

func TestAlternativesConfigEdge(t *testing.T) {
	p := New(nil)
	if p.altWindowMin != 360 || p.altStepMin != 60 {
		t.Fatalf("defaults = %d/%d, want 360/60", p.altWindowMin, p.altStepMin)
	}
	p.WithAlternatives(120, 30)
	if p.altWindowMin != 120 || p.altStepMin != 30 {
		t.Fatalf("valid override rejected: %d/%d", p.altWindowMin, p.altStepMin)
	}
	for _, tc := range [][2]int{{0, 30}, {120, 0}, {-5, 30}, {120, -1}, {30, 60}, {120, 121}} {
		p.WithAlternatives(tc[0], tc[1])
		if p.altWindowMin != 120 || p.altStepMin != 30 {
			t.Fatalf("invalid (%d,%d) must keep previous: %d/%d", tc[0], tc[1], p.altWindowMin, p.altStepMin)
		}
	}
	p.WithAlternatives(60, 60)
	if p.altWindowMin != 60 || p.altStepMin != 60 {
		t.Fatalf("step==window is valid single extra run: %d/%d", p.altWindowMin, p.altStepMin)
	}
}
