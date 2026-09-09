package planner

import (
	"path/filepath"
	"strings"
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

func TestPlanProvisionalStopNoTransfer(t *testing.T) {
	p, net := newTestPlanner(t)

	for _, trip := range net.Trips {
		for i := range trip.StopTimes {
			if trip.StopTimes[i].StopID == "c2" {
				trip.StopTimes[i].IsProvisional = true
			}
		}
	}
	net.BuildIndexes()
	if !net.ProvisionalStops["c2"] {
		t.Fatal("c2 must be indexed as provisional")
	}

	journey, err := p.Plan(net,
		model.Coords{Lat: 58.003, Lon: 56.285},
		model.Coords{Lat: 58.010, Lon: 56.295},
		model.SearchParams{Departure: dep("06:00"), MaxTransfers: -1},
	)
	if err == nil {
		for _, l := range journey.Legs {
			if l.Mode == model.ModeWalk && l.From.StopID == "c2" && l.To.StopID == "c2x" {
				t.Fatal("provisional stop must not allow footpath transfer c2 -> c2x")
			}
		}
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
	net, err := providers.NewIntercity(filepath.Join("..", "..", "testdata", "test.json"), day).NetworkForDay(day)
	if err != nil {
		t.Fatalf("network: %v", err)
	}

	origin := map[string]bool{}
	for _, trip := range net.Trips {
		if len(trip.StopTimes) > 0 {
			origin[trip.StopTimes[0].StopID] = true
		}
	}

	nsk := model.Coords{Lat: 55.0410573, Lon: 83.0273816}
	st, ok := findStopByPlace(net.Stops, "Новосибирск", nsk, 30, origin, nil)
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
	net, err := providers.NewIntercity(filepath.Join("..", "..", "testdata", "test.json"), day).NetworkForDay(day)
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

func TestFindStopByPlaceDisambiguatesOmskFromTomsk(t *testing.T) {
	// "Омск" не должен матчить "Томск" ни по подстроке, ни по токену;
	// стоп обязан быть в пешей доступности от геокоординат места.
	omskCity := model.Coords{Lat: 54.998, Lon: 73.281}    // реальный центр Омска
	tomskCity := model.Coords{Lat: 56.4613, Lon: 84.9914} // реальный центр Томска
	stops := map[string]*model.Stop{
		"omsk":  {ID: "omsk", Name: "Омск", Lat: 54.998, Lon: 73.281, Type: model.StopTypeHub},
		"tomsk": {ID: "tomsk", Name: "Томск", Lat: 56.4613, Lon: 84.9914, Type: model.StopTypeHub},
	}

	st, ok := findStopByPlace(stops, "Омск", omskCity, 30, nil, nil)
	if !ok {
		t.Fatal("findStopByPlace failed for Омск")
	}
	if st.ID != "omsk" {
		t.Fatalf("expected stop 'omsk' for query 'Омск', got '%s' (%q)", st.ID, st.Name)
	}

	st, ok = findStopByPlace(stops, "Томск", tomskCity, 30, nil, nil)
	if !ok {
		t.Fatal("findStopByPlace failed for Томск")
	}
	if st.ID != "tomsk" {
		t.Fatalf("expected stop 'tomsk' for query 'Томск', got '%s' (%q)", st.ID, st.Name)
	}
}

func TestFindStopByPlaceSubstringFallbackForCompoundNames(t *testing.T) {
	// "Омск" матчит "Автовокзал Омск" по подстроке (нет токен-совпадения),
	// при условии что стоп в пешой доступности от геокоординат.
	stops := map[string]*model.Stop{
		"omsk-av": {ID: "omsk-av", Name: "Автовокзал Омск", Lat: 54.998, Lon: 73.281, Type: model.StopTypeHub},
	}

	omskCity := model.Coords{Lat: 54.998, Lon: 73.281}
	st, ok := findStopByPlace(stops, "Омск", omskCity, 30, nil, nil)
	if !ok {
		t.Fatal("findStopByPlace failed for Омск")
	}
	if st.ID != "omsk-av" {
		t.Fatalf("expected 'omsk-av' for query 'Омск', got '%s' (%q)", st.ID, st.Name)
	}
}

func TestFindStopByPlaceRejectsFarSubstringMatch(t *testing.T) {
	// "Омск" coords are (54.998, 73.281). "Автовокзал Томск" coords are (56.461, 84.991) — ~1300km away.
	// Substring match "омск" in "автовокзал томск" should be rejected by coordinate check.
	stops := map[string]*model.Stop{
		"tomsk-av": {ID: "tomsk-av", Name: "Автовокзал Томск", Lat: 56.4613, Lon: 84.9914, Type: model.StopTypeHub},
	}
	omskCoords := model.Coords{Lat: 54.998, Lon: 73.281}
	_, ok := findStopByPlace(stops, "Омск", omskCoords, 130, nil, nil)
	if ok {
		t.Fatal("findStopByPlace should NOT match 'Автовокзал Томск' for query 'Омск' when coords are 1300km apart")
	}
}

func TestFindStopByPlaceRejectsFarTokenMatch(t *testing.T) {
	// Токен-совпадение тоже ограничено пешей доступностью: терминал
	// «Красноярск» (стоит на координатах аэропорта, 0 рейсов) не должен
	// матчиться на запрос центра Красноярска в 37 км.
	stops := map[string]*model.Stop{
		"krk-dead": {ID: "krk-dead", Name: "Красноярск", Lat: 56.1806, Lon: 92.4864, Type: model.StopTypeHub},
	}
	krasnoyarskCenter := model.Coords{Lat: 56.033, Lon: 92.906}
	_, ok := findStopByPlace(stops, "Красноярск", krasnoyarskCenter, 130, nil, nil)
	if ok {
		t.Fatal("findStopByPlace should NOT match far-away stop even with token match")
	}
}

func TestPlanGapLegBoundedByMaxWalk(t *testing.T) {
	// Gap-переможка ограничена maxWalk: маршрут «5 дней пешком» не строится,
	// вместо него — явная ошибка «не найден».
	day := time.Date(2026, 8, 30, 0, 0, 0, 0, time.UTC)
	p := New(nil)
	net := model.NewNetwork()
	net.Stops["x-bus"] = &model.Stop{ID: "x-bus", Name: "X, Автовокзал", Lat: 55.0, Lon: 83.0, Type: model.StopTypeHub}
	net.Stops["y-bus"] = &model.Stop{ID: "y-bus", Name: "Y, Автовокзал", Lat: 56.5, Lon: 89.0, Type: model.StopTypeHub}
	net.BuildIndexes()

	_, err := p.Plan(net,
		model.Coords{Lat: 55.0, Lon: 83.0},
		model.Coords{Lat: 56.5, Lon: 89.0},
		model.SearchParams{Departure: day.Add(10 * time.Hour), MaxTransfers: -1, AllowGap: true},
	)
	if err == nil {
		t.Fatal("expected error for unreachable destination with huge gap, got route")
	}
	if !strings.Contains(err.Error(), "не найден") && !strings.Contains(err.Error(), "нет остановок") {
		t.Fatalf("expected bounded-gap error, got: %v", err)
	}
}

func TestPlanPlaceNotFoundReturnsNoRoute(t *testing.T) {
	// Регрессия на панику: если место не резолвится в стоп (нет покрытия),
	// PlanWithPlaces должен вернуть ошибку «нет остановок», а не падать
	// nil-pointer разыменованием в debug-логе (planner.go:220).
	day := time.Date(2026, 8, 30, 0, 0, 0, 0, time.UTC)
	p := New(nil)
	net, err := providers.NewSynth(day).Network()
	if err != nil {
		t.Fatalf("synth network: %v", err)
	}

	_, err = p.PlanWithPlaces(net,
		model.Coords{Lat: 55.7132, Lon: 84.9023},
		model.Coords{Lat: 54.9986, Lon: 73.2812},
		model.SearchParams{Departure: day.Add(10 * time.Hour), MaxTransfers: -1, AllowGap: true},
		&PlaceHint{Name: "Юрга"},
		&PlaceHint{Name: "Омск"},
	)
	if err == nil {
		t.Fatal("expected error for destination without stop coverage, got nil")
	}
	if !strings.Contains(err.Error(), "нет остановок") {
		t.Fatalf("expected 'нет остановок' error, got: %v", err)
	}
}
