package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"travelmcp/internal/config"
)

func boolPtr(b bool) *bool { return &b }

func TestLokiHandlerReceivesLogsWithTraceID(t *testing.T) {
	type pushPayload struct {
		Streams []struct {
			Stream map[string]string `json:"stream"`
			Values [][2]string       `json:"values"`
		} `json:"streams"`
	}
	var sent []pushPayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body pushPayload
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
	first := sent[0]
	if len(first.Streams) == 0 || len(first.Streams[0].Values) == 0 {
		t.Fatalf("malformed push payload: %+v", first)
	}
	ts := first.Streams[0].Values[0][0]
	line := first.Streams[0].Values[0][1]
	if _, err := strconv.ParseInt(ts, 10, 64); err != nil {
		t.Fatalf("loki timestamp must be nanoseconds integer, got %q: %v", ts, err)
	}
	if !strings.Contains(line, "test message") || !strings.Contains(line, "trace_id=traceABC") {
		t.Fatalf("line must contain message and trace_id, got %q", line)
	}
}

func TestLokiHandlerReportsPushErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"bad timestamp"}`))
	}))
	defer srv.Close()

	var buf bytes.Buffer
	base := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	cfg := config.LokiLog{URL: srv.URL, Enabled: boolPtr(true), BatchSize: 1, BatchWait: "100ms"}
	lg := NewLokiHandler(cfg, base)
	lg.Info("will fail")

	time.Sleep(500 * time.Millisecond)
	out := buf.String()
	if !strings.Contains(out, "loki push status 400") {
		t.Fatalf("push error must be logged to stderr handler, got: %s", out)
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
