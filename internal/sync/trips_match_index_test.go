package sync

import (
	"log/slog"
	"testing"

	"travelmcp/internal/model"
)

func TestMatchIndexBlocking(t *testing.T) {
	// D-1: пул кандидатов = гео-окно ∪ код-совпадение ∪ no-coords.
	// Терминал за 79км без кода — НЕ кандидат; терминал за 79км с кодом —
	// кандидат (golden-кейс «тёзка верифицируется по коду»).
	farLat, farLon := 56.0, 87.5 // ~79км от базовой точки
	nearLat, nearLon := 55.0, 86.0
	terms := []AttachTerminal{
		{ID: 1, Name: "Далёкий терминал", Lat: &farLat, Lon: &farLon, Settlement: "даль", Source: "osm", GeomFinalized: true},
		{ID: 2, Name: "Далёкий с кодом", Lat: &farLat, Lon: &farLon, Settlement: "даль", Source: "osm", GeomFinalized: true,
			Codes: []model.AdaptedIdentifier{{System: testSource, CodeType: "op_reg", Code: "op123"}}},
		{ID: 3, Name: "Ближний терминал", Lat: &nearLat, Lon: &nearLon, Settlement: "близко", Source: "osm", GeomFinalized: true},
		{ID: 4, Name: "Терминал без координат", Settlement: "где-то", Source: "osm", GeomFinalized: false},
	}
	idx := buildMatchIndex(terms)

	stopNear := model.FlatStop{StopID: "s1", Name: "Ближний терминал", Region: "42", Lat: &nearLat, Lon: &nearLon}
	got := map[int64]bool{}
	for _, i := range idx.candidates(stopNear) {
		got[terms[i].ID] = true
	}
	if !got[3] {
		t.Fatal("ближний терминал обязан быть кандидатом (гео-окно)")
	}
	if !got[4] {
		t.Fatal("бескординатный терминал обязан быть в каждом пуле")
	}
	if got[1] {
		t.Fatal("терминал за 79км без кода не кандидат (blocking)")
	}

	stopCoded := model.FlatStop{StopID: "s2", Name: "Совсем другое имя", Region: "42", Lat: &nearLat, Lon: &nearLon,
		Codes: []model.AdaptedIdentifier{{System: testSource, CodeType: "op_reg", Code: "op123"}}}
	gotCoded := map[int64]bool{}
	for _, i := range idx.candidates(stopCoded) {
		gotCoded[terms[i].ID] = true
	}
	if !gotCoded[2] {
		t.Fatal("код-совпадение обязано проходить гео-окно (тёзка за 79км верифицируется по коду)")
	}

	// стоп без координат: пул = код-совпадения ∪ no-coords (не вся страна)
	stopNoCoords := model.FlatStop{StopID: "s3", Name: "Терминал без координат", Region: "42"}
	gotNC := map[int64]bool{}
	for _, i := range idx.candidates(stopNoCoords) {
		gotNC[terms[i].ID] = true
	}
	if gotNC[1] || gotNC[2] || gotNC[3] {
		t.Fatalf("стоп без координат и кода: только no-coords терминалы, получено %v", gotNC)
	}
	if !gotNC[4] {
		t.Fatal("no-coords терминал обязан быть в пуле")
	}
}

func TestMatchStopsExclusivityWithinTrip(t *testing.T) {
	// D-2: два стопа одного рейса не могут сесть на один терминал.
	// Первый берёт очевидный терминал, второй (тёзка) уходит в unmatched,
	// а не дублем на тот же терминал.
	lat, lon := 55.0, 86.0
	terms := []AttachTerminal{
		{ID: 7, Name: "Единственный терминал", Lat: &lat, Lon: &lon, Settlement: "альфа", Transport: "bus", Source: "osm", GeomFinalized: true},
	}
	trips := []model.FlatTrip{{
		RouteReg: "42.01.001", Direction: "forward", ServiceID: 1, Period: "winter",
		Stops: []model.FlatStop{
			{StopID: "a", Name: "Единственный терминал", Region: "42", Lat: &lat, Lon: &lon, ArrMin: intPtr(600), DepMin: intPtr(600)},
			{StopID: "b", Name: "Единственный терминал", Region: "42", Lat: &lat, Lon: &lon, ArrMin: intPtr(700), DepMin: intPtr(700)},
		},
	}}
	idx := buildMatchIndex(terms)
	matched, reason, _, unmatched := matchStops(trips[0], idx, testSource,
		func(string) model.DensityClass { return model.DensityRural }, attachParamsFor, slog.Default())
	if len(matched) != 1 {
		t.Fatalf("только первый стоп берёт терминал: matched=%d", len(matched))
	}
	if matched[0].TerminalID != 7 {
		t.Fatalf("terminal_id=%d, want 7", matched[0].TerminalID)
	}
	if len(unmatched) != 1 || unmatched[0] != "b" {
		t.Fatalf("второй стоп — unmatched (не дубль): %v", unmatched)
	}
	if reason != "incomplete_trip" {
		t.Fatalf("reason=%s, want incomplete_trip (сильнейший из unmatched)", reason)
	}
}

func TestMatchStopsCircularRouteAllowed(t *testing.T) {
	// Кольцевой рейс: первый и последний стоп — один легитимный терминал.
	// Exclusivity не должен рвать кольцо (§5.1 «кольцевые рейсы разрешены»):
	// повторный визит того же терминала допустим, если это не соседний дубль
	// (соседние схлопывает collapseConsecutive).
	latA, lonA := 55.0, 86.0
	latB, lonB := 55.5, 86.5
	terms := []AttachTerminal{
		{ID: 1, Name: "Альфа", Lat: &latA, Lon: &lonA, Settlement: "альфа", Source: "osm", GeomFinalized: true},
		{ID: 2, Name: "Бета", Lat: &latB, Lon: &lonB, Settlement: "бета", Source: "osm", GeomFinalized: true},
	}
	trips := []model.FlatTrip{{
		RouteReg: "42.02.001", Direction: "forward", ServiceID: 1, Period: "winter",
		Stops: []model.FlatStop{
			{StopID: "a", Name: "Альфа", Region: "42", Lat: &latA, Lon: &lonA, ArrMin: intPtr(600), DepMin: intPtr(600)},
			{StopID: "b", Name: "Бета", Region: "42", Lat: &latB, Lon: &lonB, ArrMin: intPtr(700), DepMin: intPtr(700)},
			{StopID: "a2", Name: "Альфа", Region: "42", Lat: &latA, Lon: &lonA, ArrMin: intPtr(800), DepMin: intPtr(800)},
		},
	}}
	idx := buildMatchIndex(terms)
	matched, _, _, unmatched := matchStops(trips[0], idx, testSource,
		func(string) model.DensityClass { return model.DensityRural }, attachParamsFor, slog.Default())
	if len(unmatched) != 0 {
		t.Fatalf("кольцевой повторный визит легитимен: unmatched=%v", unmatched)
	}
	if len(matched) != 3 {
		t.Fatalf("все 3 стопа матчатся (кольцо): matched=%d", len(matched))
	}
	// collapseConsecutive срежет только соседний дубль, кольцо через Бету — целое
	collapsed := collapseConsecutive(matched)
	if len(collapsed) != 3 {
		t.Fatalf("кольцо через другой терминал не схлопывается: %d", len(collapsed))
	}
}
