package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"travelmcp/internal/config"
	"travelmcp/internal/model"
	"travelmcp/internal/providers"
	"travelmcp/internal/server"
	"travelmcp/internal/store"
	"travelmcp/internal/store/memory"
	"travelmcp/internal/telemetry"
)

func seedBrowserStore(t *testing.T) *memory.MemoryStore {
	t.Helper()
	ms := memory.NewMemoryStore()
	ctx := t.Context()

	carrierID, err := ms.UpsertCarrier(ctx, store.CarrierRow{Name: "ООО Тест-Перевозчик"})
	if err != nil {
		t.Fatal(err)
	}
	termA, err := ms.UpsertTerminal(ctx, store.TerminalRow{Lat: 55.35, Lon: 86.08}, map[string]string{"ru": "Кемерово, автовокзал"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	termB, err := ms.UpsertTerminal(ctx, store.TerminalRow{Lat: 55.44, Lon: 84.98}, map[string]string{"ru": "Топки, автостанция"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	termDead, err := ms.UpsertTerminal(ctx, store.TerminalRow{Lat: 56.01, Lon: 92.87}, map[string]string{"ru": "Красноярск, терминал-призрак"}, nil)
	if err != nil {
		t.Fatal(err)
	}

	stopA, err := ms.UpsertStop(ctx, store.StopRow{TerminalID: termA, Name: "Кемерово, автовокзал"})
	if err != nil {
		t.Fatal(err)
	}
	stopB, err := ms.UpsertStop(ctx, store.StopRow{TerminalID: termB, Name: "Топки, автостанция"})
	if err != nil {
		t.Fatal(err)
	}

	routeID, err := ms.UpsertRoute(ctx, store.RouteRow{ProviderID: "intercity", CarrierID: carrierID, ExternalRouteCode: "42.42.001", LongName: "Кемерово — Топки", Mode: "bus"})
	if err != nil {
		t.Fatal(err)
	}
	tripID, err := ms.UpsertTrip(ctx, store.TripRow{RouteID: routeID, ProviderID: "intercity", ExternalTripCode: "weds", ServiceDays: "0,3"})
	if err != nil {
		t.Fatal(err)
	}
	_ = ms.UpsertStopTime(ctx, store.StopTimeRow{TripID: tripID, StopID: stopA, Seq: 0, Departure: 8 * 3600})
	_ = ms.UpsertStopTime(ctx, store.StopTimeRow{TripID: tripID, StopID: stopB, Seq: 1, Arrival: 9 * 3600, Departure: 9 * 3600})
	_ = ms.SaveReviewQueue(ctx, model.ReviewQueueEntry{EntityType: "terminal", EntityID: termDead, Reason: "skeleton_unverified", Score: 0.3})
	return ms
}

func newBrowserApp(t *testing.T) (*httptest.Server, *memory.MemoryStore) {
	cfg := config.Defaults()
	cfg.Auth.AdminToken = "secret"
	reg := providers.NewRegistry([]string{"synth"})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ms := seedBrowserStore(t)
	ts := httptest.NewServer(server.NewWithStore(cfg, logger, telemetry.New(), reg, ms))
	t.Cleanup(ts.Close)
	return ts, ms
}

func adminGet(t *testing.T, url string) map[string]any {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("X-API-Key", "secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET %s: http %d: %s", url, resp.StatusCode, body)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestAdminDataBrowser(t *testing.T) {
	ts, _ := newBrowserApp(t)
	base := ts.URL + "/api/v1/admin"

	routes := adminGet(t, base+"/routes")
	items, _ := routes["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("routes: want 1, got %d", len(items))
	}
	route := items[0].(map[string]any)
	if route["external_route_code"] != "42.42.001" || route["live_trips"].(float64) != 1 {
		t.Fatalf("route payload wrong: %v", route)
	}
	routeID := int64(route["id"].(float64))

	trips := adminGet(t, base+fmt.Sprintf("/trips?route_id=%d", routeID))
	tItems, _ := trips["items"].([]any)
	if len(tItems) != 1 {
		t.Fatalf("trips: want 1, got %d", len(tItems))
	}
	tripID := int64(tItems[0].(map[string]any)["id"].(float64))

	trip := adminGet(t, base+fmt.Sprintf("/trips/%d", tripID))
	sts, _ := trip["items"].([]any)
	if len(sts) != 2 {
		t.Fatalf("stop_times: want 2, got %d", len(sts))
	}
	if sts[0].(map[string]any)["departure"].(float64) != 8*3600 {
		t.Fatalf("first stop_time departure wrong: %v", sts[0])
	}

	dead := adminGet(t, base+"/terminals/liveness?dead=yes")
	dItems, _ := dead["items"].([]any)
	if len(dItems) != 1 {
		t.Fatalf("dead terminals: want 1, got %d", len(dItems))
	}
	deadTerm := dItems[0].(map[string]any)
	if !deadTerm["dead"].(bool) || deadTerm["trips_served"].(float64) != 0 {
		t.Fatalf("dead terminal payload wrong: %v", deadTerm)
	}

	alive := adminGet(t, base+"/terminals/liveness?dead=no")
	aItems, _ := alive["items"].([]any)
	if len(aItems) != 2 {
		t.Fatalf("alive terminals: want 2, got %d", len(aItems))
	}
}

func TestAdminTerminalCardAndSchedule(t *testing.T) {
	ts, _ := newBrowserApp(t)
	base := ts.URL + "/api/v1/admin"

	alive := adminGet(t, base+"/terminals/liveness?dead=no")
	aItems, _ := alive["items"].([]any)
	if len(aItems) == 0 {
		t.Fatal("no alive terminals to test card")
	}
	aliveID := int64(aItems[0].(map[string]any)["id"].(float64))

	card := adminGet(t, base+fmt.Sprintf("/terminals/%d/card", aliveID))
	if card["name"] != "Кемерово, автовокзал" {
		t.Fatalf("card name wrong: %v", card["name"])
	}
	stats, _ := card["stats"].(map[string]any)
	if stats == nil || stats["dead"].(bool) {
		t.Fatalf("terminal must be alive, stats=%v", stats)
	}
	if stats["live_trips"].(float64) != 1 {
		t.Fatalf("terminal must serve 1 live trip, stats=%v", stats)
	}

	deadList := adminGet(t, base+"/terminals/liveness?dead=yes")
	dItems, _ := deadList["items"].([]any)
	if len(dItems) == 0 {
		t.Fatal("no dead terminals to test card")
	}
	deadID := int64(dItems[0].(map[string]any)["id"].(float64))

	deadCard := adminGet(t, base+fmt.Sprintf("/terminals/%d/card", deadID))
	deadStats, _ := deadCard["stats"].(map[string]any)
	if deadStats == nil || !deadStats["dead"].(bool) {
		t.Fatalf("terminal must be dead, stats=%v", deadStats)
	}
	if _, hasReview := deadCard["review"]; !hasReview {
		t.Fatal("dead terminal card must contain review entries")
	}

	wednesday := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)
	sched := adminGet(t, base+fmt.Sprintf("/terminals/%d/schedule?date=%s", aliveID, wednesday.Format("2006-01-02")))
	sItems, _ := sched["items"].([]any)
	if len(sItems) != 1 {
		t.Fatalf("schedule on wednesday: want 1 departure, got %d", len(sItems))
	}

	thursday := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	thuSched := adminGet(t, base+fmt.Sprintf("/terminals/%d/schedule?date=%s", aliveID, thursday.Format("2006-01-02")))
	thuItems, _ := thuSched["items"].([]any)
	if len(thuItems) != 0 {
		t.Fatalf("schedule on thursday: want 0 departures, got %d", len(thuItems))
	}
}

func TestAdminTerminalApprove(t *testing.T) {
	ts, ms := newBrowserApp(t)
	base := ts.URL + "/api/v1/admin"

	deadList := adminGet(t, base+"/terminals/liveness?dead=yes")
	dItems, _ := deadList["items"].([]any)
	if len(dItems) == 0 {
		t.Fatal("no dead terminals to approve")
	}
	deadID := int64(dItems[0].(map[string]any)["id"].(float64))

	body := `{"name":"Красноярск, автовокзал","settlement":"Красноярск","lat":56.01,"lon":92.87,"approve":true}`
	req, _ := http.NewRequest("PUT", base+fmt.Sprintf("/terminals/%d", deadID), bytes.NewReader([]byte(body)))
	req.Header.Set("X-API-Key", "secret")
	req.ContentLength = int64(len(body))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("approve: http %d: %s", resp.StatusCode, b)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out["approved"] != true {
		t.Fatalf("approved flag missing: %v", out)
	}
	term, err := ms.GetTerminal(t.Context(), deadID)
	if err != nil {
		t.Fatal(err)
	}
	if term["is_locked"] != true {
		t.Fatalf("terminal must be locked after approve: %v", term)
	}
	entries, _ := ms.ListTerminalReviewEntries(t.Context(), deadID)
	if len(entries) != 0 {
		t.Fatalf("review entries must be removed after approve, got %d", len(entries))
	}
}

func TestReviewTripSnapshot(t *testing.T) {
	ts, ms := newBrowserApp(t)
	ctx := t.Context()

	// staging-трип с матчингом на терминалы канона (как persistStagedTrip)
	matched := `[{"seq":0,"terminal_id":100,"arrival_s":28800,"departure_s":28800,"match_score":0.9},{"seq":1,"terminal_id":101,"arrival_s":32400,"departure_s":32400,"match_score":0.9}]`
	termA, err := ms.UpsertTerminal(ctx, store.TerminalRow{Lat: 55.35, Lon: 86.08}, map[string]string{"ru": "Кемерово, автовокзал"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	termB, err := ms.UpsertTerminal(ctx, store.TerminalRow{Lat: 55.44, Lon: 84.98}, map[string]string{"ru": "Топки, автостанция"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	matched = `[{"seq":0,"terminal_id":` + fmt.Sprint(termA) + `,"arrival_s":28800,"departure_s":28800,"match_score":0.9},{"seq":1,"terminal_id":` + fmt.Sprint(termB) + `,"arrival_s":32400,"departure_s":32400,"match_score":0.9}]`
	if _, err := ms.UpsertStagingTrip(ctx, store.StagingTripRow{
		Source: "yandex", ExternalRouteCode: "кемерово — топки|тест-перевозчик",
		ExternalTripCode: "forward:1:0", State: "incomplete_trip",
		MatchedStopTimes: matched, UnmatchedStops: `[]`,
	}); err != nil {
		t.Fatal(err)
	}
	// trip-ревью как из trips_attach: fingerprint source:routeNK|tripNK,
	// tripNK = routeNK+"|"+dir:svc:run → фактический вид source:R|R|tail
	fp := "yandex:кемерово — топки|тест-перевозчик|кемерово — топки|тест-перевозчик|forward:1:0"
	eid := int64(-4314202765230644000)
	if err := ms.SaveReviewQueue(ctx, model.ReviewQueueEntry{
		EntityType: "trip", EntityID: eid, Reason: "low_confidence",
		Score: 0.52, Fingerprint: fp,
	}); err != nil {
		t.Fatal(err)
	}

	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/review", nil)
	req.Header.Set("X-API-Key", "secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var list []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	var found map[string]any
	for _, it := range list {
		if it["entity_type"] == "trip" {
			found = it
		}
	}
	if found == nil {
		t.Fatalf("trip review not found: %v", list)
	}
	if found["fingerprint"] != fp {
		t.Fatalf("fingerprint missing: %v", found)
	}
	snap, _ := found["trip"].(map[string]any)
	if snap == nil {
		t.Fatalf("trip snapshot missing: %v", found)
	}
	if snap["source"] != "yandex" || snap["route_nk"] != "кемерово — топки|тест-перевозчик" || snap["trip_nk"] != "кемерово — топки|тест-перевозчик|forward:1:0" {
		t.Fatalf("snapshot keys wrong: %v", snap)
	}
	stops, _ := snap["stops"].([]any)
	if len(stops) != 2 {
		t.Fatalf("stops: want 2, got %d (%v)", len(stops), snap)
	}
	first, _ := stops[0].(map[string]any)
	last, _ := stops[1].(map[string]any)
	if first["name"] != "Кемерово, автовокзал" || last["name"] != "Топки, автостанция" {
		t.Fatalf("stop names wrong: %v / %v", first, last)
	}
	if first["departure_hhmm"] != "08:00" || last["arrival_hhmm"] != "09:00" {
		t.Fatalf("hhmm wrong: %v / %v", first, last)
	}
	if first["lat"] == nil || first["lon"] == nil {
		t.Fatalf("coords missing: %v", first)
	}
	fm, _ := snap["from"].(map[string]any)
	to, _ := snap["to"].(map[string]any)
	if fm == nil || to == nil || fm["name"] != "Кемерово, автовокзал" || to["name"] != "Топки, автостанция" {
		t.Fatalf("from/to wrong: %v %v", fm, to)
	}
}
