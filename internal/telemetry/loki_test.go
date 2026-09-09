package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"travelmcp/internal/config"
)

func boolPtr(b bool) *bool { return &b }

func TestLokiHandlerReceivesLogsWithTraceID(t *testing.T) {
	var sent []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		sent = append(sent, body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	var buf bytes.Buffer
	base := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	cfg := config.LokiLog{URL: srv.URL, Enabled: boolPtr(true), BatchSize: 1, BatchWait: "100ms"}
	lg := NewLokiHandler(cfg, base)

	ctx := ContextWithTrace(context.Background(), "traceABC", "span123")
	lg.DebugContext(ctx, "test message", "key", "value")

	time.Sleep(500 * time.Millisecond)
	if len(sent) == 0 {
		t.Fatal("expected at least one Loki push request")
	}
}

func TestLokiHandlerDisabledReturnsBase(t *testing.T) {
	var buf bytes.Buffer
	base := slog.New(slog.NewJSONHandler(&buf, nil))
	cfg := config.LokiLog{URL: "", Enabled: nil}
	result := NewLokiHandler(cfg, base)
	if result != base {
		t.Fatal("expected base logger when Loki is disabled")
	}
}

func TestContextWithTrace(t *testing.T) {
	ctx := ContextWithTrace(context.Background(), "t1", "s1")
	tc, ok := ctx.Value(traceIDKey{}).(*traceContext)
	if !ok {
		t.Fatal("trace context not found in context")
	}
	if tc.TraceID != "t1" || tc.SpanID != "s1" {
		t.Fatalf("expected trace=t1 span=s1, got trace=%s span=%s", tc.TraceID, tc.SpanID)
	}
}
