package planner

import (
	"testing"
	"time"

	"travelmcp/internal/model"
	"travelmcp/internal/providers"
	"travelmcp/internal/telemetry"
)

func TestPlanArrivalBy(t *testing.T) {
	day := time.Date(2026, 8, 30, 0, 0, 0, 0, time.UTC)
	synth := providers.NewSynth(day)
	net, _ := synth.NetworkForDay(day)
	p := New(telemetry.New())
	from := model.Coords{Lat: 58.0135, Lon: 56.2495}
	to := model.Coords{Lat: 56.84, Lon: 60.607}
	arrival := time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)
	j, err := p.Plan(net, from, to, model.SearchParams{Arrival: &arrival, MaxTransfers: -1})
	if err != nil {
		t.Fatalf("arrival plan failed: %v", err)
	}
	if j.Arrival.After(arrival) {
		t.Fatalf("arrival %v after requested %v", j.Arrival, arrival)
	}
	if len(j.Legs) == 0 {
		t.Fatal("no legs")
	}
	// arrival should be 09:36 (same as 06:00 departure case) but not after 10:00
	if !j.Arrival.Equal(time.Date(2026, 8, 30, 9, 36, 0, 0, time.UTC)) {
		t.Fatalf("want 09:36, got %v", j.Arrival)
	}
}

func TestPlanPareto(t *testing.T) {
	day := time.Date(2026, 8, 30, 6, 0, 0, 0, time.UTC)
	synth := providers.NewSynth(day)
	net, _ := synth.NetworkForDay(day)
	p := New(telemetry.New())
	from := model.Coords{Lat: 58.0135, Lon: 56.2495}
	to := model.Coords{Lat: 56.84, Lon: 60.607}
	j, err := p.Plan(net, from, to, model.SearchParams{Departure: day, MaxTransfers: -1})
	if err != nil {
		t.Fatalf("plan failed: %v", err)
	}
	if len(j.Alternatives) == 0 {
		t.Fatalf("want >=1 alternative, got 0")
	}
	for _, alt := range j.Alternatives {
		if alt.Arrival.Equal(j.Arrival) && alt.Transfers == j.Transfers {
			t.Fatalf("alternative duplicates best")
		}
	}
}
