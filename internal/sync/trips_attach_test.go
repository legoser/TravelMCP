package sync

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"travelmcp/internal/adapters/mintrans"
	"travelmcp/internal/config"
	"travelmcp/internal/model"
	"travelmcp/internal/support/namesim"
	"travelmcp/internal/verification"
)

func attachParamsFor(class model.DensityClass) verification.Params {
	return verification.DefaultStopTerminalParams(config.Verification{}, class)
}

func loadFlatTrips(t *testing.T) []model.FlatTrip {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "test.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	ds, err := mintrans.ParseDataset(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	trips, _ := mintrans.FlattenTrips(ds)
	return trips
}

func indexFromFixture(t *testing.T, trips []model.FlatTrip) []AttachTerminal {
	t.Helper()
	seen := map[string]AttachTerminal{}
	var out []AttachTerminal
	id := int64(1)
	for _, ft := range trips {
		for _, s := range ft.Stops {
			if _, ok := seen[s.StopID]; ok {
				continue
			}
			lat, lon := s.Lat, s.Lon
			seen[s.StopID] = AttachTerminal{
				ID: id, Name: s.Name, Lat: lat, Lon: lon,
				Settlement: namesim.ExtractSettlement(s.Name),
				Transport:  "bus", Source: "osm", GeomFinalized: true,
			}
			id++
		}
	}
	for _, ft := range trips {
		for _, s := range ft.Stops {
			term := seen[s.StopID]
			out = append(out, term)
			delete(seen, s.StopID)
		}
	}
	return out
}

func baseInput(trips []model.FlatTrip, terms []AttachTerminal) AttachInput {
	return AttachInput{
		Trips:          trips,
		Terminals:      terms,
		TrustRouteNK:   true,
		Source:         "mintrans",
		ChurnThreshold: 0.2,
		MaxSpeedKmh:    200,
		ParamsFor:      attachParamsFor,
	}
}

func TestAttachFixtureOutcome(t *testing.T) {
	trips := loadFlatTrips(t)
	rep, err := AttachTrips(context.Background(), baseInput(trips, indexFromFixture(t, trips)))
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if len(rep.Promoted) != 4 || len(rep.Staged) != 2 || len(rep.Dead) != 2 {
		t.Fatalf("promoted=%d staged=%d dead=%d", len(rep.Promoted), len(rep.Staged), len(rep.Dead))
	}
	for _, s := range rep.Staged {
		if s.State != "incomplete_trip" || len(s.Unmatched) == 0 {
			t.Fatalf("staged = %+v", s)
		}
	}
	for _, d := range rep.Dead {
		if d.Reason != "overspeed" {
			t.Fatalf("dead = %+v", d)
		}
	}
	for _, p := range rep.Promoted {
		if p.WinnerSource != "mintrans:winter" && p.WinnerSource != "mintrans:summer" {
			t.Fatalf("%s: winner = %q", p.TripNK, p.WinnerSource)
		}
		if !p.IsSyntheticKey {
			t.Fatalf("%s: trip key must be synthetic", p.TripNK)
		}
		if msg := checkMonotonic(p.StopTimes); msg != "" {
			t.Fatalf("%s: %s", p.TripNK, msg)
		}
	}
}

