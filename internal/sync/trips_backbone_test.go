package sync

import (
	"context"
	"testing"

	"travelmcp/internal/model"
)

// D-3 backbone: трип с verified-концами и дыркой в середине промоутится
// без непроверенного стопа (не уходит в staging целиком); перегон через
// пропуск валидируется на эффективных соседях (склеенный перегон).
func TestBackbonePromotesWithMidGap(t *testing.T) {
	latA, lonA := 55.0, 86.0
	latC, lonC := 55.3, 86.3
	terms := []AttachTerminal{
		{ID: 1, Name: "Альфа", Lat: &latA, Lon: &lonA, Settlement: "альфа", Transport: "bus", Source: "osm", GeomFinalized: true},
		{ID: 3, Name: "Гамма", Lat: &latC, Lon: &lonC, Settlement: "гамма", Transport: "bus", Source: "osm", GeomFinalized: true},
	}
	// средний стоп без терминала в скелете (никакой не матчится)
	trips := []model.FlatTrip{{
		RouteReg: "42.05.001", Direction: "forward", ServiceID: 1, Period: "winter",
		Stops: []model.FlatStop{
			{StopID: "a", Name: "Альфа", Region: "42", Lat: &latA, Lon: &lonA, ArrMin: intPtr(600), DepMin: intPtr(600)},
			{StopID: "zzz", Name: "Несуществующий хутор без терминала", Region: "42", ArrMin: intPtr(650), DepMin: intPtr(650)},
			{StopID: "c", Name: "Гамма", Region: "42", Lat: &latC, Lon: &lonC, ArrMin: intPtr(700), DepMin: intPtr(700)},
		},
	}}
	rep, err := AttachTrips(context.Background(), AttachInput{
		Trips: trips, Terminals: terms, TrustRouteNK: true, Source: "mintrans",
		ChurnThreshold: 0.2, MaxSpeedKmh: 200, ParamsFor: attachParamsFor,
		ClassForRegion: func(string) model.DensityClass { return model.DensityRural },
	})
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if len(rep.Promoted) != 1 {
		t.Fatalf("backbone-трип обязан промоутиться с дыркой: promoted=%d staged=%d dead=%d",
			len(rep.Promoted), len(rep.Staged), len(rep.Dead))
	}
	p := rep.Promoted[0]
	if len(p.StopTimes) != 2 {
		t.Fatalf("непроверенный стоп обязан быть исключён: stop_times=%d", len(p.StopTimes))
	}
	if p.StopTimes[0].TerminalID != 1 || p.StopTimes[1].TerminalID != 3 {
		t.Fatalf("концы обязаны сохраниться: %+v", p.StopTimes)
	}
	if rep.MidGaps != 1 {
		t.Fatalf("mid_gaps=1, got %d", rep.MidGaps)
	}
	if rep.GappedPromoted != 1 {
		t.Fatalf("gapped_promoted=1, got %d", rep.GappedPromoted)
	}
}

// Концы НЕ verified (первый стоп не матчится) — трип обязан уйти в staging:
// пассажир не поедет из никуда
func TestBackboneEndsRequired(t *testing.T) {
	latC, lonC := 55.3, 86.3
	terms := []AttachTerminal{
		{ID: 3, Name: "Гамма", Lat: &latC, Lon: &lonC, Settlement: "гамма", Transport: "bus", Source: "osm", GeomFinalized: true},
	}
	trips := []model.FlatTrip{{
		RouteReg: "42.06.001", Direction: "forward", ServiceID: 1, Period: "winter",
		Stops: []model.FlatStop{
			{StopID: "zzz1", Name: "Несуществующий хутор", Region: "42", ArrMin: intPtr(600), DepMin: intPtr(600)},
			{StopID: "c", Name: "Гамма", Region: "42", Lat: &latC, Lon: &lonC, ArrMin: intPtr(700), DepMin: intPtr(700)},
		},
	}}
	rep, err := AttachTrips(context.Background(), AttachInput{
		Trips: trips, Terminals: terms, TrustRouteNK: true, Source: "mintrans",
		ChurnThreshold: 0.2, MaxSpeedKmh: 200, ParamsFor: attachParamsFor,
		ClassForRegion: func(string) model.DensityClass { return model.DensityRural },
	})
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if len(rep.Promoted) != 0 || len(rep.Staged) != 1 {
		t.Fatalf("без verified-концов — staging целиком: promoted=%d staged=%d", len(rep.Promoted), len(rep.Staged))
	}
}

// Валидаторы на склеенном перегоне: дырка в середине → перегон А→Г
// обязан пересчитаться как один (30 мин на ~40км — в норме; если бы
// валидатор считал старую структуру — ложный overspeed не появлялся бы,
// тут проверяем что скорость считается по эффективным соседям)
func TestBackboneGapSpeedOnEffectiveNeighbors(t *testing.T) {
	latA, lonA := 55.0, 86.0
	latC, lonC := 55.3, 86.3
	terms := []AttachTerminal{
		{ID: 1, Name: "Альфа", Lat: &latA, Lon: &lonA, Source: "osm", GeomFinalized: true},
		{ID: 3, Name: "Гамма", Lat: &latC, Lon: &lonC, Source: "osm", GeomFinalized: true},
	}
	// 2 стопа, между ними 30 мин; расстояние ~40км → ~80 км/ч — в пределах 200.
	// Если бы считали по «сырой» структуре с фиктивным средним стопом без
	// координат — получили бы деление на ноль или overspeed. Эффективные
	// соседи: перегон один, скорость честная.
	m := []MatchedStopTime{
		{Seq: 0, TerminalID: 1, ArrivalS: 36000, DepartureS: 36000},
		{Seq: 1, TerminalID: 3, ArrivalS: 37800, DepartureS: 37800},
	}
	if bad := checkSpeeds(m, terms, 200); bad != nil {
		t.Fatalf("перегон через дырку обязан валидироваться на эффективных соседях: %+v", bad)
	}
	// контр-кейс: те же терминалы, 3 минуты — уже overspeed
	m[1].ArrivalS = 36180
	m[1].DepartureS = 36180
	if bad := checkSpeeds(m, terms, 200); bad == nil {
		t.Fatal("overspeed на склеенном перегоне обязан ловиться (hard-валидатор)")
	}
}
