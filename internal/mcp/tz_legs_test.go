package mcp

import (
	"testing"
	"time"

	"travelmcp/internal/model"
)

func TestNormalizeJourneyTimezones(t *testing.T) {
	nov := time.FixedZone("UTC+7", 7*3600)
	utc := time.Date(2026, 9, 11, 4, 0, 0, 0, time.UTC)
	local := time.Date(2026, 9, 11, 10, 0, 0, 0, nov)
	j := &model.Journey{
		Departure: local, Arrival: utc.Add(196 * time.Minute),
		Legs: []model.Leg{
			{Departure: local, Arrival: local.Add(time.Minute)},
			{Departure: utc, Arrival: utc.Add(135 * time.Minute)},
		},
		Alternatives: []model.Journey{{Legs: []model.Leg{{Departure: local}}}},
	}
	normalizeJourneyTimezones(j)
	for _, l := range j.Legs {
		if l.Departure.Location() != time.UTC || l.Arrival.Location() != time.UTC {
			t.Fatalf("leg not UTC: %v → %v", l.Departure, l.Arrival)
		}
	}
	if j.Departure.Location() != time.UTC || j.Arrival.Location() != time.UTC {
		t.Fatal("journey times not UTC")
	}
	if j.Legs[0].Departure.Format("15:04") != "03:00" {
		t.Fatalf("10:00 +07 → 03:00 UTC, got %s", j.Legs[0].Departure.Format("15:04"))
	}
	if j.Alternatives[0].Legs[0].Departure.Location() != time.UTC {
		t.Fatal("alternative not UTC")
	}
}
