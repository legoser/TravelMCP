package planner

import (
	"testing"
	"time"

	"travelmcp/internal/model"
)

func planTrio(t *testing.T, net *model.Network, from, to model.Coords, params model.SearchParams) (*model.Journey, *model.Journey, error, error) {
	t.Helper()
	csa, errCSA := New(nil).Plan(net, from, to, params)
	mc, errMC := NewWithEngine(nil, "mcraptor").Plan(net, from, to, params)
	return csa, mc, errCSA, errMC
}

func assertMcParity(t *testing.T, name string, csa, mc *model.Journey, errCSA, errMC error) {
	t.Helper()
	if (errCSA == nil) != (errMC == nil) {
		t.Fatalf("%s: csa err=%v, mcraptor err=%v (route presence must match)", name, errCSA, errMC)
	}
	if errCSA != nil {
		return
	}
	if !csa.Arrival.Equal(mc.Arrival) {
		t.Fatalf("%s: arrival differs: csa=%v mcraptor=%v", name, csa.Arrival, mc.Arrival)
	}
	if csa.Transfers != mc.Transfers {
		t.Fatalf("%s: transfers differ: csa=%d mcraptor=%d", name, csa.Transfers, mc.Transfers)
	}
}

func TestMcraptorParitySynth(t *testing.T) {
	_, net := newTestPlanner(t)
	perm := model.Coords{Lat: 58.0135, Lon: 56.2495}
	ekb := model.Coords{Lat: 56.84, Lon: 60.607}
	aptFrom := model.Coords{Lat: 57.9, Lon: 56.0}
	aptTo := model.Coords{Lat: 56.0, Lon: 60.8}

	csa, mc, errCSA, errMC := planTrio(t, net, perm, ekb,
		model.SearchParams{Departure: dep("06:00"), MaxTransfers: -1})
	assertMcParity(t, "ground", csa, mc, errCSA, errMC)

	csa, mc, errCSA, errMC = planTrio(t, net, aptFrom, aptTo,
		model.SearchParams{Departure: dep("07:00"), MaxTransfers: -1})
	assertMcParity(t, "flight", csa, mc, errCSA, errMC)

	_, _, errCSA, errMC = planTrio(t, net,
		model.Coords{Lat: 58.0, Lon: 56.3}, model.Coords{Lat: 56.84, Lon: 60.607},
		model.SearchParams{Departure: dep("06:00"), MaxTransfers: -1})
	if errCSA == nil || errMC == nil {
		t.Fatalf("no-route: csa err=%v mcraptor err=%v, both must fail", errCSA, errMC)
	}

	csa, mc, errCSA, errMC = planTrio(t, net, perm, ekb,
		model.SearchParams{Departure: dep("06:00"), MaxTransfers: 0})
	assertMcParity(t, "zero-transfers", csa, mc, errCSA, errMC)
}

func TestMcraptorParityIntercity(t *testing.T) {
	day := time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC)
	net := reestrTestNetwork(t, day)
	var probe *model.Trip
	for _, trip := range net.Trips {
		if len(trip.StopTimes) >= 2 && (probe == nil || trip.ID < probe.ID) {
			probe = trip
		}
	}
	if probe == nil {
		t.Skip("no trips in flat fixture")
	}
	first, last := probe.StopTimes[0], probe.StopTimes[len(probe.StopTimes)-1]
	from, to := net.Stops[first.StopID], net.Stops[last.StopID]
	if from == nil || to == nil {
		t.Skip("probe stops missing")
	}
	csa, mc, errCSA, errMC := planTrio(t, net,
		model.Coords{Lat: from.Lat, Lon: from.Lon},
		model.Coords{Lat: to.Lat, Lon: to.Lon},
		model.SearchParams{Departure: day.Add(6 * time.Hour), MaxTransfers: -1})
	assertMcParity(t, "intercity "+probe.ID, csa, mc, errCSA, errMC)
}

