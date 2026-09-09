package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"travelmcp/internal/config"
)

type LokiHandler struct {
	mu        sync.Mutex
	url       string
	client    *http.Client
	batch     []lokiStreamEntry
	batchMax  int
	batchWait time.Duration
	labels    string
}

type lokiPushRequest struct {
	Streams []lokiStream `json:"streams"`
}

type lokiStream struct {
	Stream map[string]string `json:"stream"`
	Values []lokiStreamEntry `json:"values"`
}

type lokiStreamEntry struct {
	Timestamp string
	Line      string
}

func (e lokiStreamEntry) MarshalJSON() ([]byte, error) {
	return json.Marshal([2]string{e.Timestamp, e.Line})
}

func NewLokiHandler(cfg config.LokiLog, base *slog.Logger) *slog.Logger {
	if cfg.URL == "" || (cfg.Enabled != nil && !*cfg.Enabled) {
		return base
	}
	h := &LokiHandler{
		url:       strings.TrimRight(cfg.URL, "/") + "/loki/api/v1/push",
		client:    &http.Client{Timeout: 10 * time.Second},
		batchMax:  cfg.BatchSize,
		batchWait: parseDur(cfg.BatchWait, 1*time.Second),
		labels:    "service=travelmcp",
	}
	if h.batchMax <= 0 {
		h.batchMax = 100
	}
	go h.flushLoop()
	return slog.New(newLokiSlogHandler(h, base))
}

func parseDur(s string, def time.Duration) time.Duration {
	if s == "" {
		return def
	}
	if d, err := time.ParseDuration(s); err == nil {
		return d
	}
	return def
}

func (h *LokiHandler) flushLoop() {
	t := time.NewTicker(h.batchWait)
	defer t.Stop()
	for range t.C {
		h.flush()
	}
}

func (h *LokiHandler) flush() {
	h.mu.Lock()
	batch := h.batch
	h.batch = nil
	h.mu.Unlock()
	if len(batch) == 0 {
		return
	}
	stream := lokiStream{
		Stream: map[string]string{"service": "travelmcp", "level": "debug"},
		Values: batch,
	}
	req := lokiPushRequest{Streams: []lokiStream{stream}}
	data, err := json.Marshal(req)
	if err != nil {
		return
	}
	httpReq, _ := http.NewRequest("POST", h.url, bytes.NewReader(data))
	httpReq.Header.Set("Content-Type", "application/json")
	_, _ = h.client.Do(httpReq)
}

func (h *LokiHandler) Write(level slog.Level, msg string, attrs []slog.Attr) {
	parts := []string{msg}
	for _, a := range attrs {
		parts = append(parts, fmt.Sprintf("%s=%v", a.Key, a.Value.Any()))
	}
	entry := lokiStreamEntry{
		Timestamp: time.Now().Format(time.RFC3339Nano),
		Line:      strings.Join(parts, " "),
	}
	h.mu.Lock()
	h.batch = append(h.batch, entry)
	if len(h.batch) >= h.batchMax {
		go h.flush()
	}
	h.mu.Unlock()
}

type lokiSlogHandler struct {
	inner *LokiHandler
	next  slog.Handler
	attrs []slog.Attr
}

func newLokiSlogHandler(h *LokiHandler, base *slog.Logger) *lokiSlogHandler {
	return &lokiSlogHandler{inner: h, next: base.Handler()}
}

func (l *lokiSlogHandler) Enabled(_ context.Context, level slog.Level) bool {
	return l.next.Enabled(context.Background(), level)
}

func (l *lokiSlogHandler) Handle(ctx context.Context, r slog.Record) error {
	attrs := make([]slog.Attr, 0, r.NumAttrs()+len(l.attrs))
	attrs = append(attrs, l.attrs...)
	r.Attrs(func(a slog.Attr) bool {
		attrs = append(attrs, a)
		return true
	})
	if tr, ok := ctx.Value(traceIDKey{}).(*traceContext); ok {
		attrs = append(attrs, slog.String("trace_id", tr.TraceID), slog.String("span_id", tr.SpanID))
	}
	l.inner.Write(r.Level, r.Message, attrs)
	return l.next.Handle(ctx, r)
}

func (l *lokiSlogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &lokiSlogHandler{inner: l.inner, next: l.next.WithAttrs(attrs), attrs: append(append([]slog.Attr{}, l.attrs...), attrs...)}
}

func (l *lokiSlogHandler) WithGroup(name string) slog.Handler {
	return &lokiSlogHandler{inner: l.inner, next: l.next.WithGroup(name), attrs: l.attrs}
}

type traceContext struct {
	TraceID string
	SpanID  string
}

type traceIDKey struct{}

func ContextWithTrace(ctx context.Context, traceID, spanID string) context.Context {
	return context.WithValue(ctx, traceIDKey{}, &traceContext{TraceID: traceID, SpanID: spanID})
}
