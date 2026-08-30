// Package common содержит заготовленные хелперы и фиксированные сценарии
// для интеграционных и smoke-тестов. Ничего не генерируется на лету:
// сценарии описаны явно и исполняются как есть.
package common

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// RepoRoot возвращает корень репозитория (каталог с go.mod).
func RepoRoot() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("runtime.Caller failed")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			panic("go.mod not found above " + filepath.Dir(file))
		}
		dir = parent
	}
}

// Build собирает cmd/mcp-server в bin.
func Build(t *testing.T, bin string) {
	t.Helper()
	if err := BuildE(bin); err != nil {
		t.Fatalf("build mcp-server: %v", err)
	}
}

// BuildE собирает cmd/mcp-server в bin и возвращает ошибку вместо t.Fatal.
func BuildE(bin string) error {
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/mcp-server")
	cmd.Dir = RepoRoot()
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, out)
	}
	return nil
}

// FreePort возвращает свободный порт на локальной машине.
func FreePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

type safeBuffer struct {
	mu sync.Mutex
	bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.String()
}

// StartServer запускает собранный бинарник и возвращает его базовый URL.
// Процесс корректно останавливается по SIGTERM в конце теста.
func StartServer(t *testing.T, bin string, port int) string {
	t.Helper()
	addr := fmt.Sprintf("http://127.0.0.1:%d", port)

	cmd := exec.Command(bin, "-config", filepath.Join(t.TempDir(), "config.missing.yaml"))
	cmd.Env = append(os.Environ(),
		"HTTP_ADDR="+fmt.Sprintf("127.0.0.1:%d", port),
		"PROVIDERS_ENABLED=synth",
		"ADMIN_TOKEN=",
	)
	var logs safeBuffer
	cmd.Stdout = &logs
	cmd.Stderr = &logs

	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}

	t.Cleanup(func() {
		_ = cmd.Process.Signal(os.Interrupt)
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
		if t.Failed() {
			t.Logf("server log:\n%s", logs.String())
		}
	})

	waitHealthy(t, addr, &logs)
	return addr
}

func waitHealthy(t *testing.T, addr string, logs *safeBuffer) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	client := &http.Client{Timeout: time.Second}
	for time.Now().Before(deadline) {
		resp, err := client.Get(addr + "/healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("server at %s not healthy; log:\n%s", addr, logs.String())
}

// Get выполняет GET-запрос и возвращает тело при статусе 200.
func Get(t *testing.T, base, path string) []byte {
	t.Helper()
	resp, err := http.Get(base + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: http %d: %s", path, resp.StatusCode, body)
	}
	return body
}

// MCPClient — минимальный JSON-RPC клиент поверх Streamable HTTP.
type MCPClient struct {
	Base   string
	Key    string
	client *http.Client
	mu     sync.Mutex
	sid    string
	id     int
}

func NewMCPClient(base string) *MCPClient {
	return &MCPClient{Base: base, client: &http.Client{Timeout: 15 * time.Second}}
}

func (c *MCPClient) do(method string, params any) (int, []byte, error) {
	c.mu.Lock()
	c.id++
	id := c.id
	sid := c.sid
	c.mu.Unlock()

	payload := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		payload["params"] = params
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return 0, nil, err
	}

	req, err := http.NewRequest(http.MethodPost, c.Base+"/mcp", bytes.NewReader(raw))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.Key != "" {
		req.Header.Set("X-API-Key", c.Key)
	}
	if sid != "" {
		req.Header.Set("Mcp-Session-Id", sid)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if ns := resp.Header.Get("Mcp-Session-Id"); ns != "" {
		c.mu.Lock()
		c.sid = ns
		c.mu.Unlock()
	}
	return resp.StatusCode, body, nil
}

// Initialize проходит рукопожатие MCP (неявные сессии по заголовку).
func (c *MCPClient) Initialize(t *testing.T) {
	t.Helper()
	_, _, err := c.do("initialize", map[string]any{
		"protocolVersion": "2025-03-26",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "travelmcp-test", "version": "0.1.0"},
	})
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
}

func (c *MCPClient) callOK(t *testing.T, method string, params any) json.RawMessage {
	t.Helper()
	status, body, err := c.do(method, params)
	if err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	if status != http.StatusOK {
		t.Fatalf("%s: http %d: %s", method, status, body)
	}
	var rpc struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &rpc); err != nil {
		t.Fatalf("%s: bad json-rpc: %v", method, err)
	}
	if rpc.Error != nil {
		t.Fatalf("%s: rpc error: %s", method, rpc.Error.Message)
	}
	return rpc.Result
}

// ToolNames возвращает имена доступных инструментов MCP.
func (c *MCPClient) ToolNames(t *testing.T) []string {
	t.Helper()
	res := c.callOK(t, "tools/list", map[string]any{})
	var list struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(res, &list); err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	names := make([]string, 0, len(list.Tools))
	for _, tl := range list.Tools {
		names = append(names, tl.Name)
	}
	return names
}

// CallTool вызывает инструмент и возвращает склеенный текст результата.
func (c *MCPClient) CallTool(t *testing.T, name string, args map[string]any) (text string, isError bool) {
	t.Helper()
	res := c.callOK(t, "tools/call", map[string]any{"name": name, "arguments": args})
	var rr struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(res, &rr); err != nil {
		t.Fatalf("tools/call %s: %v", name, err)
	}
	for _, c := range rr.Content {
		text += c.Text
	}
	return text, rr.IsError
}

// ——— Заготовленные сценарии ———

func assertJourney(t *testing.T, text string) JourneyView {
	t.Helper()
	var j JourneyView
	if err := json.Unmarshal([]byte(text), &j); err != nil {
		t.Fatalf("journey json: %v", err)
	}
	return j
}