// TestMcraptorTransferLimitChoice — критерий вместо пост-проверки: быстрый
// маршрут с пересадкой и медленный прямой. Нативный фронт видит обоих
// (эвристика сдвигов видела только попутных победителей), поэтому при
// дефолтном preference=transfers лучшим становится прямой 08:00,
// а при preference=arrival — быстрый 06:35. При лимите 0 mcraptor обязан
// выбрать прямой (раннее прибытие среди допустимых), а не упасть.
func TestMcraptorTransferLimitChoice(t *testing.T) {
	day := time.Date(2026, 8, 30, 0, 0, 0, 0, time.UTC)
	mkStop := func(id string, lat, lon float64) *model.Stop {
		return &model.Stop{ID: id, ProviderID: "test", Name: id, Lat: lat, Lon: lon}
	}
	net := model.NewNetwork()
	// Tight cluster (<5km legs bypass the speed filter; the point here is
	// the transfer criterion, not geography).
	net.Stops["A"] = mkStop("A", 55.0, 86.0)
	net.Stops["B"] = mkStop("B", 55.01, 86.01)
	net.Stops["C"] = mkStop("C", 55.03, 86.03)
	mkTrip := func(id, route string, a, b string, depMin, arrMin int) {
		net.Trips[id] = &model.Trip{ID: id, RouteID: route, ProviderID: "test", Mode: model.ModeBus, StopTimes: []model.StopTime{
			{StopID: a, Sequence: 0, ArrivalSec: depMin * 60, DepartureSec: depMin * 60},
			{StopID: b, Sequence: 1, ArrivalSec: arrMin * 60, DepartureSec: arrMin * 60},
		}}
	}
	// Fast with transfer: A->B 06:10-06:20, B->C 06:25-06:35 (arr 06:35).
	mkTrip("fast1", "r1", "A", "B", 370, 380)
	mkTrip("fast2", "r2", "B", "C", 385, 395)
	// Slow direct: A->C 06:05-08:00 (arr 08:00, 0 transfers).
	mkTrip("slow", "r3", "A", "C", 365, 480)
	net.BuildIndexes()
	from := model.Coords{Lat: 55.0, Lon: 86.0}
	to := model.Coords{Lat: 55.03, Lon: 86.03}

	mc, err := NewWithEngine(nil, "mcraptor").Plan(net, from, to,
		model.SearchParams{Departure: day.Add(6 * time.Hour), MaxTransfers: -1})
	if err != nil {
		t.Fatalf("unlimited: %v", err)
	}
	// Default preference=transfers: the slow direct wins as best, the fast
	// one stays visible as the alternative (invisible to the old heuristic).
	if mc.Transfers != 0 {
		t.Fatalf("default-pref best transfers=%d, want 0 (slow direct)", mc.Transfers)
	}
	if got := mc.Arrival.Hour()*60 + mc.Arrival.Minute(); got != 480 {
		t.Fatalf("default-pref arrival=%v, want 08:00", mc.Arrival)
	}
	if len(mc.Alternatives) != 1 || mc.Alternatives[0].Transfers != 1 {
		t.Fatalf("want exactly the fast alternative (transfers=1), got %+v", mc.Alternatives)
	}

	mc, err = NewWithEngine(nil, "mcraptor").Plan(net, from, to,
		model.SearchParams{Departure: day.Add(6 * time.Hour), MaxTransfers: -1, Preference: model.PreferenceArrival})
	if err != nil {
		t.Fatalf("arrival-pref: %v", err)
	}
	if got := mc.Arrival.Hour()*60 + mc.Arrival.Minute(); got != 395 {
		t.Fatalf("arrival-pref arrival=%v, want 06:35 (fast with transfer)", mc.Arrival)
	}

	arrival := day.Add(8 * time.Hour)
	mc, err = NewWithEngine(nil, "mcraptor").Plan(net, from, to,
		model.SearchParams{Arrival: &arrival, MaxTransfers: -1, Preference: model.PreferenceArrival})
	if err != nil {
		t.Fatalf("arrival query: %v", err)
	}
	if got := mc.Arrival.Hour()*60 + mc.Arrival.Minute(); got != 395 {
		t.Fatalf("arrival query arrival=%v, want 06:35", mc.Arrival)
	}

	mc, err = NewWithEngine(nil, "mcraptor").Plan(net, from, to,
		model.SearchParams{Departure: day.Add(6 * time.Hour), MaxTransfers: 0})
	if err != nil {
		t.Fatalf("zero-transfers must pick the slow direct trip: %v", err)
	}
	if mc.Transfers != 0 {
		t.Fatalf("transfers=%d, want 0", mc.Transfers)
	}
	if got := mc.Arrival.Hour()*60 + mc.Arrival.Minute(); got != 480 {
		t.Fatalf("arrival=%v, want 08:00", mc.Arrival)
	}
}
