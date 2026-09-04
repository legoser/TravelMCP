package store

import (
	"context"
	"testing"
)

func TestPlaceClosure(t *testing.T) {
	ctx := context.Background()
	s, err := NewSQLiteStore("file:places_test?mode=memory&cache=private")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer s.Close()
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := seedPilotPlaces(ctx, s); err != nil {
		t.Fatalf("seed: %v", err)
	}
	var cnt int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM places`).Scan(&cnt); err != nil {
		t.Fatalf("count places: %v", err)
	}
	if cnt < 6 {
		t.Fatalf("places %d want >=6", cnt)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM place_closure`).Scan(&cnt); err != nil {
		t.Fatalf("count closure: %v", err)
	}
	if cnt == 0 {
		t.Fatal("closure empty")
	}
	// Kemerovo id=5 should be under Kemerovskaya oblast 3 and SFO 2 and RF 1
	var depth int
	if err := s.db.QueryRowContext(ctx, `SELECT depth FROM place_closure WHERE ancestor_id=1 AND descendant_id=5`).Scan(&depth); err != nil {
		t.Fatalf("closure 1->5 missing: %v", err)
	}
	if depth != 4 {
		t.Fatalf("depth 1->5 %d want 4 (РФ->Кемерово 4 hops)", depth)
	}
	// city lookup via closure level=4
	var cityID int64
	var tz string
	cityID, tz, err = s.GetPlaceCity(ctx, 6)
	if err != nil {
		t.Fatalf("GetPlaceCity: %v", err)
	}
	if cityID != 5 {
		t.Fatalf("city for 6 (central district) = %d want 5 (Кемерово)", cityID)
	}
	if tz == "" {
		t.Fatalf("tz empty")
	}
	// osm sync trigger
	var osm string
	s.db.QueryRowContext(ctx, `SELECT osm_compatible_name FROM terminals LIMIT 1`).Scan(&osm)
	// insert terminal and check trigger
	names := map[string]string{"ru": "Кемерово автовокзал", "en": "Kemerovo Bus Station"}
	row := TerminalRow{Lat: 55.355, Lon: 86.088, Tz: "Asia/Novosibirsk", ValidFrom: "2026-01-01", OsmName: "temp"}
	id, err := s.UpsertTerminal(ctx, row, names, nil)
	if err != nil {
		t.Fatalf("upsert terminal: %v", err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT osm_compatible_name FROM terminals WHERE id=?`, id).Scan(&osm); err != nil {
		t.Fatalf("select osm: %v", err)
	}
	if osm != "кемерово автовокзал" {
		t.Fatalf("osm_compatible_name %q want lower ru", osm)
	}
	// verify closure consistency via places service?
	// simple check: no orphan
	var orphan int
	s.db.QueryRowContext(ctx, `SELECT count(*) FROM place_closure pc LEFT JOIN places p ON p.id=pc.ancestor_id WHERE p.id IS NULL`).Scan(&orphan)
	if orphan != 0 {
		t.Fatalf("orphan closure %d", orphan)
	}
}

func seedPilotPlaces(ctx context.Context, s Store) error {
	pilots := []struct {
		name       string
		adminLevel int
		level      int
		parent     *int64
		lat, lon   float64
		tz         string
	}{
		{"РФ", 2, 0, nil, 61, 105, "Europe/Moscow"},
		{"Сибирский федеральный округ", 3, 1, int64Ptr(1), 55, 83, "Asia/Novosibirsk"},
		{"Кемеровская область", 4, 2, int64Ptr(2), 55.3, 86.08, "Asia/Novosibirsk"},
		{"Кемеровский городской округ", 6, 3, int64Ptr(3), 55.35, 86.08, "Asia/Novosibirsk"},
		{"Кемерово", 8, 4, int64Ptr(4), 55.355, 86.088, "Asia/Novosibirsk"},
		{"Центральный район", 9, 5, int64Ptr(5), 55.355, 86.088, "Asia/Novosibirsk"},
		{"Томская область", 4, 2, int64Ptr(2), 56.48, 84.95, "Asia/Tomsk"},
		{"Томск", 8, 4, int64Ptr(7), 56.48, 84.95, "Asia/Tomsk"},
		{"Новосибирская область", 4, 2, int64Ptr(2), 55.04, 82.93, "Asia/Novosibirsk"},
		{"Новосибирск", 8, 4, int64Ptr(9), 55.008, 82.935, "Asia/Novosibirsk"},
	}
	for i, p := range pilots {
		id := int64(i + 1)
		row := PlaceRow{ID: id, ParentID: p.parent, AdminLevel: p.adminLevel, Level: p.level, Lat: &p.lat, Lon: &p.lon, Tz: p.tz, ValidFrom: "2026-01-01"}
		names := map[string]string{"ru": p.name, "en": p.name}
		if p.name == "Кемеровская область" {
			names["en"] = "Kemerovo Oblast"
		} else if p.name == "Томская область" {
			names["en"] = "Tomsk Oblast"
		} else if p.name == "Кемерово" {
			names["en"] = "Kemerovo"
		} else if p.name == "Томск" {
			names["en"] = "Tomsk"
		} else if p.name == "Сибирский федеральный округ" {
			names["en"] = "Siberian Federal District"
		} else if p.name == "РФ" {
			names["en"] = "Russia"
		}
		if _, err := s.UpsertPlace(ctx, row, names); err != nil {
			return err
		}
	}
	return nil
}

func int64Ptr(v int64) *int64 { return &v }
