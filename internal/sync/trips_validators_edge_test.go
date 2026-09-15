package sync

import (
	"testing"
)

func edgeTerm(id int64, lat, lon float64, finalized bool) AttachTerminal {
	return AttachTerminal{ID: id, Lat: &lat, Lon: &lon, GeomFinalized: finalized}
}

func edgeStop(seq int, termID int64, arr, dep int) MatchedStopTime {
	return MatchedStopTime{Seq: seq, TerminalID: termID, StopID: "s", ArrivalS: arr, DepartureS: dep}
}

func TestCheckMonotonicEdge(t *testing.T) {
	cases := []struct {
		name    string
		stops   []MatchedStopTime
		wantBad bool
	}{
		{"empty", nil, false},
		{"single", []MatchedStopTime{edgeStop(0, 1, 3600, 3660)}, false},
		{"equal arr dep", []MatchedStopTime{edgeStop(0, 1, 3600, 3600)}, false},
		{"touching legs", []MatchedStopTime{edgeStop(0, 1, 0, 3600), edgeStop(1, 2, 3600, 3660)}, false},
		{"overnight absolute", []MatchedStopTime{edgeStop(0, 1, 84600, 84600), edgeStop(1, 2, 87000, 87060)}, false},
		{"ring revisit later", []MatchedStopTime{edgeStop(0, 1, 0, 100), edgeStop(1, 2, 200, 300), edgeStop(2, 1, 400, 500)}, false},
		{"arr after dep", []MatchedStopTime{edgeStop(0, 1, 3700, 3600)}, true},
		{"decreasing", []MatchedStopTime{edgeStop(0, 1, 0, 3600), edgeStop(1, 2, 3599, 3660)}, true},
		{"negative times ordered", []MatchedStopTime{edgeStop(0, 1, -200, -100), edgeStop(1, 2, -50, 0)}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := checkMonotonic(tc.stops) != ""
			if got != tc.wantBad {
				t.Fatalf("checkMonotonic=%v, want bad=%v", got, tc.wantBad)
			}
		})
	}
}

func TestLegSpeedEdge(t *testing.T) {
	lat, lon := 55.0, 86.0
	a := edgeTerm(1, lat, lon, true)
	b := edgeTerm(2, lat+0.0018, lon, true)
	speed, ok := legSpeedKmH(&a, &b, 45)
	if !ok {
		t.Fatal("sub-minute leg must be measurable")
	}
	if speed < 10 || speed > 25 {
		t.Fatalf("200m/45s must be ~16 km/h, got %.1f", speed)
	}
	if _, ok := legSpeedKmH(&a, &b, 0); !ok {
		t.Fatal("zero-time far jump must signal")
	}
	same := edgeTerm(3, lat, lon, true)
	if _, ok := legSpeedKmH(&a, &same, 0); ok {
		t.Fatal("zero-time same point must be skipped")
	}
	var nilTerm *AttachTerminal
	if _, ok := legSpeedKmH(nilTerm, &b, 60); ok {
		t.Fatal("nil terminal must be skipped")
	}
	noGeo := AttachTerminal{ID: 4, GeomFinalized: true}
	if _, ok := legSpeedKmH(&noGeo, &b, 60); ok {
		t.Fatal("missing coords must be skipped")
	}
}

func TestCheckSpeedsBoundary(t *testing.T) {
	a := edgeTerm(1, 55.0, 86.0, true)
	b := edgeTerm(2, 55.9, 86.0, true)
	terms := []AttachTerminal{a, b}
	maxSpeed := 200.0
	leg := []MatchedStopTime{edgeStop(0, 1, 0, 0), edgeStop(1, 2, 1800, 1800)}
	measured, ok := legSpeedKmH(&a, &b, 1800)
	if !ok {
		t.Fatal("must be measurable")
	}
	if bad := checkSpeeds(leg, terms, measured); bad != nil {
		t.Fatalf("speed exactly at limit must pass (strict >): %+v", bad)
	}
	if bad := checkSpeeds(leg, terms, measured-0.001); bad == nil {
		t.Fatal("speed over limit must be dead")
	}
	over := []MatchedStopTime{edgeStop(0, 1, 0, 0), edgeStop(1, 2, 60, 60)}
	if bad := checkSpeeds(over, terms, maxSpeed); bad == nil || bad.Reason != "overspeed" {
		t.Fatalf("100km/60s finalized must be dead overspeed: %+v", bad)
	}
	soft := []AttachTerminal{edgeTerm(1, 55.0, 86.0, false), edgeTerm(2, 55.9, 86.0, false)}
	if bad := checkSpeeds(over, soft, maxSpeed); bad != nil {
		t.Fatalf("unfinalized geom must not be dead: %+v", bad)
	}
	if s := softSpeeds(over, soft, maxSpeed); s == "" {
		t.Fatal("unfinalized overspeed must be soft review")
	}
	if s := softSpeeds(over, terms, maxSpeed); s != "" {
		t.Fatalf("finalized overspeed must not be soft: %q", s)
	}
}
