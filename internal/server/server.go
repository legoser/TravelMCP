package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"

	mcpserver "github.com/mark3labs/mcp-go/server"
	"golang.org/x/crypto/bcrypt"

	"travelmcp/internal/config"
	"travelmcp/internal/mcp"
	"travelmcp/internal/planner"
	"travelmcp/internal/providers"
	"travelmcp/internal/store"
	"travelmcp/internal/telemetry"
)

type Server struct {
	cfg      *config.Config
	metrics  *telemetry.Metrics
	registry *providers.Registry
	logger   *slog.Logger
	started  time.Time
	store    store.Store
}

func New(cfg *config.Config, logger *slog.Logger, metrics *telemetry.Metrics, registry *providers.Registry) http.Handler {
	return NewWithStore(cfg, logger, metrics, registry, nil)
}

func NewWithStore(cfg *config.Config, logger *slog.Logger, metrics *telemetry.Metrics, registry *providers.Registry, st store.Store) http.Handler {
	s := &Server{
		cfg:      cfg,
		metrics:  metrics,
		registry: registry,
		logger:   logger,
		started:  time.Now(),
		store:    st,
	}

	app := mcp.NewWithStore(planner.NewWithLogger(metrics, cfg.Planner.Engine, logger), registry, st, logger)
	mcpHandler := mcpserver.NewStreamableHTTPServer(app.Server(), mcpserver.WithStateLess(true))

	mux := http.NewServeMux()
	mux.Handle("GET /healthz", http.HandlerFunc(s.handleHealthz))
	mux.Handle("GET /readyz", http.HandlerFunc(s.handleReadyz))
	mux.Handle("POST /api/v1/register", http.HandlerFunc(s.handleRegister))
	mux.Handle("POST /api/v1/login", http.HandlerFunc(s.handleLogin))
	mux.Handle("GET /api/v1/providers", s.auth(http.HandlerFunc(s.handleProviders), "mcp:read"))
	mux.Handle("GET /api/v1/dashboard", s.auth(http.HandlerFunc(s.handleDashboard), "mcp:read"))
	mux.Handle("GET /api/v1/users", s.auth(http.HandlerFunc(s.handleListUsers), "admin"))
	mux.Handle("POST /api/v1/users/{id}/moderate", s.auth(http.HandlerFunc(s.handleModerateUser), "admin"))
	mux.Handle("GET /api/v1/keys", s.auth(http.HandlerFunc(s.handleListKeys), "mcp:read"))
	mux.Handle("POST /api/v1/keys", s.auth(http.HandlerFunc(s.handleCreateKey), "mcp:read"))
	mux.Handle("DELETE /api/v1/keys/{id}", s.auth(http.HandlerFunc(s.handleDeleteKey), "mcp:read"))
	mux.Handle("GET /api/v1/me", s.auth(http.HandlerFunc(s.handleMe), "mcp:read"))
	mux.Handle("PUT /api/v1/users/{id}/config", s.auth(http.HandlerFunc(s.handleUpdateUserConfig), "admin"))
	mux.Handle("GET /admin", s.auth(http.HandlerFunc(s.handleAdminPage), "admin"))
	mux.Handle("/mcp", s.auth(mcpHandler, "mcp:read"))

	return mux
}

type ctxKey string

const ctxUserKey ctxKey = "user"

func (s *Server) auth(next http.Handler, requiredScope string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.Auth.AdminToken == "" {
			s.logger.Warn("auth open-mode: ADMIN_TOKEN empty")
			next.ServeHTTP(w, r)
			return
		}
		key := ""
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			key = strings.TrimPrefix(h, "Bearer ")
		} else if h := r.Header.Get("X-API-Key"); h != "" {
			key = h
		}
		if s.cfg.Auth.AdminToken != "" && subtle.ConstantTimeCompare([]byte(key), []byte(s.cfg.Auth.AdminToken)) == 1 {
			ctx := context.WithValue(r.Context(), ctxUserKey, &store.UserRow{ID: 0, Email: "admin", Role: "admin", Status: "active"})
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		if key == "" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="travelmcp"`)
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized", "message": "API key required: Authorization: Bearer <key> or X-API-Key"})
			return
		}
		if s.store == nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="travelmcp"`)
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized", "message": "invalid API key"})
			return
		}
		ak, ok := s.store.GetApiKey(r.Context(), key)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized", "message": "invalid API key"})
			return
		}
		user, ok := s.store.GetUserByID(r.Context(), ak.UserID)
		if !ok || user.Status != "active" {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "forbidden", "message": "user not active or pending moderation"})
			return
		}
		if requiredScope != "" && !hasScope(ak.Scopes, requiredScope) && user.Role != "admin" {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "forbidden", "message": "insufficient scope"})
			return
		}
		_ = s.store.TouchApiKey(r.Context(), key)
		ctx := context.WithValue(r.Context(), ctxUserKey, &user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func hasScope(scopes, required string) bool {
	for _, s := range strings.Split(scopes, ",") {
		if strings.TrimSpace(s) == required {
			return true
		}
	}
	return false
}

func hashPassword(pw string) string {
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		panic(err)
	}
	return string(h)
}

func checkPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	statuses := s.registry.HealthStatusesCached()
	if len(statuses) == 0 {
		statuses = s.registry.HealthStatuses()
	}
	allUp := true
	for id, st := range statuses {
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

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "storage disabled"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	if _, err := mail.ParseAddress(req.Email); err != nil || req.Email == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "valid email required"})
		return
	}
	if len(req.Password) < 8 || len(req.Password) > 72 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "password 8..72 required"})
		return
	}
	if _, ok := s.store.GetUserByEmail(r.Context(), req.Email); ok {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "user already exists"})
		return
	}
	role := "user"
	id, err := s.store.CreateUser(r.Context(), req.Email, hashPassword(req.Password), role)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "create failed"})
		return
	}
	user, _ := s.store.GetUserByID(r.Context(), id)
	s.logger.Info("user registered", "email", req.Email, "id", id, "status", user.Status)
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "email": req.Email, "status": user.Status, "message": "pending moderation"})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "storage disabled"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	user, ok := s.store.GetUserByEmail(r.Context(), req.Email)
	if !ok || !checkPassword(user.PassHash, req.Password) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid credentials"})
		return
	}
	if user.Status != "active" {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "user not active", "status": user.Status})
		return
	}
	// create or reuse key
	keys, _ := s.store.ListApiKeys(r.Context(), user.ID)
	var ak store.ApiKeyRow
	if len(keys) > 0 {
		ak = keys[0]
	} else {
		var err error
		ak, err = s.store.CreateApiKey(r.Context(), user.ID, "mcp:read")
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "key create failed"})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": ak.Key, "scopes": ak.Scopes, "user_id": user.ID})
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "storage disabled"})
		return
	}
	users, err := s.store.ListUsers(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "list failed"})
		return
	}
	out := make([]map[string]any, 0, len(users))
	for _, u := range users {
		out = append(out, map[string]any{"id": u.ID, "email": u.Email, "status": u.Status, "role": u.Role, "created_at": u.CreatedAt})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleModerateUser(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "storage disabled"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	var req struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}
	if req.Status != "active" && req.Status != "blocked" && req.Status != "pending" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "status must be active|blocked|pending"})
		return
	}
	if err := s.store.UpdateUserStatus(r.Context(), id, req.Status); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "user not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": req.Status})
}

func (s *Server) handleListKeys(w http.ResponseWriter, r *http.Request) {
	user, ok := r.Context().Value(ctxUserKey).(*store.UserRow)
	if !ok || user == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	keys, err := s.store.ListApiKeys(r.Context(), user.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "list failed"})
		return
	}
	writeJSON(w, http.StatusOK, keys)
}

func (s *Server) handleCreateKey(w http.ResponseWriter, r *http.Request) {
	user, ok := r.Context().Value(ctxUserKey).(*store.UserRow)
	if !ok || user == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()
	var req struct {
		Scopes string `json:"scopes"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if len(req.Scopes) > 256 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "scopes too large"})
		return
	}
	if req.Scopes != "" && req.Scopes != "mcp:read" && req.Scopes != "admin" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid scopes"})
		return
	}
	if req.Scopes == "admin" && user.Role != "admin" {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "admin scope requires admin role"})
		return
	}
	ak, err := s.store.CreateApiKey(r.Context(), user.ID, req.Scopes)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "create failed"})
		return
	}
	writeJSON(w, http.StatusCreated, ak)
}

func (s *Server) handleDeleteKey(w http.ResponseWriter, r *http.Request) {
	user, ok := r.Context().Value(ctxUserKey).(*store.UserRow)
	if !ok || user == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	if err := s.store.DeleteApiKey(r.Context(), id, user.ID); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": id})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user, ok := r.Context().Value(ctxUserKey).(*store.UserRow)
	if !ok || user == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": user.ID, "email": user.Email, "status": user.Status, "role": user.Role, "config": user.Config})
}

func (s *Server) handleUpdateUserConfig(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	var req struct {
		Config string `json:"config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}
	if len(req.Config) > 8192 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "config too large"})
		return
	}
	if err := s.store.UpdateUserConfig(r.Context(), id, req.Config); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "user not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "config": req.Config})
}

func (s *Server) handleAdminPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><html><head><title>TravelMCP Admin</title><style>body{font-family:sans-serif;margin:40px}table{border-collapse:collapse}td,th{border:1px solid #ccc;padding:8px}</style></head><body><h1>TravelMCP Admin</h1><p>Uptime: %d s</p><h2>Providers</h2><pre>%s</pre><h2>Metrics</h2><pre>%s</pre><p>API: <a href="/api/v1/providers">/api/v1/providers</a> | <a href="/api/v1/dashboard">/api/v1/dashboard</a> | <a href="/api/v1/users">/api/v1/users</a> | <a href="/api/v1/me">/api/v1/me</a></p></body></html>`,
		int64(time.Since(s.started).Seconds()),
		html.EscapeString(toJSON(s.registry.HealthStatuses())),
		html.EscapeString(toJSON(s.metrics.Named())),
	)
}

func toJSON(v any) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("write json", "error", err)
	}
}
