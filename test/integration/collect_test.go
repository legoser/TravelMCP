package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"

	"travelmcp/internal/store"
	"travelmcp/internal/sync"
)

// TestCollectEndpointsAndSyncRuns — ручной режим сбора: POST /collect/*
// кладёт job sync_collect_region, GET /sync/runs отдаёт сводки.
func TestCollectEndpointsAndSyncRuns(t *testing.T) {
	ts, ms := newBrowserApp(t)

	post := func(path string, body map[string]any) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", ts.URL+path, bytes.NewReader(raw))
		req.Header.Set("X-API-Key", "secret")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("POST %s: decode: %v", path, err)
		}
		if resp.StatusCode >= 300 {
			t.Fatalf("POST %s: http %d: %v", path, resp.StatusCode, out)
		}
		return out
	}
	getAny := func(path string) any {
		t.Helper()
		req, _ := http.NewRequest("GET", ts.URL+path, nil)
		req.Header.Set("X-API-Key", "secret")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("GET %s: http %d: %s", path, resp.StatusCode, body)
		}
		var out any
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("GET %s: decode: %v", path, err)
		}
		return out
	}

	skel := post("/api/v1/collect/skeleton", map[string]any{
		"regions":    []string{"Кемеровская область - Кузбасс"},
		"transports": []string{"bus", "train"},
		"offline":    true,
	})
	if skel["type"] != "sync_collect_region" || skel["kind"] != "skeleton" {
		t.Fatalf("skeleton job payload wrong: %v", skel)
	}
	if _, ok := skel["id"].(float64); !ok {
		t.Fatalf("skeleton job id missing: %v", skel)
	}

	bad := func() int {
		raw, _ := json.Marshal(map[string]any{"offline": true})
		req, _ := http.NewRequest("POST", ts.URL+"/api/v1/collect/skeleton", bytes.NewReader(raw))
		req.Header.Set("X-API-Key", "secret")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}()
	if bad != http.StatusBadRequest {
		t.Fatalf("skeleton без региона: want 400, got %d", bad)
	}

	tripsJob := post("/api/v1/collect/trips", map[string]any{
		"region":  "Кемеровская область - Кузбасс",
		"date":    "2026-09-04",
		"offline": true,
	})
	if tripsJob["kind"] != "trips" || tripsJob["region"] != "Кемеровская область - Кузбасс" {
		t.Fatalf("trips job payload wrong: %v", tripsJob)
	}

	if items, ok := getAny("/api/v1/sync/runs?limit=10").([]any); !ok || len(items) != 0 {
		if ok {
			t.Fatalf("empty store: runs want 0, got %d", len(items))
		}
	}

	runID, err := ms.CreateSyncRun(t.Context(), store.SyncRunRow{PlanID: "p1", Kind: "collect_skeleton", Tag: "test-run", State: "done", Summary: `{"in":10,"written":8,"review":2}`})
	if err != nil {
		t.Fatal(err)
	}
	runs2, ok := getAny("/api/v1/sync/runs").([]any)
	if !ok || len(runs2) != 1 {
		t.Fatalf("runs: want list of 1, got %v", runs2)
	}
	run := runs2[0].(map[string]any)
	if run["ID"].(float64) != float64(runID) || run["Kind"] != "collect_skeleton" || run["State"] != "done" {
		t.Fatalf("run row wrong: %v", run)
	}

	one, ok := getAny(fmt.Sprintf("/api/v1/sync/runs/%d", runID)).(map[string]any)
	if !ok {
		t.Fatalf("GET /sync/runs/%d: не map", runID)
	}
	if one["Tag"] != "test-run" {
		t.Fatalf("run detail wrong: %v", one)
	}

	var sum sync.CollectRegionSummary
	if err := json.Unmarshal([]byte(run["Summary"].(string)), &sum); err != nil {
		t.Fatalf("summary json: %v", err)
	}
	if sum.In != 10 || sum.Written != 8 || sum.Review != 2 {
		t.Fatalf("summary fields wrong: %+v", sum)
	}
}
