// issue #22: отмена заданий сбора из UI — POST /api/v1/jobs/{id}/cancel;
// пагинация/фильтры регионов сбора — GET /api/v1/collect/regions.
package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"travelmcp/internal/config"
	"travelmcp/internal/providers"
	"travelmcp/internal/server"
	"travelmcp/internal/store/memory"
	"travelmcp/internal/telemetry"
)

func doReqJSON(t *testing.T, method, url, token string, body any) (int, []byte) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, rd)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("X-API-Key", token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

func TestJobsCancelEndpoint(t *testing.T) {
	ts, _ := newBrowserApp(t)
	base := ts.URL

	// job в очереди
	code, out := doReqJSON(t, "POST", base+"/api/v1/jobs", "secret", map[string]string{"type": "sync_collect_region", "payload": `{"kind":"trips","region":"Тест"}`})
	if code != http.StatusCreated {
		t.Fatalf("enqueue: http %d: %s", code, out)
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(out, &created); err != nil {
		t.Fatal(err)
	}

	// отмена pending
	code, out = doReqJSON(t, "POST", fmt.Sprintf("%s/api/v1/jobs/%d/cancel", base, created.ID), "secret", nil)
	if code != http.StatusOK {
		t.Fatalf("cancel pending: http %d: %s", code, out)
	}
	if !strings.Contains(string(out), `"cancelled"`) {
		t.Fatalf("cancel response: %s", out)
	}

	// повторная отмена — конфликт с пояснением
	if code, out = doReqJSON(t, "POST", fmt.Sprintf("%s/api/v1/jobs/%d/cancel", base, created.ID), "secret", nil); code != http.StatusBadRequest {
		t.Fatalf("re-cancel: http %d, want 400: %s", code, out)
	}

	// reset возвращает в работу
	if code, out = doReqJSON(t, "POST", fmt.Sprintf("%s/api/v1/jobs/%d/reset", base, created.ID), "secret", nil); code != http.StatusOK {
		t.Fatalf("reset cancelled: http %d: %s", code, out)
	}

	// несуществующий job
	if code, _ = doReqJSON(t, "POST", base+"/api/v1/jobs/999999/cancel", "secret", nil); code != http.StatusBadRequest {
		t.Fatalf("cancel missing: http %d, want 400", code)
	}

	// без админ-ключа — 401
	if code, _ = doReqJSON(t, "POST", fmt.Sprintf("%s/api/v1/jobs/%d/cancel", base, created.ID), "", nil); code != http.StatusUnauthorized {
		t.Fatalf("cancel unauth: http %d, want 401", code)
	}
}

// TestCollectRegionsFilterPaginate — issue #22: ?q= + ?country= +
// limit/offset по регионам дампа, ответ {items,total,countries}.
func TestCollectRegionsFilterPaginate(t *testing.T) {
	dump := `{"countries":[
	{"title":"Россия","regions":[
		{"title":"Кемеровская область - Кузбасс","settlements":[{"title":"Кемерово","stations":[
			{"title":"Кемерово, автовокзал","longitude":86.06,"latitude":55.34,"transport_type":"bus","station_type":"bus_station","codes":{"yandex_code":"k1"}},
			{"title":"Кемерово, ж/д вокзал","longitude":86.07,"latitude":55.35,"transport_type":"train","station_type":"train_station","codes":{"yandex_code":"k2"}}]}]},
		{"title":"Красноярский край","settlements":[{"title":"Красноярск","stations":[
			{"title":"Красноярск, автовокзал","longitude":92.87,"latitude":56.01,"transport_type":"bus","station_type":"bus_station","codes":{"yandex_code":"kr1"}}]}]}]},
	{"title":"Казахстан","regions":[
		{"title":"Карагандинская область","settlements":[{"title":"Караганда","stations":[
			{"title":"Караганда, автовокзал","longitude":73.1,"latitude":49.8,"transport_type":"bus","station_type":"bus_station","codes":{"yandex_code":"q1"}}]}]}]}]}`
	path := t.TempDir() + "/dump.json"
	if err := os.WriteFile(path, []byte(dump), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Auth.AdminToken = "secret"
	cfg.Sync.YandexDumpPath = path
	reg := providers.NewRegistry([]string{"synth"})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ms := memory.NewMemoryStore()
	ts := httptest.NewServer(server.NewWithStore(cfg, logger, telemetry.New(), reg, ms))
	t.Cleanup(ts.Close)
	base := ts.URL + "/api/v1/collect/regions"

	// полный список
	code, out := doReqJSON(t, "GET", base, "secret", nil)
	if code != http.StatusOK {
		t.Fatalf("regions: http %d: %s", code, out)
	}
	var all struct {
		Items     []map[string]any `json:"items"`
		Total     int              `json:"total"`
		Countries []string         `json:"countries"`
	}
	if err := json.Unmarshal(out, &all); err != nil {
		t.Fatal(err)
	}
	if all.Total != 3 || len(all.Items) != 3 {
		t.Fatalf("total=%d items=%d, want 3/3", all.Total, len(all.Items))
	}
	if len(all.Countries) != 2 || all.Countries[0] != "Казахстан" || all.Countries[1] != "Россия" {
		t.Fatalf("countries: %v", all.Countries)
	}
	// страны в items
	if all.Items[0]["country"] != "Казахстан" {
		t.Fatalf("item country: %v", all.Items[0])
	}

	// пагинация: страница 1
	if code, out = doReqJSON(t, "GET", base+"?limit=2&offset=0", "secret", nil); code != http.StatusOK {
		t.Fatalf("page1: http %d", code)
	}
	var pg struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	if err := json.Unmarshal(out, &pg); err != nil {
		t.Fatal(err)
	}
	if len(pg.Items) != 2 || pg.Total != 3 {
		t.Fatalf("page1: items=%d total=%d, want 2/3", len(pg.Items), pg.Total)
	}

	// фильтр по подстроке
	if code, out = doReqJSON(t, "GET", base+"?q=краснояр", "secret", nil); code != http.StatusOK {
		t.Fatalf("q: http %d", code)
	}
	if err := json.Unmarshal(out, &pg); err != nil {
		t.Fatal(err)
	}
	if pg.Total != 1 || pg.Items[0]["region"] != "Красноярский край" {
		t.Fatalf("q=краснояр: %+v", pg)
	}

	// фильтр по стране
	if code, out = doReqJSON(t, "GET", base+"?country=Казахстан", "secret", nil); code != http.StatusOK {
		t.Fatalf("country: http %d", code)
	}
	if err := json.Unmarshal(out, &pg); err != nil {
		t.Fatal(err)
	}
	if pg.Total != 1 || pg.Items[0]["region"] != "Карагандинская область" {
		t.Fatalf("country=Казахстан: %+v", pg)
	}
}
