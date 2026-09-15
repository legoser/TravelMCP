package planner

import "testing"

func TestArrivalWindowConfigEdge(t *testing.T) {
	p := New(nil)
	if p.arrivalWindowH != 24 || p.arrivalStepMin != 30 {
		t.Fatalf("defaults = %d/%d, want 24/30", p.arrivalWindowH, p.arrivalStepMin)
	}
	p.WithArrivalWindow(48, 15)
	if p.arrivalWindowH != 48 || p.arrivalStepMin != 15 {
		t.Fatalf("valid override rejected: %d/%d", p.arrivalWindowH, p.arrivalStepMin)
	}
	for _, tc := range [][2]int{{0, 15}, {48, 0}, {-1, 15}, {48, -5}} {
		p.WithArrivalWindow(tc[0], tc[1])
		if p.arrivalWindowH != 48 || p.arrivalStepMin != 15 {
			t.Fatalf("invalid (%d,%d) must keep previous: %d/%d", tc[0], tc[1], p.arrivalWindowH, p.arrivalStepMin)
		}
	}
}
