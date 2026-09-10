package overpass

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"travelmcp/internal/config"
	"travelmcp/internal/geocoder"
	"travelmcp/internal/model"
)

func TestBuildRouteQuery(t *testing.T) {
	b := &BBox{MinLat: 53.5, MinLon: 84.0, MaxLat: 57.0, MaxLon: 88.5}
	q := BuildRouteQuery("42", b, 45)
	for _, want := range []string{
		"[out:json][timeout:45]",
		`["type"="route"]`,
		`["route"~"bus|trolleybus"]`,
		`["ref"="42"]`,
		"(53.500000,84.000000,57.000000,88.500000)",
		"out body;",
		">;",
		"out skel qt;",
	} {
		if !strings.Contains(q, want) {
			t.Errorf("query missing %q:\n%s", want, q)
		}
	}
}

func TestBuildRouteQueryNoRef(t *testing.T) {
	q := BuildRouteQuery("", nil, 0)
	if !strings.Contains(q, `["ref"=""]`) {
		t.Errorf("should handle empty ref:\n%s", q)
	}
}

func TestParseRouteResponse(t *testing.T) {
	data, err := os.ReadFile("../../../testdata/overpass/route.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	records, err := ParseRouteResponse(data)
	if err != nil {
		t.Fatalf("ParseRouteResponse: %v", err)
	}

	if len(records) != 1 {
		t.Errorf("want 1 record (second relation has no name, skipped), got %d", len(records))
	}

	r := records[0]
	if r.Kind != model.AdaptedTrip {
		t.Errorf("Kind = %v, want AdaptedTrip", r.Kind)
	}
	if r.NameRu != "Кемерово — Новокузнецк" {
		t.Errorf("NameRu = %q", r.NameRu)
	}
	if r.Source != "osm" {
		t.Errorf("Source = %q", r.Source)
	}
	if len(r.Identifiers) != 2 {
		t.Fatalf("want 2 identifiers, got %d", len(r.Identifiers))
	}
	if r.Identifiers[0].Code != "relation/100001" {
		t.Errorf("osm_id = %q", r.Identifiers[0].Code)
	}
	if r.Identifiers[1].Code != "42" {
		t.Errorf("route_ref = %q", r.Identifiers[1].Code)
	}
	if r.Extra["operator"] != "ГКУ КО 'Автовокзалы'" {
		t.Errorf("operator = %q", r.Extra["operator"])
	}
	if r.Extra["network"] != "Пригородные маршруты Кузбасса" {
		t.Errorf("network = %q", r.Extra["network"])
	}
	if r.Extra["route_type"] != "bus" {
		t.Errorf("route_type = %q", r.Extra["route_type"])
	}
	if r.Extra["stop_count"] != "5" {
		t.Errorf("stop_count = %q", r.Extra["stop_count"])
	}
}

func TestRelationToRecordSkipsNoRef(t *testing.T) {
	rel := routeRelation{
		Type:    "relation",
		ID:      999,
		Members: []routeMember{{Type: "node", Ref: 1, Role: "stop"}},
		Tags:    map[string]string{"route": "bus"},
	}
	_, ok := relationToRecord(rel)
	if ok {
		t.Error("relation without ref should be skipped")
	}
}

func TestRelationToRecordSkipsNoName(t *testing.T) {
	rel := routeRelation{
		Type:    "relation",
		ID:      999,
		Members: []routeMember{{Type: "node", Ref: 1, Role: "stop"}},
		Tags:    map[string]string{"ref": "999", "route": "bus"},
	}
	_, ok := relationToRecord(rel)
	if ok {
		t.Error("relation with ref but no name should be skipped")
	}
}

// TestFetchRouteRelationsHTTP — O-5: живой метод через httptest (фикстура
// route.json), порядок членов relation сохранён; identity = ref+operator.
func TestFetchRouteRelationsHTTP(t *testing.T) {
	data, err := os.ReadFile("../../../testdata/overpass/route.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	}))
	defer ts.Close()

	cfg := config.Config{}
	cfg.Overpass.URL = ts.URL
	adapter := New(cfg, nil)

	records, err := adapter.FetchRouteRelations(context.Background(), RouteQuery{Ref: "42"})
	if err != nil {
		t.Fatalf("FetchRouteRelations: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("want 1 record (второй relation без name — skip), got %d", len(records))
	}
	r := records[0]
	if r.Kind != model.AdaptedTrip {
		t.Fatalf("Kind = %v, want AdaptedTrip", r.Kind)
	}
	// порядок членов — семантика O-5 (identity-доказательство маршрута)
	var trip AdaptedTripData
	if err := json.Unmarshal(r.Raw, &trip); err != nil {
		t.Fatal(err)
	}
	if len(trip.Stops) != 5 {
		t.Fatalf("stop order lost: %+v", trip.Stops)
	}
	for i, s := range trip.Stops {
		if s.OSMID != int64(i+1) || s.Role != "stop" {
			t.Fatalf("member %d: порядок/роль потеряны: %+v", i, s)
		}
	}
	if trip.Operator != "ГКУ КО 'Автовокзалы'" || trip.Network != "Пригородные маршруты Кузбасса" {
		t.Fatalf("identity-поля: %+v", trip)
	}
}

// TestFetchRouteRelationsEmpty — пустой ответ Overpass: 0 записей, не ошибка.
func TestFetchRouteRelationsEmpty(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"elements":[]}`))
	}))
	defer ts.Close()

	cfg := config.Config{}
	cfg.Overpass.URL = ts.URL
	adapter := New(cfg, nil)

	records, err := adapter.FetchRouteRelations(context.Background(), RouteQuery{Ref: "nope"})
	if err != nil {
		t.Fatalf("empty err: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("want 0 records, got %d", len(records))
	}
}

// TestCachedRoutesProviderCacheQuota — путь cache+quota O-5 (по лекалу O-4):
// первый вызов — квота+пейсер, повторный того же ключа — hit без квоты.
func TestCachedRoutesProviderCacheQuota(t *testing.T) {
	data, err := os.ReadFile("../../../testdata/overpass/route.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	}))
	defer ts.Close()

	cfg := config.Config{}
	cfg.Overpass.URL = ts.URL
	adapter := New(cfg, nil)

	calls := 0
	quota := func(ctx context.Context, provider string, limit int) (bool, int, error) {
		calls++
		return true, calls, nil
	}
	cached := geocoder.NewCachedRoutesProvider(adapter, geocoder.NewMapGeoCacheStore(0), quota, 10)

	q := geocoder.RouteQueryParams{Ref: "42", HasBBox: true, MinLat: 53.5, MinLon: 84.0, MaxLat: 57.0, MaxLon: 88.5}
	recs1, err := cached.FetchRoutes(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs1) != 1 {
		t.Fatalf("first call: want 1 record, got %d", len(recs1))
	}
	if calls != 1 {
		t.Fatalf("первый вызов обязан взять квоту, calls=%d", calls)
	}
	recs2, err := cached.FetchRoutes(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("повторный вызов — hit кэша без квоты, calls=%d", calls)
	}
	if len(recs2) != 1 {
		t.Fatalf("cache hit: want 1 record, got %d", len(recs2))
	}
	// другой ref — новый ключ, снова квота
	if _, err := cached.FetchRoutes(context.Background(), geocoder.RouteQueryParams{Ref: "43"}); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("новый ref обязан брать квоту, calls=%d", calls)
	}
}

// TestCachedRoutesProviderQuotaExhausted — квота исчерпана: вежливый отказ,
// не обход пайплайна.
func TestCachedRoutesProviderQuotaExhausted(t *testing.T) {
	adapter := New(config.Config{}, nil)
	quota := func(ctx context.Context, provider string, limit int) (bool, int, error) {
		return false, limit, nil
	}
	cached := geocoder.NewCachedRoutesProvider(adapter, geocoder.NewMapGeoCacheStore(0), quota, 10)
	if _, err := cached.FetchRoutes(context.Background(), geocoder.RouteQueryParams{Ref: "42"}); err == nil {
		t.Fatal("исчерпанная квота обязана давать ошибку")
	}
}
