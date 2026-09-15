package main

import (
	"testing"
)

func edgeDataset() reestrDataset {
	stops := []reestrStop{
		{ID: "s1", Name: "Кемерово", Region: "42", OpReg: "op1"},
		{ID: "s2", Name: "Топки", Region: "42", OpReg: "op2"},
		{ID: "s3", Name: "Новосибирск", Region: "54", OpReg: "op3"},
		{ID: "s4", Name: "Без времени", Region: "42", OpReg: "op4"},
	}
	w := func(dep, arr []string) *reestrBlock { return &reestrBlock{Dep: dep, Arr: arr} }
	sched := reestrSched{
		Route: "42.42.001", Direction: "forward", ServiceID: 1,
		Stops: []reestrSchedStop{
			{Stop: "s1", Region: "42", Winter: w([]string{"23:50"}, nil)},
			{Stop: "s2", Region: "42", Winter: w([]string{"23:56"}, []string{"23:55"})},
			{Stop: "s3", Region: "54", Winter: w(nil, []string{"00:10"})},
			{Stop: "s4", Region: "42"},
		},
	}
	return reestrDataset{
		Routes:    []reestrRoute{{Reg: "42.42.001", Name: "Кемерово — Новосибирск"}},
		Stops:     stops,
		Carriers:  []reestrCarrier{{Name: "Перевозчик"}},
		Schedules: []reestrSched{sched},
	}
}

func TestFlattenTripsOvernight(t *testing.T) {
	trips, stats := FlattenTrips(edgeDataset(), "gov-registry")
	if stats.Trips != 1 {
		t.Fatalf("want 1 trip, stats=%+v", stats.Trips)
	}
	ft := trips[0]
	if len(ft.Stops) != 4 {
		t.Fatalf("want 4 stops, got %d", len(ft.Stops))
	}
	prev := -1
	for i, s := range ft.Stops[:3] {
		if s.ArrMin == nil || s.DepMin == nil {
			t.Fatalf("stop %d must be timed", i)
		}
		if *s.ArrMin < prev || *s.DepMin < *s.ArrMin {
			t.Fatalf("stop %d not monotonic: arr=%d dep=%d prev=%d", i, *s.ArrMin, *s.DepMin, prev)
		}
		prev = *s.DepMin
	}
	last := ft.Stops[2]
	if *last.ArrMin != 24*60+10 || *last.DepMin != 24*60+10 {
		t.Fatalf("overnight arrival must roll to next day (1450), got arr=%d dep=%d", *last.ArrMin, *last.DepMin)
	}
	fuzzy := ft.Stops[3]
	if !fuzzy.IsFuzzy || fuzzy.ArrMin != nil || fuzzy.DepMin != nil {
		t.Fatalf("blockless stop must be fuzzy untimed: %+v", fuzzy)
	}
	if len(ft.Untimed) != 1 || ft.Untimed[0] != "s4" {
		t.Fatalf("untimed must list s4: %v", ft.Untimed)
	}
}

func TestFlattenTripsBadCellUntimed(t *testing.T) {
	ds := edgeDataset()
	ds.Schedules[0].Stops[1].Winter.Arr = []string{"25:00"}
	ds.Schedules[0].Stops[1].Winter.Dep = []string{"99:99"}
	trips, _ := FlattenTrips(ds, "gov-registry")
	if len(trips) != 1 {
		t.Fatalf("want 1 trip, got %d", len(trips))
	}
	s := trips[0].Stops[1]
	if !s.IsFuzzy || s.ArrMin != nil || s.DepMin != nil {
		t.Fatalf("bad cells must degrade to fuzzy untimed: %+v", s)
	}
}

func TestFlattenTripsSingleTimePairs(t *testing.T) {
	ds := edgeDataset()
	ds.Schedules[0].Stops[1].Winter.Arr = []string{"25:00"}
	trips, _ := FlattenTrips(ds, "gov-registry")
	s := trips[0].Stops[1]
	if s.IsFuzzy || s.ArrMin == nil || s.DepMin == nil {
		t.Fatalf("single good time must pair arr=dep: %+v", s)
	}
	if *s.ArrMin != 23*60+56 || *s.DepMin != 23*60+56 {
		t.Fatalf("want arr=dep=1436, got arr=%d dep=%d", *s.ArrMin, *s.DepMin)
	}
}

func TestFlattenTripsFrequencyOnly(t *testing.T) {
	ds := edgeDataset()
	for i := range ds.Schedules[0].Stops {
		ds.Schedules[0].Stops[i].Winter = nil
	}
	trips, stats := FlattenTrips(ds, "gov-registry")
	if len(trips) != 1 || !trips[0].FrequencyOnly {
		t.Fatalf("no times must yield frequency-only trip: %+v", trips)
	}
	if stats.FrequencyOnly != 1 {
		t.Fatalf("stats.FrequencyOnly=%d, want 1", stats.FrequencyOnly)
	}
}

func TestFlattenTripsEmptyDataset(t *testing.T) {
	trips, stats := FlattenTrips(reestrDataset{}, "gov-registry")
	if len(trips) != 0 {
		t.Fatalf("empty dataset must yield no trips")
	}
	_ = stats
}
