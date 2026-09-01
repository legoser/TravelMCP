package planner

import (
	"path/filepath"
	"testing"
	"time"

	"travelmcp/internal/model"
	"travelmcp/internal/providers"
)

func newTestPlanner(t *testing.T) (*Planner, *model.Network) {
	t.Helper()
	day := time.Date(2026, 8, 30, 0, 0, 0, 0, time.UTC)
	net, err := providers.NewSynth(day).Network()
	if err != nil {
		t.Fatalf("synth network: %v", err)
	}
	return New(nil), net
}

func dep(s string) time.Time {
	t, err := time.Parse("15:04", s)
	if err != nil {
		panic(err)
	}
	return time.Date(2026, 8, 30, t.Hour(), t.Minute(), 0, 0, time.UTC)
}

func TestPlanCrossCity(t *testing.T) {
	p, net := newTestPlanner(t)

	journey, err := p.Plan(net,
		model.Coords{Lat: 58.0135, Lon: 56.2495},
		model.Coords{Lat: 56.84, Lon: 60.607},
		model.SearchParams{Departure: dep("06:00"), MaxTransfers: -1},
	)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	var transit int
	for _, l := range journey.Legs {
		if l.Mode != model.ModeWalk {
			transit++
		}
	}
	if transit != 2 {
		t.Fatalf("want 2 transit legs (bus, intercity) + footpath to market, got %d", transit)
	}
	if journey.Transfers != 2 {
		t.Fatalf("want 2 transfers (bus->intercity, intercity->footpath to market), got %d", journey.Transfers)
	}
	var footToMarket bool
	for _, l := range journey.Legs {
		if l.Mode == model.ModeWalk && l.From.StopID == "b-bus" && l.To.StopID == "b-mkt" {
			footToMarket = true
		}
	}
	if !footToMarket {
		t.Fatal("expected walk leg b-bus -> b-mkt (6 min beats the 12 min tram)")
	}
	if !journey.Arrival.After(journey.Departure) {
		t.Fatal("arrival must be after departure")
	}
	for i := 1; i < len(journey.Legs); i++ {
		if journey.Legs[i].Departure.Before(journey.Legs[i-1].Arrival) {
			t.Fatalf("leg %d departs before previous leg arrives", i)
		}
	}
}

func TestPlanFootpathTransfer(t *testing.T) {
	p, net := newTestPlanner(t)

	journey, err := p.Plan(net,
		model.Coords{Lat: 58.003, Lon: 56.285},
		model.Coords{Lat: 58.010, Lon: 56.295},
		model.SearchParams{Departure: dep("06:00"), MaxTransfers: -1},
	)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	var walkBetween bool
	for i := 1; i < len(journey.Legs); i++ {
		l := journey.Legs[i]
		if l.Mode == model.ModeWalk && l.From.StopID == "c2" && l.To.StopID == "c2x" {
			walkBetween = true
		}
		if l.Departure.Before(journey.Legs[i-1].Arrival) {
			t.Fatalf("leg %d departs before previous leg arrives", i)
		}
	}
	if !walkBetween {
		t.Fatal("expected footpath transfer leg c2 -> c2x")
	}
}

func TestPlanMaxTransfers(t *testing.T) {
	p, net := newTestPlanner(t)

	_, err := p.Plan(net,
		model.Coords{Lat: 58.0135, Lon: 56.2495},
		model.Coords{Lat: 56.84, Lon: 60.607},
		model.SearchParams{Departure: dep("06:00"), MaxTransfers: 0},
	)
	if err == nil {
		t.Fatal("expected error when transfers are forbidden, route requires 1")
	}
}

func TestPlanFlight(t *testing.T) {
	p, net := newTestPlanner(t)

	journey, err := p.Plan(net,
		model.Coords{Lat: 57.9148, Lon: 56.0217},
		model.Coords{Lat: 56.7431, Lon: 60.8028},
		model.SearchParams{Departure: dep("07:00"), MaxTransfers: -1},
	)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	var transit int
	for _, l := range journey.Legs {
		if l.Mode != model.ModeWalk {
			transit++
		}
		if l.Mode == model.ModeFlight {
			if l.From.StopID != "a-apt" || l.To.StopID != "b-apt" {
				t.Fatalf("flight leg wrong endpoints: %s -> %s", l.From.StopID, l.To.StopID)
			}
		}
	}
	if transit != 1 {
		t.Fatalf("want a single flight leg, got %d transit legs", transit)
	}
	if journey.Transfers != 0 {
		t.Fatalf("want 0 transfers, got %d", journey.Transfers)
	}
}

func TestPlanNoRoute(t *testing.T) {
	p, net := newTestPlanner(t)

	_, err := p.Plan(net,
		model.Coords{Lat: 58.003, Lon: 56.285},
		model.Coords{Lat: 56.84, Lon: 60.607},
		model.SearchParams{Departure: dep("06:00"), MaxTransfers: -1},
	)
	if err == nil {
		t.Fatal("expected error: c-cluster is not connected to the A/B cluster")
	}
}

func TestFindStopByPlacePrefersAutoStation(t *testing.T) {
	day := time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC)
	net, err := providers.NewIntercity(filepath.Join("..", "..", "testdata", "reestr", "mini.json"), day).NetworkForDay(day)
	if err != nil {
		t.Fatalf("network: %v", err)
	}

	origin := map[string]bool{}
	for _, trip := range net.Trips {
		if len(trip.StopTimes) > 0 {
			origin[trip.StopTimes[0].StopID] = true
		}
	}

	// В mini.json автовокзал и вокзал ЖД Новосибирска лежат в одной точке
	// (55.0410573, 83.0273816): op:54:54099 «АВ «Новосибирский автовокзал-Главный»
	// и op:54:54098 «ОП «Вокзал «Новосибирск-Главный». По месту «Новосибирск»
	// должен выбираться автовокзал, а не вокзал ЖД.
	nsk := model.Coords{Lat: 55.0410573, Lon: 83.0273816}
	st, ok := findStopByPlace(net.Stops, "Новосибирск", nsk, 30, origin)
	if !ok {
		t.Fatal("findStopByPlace failed for Новосибирск")
	}
	if st.ID != "op:54:54099" {
		t.Fatalf("selected stop = %s (%q), want автовокзал op:54:54099", st.ID, st.Name)
	}
}

func TestPlaceJourneyStartsAtAutoStation(t *testing.T) {
	day := time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC)
	p := New(nil)
	net, err := providers.NewIntercity(filepath.Join("..", "..", "testdata", "reestr", "mini.json"), day).NetworkForDay(day)
	if err != nil {
		t.Fatalf("network: %v", err)
	}

	journey, err := p.PlanWithPlaces(net,
		model.Coords{Lat: 55.0410573, Lon: 83.0273816},
		model.Coords{Lat: 56.4613482, Lon: 84.9914307},
		model.SearchParams{Departure: day.Add(8 * time.Hour), MaxTransfers: -1, Preference: model.PreferenceArrival},
		&PlaceHint{Name: "Новосибирск"},
		&PlaceHint{Name: "Томск"},
	)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	// Стартовая точка маршрута (после пешего подхода) должна быть автовокзалом
	// Новосибирска, а не вокзалом ЖД.
	if len(journey.Legs) == 0 {
		t.Fatal("no legs")
	}
	if first := journey.Legs[0]; first.To.StopID != "op:54:54099" {
		t.Fatalf("first leg To = %s (%q), want автовокзал op:54:54099", first.To.StopID, first.To.Name)
	}
}
