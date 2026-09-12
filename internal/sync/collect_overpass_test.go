package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"travelmcp/internal/model"
	"travelmcp/internal/store/memory"
)

// fakeOverpassStations — источник станций для overpass-скелета.
type fakeOverpassStations struct {
	records []model.AdaptedRecord
	calls   int
}

func (f *fakeOverpassStations) StationsInBBox(ctx context.Context, minLat, minLon, maxLat, maxLon float64) ([]model.AdaptedRecord, error) {
	f.calls++
	return f.records, nil
}

func osmTerminal(id int64, name string, lat, lon float64) model.AdaptedRecord {
	return model.AdaptedRecord{
		Kind:   model.AdaptedTerminal,
		NameRu: name,
		Source: "osm",
		Lat:    &lat,
		Lon:    &lon,
		Identifiers: []model.AdaptedIdentifier{
			{System: "osm", CodeType: "osm_id", Code: fmt.Sprintf("%d", id)},
		},
		Extra: map[string]string{"transport_type": "bus"},
	}
}

// TestRunCollectOverpassSkeletonPromotes — терминалы Overpass идут тем же
// чанковым промоутом: координаты → канон IdentityOnly, без координат — review
// (issue #12: источник в сводке osm, регион размечен).
func TestRunCollectOverpassSkeletonPromotes(t *testing.T) {
	ms := memory.NewMemoryStore()
	src := &fakeOverpassStations{records: []model.AdaptedRecord{
		osmTerminal(101, "Горно-Алтайск, автовокзал", 51.9604, 85.9183),
		osmTerminal(102, "Усть-Кан", 50.0426, 85.6254),
	}}
	sum, runID, err := RunCollectOverpassSkeleton(context.Background(), ms, src,
		CollectOverpassConfig{Region: "Республика Алтай", MinLat: 49.0, MinLon: 83.5, MaxLat: 52.7, MaxLon: 89.5, Tag: "test"},
		nil)
	if err != nil {
		t.Fatalf("RunCollectOverpassSkeleton: %v", err)
	}
	if runID <= 0 {
		t.Fatalf("run_id обязан создаваться, got %d", runID)
	}
	if sum.Source != "osm" || sum.Region != "Республика Алтай" {
		t.Errorf("summary: %+v", sum)
	}
	if sum.StationsIn != 2 || sum.Written != 2 {
		t.Errorf("stations=%d written=%d, want 2/2 (обе с координатами → IdentityOnly)", sum.StationsIn, sum.Written)
	}
}

// TestFlattenOverpassRoute — identity-запись O-5 → FlatTrip без времён:
// порядок сохранён, osm-коды на стопах, координаты наполнены.
func TestFlattenOverpassRoute(t *testing.T) {
	data := overpassRouteFixture(t)
	rec := model.AdaptedRecord{
		Kind:   model.AdaptedTrip,
		NameRu: "Автобус №101: Поросино - Ленина",
		Source: "osm",
		Raw:    data,
		Identifiers: []model.AdaptedIdentifier{
			{System: "osm", CodeType: "osm_id", Code: "relation/16312929"},
			{System: "osm", CodeType: "route_ref", Code: "101"},
		},
	}
	trip, err := FlattenOverpassRoute(rec, "Томская область")
	if err != nil {
		t.Fatalf("FlattenOverpassRoute: %v", err)
	}
	if len(trip.Stops) != 3 {
		t.Fatalf("stops: %d, want 3", len(trip.Stops))
	}
	if trip.Stops[0].StopID != "1" || trip.Stops[2].StopID != "3" {
		t.Errorf("порядок стопов потерян: %+v", trip.Stops)
	}
	if trip.Stops[0].ArrMin != nil || trip.Stops[1].DepMin != nil {
		t.Error("времена обязаны отсутствовать (без расписания)")
	}
	if trip.Stops[0].Lat == nil || *trip.Stops[0].Lat != 56.4572 {
		t.Errorf("координаты не наполнены: %+v", trip.Stops[0])
	}
	if trip.Stops[0].Codes[0].System != "osm" || trip.Stops[0].Codes[0].Code != "1" {
		t.Errorf("osm-код стопа: %+v", trip.Stops[0].Codes)
	}
	if trip.RouteReg != "101" || trip.Carrier != "МУП «Томскгорранс»" {
		t.Errorf("identity: route_reg=%q carrier=%q", trip.RouteReg, trip.Carrier)
	}
	if trip.RouteNK == "" || !strings.Contains(trip.RouteNK, "101") {
		t.Errorf("route_nk обязан включать ref: %q", trip.RouteNK)
	}
	if trip.Stops[0].Region != "Томская область" {
		t.Errorf("region стопа: %q", trip.Stops[0].Region)
	}
}

