package motis

import (
	"context"
	"os"
	"testing"
	"time"
)

func motisBase() string {
	if v := os.Getenv("MOTIS_BASE"); v != "" {
		return v
	}
	if v := os.Getenv("MOTIS_URL"); v != "" {
		return v
	}
	return "http://192.168.57.14:8077"
}

func TestLiveGeocodeContract(t *testing.T) {
	base := motisBase()
	c := New(base, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	n := 5
	matches, err := c.Geocode(ctx, GeocodeParams{Text: "Кемерово автовокзал", Language: []string{"ru", "en"}, NumResults: &n})
	if err != nil {
		t.Skipf("motis %s not reachable: %v", base, err)
	}
	if len(matches) == 0 {
		t.Fatalf("no matches")
	}
	for _, m := range matches {
		if err := ValidateMatch(m); err != nil {
			t.Fatalf("invalid match %+v: %v", m, err)
		}
		if m.Tz == nil || *m.Tz == "" {
			t.Fatalf("tz missing for %s", m.Name)
		}
		has3, has4, has6 := false, false, false
		for _, a := range m.Areas {
			switch int(a.AdminLevel) {
			case 3:
				has3 = true
			case 4:
				has4 = true
			case 6:
				has6 = true
			}
		}
		if !has3 || !has4 {
			t.Logf("warn areas 3/4 missing for %s: %+v", m.Name, m.Areas)
		}
		if !has6 {
			t.Logf("warn area 6 missing for %s", m.Name)
		}
	}
}

func TestLiveReverseContract(t *testing.T) {
	base := motisBase()
	c := New(base, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	matches, err := c.ReverseGeocode(ctx, ReverseParams{Place: "55.355,86.088"})
	if err != nil {
		t.Skipf("motis %s not reachable: %v", base, err)
	}
	if len(matches) == 0 {
		t.Fatalf("no reverse matches")
	}
	for _, m := range matches[:1] {
		if err := ValidateMatch(m); err != nil {
			t.Fatalf("invalid reverse match: %v", err)
		}
		found := false
		for _, a := range m.Areas {
			if int(a.AdminLevel) == 3 || int(a.AdminLevel) == 4 || int(a.AdminLevel) == 6 {
				found = true
			}
		}
		if !found {
			t.Fatalf("reverse areas lack 3/4/6: %+v", m.Areas)
		}
	}
}

func TestLiveMapStopsContract(t *testing.T) {
	base := motisBase()
	c := New(base, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	places, err := c.MapStops(ctx, "54.9,86.0", "55.4,86.3")
	if err != nil {
		t.Skipf("motis %s not reachable: %v", base, err)
	}
	if len(places) == 0 {
		t.Fatalf("no stops")
	}
	foundImportance1 := false
	for _, p := range places {
		if err := ValidatePlace(p); err != nil {
			t.Fatalf("invalid place: %v", err)
		}
		if p.Importance != nil && *p.Importance == 1.0 {
			foundImportance1 = true
			if p.Tz == nil || *p.Tz != "Asia/Krasnoyarsk" {
				t.Fatalf("kemerovo av tz unexpected %v", p.Tz)
			}
		}
	}
	if !foundImportance1 {
		t.Fatalf("importance 1 stop not found in kuzbass bbox")
	}
}

func TestHealthContract(t *testing.T) {
	base := motisBase()
	c := New(base, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h, err := c.Health(ctx)
	if err != nil {
		t.Skipf("motis %s not reachable: %v", base, err)
	}
	if h == nil {
		t.Fatalf("nil health")
	}
	init, err := c.Initial(ctx)
	if err != nil {
		t.Fatalf("initial: %v", err)
	}
	if !init.ServerConfig.HasStreetRouting {
		t.Fatalf("expected hasStreetRouting true, got %+v", init.ServerConfig)
	}
	if !init.ServerConfig.HasRoutedTransfers {
		t.Fatalf("expected hasRoutedTransfers true")
	}
}
