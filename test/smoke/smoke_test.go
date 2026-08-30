// Smoke-тесты: сборка реального бинарника и запуск отдельного процесса.
// Все сценарии заготовлены заранее в travelmcp/test/common.
package smoke

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"travelmcp/internal/providers"
	"travelmcp/test/common"
)

var (
	binOnce     sync.Once
	binPath     string
	binBuildErr error
)

func builtBinary(t *testing.T) string {
	t.Helper()
	binOnce.Do(func() {
		dir, err := os.MkdirTemp("", "travelmcp-smoke")
		if err != nil {
			binBuildErr = err
			return
		}
		binPath = filepath.Join(dir, "mcp-server")
		binBuildErr = common.BuildE(binPath)
	})
	if binBuildErr != nil {
		t.Fatalf("build mcp-server: %v", binBuildErr)
	}
	return binPath
}

func TestSmokeHealthAPI(t *testing.T) {
	base := common.StartServer(t, builtBinary(t), common.FreePort(t))

	var hz map[string]string
	if err := json.Unmarshal(common.Get(t, base, "/healthz"), &hz); err != nil {
		t.Fatalf("healthz: %v", err)
	}
	if hz["status"] != "ok" {
		t.Fatalf("healthz status = %q", hz["status"])
	}

	var prov map[string]providers.HealthStatus
	if err := json.Unmarshal(common.Get(t, base, "/api/v1/providers"), &prov); err != nil {
		t.Fatalf("providers: %v", err)
	}
	if !prov["synth"].Up {
		t.Fatalf("synth not up: %+v", prov["synth"])
	}

	common.Get(t, base, "/api/v1/dashboard")
	common.Get(t, base, "/readyz")
}

func TestSmokeMCPFlow(t *testing.T) {
	base := common.StartServer(t, builtBinary(t), common.FreePort(t))

	client := common.NewMCPClient(base)

	names := client.ToolNames(t)
	if len(names) == 0 {
		t.Fatal("no tools listed")
	}

	common.AssertGroundJourney(t, client)
	common.AssertPlaceJourney(t, client)
	common.AssertFlightJourney(t, client)
	common.AssertNoRoute(t, client)
	common.AssertBadArgs(t, client)
	common.AssertUnknownPlace(t, client)
}
