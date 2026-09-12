package planner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"travelmcp/internal/model"
	"travelmcp/internal/providers"
)

// reestrTestNetwork строит сеть из flat-фикстуры (testdata/flat_trips.json,
// генерируется tools/registry-parser из среза реестра) — замена вырезанного
// legacy-провайдера intercity (Фаза 6): тестам нужна сеть с автовокзалами и
// межгородом, а не сам коннектор реестра.
func reestrTestNetwork(t *testing.T, day time.Time) *model.Network {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "flat_trips.json"))
	if err != nil {
		t.Fatalf("read flat_trips fixture: %v", err)
	}
	var payload model.FlatPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("parse flat_trips fixture: %v", err)
	}
	source := payload.Meta.Source
	net := model.NewNetwork()
	for _, ft := range payload.Trips {
		for _, s := range ft.Stops {
			if _, ok := net.Stops[s.StopID]; ok {
				continue
			}
			lat, lon := 0.0, 0.0
			if s.Lat != nil {
				lat = *s.Lat
			}
			if s.Lon != nil {
				lon = *s.Lon
			}
			net.Stops[s.StopID] = &model.Stop{ID: s.StopID, ProviderID: source, Name: s.Name, Lat: lat, Lon: lon, Type: model.InferStopType(s.Name)}
		}
		if ft.FrequencyOnly {
			continue
		}
		routeID := ft.RouteReg
		if _, ok := net.Routes[routeID]; !ok {
			net.Routes[routeID] = &model.Route{ID: routeID, ProviderID: source, ShortName: routeID, Mode: model.ModeBus}
		}
		tripID := routeID + "-" + ft.Direction + "-" + itoa(ft.Run)
		var times []model.StopTime
		for _, st := range ft.Stops {
			if st.DepMin == nil && st.ArrMin == nil {
				continue
			}
			arr, dep := 0, 0
			if st.ArrMin != nil {
				arr = *st.ArrMin * 60
			}
			if st.DepMin != nil {
				dep = *st.DepMin * 60
			}
			if arr == 0 && dep == 0 {
				continue
			}
			if arr == 0 {
				arr = dep
			}
			if dep == 0 {
				dep = arr
			}
			times = append(times, model.StopTime{StopID: st.StopID, Sequence: len(times), ArrivalSec: arr, DepartureSec: dep})
		}
		if len(times) < 2 {
			continue
		}
		net.Trips[tripID] = &model.Trip{ID: tripID, RouteID: routeID, ProviderID: source, Mode: model.ModeBus, StopTimes: times}
		for i := 0; i < len(times)-1; i++ {
			net.Connections = append(net.Connections, model.Connection{
				TripID: tripID, ProviderID: source, RouteID: routeID, Mode: model.ModeBus,
				From: times[i].StopID, To: times[i+1].StopID,
				Departure: day.Add(time.Duration(times[i].DepartureSec) * time.Second),
				Arrival:   day.Add(time.Duration(times[i+1].ArrivalSec) * time.Second),
			})
		}
	}
	net.BuildIndexes()
	return net
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}

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
	net := reestrTestNetwork(t, day)

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
	net := reestrTestNetwork(t, day)

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

// TestBuildLegsTransitStops проверяет, что для посадки на промежуточной
// остановке транзитного рейса Leg получает Stops (все остановки пассажира:
// от посадки до высадки включательно) и IsTransit=true.
// Пример: рейс Томск—Кемерово через Юргу; пассажир едет Юрга→Кемерово.
func TestBuildLegsTransitStops(t *testing.T) {
	net := model.NewNetwork()
	for _, s := range []model.Stop{
		{ID: "t", Name: "Томск", Lat: 56.0, Lon: 97.0},
		{ID: "y", Name: "Юрга", Lat: 56.1, Lon: 97.1},
		{ID: "k", Name: "Кемерово", Lat: 55.3, Lon: 87.4},
	} {
		net.Stops[s.ID] = &s
	}
	tripID := "route1-tomsk-kemerovo"
	net.Trips[tripID] = &model.Trip{
		ID: tripID, RouteID: "route1", ProviderID: "p", Mode: model.ModeBus,
		StopTimes: []model.StopTime{
			{StopID: "t", ArrivalSec: 0, DepartureSec: 0},
			{StopID: "y", ArrivalSec: 3600, DepartureSec: 3600},
			{StopID: "k", ArrivalSec: 10800, DepartureSec: 10800},
		},
	}
	day := time.Date(2026, 8, 30, 0, 0, 0, 0, time.UTC)
	net.Connections = []model.Connection{{
		TripID: tripID, ProviderID: "p", RouteID: "route1", Mode: model.ModeBus,
		From: "y", To: "k",
		Departure: day.Add(3600 * time.Second),
		Arrival:   day.Add(10800 * time.Second),
	}}
	net.BuildIndexes()
	steps := []step{{conn: &net.Connections[0]}}

	legs := buildLegs(net, map[string]time.Time{}, steps)
	if len(legs) != 1 {
		t.Fatalf("want 1 leg, got %d", len(legs))
	}
	leg := legs[0]
	if !leg.IsTransit {
		t.Error("IsTransit should be true: посадка и высадка на промежуточных остановках")
	}
	if len(leg.Stops) != 2 {
		t.Fatalf("want 2 stops (юрга→кемерово) in Stops, got %d", len(leg.Stops))
	}
	if leg.Stops[0].StopID != "y" {
		t.Errorf("Stops[0]=%q want %q", leg.Stops[0].StopID, "y")
	}
	if leg.Stops[1].StopID != "k" {
		t.Errorf("Stops[1]=%q want %q", leg.Stops[1].StopID, "k")
	}
}

// TestBuildLegsNonTransitStops проверяет рейс от конечной посадки — Stops
// заполняются (все остановки пассажира), но IsTransit=false.
func TestBuildLegsNonTransitStops(t *testing.T) {
	net := model.NewNetwork()
	for _, s := range []model.Stop{
		{ID: "t", Name: "Томск", Lat: 56.0, Lon: 97.0},
		{ID: "k", Name: "Кемерово", Lat: 55.3, Lon: 87.4},
	} {
		net.Stops[s.ID] = &s
	}
	tripID := "route1-tomsk-kemerovo"
	net.Trips[tripID] = &model.Trip{
		ID: tripID, RouteID: "route1", ProviderID: "p", Mode: model.ModeBus,
		StopTimes: []model.StopTime{
			{StopID: "t", ArrivalSec: 0, DepartureSec: 0},
			{StopID: "k", ArrivalSec: 10800, DepartureSec: 10800},
		},
	}
	day := time.Date(2026, 8, 30, 0, 0, 0, 0, time.UTC)
	net.Connections = []model.Connection{{
		TripID: tripID, ProviderID: "p", RouteID: "route1", Mode: model.ModeBus,
		From: "t", To: "k",
		Departure: day,
		Arrival:   day.Add(10800 * time.Second),
	}}
	net.BuildIndexes()
	steps := []step{{conn: &net.Connections[0]}}

	legs := buildLegs(net, map[string]time.Time{}, steps)
	if len(legs) != 1 {
		t.Fatalf("want 1 leg, got %d", len(legs))
	}
	leg := legs[0]
	if leg.IsTransit {
		t.Error("IsTransit should be false: посадка с конечной остановки")
	}
	if len(leg.Stops) != 2 {
		t.Fatalf("want 2 stops in Stops, got %d", len(leg.Stops))
	}
}
