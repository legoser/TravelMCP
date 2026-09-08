package mintrans

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseDatasetFixture(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "test.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	ds, err := ParseDataset(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(ds.Routes) != 4 || len(ds.Stops) != 16 || len(ds.Schedules) != 8 {
		t.Fatalf("fixture shape: routes=%d stops=%d schedules=%d", len(ds.Routes), len(ds.Stops), len(ds.Schedules))
	}
}

func TestParseDatasetRejectsDrift(t *testing.T) {
	if _, err := ParseDataset([]byte(`{"source":"other"}`)); err == nil {
		t.Fatal("want contract error")
	}
}

func TestFlattenTripsFixture(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "test.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	ds, err := ParseDataset(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	trips, _ := FlattenTrips(ds)
	if len(trips) != 8 {
		t.Fatalf("trips = %d, want 8", len(trips))
	}
	untimedByTrip := map[string][]string{}
	for _, ft := range trips {
		if ft.FrequencyOnly {
			t.Fatalf("%s %s: fixture must have exact times", ft.RouteReg, ft.Direction)
		}
		if len(ft.Stops) < 2 {
			t.Fatalf("%s %s: stops = %d, want >= 2", ft.RouteReg, ft.Direction, len(ft.Stops))
		}
		if ft.Period != "winter" && ft.Period != "summer" {
			t.Fatalf("%s %s: period = %q", ft.RouteReg, ft.Direction, ft.Period)
		}
		if ft.RouteFrom == "" || ft.RouteTo == "" {
			t.Fatalf("%s: endpoints пустые", ft.RouteReg)
		}
		untimedByTrip[ft.RouteReg+"|"+ft.Direction] = ft.Untimed
	}
	if got := untimedByTrip["54.22.049|forward"]; len(got) != 1 || got[0] != "op:54:54106" {
		t.Fatalf("54.22.049 forward untimed = %v", got)
	}
}

func TestFlattenWeekdayRestriction(t *testing.T) {
	mkStop := func(id string) reestrStop {
		return reestrStop{ID: id, Name: "Стоп " + id, Region: "42", OpReg: id}
	}
	ds := reestrDataset{
		Source:   "minstran_reestr",
		Snapshot: "2026-05-10",
		Routes:   []reestrRoute{{Reg: "42.22.001", Name: "X", Carrier: "Y"}},
		Stops:    []reestrStop{mkStop("a"), mkStop("b")},
		Services: []struct {
			ID        int64  `json:"id"`
			Name      string `json:"name"`
			StartDate string `json:"start_date"`
			EndDate   string `json:"end_date"`
		}{{ID: 1, Name: "ежедневно", StartDate: "2026-01-01", EndDate: "2026-12-31"}},
		Schedules: []reestrSched{{
			Route: "42.22.001", Direction: "forward", ServiceID: 1,
			Stops: []reestrSchedStop{
				{Stop: "a", Region: "42", Winter: &reestrBlock{Days: "ежедневно", Dep: []string{"06:00", "13:35 (пт,вс)"}, Arr: []string{"06:00", "13:35 (пт,вс)"}}},
				{Stop: "b", Region: "42", Winter: &reestrBlock{Days: "ежедневно", Dep: []string{"07:00", "14:35 (пт,вс)"}, Arr: []string{"07:00", "14:35 (пт,вс)"}}},
			},
		}},
	}
	trips, stats := FlattenTrips(ds)
	if len(trips) != 2 {
		t.Fatalf("trips = %d, want 2", len(trips))
	}
	if trips[0].Weekdays != nil {
		t.Fatalf("run0 обязан быть без ограничений: %v", trips[0].Weekdays)
	}
	if !reflect.DeepEqual(trips[1].Weekdays, []int{0, 5}) {
		t.Fatalf("run1 обязан быть пт+вс: %v", trips[1].Weekdays)
	}
	if stats.RestrictedTrips != 1 {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestFlattenContradictoryWeekdaysDropped(t *testing.T) {
	mkStop := func(id string) reestrStop {
		return reestrStop{ID: id, Name: "Стоп " + id, Region: "42", OpReg: id}
	}
	ds := reestrDataset{
		Source:   "minstran_reestr",
		Snapshot: "2026-05-10",
		Routes:   []reestrRoute{{Reg: "42.22.001", Name: "X", Carrier: "Y"}},
		Stops:    []reestrStop{mkStop("a"), mkStop("b")},
		Services: []struct {
			ID        int64  `json:"id"`
			Name      string `json:"name"`
			StartDate string `json:"start_date"`
			EndDate   string `json:"end_date"`
		}{{ID: 1, Name: "ежедневно", StartDate: "2026-01-01", EndDate: "2026-12-31"}},
		Schedules: []reestrSched{{
			Route: "42.22.001", Direction: "forward", ServiceID: 1,
			Stops: []reestrSchedStop{
				{Stop: "a", Region: "42", Winter: &reestrBlock{Days: "ежедневно", Dep: []string{"06:00 (пн)"}, Arr: []string{"06:00 (пн)"}}},
				{Stop: "b", Region: "42", Winter: &reestrBlock{Days: "ежедневно", Dep: []string{"07:00 (вт)"}, Arr: []string{"07:00 (вт)"}}},
			},
		}},
	}
	trips, stats := FlattenTrips(ds)
	if len(trips) != 0 || stats.DroppedEmptyWeekdays != 1 {
		t.Fatalf("противоречивый прогон обязан дропнуться: trips=%d stats=%+v", len(trips), stats)
	}
}

func TestFlattenFrequencyOnly(t *testing.T) {
	ds := reestrDataset{
		Source:   "minstran_reestr",
		Snapshot: "2026-05-10",
		Routes:   []reestrRoute{{Reg: "54.22.078", Name: "X", Carrier: "Y"}},
		Stops: []reestrStop{
			{ID: "a", Name: "Альфа", Region: "54", OpReg: "1"},
			{ID: "b", Name: "Бета", Region: "54", OpReg: "2"},
		},
		Services: []struct {
			ID        int64  `json:"id"`
			Name      string `json:"name"`
			StartDate string `json:"start_date"`
			EndDate   string `json:"end_date"`
		}{{ID: 1, Name: "ежедневно", StartDate: "2026-01-01", EndDate: "2026-12-31"}},
		Schedules: []reestrSched{{
			Route: "54.22.078", Direction: "forward", ServiceID: 1,
			Stops: []reestrSchedStop{
				{Stop: "a", Region: "54", Winter: &reestrBlock{}},
				{Stop: "b", Region: "54", Winter: &reestrBlock{}},
			},
		}},
	}
	trips, _ := FlattenTrips(ds)
	if len(trips) != 1 || !trips[0].FrequencyOnly {
		t.Fatalf("want single frequency-only trip, got %+v", trips)
	}
}
