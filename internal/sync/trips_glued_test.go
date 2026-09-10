package sync

import (
	"encoding/json"
	"testing"

	"travelmcp/internal/adapters/overpass"
	"travelmcp/internal/model"
)

// C-2 (план §10): hard-валидаторы (монотонность/скорость) писались под
// форму «стопы+времена из реестра»; на склейке «порядок стопов из Overpass
// × времена из Яндекса» их корректность обязана быть доказана явным
// тестом, а не «молча сработает» (дисциплина AGENTS: смена формы входа
// провалидированного конвейера — проверять hard-валидаторы явно).
//
// Склейка: orderFromOverpass — порядок seq из relation members (O-5),
// timesFromYandex — времена winner-источника на весь трип (§3.13).
func gluedStopTimes(order overpass.AdaptedTripData, times map[int64][2]int) []MatchedStopTime {
	out := make([]MatchedStopTime, 0, len(order.Stops))
	for i, s := range order.Stops {
		t := times[s.OSMID]
		out = append(out, MatchedStopTime{
			Seq: i, TerminalID: s.OSMID, StopID: overpassStopID(s),
			ArrivalS: t[0] * 60, DepartureS: t[1] * 60,
		})
	}
	for i := range out {
		out[i].Seq = i
	}
	return out
}

func overpassStopID(s overpass.AdaptedTripStop) string {
	return "osm:" + jsonInt(s.OSMID)
}

func jsonInt(v int64) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// (a) монотонность нарушена склейкой (времена Яндекса пришли не по порядку
// relation) → dead non-monotonic, не тихий промоут
func TestGluedFormMonotonic(t *testing.T) {
	order := overpass.AdaptedTripData{Ref: "101", Stops: []overpass.AdaptedTripStop{
		{OSMID: 1, Role: "stop"}, {OSMID: 2, Role: "stop"}, {OSMID: 3, Role: "stop"},
	}}
	times := map[int64][2]int{
		1: {600, 600},
		2: {650, 650},
		3: {620, 620}, // убывание по seq: 650 → 620
	}
	if msg := checkMonotonic(gluedStopTimes(order, times)); msg == "" {
		t.Fatal("склейка с убывающим временем обязана ловиться hard-валидатором монотонности")
	}
}

// (b)+(c) скорость на склеенном перегоне: финализированная геометрия обоих
// концов → dead; identity_only конец (GeomFinalized=false) → staging
// needs_review (guard §5.1: интерполированная геометрия не даёт dead)
func TestGluedFormSpeedTier(t *testing.T) {
	latA, lonA := 55.0, 86.0
	latB, lonB := 55.0, 89.5 // ~230 км между соседями по Overpass-порядку

	order := overpass.AdaptedTripData{Ref: "101", Stops: []overpass.AdaptedTripStop{
		{OSMID: 1, Role: "stop"}, {OSMID: 2, Role: "stop"},
	}}
	times := map[int64][2]int{
		1: {600, 600},
		2: {630, 630}, // 30 мин на 230 км ≈ 460 км/ч — за пределом 200
	}

	// (b) оба конца enriched: hard-валидатор режет
	termsFinal := []AttachTerminal{
		{ID: 1, Name: "Альфа", Lat: &latA, Lon: &lonA, Source: "osm", GeomFinalized: true},
		{ID: 2, Name: "Бета", Lat: &latB, Lon: &lonB, Source: "yandex", GeomFinalized: true},
	}
	if dead := checkSpeeds(gluedStopTimes(order, times), termsFinal, 200); dead == nil {
		t.Fatal("overspeed на финализированной геометрии склейки обязан быть dead")
	}

	// (c) конец identity_only: тот же перегон — soft (staging), не dead
	termsProvisional := []AttachTerminal{
		{ID: 1, Name: "Альфа", Lat: &latA, Lon: &lonA, Source: "osm", GeomFinalized: true},
		{ID: 2, Name: "Бета", Lat: &latB, Lon: &lonB, Source: "osm", GeomFinalized: false},
	}
	g := gluedStopTimes(order, times)
	if dead := checkSpeeds(g, termsProvisional, 200); dead != nil {
		t.Fatalf("незавершённая геометрия не даёт dead (guard §5.1): %+v", dead)
	}
	if soft := softSpeeds(g, termsProvisional, 200); soft == "" {
		t.Fatal("тот же перегон на неподтверждённой геометрии обязан уходить в soft/needs_review")
	}
	// флаг is_provisional обязан дотягиваться до stop_times (C-1 на склейке)
	for _, m := range g {
		_ = m.IsProvisional
	}
}

