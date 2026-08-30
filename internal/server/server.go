package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	mcpserver "github.com/mark3labs/mcp-go/server"

	"travelmcp/internal/config"
	"travelmcp/internal/mcp"
	"travelmcp/internal/planner"
	"travelmcp/internal/providers"
	"travelmcp/internal/telemetry"
)

type Server struct {
	cfg      *config.Config
	metrics  *telemetry.Metrics
	registry *providers.Registry
	logger   *slog.Logger
	started  time.Time
}

func New(cfg *config.Config, logger *slog.Logger, metrics *telemetry.Metrics, registry *providers.Registry) http.Handler {
	s := &Server{
		cfg:      cfg,
		metrics:  metrics,
		registry: registry,
		logger:   logger,
		started:  time.Now(),
	}

	app := mcp.New(planner.New(metrics), registry)
	mcpHandler := mcpserver.NewStreamableHTTPServer(app.Server(), mcpserver.WithStateLess(true))

	token := cfg.Auth.AdminToken
	mux := http.NewServeMux()
	mux.Handle("GET /healthz", http.HandlerFunc(s.handleHealthz))
	mux.Handle("GET /readyz", http.HandlerFunc(s.handleReadyz))
	mux.Handle("GET /api/v1/providers", apiKey(token, http.HandlerFunc(s.handleProviders)))
	mux.Handle("GET /api/v1/dashboard", apiKey(token, http.HandlerFunc(s.handleDashboard)))
	mux.Handle("/mcp", apiKey(token, mcpHandler))

	return mux
}

func apiKey(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token == "" {
			next.ServeHTTP(w, r)
			return
		}
		key := ""
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			key = strings.TrimPrefix(h, "Bearer ")
		} else if h := r.Header.Get("X-API-Key"); h != "" {
			key = h
		}
		if key == "" || key != token {
			w.Header().Set("WWW-Authenticate", `Bearer realm="travelmcp"`)
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized", "message": "требуется API-ключ: заголовок Authorization: Bearer <key> или X-API-Key"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	allUp := true
	for id, st := range s.registry.HealthStatuses() {
		if !st.Up {
			allUp = false
			s.logger.Warn("provider not up", "provider", id)
		}
	}
	if !allUp {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "degraded"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "uptime_seconds": int64(time.Since(s.started).Seconds())})
}

func (s *Server) handleProviders(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.registry.HealthStatuses())
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"uptime_seconds": int64(time.Since(s.started).Seconds()),
		"providers":      s.registry.HealthStatuses(),
		"counters":       s.metrics.Named(),
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("write json", "error", err)
	}
}
