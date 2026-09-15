package server

// Shared HTTP helpers: JSON responses, middlewares, status recording.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"encoding/json"
	"github.com/google/uuid"
	"log/slog"
	"net/http"

	_ "travelmcp/internal/adapters/nominatim"
	_ "travelmcp/internal/adapters/yandex"
	"travelmcp/internal/store"
	"travelmcp/internal/telemetry"
)

func toJSON(v any) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

func jsonDecodeErrorMessage(err error) string {
	if err == nil {
		return "invalid json"
	}
	if se, ok := err.(*json.SyntaxError); ok {
		return fmt.Sprintf("invalid json at offset %d: %v", se.Offset, err)
	}
	if ue, ok := err.(*json.UnmarshalTypeError); ok {
		return fmt.Sprintf("invalid json: field %q expected %s got %q at offset %d", ue.Field, ue.Type, ue.Value, ue.Offset)
	}
	return fmt.Sprintf("invalid json: %v", err)
}

func writeJSONDecodeError(w http.ResponseWriter, err error, logger *slog.Logger, path string) {
	msg := jsonDecodeErrorMessage(err)
	if logger != nil {
		logger.Warn("invalid json", "path", path, "error", err, "message", msg)
	}
	writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "message": msg, "details": err.Error()})
}

func writeJSONRPCParseError(w http.ResponseWriter, err error) {
	msg := jsonDecodeErrorMessage(err)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      nil,
		"error":   map[string]any{"code": -32700, "message": "Parse error: " + msg},
	})
}

func writeJSONResponse(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Default().Error("write json", "error", err)
	}
}

func (s *Server) mcpJSONValidation(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.Contains(r.Header.Get("Content-Type"), "application/json") {
			body, err := io.ReadAll(io.LimitReader(r.Body, maxMCPBodySize))
			if err != nil {
				writeJSONRPCParseError(w, err)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			if len(body) > 0 && !json.Valid(body) {
				var tmp any
				if err := json.Unmarshal(body, &tmp); err != nil {
					writeJSONRPCParseError(w, err)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func tracingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceID := r.Header.Get("X-Trace-ID")
		spanID := r.Header.Get("X-Span-ID")
		if traceID == "" {
			traceID = uuid.NewString()[:16]
		}
		if spanID == "" {
			spanID = uuid.NewString()[:8]
		}
		w.Header().Set("X-Trace-ID", traceID)
		w.Header().Set("X-Span-ID", spanID)
		ctx := telemetry.ContextWithTrace(r.Context(), traceID, spanID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			id = uuid.NewString()
		}
		w.Header().Set("X-Request-ID", id)
		ctx := context.WithValue(r.Context(), ctxKey("request_id"), id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func metricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(rec, r)
		handler := normalizeRoute(r.URL.Path)
		telemetry.HTTPRequests.WithLabelValues(fmt.Sprint(rec.status), handler).Inc()
		telemetry.HTTPRequestDuration.WithLabelValues(fmt.Sprint(rec.status), handler).Observe(time.Since(start).Seconds())
	})
}

func normalizeRoute(path string) string {
	segs := strings.Split(path, "/")
	for i, s := range segs {
		if _, err := strconv.Atoi(s); err == nil {
			segs[i] = "{id}"
		}
	}
	return strings.Join(segs, "/")
}

func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		reqID := r.Header.Get("X-Request-ID")
		if reqID == "" {
			if v := r.Context().Value(ctxKey("request_id")); v != nil {
				if s, ok := v.(string); ok {
					reqID = s
				}
			}
		}
		var bodyLog string
		if s.logger.Enabled(r.Context(), slog.LevelDebug) && (r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch) {
			if r.Body != nil {
				limited := io.LimitReader(r.Body, maxLogBodySize)
				b, _ := io.ReadAll(limited)
				if len(b) > 0 {
					bodyLog = string(b)
					r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(b), r.Body))
				}
			}
		}
		rec := &statusRecorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(rec, r)
		elapsed := time.Since(start)
		attrs := []any{
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"elapsed_ms", elapsed.Milliseconds(),
			"remote", r.RemoteAddr,
		}
		if reqID != "" {
			attrs = append(attrs, "request_id", reqID)
		}
		if u, ok := r.Context().Value(ctxUserKey).(*store.UserRow); ok && u != nil {
			attrs = append(attrs, "user_id", u.ID, "email", u.Email, "role", u.Role)
		} else if v := r.Context().Value(ctxUserKey); v != nil {
			attrs = append(attrs, "user", fmt.Sprint(v))
		}
		if bodyLog != "" {
			attrs = append(attrs, "body", bodyLog)
		}
		switch {
		case rec.status >= 500:
			s.logger.ErrorContext(r.Context(), "request", attrs...)
		case rec.status >= 400:
			s.logger.WarnContext(r.Context(), "request", attrs...)
		default:
			if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" || r.URL.Path == "/metrics" {
				s.logger.DebugContext(r.Context(), "request", attrs...)
			} else {
				s.logger.InfoContext(r.Context(), "request", attrs...)
			}
		}
		if s.logger.Enabled(r.Context(), slog.LevelDebug) && rec.status >= 400 {
			s.logger.DebugContext(r.Context(), "request debug", "method", r.Method, "path", r.URL.Path, "query", redactedQuery(r.URL.RawQuery), "headers", redactedHeaders(r.Header))
		}
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}