func TestMatchStopsProvisionalFlag(t *testing.T) {
	latA, lonA := 55.0, 86.0
	latB, lonB := 56.0, 87.0
	terms := []AttachTerminal{
		{ID: 1, Name: "Альфа", Lat: &latA, Lon: &lonA, Settlement: "альфа", Transport: "bus", Source: "osm", GeomFinalized: true},
		{ID: 2, Name: "Бета", Lat: &latB, Lon: &lonB, Settlement: "бета", Transport: "bus", Source: "osm", GeomFinalized: false},
	}
	trips := []model.FlatTrip{{
		RouteReg: "54.22.100", Direction: "forward", ServiceID: 1, Period: "winter",
		Stops: []model.FlatStop{
			{StopID: "a", Name: "Альфа", Region: "42", Lat: &latA, Lon: &lonA, ArrMin: intPtr(600), DepMin: intPtr(600)},
			{StopID: "b", Name: "Бета", Region: "42", Lat: &latB, Lon: &lonB, ArrMin: intPtr(800), DepMin: intPtr(800)},
		},
	}}
	in := baseInput(trips, terms)
	in.ParamsFor = attachParamsFor
	in.ClassForRegion = func(string) model.DensityClass { return model.DensityRural }
	matched, _, _, unmatched := matchStops(trips[0], buildMatchIndex(terms), "mintrans", in.ClassForRegion, in.ParamsFor, slog.Default())
	if len(unmatched) != 0 {
		t.Fatalf("unmatched: %v", unmatched)
	}
	if len(matched) != 2 {
		t.Fatalf("matched=%d", len(matched))
	}
	if matched[0].IsProvisional {
		t.Fatal("enriched (GeomFinalized=true) terminal must not be provisional")
	}
	if !matched[1].IsProvisional {
		t.Fatal("identity_only (GeomFinalized=false) terminal must be provisional")
	}
}

func TestAttachUnmatchedStaging(t *testing.T) {
	lat, lon := 55.34, 86.06
	lat2, lon2 := 55.03, 82.89
	terms := []AttachTerminal{
		{ID: 1, Name: "Кемерово автовокзал", Lat: &lat, Lon: &lon, Settlement: "кемерово", Transport: "bus", Source: "osm", GeomFinalized: true},
	}
	trips := []model.FlatTrip{{
		RouteReg: "54.22.078", Direction: "forward", ServiceID: 1, Period: "winter",
		Stops: []model.FlatStop{
			{StopID: "a", Name: "Кемерово автовокзал", Region: "42", Lat: &lat, Lon: &lon, ArrMin: intPtr(600), DepMin: intPtr(610)},
			{StopID: "b", Name: "Новосибирск автовокзал", Region: "54", Lat: &lat2, Lon: &lon2, ArrMin: intPtr(900), DepMin: intPtr(910)},
		},
	}}
	rep, err := AttachTrips(context.Background(), baseInput(trips, terms))
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if len(rep.Staged) != 1 || rep.Staged[0].State != "skeleton_gap" || len(rep.Staged[0].Unmatched) == 0 {
		t.Fatalf("staged = %+v", rep.Staged)
	}
	if len(rep.Reviews) != 0 {
		t.Fatalf("skeleton gap must not file reviews: %+v", rep.Reviews)
	}
}

func TestAttachDuplicateAmbiguousReviews(t *testing.T) {
	lat, lon := 55.34, 86.06
	terms := []AttachTerminal{
		{ID: 1, Name: "Двойник", Lat: &lat, Lon: &lon, Settlement: "двойник", Transport: "bus", Source: "osm", GeomFinalized: true},
		{ID: 2, Name: "Двойник", Lat: &lat, Lon: &lon, Settlement: "двойник", Transport: "bus", Source: "osm", GeomFinalized: true},
	}
	trips := []model.FlatTrip{{
		RouteReg: "54.22.078", Direction: "forward", ServiceID: 1, Period: "winter",
		Stops: []model.FlatStop{
			{StopID: "a", Name: "Двойник", Region: "42", Lat: &lat, Lon: &lon, ArrMin: intPtr(600), DepMin: intPtr(610)},
			{StopID: "b", Name: "Двойник", Region: "42", Lat: &lat, Lon: &lon, ArrMin: intPtr(660), DepMin: intPtr(670)},
		},
	}}
	rep, err := AttachTrips(context.Background(), baseInput(trips, terms))
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if len(rep.Staged) != 1 || rep.Staged[0].State != "incomplete_trip" {
		t.Fatalf("staged = %+v", rep.Staged)
	}
	if len(rep.Reviews) != 1 || rep.Reviews[0].Reason != "duplicate_ambiguous" {
		t.Fatalf("reviews = %+v", rep.Reviews)
	}
	for _, r := range rep.Reviews {
		if r.EntityType != "trip" || r.Fingerprint == "" || r.EntityID >= 0 {
			t.Fatalf("review = %+v", r)
		}
	}
}

