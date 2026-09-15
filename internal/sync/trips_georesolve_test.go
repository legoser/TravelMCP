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

// Безымянный стоп («Варюхино»: поселение не извлеклось) не должен
// матчиться на глобальном пуле тёзок чужих регионов: Levenshtein-порог
// 0.6 ловит «Бардино»/«Шпагино» из других областей (баг зигзаг-маршрутов
// Кемерово—Томск). Только полные тёзки по Core.
func TestResolveStopCoordsNoGlobalPoolForNamelessStop(t *testing.T) {
	laBard, loBard := 53.95, 84.94 // «Бардино», Алтайский край
	laVar, loVar := 55.9, 85.6     // настоящее «Варюхино» на трассе
	terms := []AttachTerminal{
		{ID: 1, Name: "Бардино", Lat: &laBard, Lon: &loBard, Settlement: ""},
		{ID: 2, Name: "Варюхино", Lat: &laVar, Lon: &loVar, Settlement: ""},
	}
	trips := []model.FlatTrip{{
		Stops: []model.FlatStop{
			{StopID: "s9802572", Name: "Варюхино", Region: "42"},
		},
	}}
	filled, missing := ResolveStopCoords(trips, terms, testSource)
	if missing != 1 || filled != 1 {
		t.Fatalf("полный тёзка обязан закрыть стоп: filled=%d missing=%d", filled, missing)
	}
	if trips[0].Stops[0].Lat == nil || *trips[0].Stops[0].Lat != laVar {
		t.Fatal("координаты должны прийти от полного тёзки Варюхино, не от Бардино")
	}
	if !trips[0].Stops[0].CoordsBorrowed {
		t.Fatal("заимствованная геометрия обязана помечаться CoordsBorrowed")
	}
	// теперь без полного тёзки в пуле: короткий топоним не должен
	// ловиться Levenshtein-сходством на чужом регионе
	termsNoTwin := []AttachTerminal{
		{ID: 1, Name: "Бардино", Lat: &laBard, Lon: &loBard, Settlement: ""},
		{ID: 2, Name: "Шпагино", Lat: &laBard, Lon: &loBard, Settlement: ""},
	}
	trips[0].Stops[0] = model.FlatStop{StopID: "s9802572", Name: "Варюхино", Region: "42"}
	filled, missing = ResolveStopCoords(trips, termsNoTwin, testSource)
	if filled != 0 || missing != 1 {
		t.Fatalf("тёзка чужого региона не должен получать координаты: filled=%d missing=%d", filled, missing)
	}
	if trips[0].Stops[0].Lat != nil {
		t.Fatal("стоп без полного тёзки остаётся без координат (skeleton_gap, не зигзаг)")
	}
}

// Два полных тёзки без поселения — неоднозначность, координаты не выдаются.
func TestResolveStopCoordsAmbiguousTwinsNoCoords(t *testing.T) {
	la1, lo1 := 55.0, 85.0
	la2, lo2 := 56.0, 86.0
	terms := []AttachTerminal{
		{ID: 1, Name: "Октябрьский", Lat: &la1, Lon: &lo1, Settlement: ""},
		{ID: 2, Name: "Октябрьский", Lat: &la2, Lon: &lo2, Settlement: ""},
	}
	trips := []model.FlatTrip{{
		Stops: []model.FlatStop{
			{StopID: "s9802639", Name: "Октябрьский", Region: "42"},
		},
	}}
	filled, _ := ResolveStopCoords(trips, terms, testSource)
	if filled != 0 || trips[0].Stops[0].Lat != nil {
		t.Fatal("два одноимённых терминала без поселения — неоднозначность, без координат")
	}
}
