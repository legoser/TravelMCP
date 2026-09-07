package overpass

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"travelmcp/internal/config"
	"travelmcp/internal/geocoder"
)

func TestNewDefaults(t *testing.T) {
	a := New(config.Config{}, nil)
	urls := a.BaseURLs()
	if len(urls) != 2 {
		t.Fatalf("want 2 base urls, got %d", len(urls))
	}
	if urls[0] != DefaultURL {
		t.Errorf("primary = %q, want %q", urls[0], DefaultURL)
	}
	if urls[1] != DefaultMirrorURL {
		t.Errorf("mirror = %q, want %q", urls[1], DefaultMirrorURL)
	}
}

func TestNewCustomURLs(t *testing.T) {
	cfg := config.Config{}
	cfg.Overpass.URL = "https://example.invalid/api/interpreter"
	cfg.Overpass.MirrorURL = "https://mirror.invalid/api/interpreter"
	a := New(cfg, nil)
	urls := a.BaseURLs()
	if urls[0] != "https://example.invalid/api/interpreter" || urls[1] != "https://mirror.invalid/api/interpreter" {
		t.Fatalf("custom urls not honored: %v", urls)
	}
}

func TestBuildStopsQuery(t *testing.T) {
	b := BBox{MinLat: 55.0, MinLon: 84.0, MaxLat: 56.0, MaxLon: 85.0}
	q := BuildStopsQuery(b, 30)
	for _, want := range []string{
		"[out:json][timeout:30]",
		"55.000000,84.000000,56.000000,85.000000",
		`node["public_transport"~"platform|station|stop_position"]`,
		`node["highway"="bus_stop"]`,
		`node["railway"~"station|halt"]`,
		"out tags;",
	} {
		if !strings.Contains(q, want) {
			t.Errorf("query missing %q:\n%s", want, q)
		}
	}
}

func TestBuildStopsQueryDefaultTimeout(t *testing.T) {
	q := BuildStopsQuery(BBox{}, 0)
	if !strings.Contains(q, "[timeout:60]") {
		t.Errorf("want default timeout 60, got:\n%s", q)
	}
}

func TestBuildAroundQuery(t *testing.T) {
	q := BuildAroundQuery(55.354, 86.087, 1000)
	for _, want := range []string{
		"[out:json][timeout:60]",
		"node(around:1000,55.354000,86.087000)",
		`["public_transport"~"platform|station|stop_position"]`,
		`["highway"="bus_stop"]`,
		`["railway"~"station|halt"]`,
		"out center;",
	} {
		if !strings.Contains(q, want) {
			t.Errorf("query missing %q:\n%s", want, q)
		}
	}
}

func TestBuildAroundQueryDefaultRadius(t *testing.T) {
	q := BuildAroundQuery(55.0, 86.0, 0)
	if !strings.Contains(q, "node(around:500,") {
		t.Errorf("want default radius 500, got:\n%s", q)
	}
}

func TestBuildSearchByNameQuery(t *testing.T) {
	b := &BBox{MinLat: 53.5, MinLon: 84.0, MaxLat: 57.0, MaxLon: 88.5}
	q := BuildSearchByNameQuery(`Автовокзал "Южный"`, b, 25)
	for _, want := range []string{
		"[out:json][timeout:25];",
		`Автовокзал \"Южный\"`,
		"(53.500000,84.000000,57.000000,88.500000)",
		`highway"~"bus_stop|bus_station`,
		"out center 10;",
	} {
		if !strings.Contains(q, want) {
			t.Errorf("query missing %q:\n%s", want, q)
		}
	}
}

func TestStationsAroundHTTP(t *testing.T) {
	fake := overpassResponse{
		Elements: []overpassElement{
			{
				Type: "node",
				ID:   1234567,
				Lat:  float64Ptr(55.354),
				Lon:  float64Ptr(86.087),
				Tags: map[string]string{"name": "Автовокзал Кемерово", "highway": "bus_station"},
			},
		},
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(fake)
	}))
	defer ts.Close()

	cfg := config.Config{}
	cfg.Overpass.URL = ts.URL
	adapter := New(cfg, nil)

	records, err := adapter.StationsAround(context.Background(), 55.35, 86.08, 1000)
	if err != nil {
		t.Fatalf("StationsAround err: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("want 1 record, got %d", len(records))
	}
	if records[0].NameRu != "Автовокзал Кемерово" {
		t.Errorf("name = %q", records[0].NameRu)
	}
	if records[0].Source != "osm" {
		t.Errorf("source = %q", records[0].Source)
	}
}

func TestStationsAroundEmpty(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(overpassResponse{Elements: []overpassElement{}})
	}))
	defer ts.Close()

	cfg := config.Config{}
	cfg.Overpass.URL = ts.URL
	adapter := New(cfg, nil)

	records, err := adapter.StationsAround(context.Background(), 55.0, 86.0, 500)
	if err != nil {
		t.Fatalf("StationsAround empty err: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("want 0 records, got %d", len(records))
	}
}