func intPtr(v int) *int { return &v }

func TestAttachNonMonotonicDead(t *testing.T) {
	lat, lon := 55.34, 86.06
	lat2, lon2 := 55.03, 82.89
	terms := []AttachTerminal{
		{ID: 1, Name: "Кемерово автовокзал", Lat: &lat, Lon: &lon, Settlement: "кемерово", Transport: "bus", Source: "osm", GeomFinalized: true},
		{ID: 2, Name: "Новосибирск автовокзал", Lat: &lat2, Lon: &lon2, Settlement: "новосибирск", Transport: "bus", Source: "osm", GeomFinalized: true},
	}
	trips := []model.FlatTrip{{
		RouteReg: "54.22.078", Direction: "forward", ServiceID: 1, Period: "winter",
		Stops: []model.FlatStop{
			{StopID: "a", Name: "Кемерово автовокзал", Region: "42", Lat: &lat, Lon: &lon, ArrMin: intPtr(600), DepMin: intPtr(610)},
			{StopID: "b", Name: "Новосибирск автовокзал", Region: "54", Lat: &lat2, Lon: &lon2, ArrMin: intPtr(500), DepMin: intPtr(510)},
		},
	}}
	rep, err := AttachTrips(context.Background(), baseInput(trips, terms))
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if len(rep.Dead) != 1 || rep.Dead[0].Reason != "non-monotonic" {
		t.Fatalf("dead = %+v", rep.Dead)
	}
}

func TestAttachOverspeedHardAndSoft(t *testing.T) {
	latA, lonA := 55.0, 86.0
	latB, lonB := 55.0, 89.5
	mkTerms := func(finalized bool) []AttachTerminal {
		return []AttachTerminal{
			{ID: 1, Name: "Альфа", Lat: &latA, Lon: &lonA, Settlement: "альфа", Source: "osm", GeomFinalized: finalized},
			{ID: 2, Name: "Бета", Lat: &latB, Lon: &lonB, Settlement: "бета", Source: "osm", GeomFinalized: finalized},
		}
	}
	mkTrip := func() []model.FlatTrip {
		return []model.FlatTrip{{
			RouteReg: "54.22.078", Direction: "forward", ServiceID: 1, Period: "winter",
			Stops: []model.FlatStop{
				{StopID: "a", Name: "Альфа", Region: "42", Lat: &latA, Lon: &lonA, ArrMin: intPtr(0), DepMin: intPtr(0)},
				{StopID: "b", Name: "Бета", Region: "54", Lat: &latB, Lon: &lonB, ArrMin: intPtr(60), DepMin: intPtr(60)},
			},
		}}
	}
	rep, err := AttachTrips(context.Background(), baseInput(mkTrip(), mkTerms(true)))
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if len(rep.Dead) != 1 || rep.Dead[0].Reason != "overspeed" {
		t.Fatalf("finalized overspeed must be dead: %+v promoted=%d staged=%d", rep.Dead, len(rep.Promoted), len(rep.Staged))
	}
	rep, err = AttachTrips(context.Background(), baseInput(mkTrip(), mkTerms(false)))
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if len(rep.Staged) != 1 || rep.Staged[0].State != "needs_review" {
		t.Fatalf("unconfirmed overspeed must be staged review: %+v", rep.Staged)
	}
	if len(rep.Dead) != 0 {
		t.Fatalf("unconfirmed must not die: %+v", rep.Dead)
	}
}

func TestAttachFrequencyOnlyStaged(t *testing.T) {
	trips := []model.FlatTrip{{
		RouteReg: "54.22.078", Direction: "forward", ServiceID: 1, FrequencyOnly: true,
	}}
	rep, err := AttachTrips(context.Background(), baseInput(trips, nil))
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if len(rep.Staged) != 1 || rep.Staged[0].State != "awaiting_times" {
		t.Fatalf("staged = %+v", rep.Staged)
	}
	if len(rep.Reviews) != 0 {
		t.Fatalf("awaiting_times must not file review: %+v", rep.Reviews)
	}
}

