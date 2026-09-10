package sync

import (
	"testing"

	"travelmcp/internal/model"
)

func TestResolveStopCoordsFillsFromSkeleton(t *testing.T) {
	la1, lo1 := 55.3416, 86.061
	la2, lo2 := 56.4612, 84.9913
	terms := []AttachTerminal{
		{ID: 1, Name: "Кемерово, автовокзал", Lat: &la1, Lon: &lo1, Settlement: "Кемерово", Transport: "bus", Source: "yandex"},
		{ID: 2, Name: "Томск, автовокзал", Lat: &la2, Lon: &lo2, Settlement: "Томск", Transport: "bus", Source: "yandex"},
	}
	trips := []model.FlatTrip{{
		RouteReg:  "42.70.032",
		Direction: "forward",
		Stops: []model.FlatStop{
			{StopID: "op:42:42005", Name: "Кемеровский АВ", Region: "42"},
			{StopID: "op:70:70016", Name: "Автовокзал АО «Томскавтотранс»", Region: "70"},
		},
	}}
	filled, missing := ResolveStopCoords(trips, terms, testSource)
	if missing != 2 || filled == 0 {
		t.Fatalf("оба стопа без координат: missing=%d filled=%d", missing, filled)
	}
	if trips[0].Stops[0].Lat == nil {
		t.Fatal("Кемеровский АВ обязан получить координаты через прилагательную форму")
	}
	if *trips[0].Stops[0].Lat != la1 {
		t.Fatalf("координаты должны прийти от терминала Кемерово-автовокзал: got %v", *trips[0].Stops[0].Lat)
	}
}

func TestResolveStopCoordsDisambiguatesCityTwins(t *testing.T) {
	laAv, loAv := 55.3416, 86.061
	laRail, loRail := 55.3441, 86.0636
	laAir, loAir := 55.2806, 86.1178
	terms := []AttachTerminal{
		{ID: 1, Name: "Кемерово, ж/д вокзал", Lat: &laRail, Lon: &loRail, Settlement: "Кемерово", Transport: "bus"},
		{ID: 2, Name: "Кемерово, автовокзал", Lat: &laAv, Lon: &loAv, Settlement: "Кемерово", Transport: "bus"},
		{ID: 3, Name: "Кемерово, аэропорт", Lat: &laAir, Lon: &loAir, Settlement: "Кемерово", Transport: "bus"},
		{ID: 4, Name: "Кемерово", Lat: &laRail, Lon: &loRail, Settlement: "Кемерово", Transport: "rail"},
	}
	trips := []model.FlatTrip{{
		Stops: []model.FlatStop{
			{StopID: "op:42:42005", Name: "Кемеровский АВ", Region: "42"},
		},
	}}
	filled, _ := ResolveStopCoords(trips, terms, testSource)
	if filled != 1 || trips[0].Stops[0].Lat == nil {
		t.Fatal("Кемеровский АВ должен резолвиться при тёзках города")
	}
	if *trips[0].Stops[0].Lat != laAv {
		t.Fatalf("объектный сигнал обязан выбрать автовокзал, а не ж/д вокзал/аэропорт: got lat %v, want %v", *trips[0].Stops[0].Lat, laAv)
	}
}

func TestResolveStopCoordsNoGuess(t *testing.T) {
	la, lo := 55.0, 82.0
	terms := []AttachTerminal{
		{Name: "Новосибирск, Речной вокзал", Lat: &la, Lon: &lo, Settlement: "Новосибирск"},
	}
	trips := []model.FlatTrip{{
		Stops: []model.FlatStop{
			{StopID: "x", Name: "ОП с. Таштагол", Region: "42"},
		},
	}}
	filled, missing := ResolveStopCoords(trips, terms, testSource)
	if filled != 0 || missing != 1 {
		t.Fatalf("неуверенный матч не должен давать координаты: filled=%d missing=%d", filled, missing)
	}
	if trips[0].Stops[0].Lat != nil {
		t.Fatal("стоп без уверенного матча остаётся без координат")
	}
}
