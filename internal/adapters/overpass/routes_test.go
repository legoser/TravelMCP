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
	// issue #12: пустой ref — регио-сбор всех маршрутных relation'ов
	// bbox, а не поиск с пустым номером.
	q := BuildRouteQuery("", nil, 0)
	if strings.Contains(q, `["ref"=`) {
		t.Errorf("пустой ref не должен давать ref-фильтр:\n%s", q)
	}
	if !strings.Contains(q, `["type"="route"]`) {
		t.Errorf("маршрутный фильтр обязан остаться:\n%s", q)
	}
}

// TestBuildRouteByIDQuery — issue #11: точечный запрос по relation_id.
func TestBuildRouteByIDQuery(t *testing.T) {
	q := BuildRouteByIDQuery(16312929, 0)
	for _, want := range []string{
		"[out:json][timeout:60]",
		"relation(16312929);",
		"out body;",
		">;",
		"out qt;",
	} {
		if !strings.Contains(q, want) {
			t.Errorf("query missing %q:\n%s", want, q)
		}
	}
	if strings.Contains(q, `"ref"`) {
		t.Errorf("id-запрос не должен фильтровать по ref:\n%s", q)
	}
}

// TestParseRouteResponseFull — issue #11: node/way элементы больше не
// выбрасываются: стопы получают координаты и имена, way-platform —
// центроид, геометрия склеивается из way-членов (прямой стык, стык
// хвостом с разворотом, разрыв — конкатенация сегмента).
func TestParseRouteResponseFull(t *testing.T) {
	data, err := os.ReadFile("../../../testdata/overpass/route_full.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	records, err := ParseRouteResponse(data)
	if err != nil {
		t.Fatalf("ParseRouteResponse: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("want 1 record, got %d", len(records))
	}
	var trip AdaptedTripData
	if err := json.Unmarshal(records[0].Raw, &trip); err != nil {
		t.Fatal(err)
	}

	// порядок и роли членов (включая way-platform) сохранены
	wantStops := []struct {
		osmID int64
		role  string
	}{
		{1, "stop"}, {2, "platform"}, {300, "platform"},
		{3, "stop"}, {4, "stop"}, {5, "stop"},
	}
	if len(trip.Stops) != len(wantStops) {
		t.Fatalf("want %d stops, got %d: %+v", len(wantStops), len(trip.Stops), trip.Stops)
	}
	for i, w := range wantStops {
		s := trip.Stops[i]
		if s.OSMID != w.osmID || s.Role != w.role {
			t.Errorf("stop %d: got {osm:%d role:%s}, want {osm:%d role:%s}", i, s.OSMID, s.Role, w.osmID, w.role)
		}
	}

	// координаты нод-стопов наполнены, имена — из тегов нод
	if trip.Stops[0].Lat != 56.4572 || trip.Stops[0].Lon != 84.9747 {
		t.Errorf("stop[0] coords: %+v", trip.Stops[0])
	}
	if trip.Stops[0].Name != "Поросино" {
		t.Errorf("stop[0].Name = %q", trip.Stops[0].Name)
	}
	if trip.Stops[1].Name != "Северный рынок" {
		t.Errorf("stop[1].Name = %q", trip.Stops[1].Name)
	}

	// way-platform: центроид его нод [10,11,12]
	wantLat, wantLon := (56.4601+56.4605+56.4609)/3, (84.9750+84.9800+84.9850)/3
	if trip.Stops[2].Lat != wantLat || trip.Stops[2].Lon != wantLon {
		t.Errorf("way-platform centroid: got (%v,%v), want (%v,%v)",
			trip.Stops[2].Lat, trip.Stops[2].Lon, wantLat, wantLon)
	}
	if trip.Stops[2].Name != "Платформа Северная" {
		t.Errorf("way-platform name = %q", trip.Stops[2].Name)
	}

	// геометрия: way301 [1,20,21] + way302 [21,...,25] прямой стык + way303
	// [30,31,32] разрыв → конкатенация: 1,20,21,22,23,24,25 + 30,31,32
	wantSeq := []int64{1, 20, 21, 22, 23, 24, 25, 30, 31, 32}
	if len(trip.Geometry) != len(wantSeq) {
		t.Fatalf("geometry: want %d points, got %d: %+v", len(wantSeq), len(trip.Geometry), trip.Geometry)
	}
	wantCoords := map[int64][2]float64{
		1: {56.4572, 84.9747}, 20: {56.4580, 84.9760}, 21: {56.4584, 84.9770},
		22: {56.4590, 84.9780}, 23: {56.4596, 84.9790}, 24: {56.4600, 84.9798},
		25: {56.4602, 84.9805}, 30: {56.4610, 84.9820}, 31: {56.4612, 84.9830},
		32: {56.4615, 84.9840},
	}
	for i, nid := range wantSeq {
		want := wantCoords[nid]
		if trip.Geometry[i] != want {
			t.Errorf("geometry[%d]: got %v, want %v (node %d)", i, trip.Geometry[i], want, nid)
		}
	}
}

// TestFetchRouteByIDHTTP — issue #11: живой FetchRoute по relation_id через
// httptest; проверка запроса, координат стопов, геометрии.
func TestFetchRouteByIDHTTP(t *testing.T) {
	data, err := os.ReadFile("../../../testdata/overpass/route_full.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var gotQuery string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotQuery = r.Form.Get("data")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	}))
	defer ts.Close()

	cfg := config.Config{}
	cfg.Overpass.URL = ts.URL
	adapter := New(cfg, nil)

	records, err := adapter.FetchRoute(context.Background(), 16312929)
	if err != nil {
		t.Fatalf("FetchRoute: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("want 1 record, got %d", len(records))
	}
	if !strings.Contains(gotQuery, "relation(16312929);") {
		t.Errorf("query must target relation id, got:\n%s", gotQuery)
	}
	var trip AdaptedTripData
	if err := json.Unmarshal(records[0].Raw, &trip); err != nil {
		t.Fatal(err)
	}
	if len(trip.Stops) != 6 {
		t.Fatalf("stops: %d", len(trip.Stops))
	}
	if trip.Stops[0].Lat == 0 && trip.Stops[0].Lon == 0 {
		t.Fatal("стопы обязаны получить координаты из node-элементов")
	}
	if len(trip.Geometry) == 0 {
		t.Fatal("геометрия обязана строиться из way-членов")
	}
}

// TestFetchRouteByIDInvalid — неположительный id: явная ошибка, не пустой ответ.
func TestFetchRouteByIDInvalid(t *testing.T) {
	adapter := New(config.Config{}, nil)
	if _, err := adapter.FetchRoute(context.Background(), 0); err == nil {
		t.Fatal("id<=0 обязан давать ошибку")
	}
}

// TestBuildGeometry* — склейка way-членов: прямой стык, стык хвостом
// (reverse), разрыв (конкатенация сегментов), пропуск координат узлов
// без изменения связности.
func buildTestIndex(t *testing.T, els ...routeElement) routeIndex {
	t.Helper()
	idx := make(routeIndex, len(els))
	for i := range els {
		idx[elementKey{Type: els[i].Type, ID: els[i].ID}] = &els[i]
	}
	return idx
}

func TestBuildGeometryChained(t *testing.T) {
	idx := buildTestIndex(t,
		routeElement{Type: "node", ID: 1, Lat: latp(56.1), Lon: lonp(84.1)},
		routeElement{Type: "node", ID: 2, Lat: latp(56.2), Lon: lonp(84.2)},
		routeElement{Type: "node", ID: 3, Lat: latp(56.3), Lon: lonp(84.3)},
		routeElement{Type: "way", ID: 10, Nodes: []int64{1, 2}},
		routeElement{Type: "way", ID: 11, Nodes: []int64{2, 3}},
	)
	poly := buildGeometry([]int64{10, 11}, idx)
	if len(poly) != 3 {
		t.Fatalf("want 3 points, got %d: %+v", len(poly), poly)
	}
	if poly[0] != [2]float64{56.1, 84.1} || poly[2] != [2]float64{56.3, 84.3} {
		t.Errorf("chained: %+v", poly)
	}
}

func TestBuildGeometryReversed(t *testing.T) {
	idx := buildTestIndex(t,
		routeElement{Type: "node", ID: 1, Lat: latp(56.1), Lon: lonp(84.1)},
		routeElement{Type: "node", ID: 2, Lat: latp(56.2), Lon: lonp(84.2)},
		routeElement{Type: "node", ID: 3, Lat: latp(56.3), Lon: lonp(84.3)},
		routeElement{Type: "way", ID: 10, Nodes: []int64{1, 2}},
		routeElement{Type: "way", ID: 11, Nodes: []int64{3, 2}},
	)
	poly := buildGeometry([]int64{10, 11}, idx)
	if len(poly) != 3 {
		t.Fatalf("want 3 points, got %d: %+v", len(poly), poly)
	}
	if poly[0] != [2]float64{56.1, 84.1} || poly[1] != [2]float64{56.2, 84.2} || poly[2] != [2]float64{56.3, 84.3} {
		t.Errorf("reversed: %+v", poly)
	}
}

func TestBuildGeometryBroken(t *testing.T) {
	idx := buildTestIndex(t,
		routeElement{Type: "node", ID: 1, Lat: latp(56.1), Lon: lonp(84.1)},
		routeElement{Type: "node", ID: 2, Lat: latp(56.2), Lon: lonp(84.2)},
		routeElement{Type: "node", ID: 3, Lat: latp(56.3), Lon: lonp(84.3)},
		routeElement{Type: "node", ID: 4, Lat: latp(56.4), Lon: lonp(84.4)},
		routeElement{Type: "way", ID: 10, Nodes: []int64{1, 2}},
		routeElement{Type: "way", ID: 11, Nodes: []int64{3, 4}},
	)
	poly := buildGeometry([]int64{10, 11}, idx)
	// разрыв между way10 и way11: сегменты конкатенируются, точки не теряются
	if len(poly) != 4 {
		t.Fatalf("want 4 points (конкатенация сегментов), got %d: %+v", len(poly), poly)
	}
	if poly[1] != [2]float64{56.2, 84.2} || poly[2] != [2]float64{56.3, 84.3} {
		t.Errorf("broken chain: %+v", poly)
	}
}

func TestBuildGeometrySkipsUnknownNodes(t *testing.T) {
	// нода 2 без координат (нет в индексе): связность сохраняется, точка
	// пропускается, дуга 1→3 остаётся корректной полилинией
	idx := buildTestIndex(t,
		routeElement{Type: "node", ID: 1, Lat: latp(56.1), Lon: lonp(84.1)},
		routeElement{Type: "node", ID: 3, Lat: latp(56.3), Lon: lonp(84.3)},
		routeElement{Type: "way", ID: 10, Nodes: []int64{1, 2, 3}},
	)
	poly := buildGeometry([]int64{10}, idx)
	if len(poly) != 2 {
		t.Fatalf("want 2 points, got %d: %+v", len(poly), poly)
	}
	if poly[0] != [2]float64{56.1, 84.1} || poly[1] != [2]float64{56.3, 84.3} {
		t.Errorf("skip unknown node: %+v", poly)
	}
}

// TestCachedRoutesProviderRelationID — мост cache+quota по relation_id
// (issue #11): ключ overpass:route:relation/<id>, кэш-хит без квоты,
// FetchRoute вызывается вместо ref-поиска.
func TestCachedRoutesProviderRelationID(t *testing.T) {
	data, err := os.ReadFile("../../../testdata/overpass/route_full.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var gotQuery string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotQuery = r.Form.Get("data")
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

	q := geocoder.RouteQueryParams{RelationID: 16312929}
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
	if !strings.Contains(gotQuery, "relation(16312929);") {
		t.Errorf("запрос обязан идти по relation id, got:\n%s", gotQuery)
	}

	// повторный вызов того же id — кэш-хит без квоты и без сети
	gotQuery = ""
	if _, err := cached.FetchRoutes(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("повтор — кэш-хит без квоты, calls=%d", calls)
	}
	if gotQuery != "" {
		t.Errorf("кэш-хит не должен ходить в сеть, got query: %s", gotQuery)
	}
}

func latp(v float64) *float64 { return &v }
func lonp(v float64) *float64 { return &v }

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
	rel := routeElement{
		Type:    "relation",
		ID:      999,
		Members: []routeMember{{Type: "node", Ref: 1, Role: "stop"}},
		Tags:    map[string]string{"route": "bus"},
	}
	_, ok := relationToRecord(&rel, routeIndex{})
	if ok {
		t.Error("relation without ref should be skipped")
	}
}

func TestRelationToRecordSkipsNoName(t *testing.T) {
	rel := routeElement{
		Type:    "relation",
		ID:      999,
		Members: []routeMember{{Type: "node", Ref: 1, Role: "stop"}},
		Tags:    map[string]string{"ref": "999", "route": "bus"},
	}
	_, ok := relationToRecord(&rel, routeIndex{})
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