func currentNKs(rep AttachReport) map[string][]string {
	out := map[string][]string{}
	for _, p := range rep.Promoted {
		out[p.RouteNK] = append(out[p.RouteNK], p.TripNK)
	}
	return out
}

func TestAttachTombstoneAndChurn(t *testing.T) {
	trips := loadFlatTrips(t)
	terms := indexFromFixture(t, trips)
	first, err := AttachTrips(context.Background(), baseInput(trips, terms))
	if err != nil {
		t.Fatalf("first attach: %v", err)
	}
	prev := currentNKs(first)
	prev["00.00.000"] = []string{"00.00.000|forward:9:0"}
	in := baseInput(trips, terms)
	in.PrevCanon = prev
	rep, err := AttachTrips(context.Background(), in)
	if err != nil {
		t.Fatalf("second attach: %v", err)
	}
	if len(rep.Tombstoned) != 1 || rep.Tombstoned[0].TripNK != "00.00.000|forward:9:0" {
		t.Fatalf("tombstoned = %+v", rep.Tombstoned)
	}
	empty := baseInput(trips[:1], terms)
	empty.PrevCanon = prev
	if _, err := AttachTrips(context.Background(), empty); err == nil {
		t.Fatal("want churn-alert on mass disappearance")
	}
	fresh := baseInput(trips, terms)
	fresh.PrevCanon = map[string][]string{}
	if _, err := AttachTrips(context.Background(), fresh); err != nil {
		t.Fatalf("initial load must not alert: %v", err)
	}
}

func TestAttachRequiresParams(t *testing.T) {
	in := baseInput(nil, nil)
	in.ParamsFor = nil
	if _, err := AttachTrips(context.Background(), in); err == nil {
		t.Fatal("want error without ParamsFor")
	}
}

func TestGoldenValidateCases(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "golden_trips.json"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	var cases []map[string]any
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatalf("parse golden: %v", err)
	}
	seen := map[string]bool{}
	for _, tc := range cases {
		id, _ := tc["id"].(string)
		switch id {
		case "sequence-monotonic":
			seen[id] = true
			stops, _ := tc["stops"].([]any)
			if len(stops) != 2 {
				t.Fatalf("sequence case shape: %+v", tc)
			}
			dep := int(stops[0].(map[string]any)["dep"].(float64)) * 60
			arr := int(stops[1].(map[string]any)["arr"].(float64)) * 60
			m := []MatchedStopTime{
				{Seq: 0, TerminalID: 1, StopID: "a", ArrivalS: dep, DepartureS: dep},
				{Seq: 1, TerminalID: 2, StopID: "b", ArrivalS: arr, DepartureS: arr},
			}
			if msg := checkMonotonic(m); msg != "" {
				t.Fatalf("sequence-monotonic must pass: %s", msg)
			}
		case "interpolated-speed-soft":
			seen[id] = true
			latA, lonA := 55.0, 86.0
			latB, lonB := 55.0, 89.5
			terms := []AttachTerminal{
				{ID: 1, Name: "Альфа", Lat: &latA, Lon: &lonA, Source: "osm", GeomFinalized: true},
				{ID: 2, Name: "Бета", Lat: &latB, Lon: &lonB, Source: "osm", GeomFinalized: false},
			}
			m := []MatchedStopTime{
				{Seq: 0, TerminalID: 1, StopID: "a", ArrivalS: 0, DepartureS: 0},
				{Seq: 1, TerminalID: 2, StopID: "b", ArrivalS: 3600, DepartureS: 3600},
			}
			if dead := checkSpeeds(m, terms, 200); dead != nil {
				t.Fatalf("interpolated end must not die: %+v", dead)
			}
			if soft := softSpeeds(m, terms, 200); soft == "" {
				t.Fatal("interpolated fast leg must soft-review")
			}
		}
	}
	if len(seen) != 2 {
		t.Fatalf("golden validate cases not found: %v", seen)
	}
}
