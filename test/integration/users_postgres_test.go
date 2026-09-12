// Интеграционный тест users/api_keys на реальном PostgresStore
// (issue #19: методы были заглушками errNotImplemented — регистрация
// и вкладка «пользователи» падали на postgres-хранилище).
// Пропускается без TRAVELMCP_TEST_DSN — в CI/dev DSN задан в .env.
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
	"time"

	"travelmcp/internal/config"
	"travelmcp/internal/providers"
	"travelmcp/internal/server"
	"travelmcp/internal/store/postgres"
	"travelmcp/internal/telemetry"
)

func testDSN() string {
	if d := os.Getenv("TRAVELMCP_TEST_DSN"); d != "" {
		return d
	}
	return os.Getenv("DATABASE_DSN")
}

func newPostgresApp(t *testing.T) *httptest.Server {
	t.Helper()
	dsn := testDSN()
	if dsn == "" {
		t.Skip("postgres dsn not configured (TRAVELMCP_TEST_DSN/DATABASE_DSN)")
	}
	ps, err := postgres.NewPostgresStore(t.Context(), dsn)
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	t.Cleanup(func() { _ = ps.Close() })
	if err := ps.Migrate(t.Context()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := config.Defaults()
	cfg.Auth.AdminToken = "secret"
	reg := providers.NewRegistry([]string{"synth"})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ts := httptest.NewServer(server.NewWithStore(cfg, logger, telemetry.New(), reg, ps))
	t.Cleanup(ts.Close)
	return ts
}

func doJSON(t *testing.T, method, url, token string, body any) (int, []byte) {
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
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
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

// TestPostgresUsersCRUD повторяет сценарий бага: регистрация → список
// пользователей (вкладка «пользователи») → модерация → ключи → удаление.
func TestPostgresUsersCRUD(t *testing.T) {
	ts := newPostgresApp(t)
	base := ts.URL
	email := fmt.Sprintf("it-user-%d@example.test", time.Now().UnixNano())

	// регистрация
	code, body := doJSON(t, "POST", base+"/api/v1/register", "", map[string]string{"email": email, "password": "secret-pass-123"})
	if code != http.StatusCreated {
		t.Fatalf("register: http %d: %s", code, body)
	}
	var regResp struct {
		ID     int64  `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(body, &regResp); err != nil {
		t.Fatal(err)
	}
	if regResp.Status != "pending" {
		t.Fatalf("register status = %q, want pending", regResp.Status)
	}

	// вкладка «пользователи»: раньше — 500 «метод ещё не реализован»
	code, body = doJSON(t, "GET", base+"/api/v1/users", "secret", nil)
	if code != http.StatusOK {
		t.Fatalf("list users: http %d: %s", code, body)
	}
	if bytes.Contains(body, []byte("не реализован")) {
		t.Fatalf("list users still returns not-implemented: %s", body)
	}
	var users []map[string]any
	if err := json.Unmarshal(body, &users); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, u := range users {
		if u["email"] == email {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("registered user %s not in list (%d users)", email, len(users))
	}

	// повторная регистрация — конфликт
	if code, _ := doJSON(t, "POST", base+"/api/v1/register", "", map[string]string{"email": email, "password": "secret-pass-123"}); code != http.StatusConflict {
		t.Fatalf("re-register: http %d, want 409", code)
	}

	// вход до активации запрещён
	if code, _ := doJSON(t, "POST", base+"/api/v1/login", "", map[string]string{"email": email, "password": "secret-pass-123"}); code != http.StatusForbidden {
		t.Fatalf("login before moderation: http %d, want 403", code)
	}

	// модерация
	if code, body := doJSON(t, "POST", fmt.Sprintf("%s/api/v1/users/%d/moderate", base, regResp.ID), "secret", map[string]string{"status": "active"}); code != http.StatusOK {
		t.Fatalf("moderate: http %d: %s", code, body)
	}

	// вход — выдаёт ключ
	code, body = doJSON(t, "POST", base+"/api/v1/login", "", map[string]string{"email": email, "password": "secret-pass-123"})
	if code != http.StatusOK {
		t.Fatalf("login: http %d: %s", code, body)
	}
	var loginResp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &loginResp); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(loginResp.Token, "tm_") {
		t.Fatalf("login token = %q, want tm_ prefix", loginResp.Token)
	}

	// ключ работает (me)
	if code, _ := doJSON(t, "GET", base+"/api/v1/me", loginResp.Token, nil); code != http.StatusOK {
		t.Fatalf("me with issued key: http %d", code)
	}

	// админские ключи пользователя
	code, body = doJSON(t, "POST", fmt.Sprintf("%s/api/v1/users/%d/keys", base, regResp.ID), "secret", map[string]string{"scopes": "mcp:read"})
	if code != http.StatusCreated {
		t.Fatalf("create user key: http %d: %s", code, body)
	}
	var newKey struct {
		ID  int64  `json:"id"`
		Key string `json:"key"`
	}
	if err := json.Unmarshal(body, &newKey); err != nil {
		t.Fatal(err)
	}
	if code, body = doJSON(t, "GET", fmt.Sprintf("%s/api/v1/users/%d/keys", base, regResp.ID), "secret", nil); code != http.StatusOK {
		t.Fatalf("list user keys: http %d: %s", code, body)
	} else if !bytes.Contains(body, []byte(newKey.Key[:8])) {
		t.Fatalf("created key not listed: %s", body)
	}
	// ключ живёт в auth-пути
	if code, _ := doJSON(t, "GET", base+"/api/v1/me", newKey.Key, nil); code != http.StatusOK {
		t.Fatalf("me with admin-created key: http %d", code)
	}
	// TouchApiKey обновляет last_used (auth-путь трогает ключ)
	if code, body = doJSON(t, "GET", fmt.Sprintf("%s/api/v1/users/%d/keys", base, regResp.ID), "secret", nil); code != http.StatusOK {
		t.Fatalf("list keys after touch: http %d: %s", code, body)
	}
	var keysAfter []struct {
		Key      string `json:"Key"`
		LastUsed int64  `json:"LastUsed"`
	}
	if err := json.Unmarshal(body, &keysAfter); err != nil {
		t.Fatal(err)
	}
	var keyTouched bool
	for _, k := range keysAfter {
		if k.Key == newKey.Key && k.LastUsed > 0 {
			keyTouched = true
		}
	}
	if !keyTouched {
		t.Fatalf("admin-created key LastUsed not updated: %s", body)
	}

	// удаление ключа
	if code, body = doJSON(t, "DELETE", fmt.Sprintf("%s/api/v1/users/%d/keys/%d", base, regResp.ID, newKey.ID), "secret", nil); code != http.StatusOK {
		t.Fatalf("delete user key: http %d: %s", code, body)
	}
	if code, _ := doJSON(t, "GET", base+"/api/v1/me", newKey.Key, nil); code != http.StatusUnauthorized {
		t.Fatalf("me with deleted key: http %d, want 401", code)
	}

	// смена роли и удаление пользователя
	if code, body = doJSON(t, "PATCH", fmt.Sprintf("%s/api/v1/users/%d", base, regResp.ID), "secret", map[string]any{"role": "admin"}); code != http.StatusOK {
		t.Fatalf("patch user role: http %d: %s", code, body)
	}
	if code, body = doJSON(t, "DELETE", fmt.Sprintf("%s/api/v1/users/%d", base, regResp.ID), "secret", nil); code != http.StatusOK {
		t.Fatalf("delete user: http %d: %s", code, body)
	}
	if code, _ := doJSON(t, "GET", fmt.Sprintf("%s/api/v1/users/%d", base, regResp.ID), "secret", nil); code != http.StatusNotFound {
		t.Fatalf("get deleted user: http %d, want 404", code)
	}
	if code, _ := doJSON(t, "GET", base+"/api/v1/me", loginResp.Token, nil); code != http.StatusUnauthorized {
		t.Fatalf("me with key of deleted user: http %d, want 401 (cascade)", code)
	}
}