type JourneyView struct {
	Arrival   time.Time `json:"arrival"`
	Transfers int       `json:"transfers"`
	Legs      []LegView `json:"legs"`
}

type LegView struct {
	Mode string       `json:"mode"`
	From LegPointView `json:"from"`
	To   LegPointView `json:"to"`
}

type LegPointView struct {
	StopID string  `json:"stop_id"`
	Name   string  `json:"name"`
	Lat    float64 `json:"lat"`
	Lon    float64 `json:"lon"`
}

// AssertGroundJourney — фиксированный сценарий: Пермь → Екатеринбург наземкой (06:00).
func AssertGroundJourney(t *testing.T, c *MCPClient) {
	t.Helper()
	text, isErr := c.CallTool(t, "find_route", map[string]any{
		"from_lat": 58.0135, "from_lon": 56.2495,
		"to_lat": 56.84, "to_lon": 60.607,
		"departure": "2026-08-30T06:00:00Z",
	})
	if isErr {
		t.Fatalf("ground route failed: %s", text)
	}
	assertGround(t, text)
}

// AssertPlaceJourney — сценарий по населённым пунктам: «Пермь» → «Екатеринбург»
// (точки резолвятся в автовокзалы, межгород 900 без пересадок).
func AssertPlaceJourney(t *testing.T, c *MCPClient) {
	t.Helper()
	text, isErr := c.CallTool(t, "find_route", map[string]any{
		"from_place": "Пермь",
		"to_place":   "Екатеринбург",
		"departure":  "2026-08-30T06:00:00Z",
	})
	if isErr {
		t.Fatalf("place route failed: %s", text)
	}
	j := assertJourney(t, text)
	if want := "2026-08-30T08:30:00Z"; !j.Arrival.Equal(time.Date(2026, 8, 30, 8, 30, 0, 0, time.UTC)) {
		t.Fatalf("arrival = %s, want %s", j.Arrival.Format(time.RFC3339), want)
	}
	if len(j.Legs) != 3 {
		t.Fatalf("legs = %d, want 3 (walk, intercity 900, walk)", len(j.Legs))
	}
	if j.Transfers != 0 {
		t.Fatalf("transfers = %d, want 0", j.Transfers)
	}
}

func assertGround(t *testing.T, text string) {
	t.Helper()
	j := assertJourney(t, text)
	if want := "2026-08-30T09:36:00Z"; !j.Arrival.Equal(time.Date(2026, 8, 30, 9, 36, 0, 0, time.UTC)) {
		t.Fatalf("arrival = %s, want %s", j.Arrival.Format(time.RFC3339), want)
	}
	if len(j.Legs) != 5 {
		t.Fatalf("legs = %d, want 5 (walk, bus a, intercity 900, walk, walk)", len(j.Legs))
	}
	if j.Transfers != 2 {
		t.Fatalf("transfers = %d, want 2", j.Transfers)
	}
}

// AssertFlightJourney — сценарий с перелётом: аэропорт → аэропорт (07:00).
func AssertFlightJourney(t *testing.T, c *MCPClient) {
	t.Helper()
	text, isErr := c.CallTool(t, "find_route", map[string]any{
		"from_lat": 57.9148, "from_lon": 56.0217,
		"to_lat": 56.7431, "to_lon": 60.8028,
		"departure": "2026-08-30T07:00:00Z",
	})
	if isErr {
		t.Fatalf("flight route failed: %s", text)
	}
	j := assertJourney(t, text)
	if j.Transfers != 0 {
		t.Fatalf("transfers = %d, want 0", j.Transfers)
	}
	var flight bool
	for _, l := range j.Legs {
		if l.Mode == "flight" {
			flight = true
			if l.From.StopID != "a-apt" || l.To.StopID != "b-apt" {
				t.Fatalf("flight leg %q -> %q, want a-apt -> b-apt", l.From.StopID, l.To.StopID)
			}
		}
	}
	if !flight {
		t.Fatal("expected a flight leg")
	}
	if want := time.Date(2026, 8, 30, 9, 5, 0, 0, time.UTC); !j.Arrival.Equal(want) {
		t.Fatalf("arrival = %s, want %s", j.Arrival.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}

// AssertUnknownPlace — населённый пункт вне газетира.
func AssertUnknownPlace(t *testing.T, c *MCPClient) {
	t.Helper()
	text, isErr := c.CallTool(t, "find_route", map[string]any{
		"from_place": "Пермь",
		"to_place":   "Нигдегород",
		"departure":  "2026-08-30T06:00:00Z",
	})
	if !isErr {
		t.Fatalf("expected unknown place error, got %s", text)
	}
	if !strings.Contains(text, "Нигдегород") {
		t.Fatalf("error должен упоминать название места, got %s", text)
	}
}

// AssertNoRoute — сценарий белого пятна: кластер C не связан с кластерами A/B.
func AssertNoRoute(t *testing.T, c *MCPClient) {
	t.Helper()
	text, isErr := c.CallTool(t, "find_route", map[string]any{
		"from_lat": 58.003, "from_lon": 56.285,
		"to_lat": 56.84, "to_lon": 60.607,
		"departure": "2026-08-30T06:00:00Z",
	})
	if !isErr {
		t.Fatalf("expected error for disconnected cluster, got %s", text)
	}
}

// AssertBadArgs — валидация обязательных аргументов инструмента.
func AssertBadArgs(t *testing.T, c *MCPClient) {
	t.Helper()
	text, isErr := c.CallTool(t, "find_route", map[string]any{
		"from_lon": 56.2495,
		"to_lat":   56.84,
		"to_lon":   60.607,
	})
	if !isErr {
		t.Fatalf("expected validation error, got %s", text)
	}
}
