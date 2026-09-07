package overpass

import (
	"os"
	"strings"
	"testing"

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
