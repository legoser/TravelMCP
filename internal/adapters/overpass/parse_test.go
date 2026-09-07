package overpass

import (
	"os"
	"testing"

	"travelmcp/internal/model"
)

func TestParseResponse(t *testing.T) {
	data, err := os.ReadFile("../../../testdata/overpass/stations.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	records, err := ParseResponse(data)
	if err != nil {
		t.Fatalf("ParseResponse: %v", err)
	}

	if len(records) != 3 {
		t.Errorf("want 3 records (node+node+way with coords, skip 2 without), got %d", len(records))
	}

	got := func(name string) model.AdaptedRecord {
		for _, r := range records {
			if r.NameRu == name {
				return r
			}
		}
		t.Fatalf("record %q not found in %v", name, records)
		return model.AdaptedRecord{}
	}

	r := got("Кемерово, автовокзал")
	if r.Source != "osm" {
		t.Errorf("Source = %q, want osm", r.Source)
	}
	if r.Kind != model.AdaptedTerminal {
		t.Errorf("Kind = %v, want AdaptedTerminal", r.Kind)
	}
	if r.Lat == nil || *r.Lat != 55.3541 {
		t.Errorf("Lat = %v", r.Lat)
	}
	if r.Lon == nil || *r.Lon != 86.0872 {
		t.Errorf("Lon = %v", r.Lon)
	}
	if len(r.Identifiers) != 1 {
		t.Fatalf("want 1 identifier, got %d", len(r.Identifiers))
	}
	if r.Identifiers[0] != (model.AdaptedIdentifier{System: "osm", CodeType: "osm_id", Code: "2001001"}) {
		t.Errorf("Identifier = %+v", r.Identifiers[0])
	}
	if r.Extra["transport_type"] != "bus" {
		t.Errorf("transport_type = %q, want bus", r.Extra["transport_type"])
	}
	if r.Extra["object_type"] != "station" {
		t.Errorf("object_type = %v, want station", r.Extra["object_type"])
	}
	if r.Extra["osm_kind"] != "node" {
		t.Errorf("osm_kind = %q", r.Extra["osm_kind"])
	}

	r = got("Хромовка")
	if r.Extra["transport_type"] != "rail" {
		t.Errorf("Хромовка transport_type = %q, want rail", r.Extra["transport_type"])
	}
	if r.Lat == nil || *r.Lat != 56.4934 {
		t.Errorf("Хромовка Lat = %v", r.Lat)
	}
	if r.Identifiers[0].Code != "2001002" {
		t.Errorf("Хромовка osm_id = %q", r.Identifiers[0].Code)
	}

	r = got("Собор")
	if r.Extra["osm_kind"] != "way" {
		t.Errorf("Собор osm_kind = %q, want way", r.Extra["osm_kind"])
	}
	if r.Identifiers[0].Code != "way/3002001" {
		t.Errorf("Собор osm_id = %q", r.Identifiers[0].Code)
	}
	if r.Lat == nil || *r.Lat != 55.355 {
		t.Errorf("Собор Lat from center = %v", r.Lat)
	}
}

func TestParseResponseEmpty(t *testing.T) {
	data, err := os.ReadFile("../../../testdata/overpass/empty.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	records, err := ParseResponse(data)
	if err != nil {
		t.Fatalf("ParseResponse empty: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("want 0 records from empty fixture, got %d", len(records))
	}
}

func TestElementToRecordSkips(t *testing.T) {
	cases := []struct {
		name string
		el   overpassElement
		want bool
	}{
		{
			name: "no name",
			el:   overpassElement{Type: "node", ID: 1, Lat: float64Ptr(1), Lon: float64Ptr(2), Tags: map[string]string{"name": ""}},
			want: false,
		},
		{
			name: "no coords node",
			el:   overpassElement{Type: "node", ID: 2, Tags: map[string]string{"name": "Без гео"}},
			want: false,
		},

		{
			name: "valid",
			el:   overpassElement{Type: "node", ID: 4, Lat: float64Ptr(55.0), Lon: float64Ptr(86.0), Tags: map[string]string{"name": "Нормальная"}},
			want: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := elementToRecord(tc.el)
			if ok != tc.want {
				t.Errorf("elementToRecord(%q) = %v, want %v", tc.name, ok, tc.want)
			}
		})
	}
}