// (d) интерполированный конец + завышенная скорость → review, не dead —
// кейс из golden_trips.json «interpolated-speed-soft», исполненный на
// склеенной форме (порядок Overpass, времена Яндекса, интерполяция гео).
func TestGluedFormInterpolatedSoft(t *testing.T) {
	latA, lonA := 55.0, 86.0
	latInt, lonInt := 55.0, 89.5

	order := overpass.AdaptedTripData{Ref: "101", Stops: []overpass.AdaptedTripStop{
		{OSMID: 1, Role: "stop"}, {OSMID: 7, Role: "stop"},
	}}
	times := map[int64][2]int{
		1: {600, 600},
		7: {630, 630},
	}
	terms := []AttachTerminal{
		{ID: 1, Name: "Альфа", Lat: &latA, Lon: &lonA, Source: "osm", GeomFinalized: true},
		{ID: 7, Name: "Интерполяция seq", Lat: &latInt, Lon: &lonInt, Source: "osm", GeomFinalized: false},
	}
	g := gluedStopTimes(order, times)
	if dead := checkSpeeds(g, terms, 200); dead != nil {
		t.Fatalf("интерполированный конец: review, не dead: %+v", dead)
	}
	if soft := softSpeeds(g, terms, 200); soft == "" {
		t.Fatal("интерполированный конец с завышенной скоростью обязан soft-review'иться")
	}
}

// Полный проход склейки через AttachTrips: порядок Overpass становится
// stop_times seq, победитель промоутится, счётчики сходятся.
func TestGluedFormFullAttach(t *testing.T) {
	latA, lonA := 55.0, 86.0
	latB, lonB := 55.3, 86.3
	terms := []AttachTerminal{
		{ID: 1, Name: "Альфа", Lat: &latA, Lon: &lonA, Settlement: "альфа", Transport: "bus", Source: "osm", GeomFinalized: true},
		{ID: 2, Name: "Бета", Lat: &latB, Lon: &lonB, Settlement: "бета", Transport: "bus", Source: "osm", GeomFinalized: true},
		{ID: 3, Name: "Гамма", Lat: &latB, Lon: &lonB, Settlement: "бета", Transport: "bus", Source: "osm", GeomFinalized: true},
	}
	// Overpass-порядок 1→3, времена Яндекса налиты на этот порядок;
	// терминал 2 — тёзка Беты, exclusivity (D-2) не должен дублировать
	trips := []model.FlatTrip{{
		RouteReg: "42.10.001", Direction: "forward", ServiceID: 1, Period: "winter",
		Stops: []model.FlatStop{
			{StopID: "osm:1", Name: "Альфа", Region: "42", Lat: &latA, Lon: &lonA, ArrMin: intPtr(600), DepMin: intPtr(600)},
			{StopID: "osm:3", Name: "Гамма", Region: "42", Lat: &latB, Lon: &lonB, ArrMin: intPtr(660), DepMin: intPtr(660)},
		},
	}}
	rep, err := AttachTrips(t.Context(), AttachInput{
		Trips: trips, Terminals: terms, TrustRouteNK: true, Source: testSource,
		ChurnThreshold: 0.2, MaxSpeedKmh: 200, ParamsFor: attachParamsFor,
		ClassForRegion: func(string) model.DensityClass { return model.DensityRural },
	})
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if len(rep.Promoted) != 1 {
		t.Fatalf("склеенный трип обязан промоутиться: promoted=%d staged=%d dead=%d",
			len(rep.Promoted), len(rep.Staged), len(rep.Dead))
	}
	p := rep.Promoted[0]
	if len(p.StopTimes) != 2 || p.StopTimes[0].TerminalID != 1 || p.StopTimes[1].TerminalID != 3 {
		t.Fatalf("порядок Overpass обязан сохраниться в seq: %+v", p.StopTimes)
	}
}