// TestFlattenOverpassRouteTooFewStops — 0-1 остановка — явная ошибка.
func TestFlattenOverpassRouteTooFewStops(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{
		"ref": "101",
		"stops": []map[string]any{
			{"osm_id": 1, "role": "stop", "lat": 56.1, "lon": 84.1, "name": "Единственная"},
		},
	})
	rec := model.AdaptedRecord{Kind: model.AdaptedTrip, NameRu: "x", Raw: raw}
	if _, err := FlattenOverpassRoute(rec, "R"); err == nil {
		t.Fatal("меньше двух остановок обязано давать ошибку")
	}
}

func overpassRouteFixture(t *testing.T) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"ref":      "101",
		"operator": "МУП «Томскгорранс»",
		"network":  "Томск гор",
		"stops": []map[string]any{
			{"osm_id": 1, "role": "stop", "lat": 56.4572, "lon": 84.9747, "name": "Поросино"},
			{"osm_id": 2, "role": "platform", "lat": 56.4660, "lon": 84.9915, "name": "Северный рынок"},
			{"osm_id": 3, "role": "stop", "lat": 56.4712, "lon": 85.0043, "name": "Площадь Ленина"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestAttachUntimedTripAwaitsTimes — полностью безвременный рейс (overpass,
// issue #12): стопы матчатся к терминалам, трип уходит в staging
// awaiting_times с Matched — маршрут привязан к канону, ждёт времён.
func TestAttachUntimedTripAwaitsTimes(t *testing.T) {
	terms := []AttachTerminal{
		{ID: 1, Name: "Поросино", Lat: p64(56.4572), Lon: p64(84.9747), Settlement: "Поросино", Transport: "bus", Source: "osm", GeomFinalized: true,
			Codes: []model.AdaptedIdentifier{{System: "osm", CodeType: "osm_id", Code: "1"}}},
		{ID: 2, Name: "Северный рынок", Lat: p64(56.4660), Lon: p64(84.9915), Settlement: "Томск", Transport: "bus", Source: "osm", GeomFinalized: true,
			Codes: []model.AdaptedIdentifier{{System: "osm", CodeType: "osm_id", Code: "2"}}},
		{ID: 3, Name: "Площадь Ленина", Lat: p64(56.4712), Lon: p64(85.0043), Settlement: "Томск", Transport: "bus", Source: "osm", GeomFinalized: true,
			Codes: []model.AdaptedIdentifier{{System: "osm", CodeType: "osm_id", Code: "3"}}},
	}
	ft := model.FlatTrip{
		RouteNK: "osm-101-16312929", RouteReg: "101", Direction: "forward",
		ServiceID: 777, Run: 1, Carrier: "МУП «Томскгорранс»",
		Stops: []model.FlatStop{
			{StopID: "1", Name: "Поросино", Region: "Томская область", Codes: []model.AdaptedIdentifier{{System: "osm", CodeType: "osm_id", Code: "1"}}},
			{StopID: "2", Name: "Северный рынок", Region: "Томская область", Codes: []model.AdaptedIdentifier{{System: "osm", CodeType: "osm_id", Code: "2"}}},
			{StopID: "3", Name: "Площадь Ленина", Region: "Томская область", Codes: []model.AdaptedIdentifier{{System: "osm", CodeType: "osm_id", Code: "3"}}},
		},
	}
	rep, err := AttachTrips(context.Background(), AttachInput{
		Trips: []model.FlatTrip{ft}, Terminals: terms,
		Source: "osm", ChurnThreshold: 0.2, MaxSpeedKmh: 200,
		ParamsFor: attachParamsFor,
	})
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if len(rep.Staged) != 1 {
		t.Fatalf("want 1 staged, got %d (promoted=%d dead=%d)", len(rep.Staged), len(rep.Promoted), len(rep.Dead))
	}
	st := rep.Staged[0]
	if st.State != "awaiting_times" {
		t.Fatalf("state = %q, want awaiting_times", st.State)
	}
	if len(st.Matched) != 3 {
		t.Fatalf("стопы обязаны сматчиться к терминалам: %+v", st.Matched)
	}
	if st.Matched[0].TerminalID != 1 || st.Matched[2].TerminalID != 3 {
		t.Errorf("матчинг потерял порядок: %+v", st.Matched)
	}
}

func p64(v float64) *float64 { return &v }

// TestStagedTripRegionFromStops — инцидент job 53 (SQLSTATE 22021):
// кириллический ref маршрута («1к») больше не режется по байтам для
// «региона»: регион трипа берётся из стопов (настоящее название),
// персист staging не падает на битом UTF-8.
func TestStagedTripRegionFromStops(t *testing.T) {
	terms := []AttachTerminal{
		{ID: 1, Name: "Артыбаш", Lat: p64(52.1086), Lon: p64(86.4867), Settlement: "Артыбаш", Transport: "bus", Source: "osm", GeomFinalized: true,
			Codes: []model.AdaptedIdentifier{{System: "osm", CodeType: "osm_id", Code: "1"}}},
		{ID: 2, Name: "Горно-Алтайск", Lat: p64(51.9604), Lon: p64(85.9183), Settlement: "Горно-Алтайск", Transport: "bus", Source: "osm", GeomFinalized: true,
			Codes: []model.AdaptedIdentifier{{System: "osm", CodeType: "osm_id", Code: "2"}}},
	}
	ft := model.FlatTrip{
		RouteNK: "osm-1к-1867629", RouteReg: "1к", Direction: "forward",
		ServiceID: 777, Run: 1,
		Stops: []model.FlatStop{
			{StopID: "1", Name: "Артыбаш", Region: "Республика Алтай", Codes: []model.AdaptedIdentifier{{System: "osm", CodeType: "osm_id", Code: "1"}}},
			{StopID: "2", Name: "Горно-Алтайск", Region: "Республика Алтай", Codes: []model.AdaptedIdentifier{{System: "osm", CodeType: "osm_id", Code: "2"}}},
		},
	}
	rep, err := AttachTrips(context.Background(), AttachInput{
		Trips: []model.FlatTrip{ft}, Terminals: terms,
		Source: "osm", ChurnThreshold: 0.2, MaxSpeedKmh: 200,
		ParamsFor: attachParamsFor,
	})
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if len(rep.Staged) != 1 {
		t.Fatalf("want 1 staged, got %d", len(rep.Staged))
	}
	if got := rep.Staged[0].Region; got != "Республика Алтай" {
		t.Fatalf("region = %q, want from stops", got)
	}
	if !utf8.ValidString(rep.Staged[0].Region) || !utf8.ValidString(string(rep.Staged[0].RouteNK)) {
		t.Fatal("staged fields must be valid UTF-8")
	}

	// персист в memory-стор: строка обязана записаться без ошибок
	ms := memory.NewMemoryStore()
	if _, err := PersistAttachReport(context.Background(), ms, rep, "osm", nil); err != nil {
		t.Fatalf("persist: %v (не должно падать на кириллическом ref)", err)
	}
}
