package planner

import (
	"testing"
	"time"

	"travelmcp/internal/model"
)

func planBoth(t *testing.T, net *model.Network, from, to model.Coords, params model.SearchParams) (*model.Journey, *model.Journey, error, error) {
	t.Helper()
	csa, errCSA := New(nil).Plan(net, from, to, params)
	raptor, errRaptor := NewWithEngine(nil, "raptor").Plan(net, from, to, params)
	return csa, raptor, errCSA, errRaptor
}

func assertParity(t *testing.T, name string, csa, raptor *model.Journey, errCSA, errRaptor error) {
	t.Helper()
	if (errCSA == nil) != (errRaptor == nil) {
		t.Fatalf("%s: csa err=%v, raptor err=%v (наличие маршрута обязано совпадать)", name, errCSA, errRaptor)
	}
	if errCSA != nil {
		return
	}
	if !csa.Arrival.Equal(raptor.Arrival) {
		t.Fatalf("%s: arrival differs: csa=%v raptor=%v", name, csa.Arrival, raptor.Arrival)
	}
	if csa.Transfers != raptor.Transfers {
		t.Fatalf("%s: transfers differ: csa=%d raptor=%d", name, csa.Transfers, raptor.Transfers)
	}
}

func TestRaptorParitySynth(t *testing.T) {
	_, net := newTestPlanner(t)
	perm := model.Coords{Lat: 58.0135, Lon: 56.2495}
	ekb := model.Coords{Lat: 56.84, Lon: 60.607}
	aptFrom := model.Coords{Lat: 57.9, Lon: 56.0}
	aptTo := model.Coords{Lat: 56.0, Lon: 60.8}

	csa, raptor, errCSA, errRaptor := planBoth(t, net, perm, ekb,
		model.SearchParams{Departure: dep("06:00"), MaxTransfers: -1})
	assertParity(t, "ground", csa, raptor, errCSA, errRaptor)

	csa, raptor, errCSA, errRaptor = planBoth(t, net, aptFrom, aptTo,
		model.SearchParams{Departure: dep("07:00"), MaxTransfers: -1})
	assertParity(t, "flight", csa, raptor, errCSA, errRaptor)

	_, _, errCSA, errRaptor = planBoth(t, net,
		model.Coords{Lat: 58.0, Lon: 56.3}, model.Coords{Lat: 56.84, Lon: 60.607},
		model.SearchParams{Departure: dep("06:00"), MaxTransfers: -1})
	if errCSA == nil || errRaptor == nil {
		t.Fatalf("no-route: csa err=%v raptor err=%v, оба обязаны вернуть ошибку", errCSA, errRaptor)
	}

	csa, raptor, errCSA, errRaptor = planBoth(t, net, perm, ekb,
		model.SearchParams{Departure: dep("06:00"), MaxTransfers: 0})
	assertParity(t, "zero-transfers", csa, raptor, errCSA, errRaptor)
}

func TestRaptorParityIntercity(t *testing.T) {
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
	csa, raptor, errCSA, errRaptor := planBoth(t, net,
		model.Coords{Lat: from.Lat, Lon: from.Lon},
		model.Coords{Lat: to.Lat, Lon: to.Lon},
		model.SearchParams{Departure: day.Add(6 * time.Hour), MaxTransfers: -1})
	assertParity(t, "intercity "+probe.ID, csa, raptor, errCSA, errRaptor)
}
