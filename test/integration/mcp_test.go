// Интеграционные тесты: вся HTTP+MCP-обвязка in-process через httptest,
// без запуска отдельного процесса. Фиксированные заготовленные сценарии
// находятся в travelmcp/test/common.
package integration

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"

	"travelmcp/internal/config"
	"travelmcp/internal/model"
	"travelmcp/internal/providers"
	"travelmcp/internal/server"
	"travelmcp/internal/telemetry"
	"travelmcp/test/common"
)

func newApp(t *testing.T) (*httptest.Server, *common.MCPClient) {
	reg := providers.NewRegistry([]string{"synth"})
	metrics := telemetry.New()
	cfg := config.Defaults()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ts := httptest.NewServer(server.New(cfg, logger, metrics, reg))
	t.Cleanup(ts.Close)

	client := common.NewMCPClient(ts.URL)
	client.Initialize(t)
	return ts, client
}

func TestHealthEndpoints(t *testing.T) {
	ts, _ := newApp(t)

	var hz map[string]string
	if err := json.Unmarshal(common.Get(t, ts.URL, "/healthz"), &hz); err != nil {
		t.Fatalf("healthz: %v", err)
	}
	if hz["status"] != "ok" {
		t.Fatalf("healthz status = %q", hz["status"])
	}

	var rz struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(common.Get(t, ts.URL, "/readyz"), &rz); err != nil {
		t.Fatalf("readyz: %v", err)
	}
	if rz.Status != "ok" {
		t.Fatalf("readyz status = %q", rz.Status)
	}
}

func TestReadyzDegraded(t *testing.T) {
	reg := providers.NewRegistry(nil)
	reg.Register(brokenProvider{})
	cfg := config.Defaults()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ts := httptest.NewServer(server.New(cfg, logger, telemetry.New(), reg))
	t.Cleanup(ts.Close)

	resp, err := ts.Client().Get(ts.URL + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 503 {
		t.Fatalf("readyz = %d, want 503 when a provider is down", resp.StatusCode)
	}
}

type brokenProvider struct{}

func (brokenProvider) ID() string { return "broken" }
func (brokenProvider) Health() providers.HealthStatus {
	return providers.HealthStatus{Up: false, LastError: "connection refused"}
}
func (brokenProvider) Network() (*model.Network, error) { return model.NewNetwork(), nil }

func TestProvidersAndDashboardAPI(t *testing.T) {
	ts, client := newApp(t)

	var prov map[string]providers.HealthStatus
	if err := json.Unmarshal(common.Get(t, ts.URL, "/api/v1/providers"), &prov); err != nil {
		t.Fatalf("providers: %v", err)
	}
	synth, ok := prov["synth"]
	if !ok {
		t.Fatal("synth provider missing")
	}
	if !synth.Up || synth.Records <= 0 {
		t.Fatalf("synth not up: %+v", synth)
	}

	common.AssertGroundJourney(t, client)

	var dash struct {
		Counters []telemetry.Named `json:"counters"`
	}
	if err := json.Unmarshal(common.Get(t, ts.URL, "/api/v1/dashboard"), &dash); err != nil {
		t.Fatalf("dashboard: %v", err)
	}
	found := false
	for _, c := range dash.Counters {
		if c.Name == "planner.planned" && c.Value == 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("planner.planned counter not 1: %+v", dash.Counters)
	}
}

func TestMCPToolsListed(t *testing.T) {
	_, client := newApp(t)
	names := client.ToolNames(t)
	want := map[string]bool{"find_route": false, "list_providers": false}
	for _, n := range names {
		if _, ok := want[n]; ok {
			want[n] = true
		}
	}
	for n, ok := range want {
		if !ok {
			t.Fatalf("tool %q not listed: %v", n, names)
		}
	}
}

func TestFindRouteGround(t *testing.T) {
	_, client := newApp(t)
	common.AssertGroundJourney(t, client)
}

func TestFindRouteFlight(t *testing.T) {
	_, client := newApp(t)
	common.AssertFlightJourney(t, client)
}

func TestFindRouteNoRoute(t *testing.T) {
	_, client := newApp(t)
	common.AssertNoRoute(t, client)
}

func TestFindRouteValidation(t *testing.T) {
	_, client := newApp(t)
	common.AssertBadArgs(t, client)
}
