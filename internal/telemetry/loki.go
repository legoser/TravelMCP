package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
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
	errs      *slog.Logger
	failed    int
}

type lokiPushRequest struct {
	Streams []lokiStream `json:"streams"`
}

type lokiStream struct {
	Stream map[string]string `json:"stream"`
	Values []lokiStreamEntry `json:"values"`
}

type lokiStreamEntry struct {
	Timestamp int64
	Line      string
}

func (e lokiStreamEntry) MarshalJSON() ([]byte, error) {
	return json.Marshal([2]any{strconv.FormatInt(e.Timestamp, 10), e.Line})
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
		errs:      base.With("module", "loki"),
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
		h.reportError("loki marshal", err, 0)
		return
	}
	httpReq, err := http.NewRequest("POST", h.url, bytes.NewReader(data))
	if err != nil {
		h.reportError("loki request build", err, 0)
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := h.client.Do(httpReq)
	if err != nil {
		h.reportError("loki push", err, len(batch))
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		h.reportError(fmt.Sprintf("loki push status %d: %s", resp.StatusCode, strings.TrimSpace(string(body))), nil, len(batch))
	}
}

func (h *LokiHandler) reportError(msg string, err error, dropped int) {
	h.mu.Lock()
	h.failed++
	h.mu.Unlock()
	if h.errs != nil {
		if err != nil {
			h.errs.Error(msg, "error", err, "dropped", dropped)
		} else {
			h.errs.Error(msg, "dropped", dropped)
		}
	}
}

func (h *LokiHandler) Write(level slog.Level, msg string, attrs []slog.Attr) {
	parts := []string{msg}
	for _, a := range attrs {
		parts = append(parts, fmt.Sprintf("%s=%v", a.Key, a.Value.Any()))
	}
	entry := lokiStreamEntry{
		Timestamp: time.Now().UnixNano(),
		Line:      strings.Join(parts, " "),
	}
	h.mu.Lock()
	h.batch = append(h.batch, entry)
	flushNow := len(h.batch) >= h.batchMax
	h.mu.Unlock()
	if flushNow {
		h.flush()
	}
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
