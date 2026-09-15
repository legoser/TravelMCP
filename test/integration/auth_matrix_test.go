package integration

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"travelmcp/internal/config"
	"travelmcp/internal/providers"
	"travelmcp/internal/server"
	"travelmcp/internal/store/memory"
	"travelmcp/internal/telemetry"
)

func authApp(t *testing.T) (*httptest.Server, *memory.MemoryStore) {
	t.Helper()
	cfg := config.Defaults()
	cfg.Auth.AdminToken = "matrix-admin-token"
	reg := providers.NewRegistry([]string{"synth"})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ms := memory.NewMemoryStore()
	ts := httptest.NewServer(server.NewWithStore(cfg, logger, telemetry.New(), reg, ms))
	t.Cleanup(ts.Close)
	return ts, ms
}

func getStatus(t *testing.T, ts *httptest.Server, path, key string, useXHeader bool) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, ts.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if key != "" {
		if useXHeader {
			req.Header.Set("X-API-Key", key)
		} else {
			req.Header.Set("Authorization", "Bearer "+key)
		}
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func mustKey(t *testing.T, ms *memory.MemoryStore, email, role, status, scopes string) string {
	t.Helper()
	ctx := context.Background()
	id, err := ms.CreateUser(ctx, email, "hash", role)
	if err != nil {
		t.Fatal(err)
	}
	if status != "" {
		if err := ms.UpdateUserStatus(ctx, id, status); err != nil {
			t.Fatal(err)
		}
	}
	ak, err := ms.CreateApiKey(ctx, id, scopes)
	if err != nil {
		t.Fatal(err)
	}
	return ak.Key
}

func TestAuthMatrix(t *testing.T) {
	ts, ms := authApp(t)
	userKey := mustKey(t, ms, "user@x.io", "user", "active", "mcp:read")
	pendingKey := mustKey(t, ms, "pending@x.io", "user", "pending", "mcp:read")
	adminKey := mustKey(t, ms, "admin@x.io", "admin", "active", "mcp:read")
	narrowKey := mustKey(t, ms, "bare@x.io", "user", "active", "other:scope")

	cases := []struct {
		name string
		path string
		key  string
		xhdr bool
		want int
	}{
		{"no key", "/api/v1/providers", "", false, 401},
		{"wrong key", "/api/v1/providers", "tm_wrong", false, 401},
		{"admin token bearer", "/api/v1/users", "matrix-admin-token", false, 200},
		{"admin token x-api-key", "/api/v1/providers", "matrix-admin-token", true, 200},
		{"user key bearer", "/api/v1/providers", userKey, false, 200},
		{"user key x-api-key", "/api/v1/providers", userKey, true, 200},
		{"pending user denied", "/api/v1/providers", pendingKey, false, 403},
		{"user key on admin endpoint", "/api/v1/users", userKey, false, 403},
		{"admin role overrides scope", "/api/v1/users", adminKey, false, 200},
		{"narrow scope denied", "/api/v1/providers", narrowKey, false, 403},
	}
	for _, c := range cases {
		if got := getStatus(t, ts, c.path, c.key, c.xhdr); got != c.want {
			t.Errorf("%s: GET %s = %d, want %d", c.name, c.path, got, c.want)
		}
	}
}