func TestRegisteredInGeocoder(t *testing.T) {
	found := false
	for _, k := range geocoder.RegisteredKinds() {
		if k == "overpass" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("overpass not found in geocoder registry: %v", geocoder.RegisteredKinds())
	}

	g, err := geocoder.NewSingle(config.Config{}, nil, "overpass")
	if err != nil {
		t.Fatalf("NewSingle(overpass): %v", err)
	}
	if _, ok := g.(geocoder.MultiGeocoder); !ok {
		t.Errorf("expected Geocoder to implement geocoder.MultiGeocoder")
	}
}

func TestReverseUnsupported(t *testing.T) {
	a := New(config.Config{}, nil)
	_, err := a.Reverse(context.Background(), 55.0, 84.0)
	if err == nil {
		t.Fatal("want error from Reverse, got nil")
	}
	if !strings.Contains(err.Error(), "not supported") {
		t.Errorf("want 'not supported' error message, got: %v", err)
	}
}

func TestGeocodeCandidatesHTTPTest(t *testing.T) {
	fakeResp := overpassResponse{
		Elements: []overpassElement{
			{
				Type: "node",
				ID:   1234567,
				Lat:  float64Ptr(55.354),
				Lon:  float64Ptr(86.087),
				Tags: map[string]string{
					"name":    "Автовокзал Кемерово",
					"highway": "bus_station",
				},
			},
			{
				Type: "way",
				ID:   7654321,
				Center: &struct {
					Lat float64 `json:"lat"`
					Lon float64 `json:"lon"`
				}{
					Lat: 55.355,
					Lon: 86.088,
				},
				Tags: map[string]string{
					"name": "Платформа 1",
				},
			},
		},
	}

	var capturedBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "bad method", http.StatusMethodNotAllowed)
			return
		}
		b, _ := io.ReadAll(r.Body)
		capturedBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(fakeResp)
	}))
	defer ts.Close()

	cfg := config.Config{}
	cfg.Overpass.URL = ts.URL
	cfg.Overpass.MirrorURL = ts.URL
	adapter := New(cfg, nil)

	cands, err := adapter.GeocodeCandidates(context.Background(), "Автовокзал Кемерово", 5)
	if err != nil {
		t.Fatalf("GeocodeCandidates err: %v", err)
	}
	if len(cands) != 2 {
		t.Fatalf("want 2 candidates, got %d", len(cands))
	}
	if cands[0].Name != "Автовокзал Кемерово" || cands[0].Lat != 55.354 || cands[0].Lon != 86.087 {
		t.Errorf("cand[0] mismatch: %+v", cands[0])
	}
	if cands[0].Provider != "overpass" {
		t.Errorf("want provider overpass, got %q", cands[0].Provider)
	}
	if cands[1].Name != "Платформа 1" || cands[1].Lat != 55.355 || cands[1].Lon != 86.088 {
		t.Errorf("cand[1] mismatch: %+v", cands[1])
	}

	res, err := adapter.Geocode(context.Background(), "Автовокзал Кемерово")
	if err != nil {
		t.Fatalf("Geocode err: %v", err)
	}
	if res.Name != "Автовокзал Кемерово" || res.Lat != 55.354 || res.Lon != 86.087 {
		t.Errorf("Geocode result mismatch: %+v", res)
	}

	if !strings.Contains(capturedBody, "data=") {
		t.Errorf("request body did not contain data= parameter: %s", capturedBody)
	}
}

func TestGeocodeCandidatesNotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(overpassResponse{Elements: []overpassElement{}})
	}))
	defer ts.Close()

	cfg := config.Config{}
	cfg.Overpass.URL = ts.URL
	adapter := New(cfg, nil)

	_, err := adapter.GeocodeCandidates(context.Background(), "Несуществующая точка", 5)
	if err == nil {
		t.Fatal("want error on empty elements, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("want 'not found' error, got %v", err)
	}
}

func TestGeocodeFallbackToMirror(t *testing.T) {
	primaryCalled := false
	mirrorCalled := false

	primaryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryCalled = true
		http.Error(w, "rate limited / 429", http.StatusTooManyRequests)
	}))
	defer primaryServer.Close()

	mirrorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mirrorCalled = true
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(overpassResponse{
			Elements: []overpassElement{
				{
					Type: "node",
					ID:   999,
					Lat:  float64Ptr(56.0),
					Lon:  float64Ptr(85.0),
					Tags: map[string]string{"name": "Зеркало"},
				},
			},
		})
	}))
	defer mirrorServer.Close()

	cfg := config.Config{}
	cfg.Overpass.URL = primaryServer.URL
	cfg.Overpass.MirrorURL = mirrorServer.URL
	adapter := New(cfg, nil)

	res, err := adapter.Geocode(context.Background(), "Зеркало")
	if err != nil {
		t.Fatalf("Geocode with mirror fallback failed: %v", err)
	}
	if !primaryCalled {
		t.Errorf("expected primary server to be called first")
	}
	if !mirrorCalled {
		t.Errorf("expected mirror server to be called on primary failure")
	}
	if res.Name != "Зеркало" {
		t.Errorf("expected result from mirror, got %+v", res)
	}
}

func float64Ptr(v float64) *float64 {
	return &v
}
