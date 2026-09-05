package server

import (
	"bytes"
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/mail"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/crypto/bcrypt"

	"travelmcp/internal/config"
	gtfspkg "travelmcp/internal/export/gtfs"
	gtfsperregion "travelmcp/internal/gtfs"
	logfactory "travelmcp/internal/logger"
	"travelmcp/internal/mcp"
	"travelmcp/internal/middleware"
	"travelmcp/internal/model"
	"travelmcp/internal/planner"
	"travelmcp/internal/providers"
	"travelmcp/internal/store"
	"travelmcp/internal/telemetry"
)

//go:embed web
var webFS embed.FS

type Server struct {
	cfg      *config.Config
	metrics  *telemetry.Metrics
	registry *providers.Registry
	logger   *slog.Logger
	started  time.Time
	store    store.Store
	mu       sync.RWMutex
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
	plannerLogger := logger
	mcpLogger := logger
	if cfg != nil {
		lf := logfactory.NewFactory(cfg.Log)
		plannerLogger = lf.For("planner")
		mcpLogger = lf.For("mcp")
		if logger == nil {
			s.logger = lf.For("http")
		}
	}
	app := mcp.NewWithStore(planner.NewWithConfig(metrics, cfg.Planner.Engine, plannerLogger, cfg.Planner.SemaphoreSize, cfg.Planner.SemaphoreEnable), registry, st, mcpLogger)
	mcpHandler := mcpserver.NewStreamableHTTPServer(app.Server(), mcpserver.WithStateLess(true))

	mux := http.NewServeMux()
	mux.Handle("GET /healthz", http.HandlerFunc(s.handleHealthz))
	mux.Handle("GET /readyz", http.HandlerFunc(s.handleReadyz))
	mux.Handle("GET /metrics", promhttp.Handler())
	mux.Handle("POST /api/v1/register", http.HandlerFunc(s.handleRegister))
	mux.Handle("POST /api/v1/login", http.HandlerFunc(s.handleLogin))
	mux.Handle("GET /api/v1/providers", s.auth(http.HandlerFunc(s.handleProviders), "mcp:read"))
	mux.Handle("GET /api/v1/dashboard", s.auth(http.HandlerFunc(s.handleDashboard), "mcp:read"))
	mux.Handle("GET /api/v1/me", s.auth(http.HandlerFunc(s.handleMe), "mcp:read"))
	mux.Handle("GET /api/v1/users", s.auth(http.HandlerFunc(s.handleListUsers), "admin"))
	mux.Handle("GET /api/v1/users/{id}", s.auth(http.HandlerFunc(s.handleGetUser), "admin"))
	mux.Handle("PATCH /api/v1/users/{id}", s.auth(http.HandlerFunc(s.handlePatchUser), "admin"))
	mux.Handle("DELETE /api/v1/users/{id}", s.auth(http.HandlerFunc(s.handleDeleteUser), "admin"))
	mux.Handle("POST /api/v1/users/{id}/moderate", s.auth(http.HandlerFunc(s.handleModerateUser), "admin"))
	mux.Handle("PUT /api/v1/users/{id}/config", s.auth(http.HandlerFunc(s.handleUpdateUserConfig), "admin"))
	mux.Handle("GET /api/v1/users/{id}/keys", s.auth(http.HandlerFunc(s.handleListUserKeys), "admin"))
	mux.Handle("POST /api/v1/users/{id}/keys", s.auth(http.HandlerFunc(s.handleCreateUserKey), "admin"))
	mux.Handle("DELETE /api/v1/users/{id}/keys/{keyId}", s.auth(http.HandlerFunc(s.handleDeleteUserKey), "admin"))
	mux.Handle("GET /api/v1/keys", s.auth(http.HandlerFunc(s.handleListKeys), "mcp:read"))
	mux.Handle("POST /api/v1/keys", s.auth(http.HandlerFunc(s.handleCreateKey), "mcp:read"))
	mux.Handle("DELETE /api/v1/keys/{id}", s.auth(http.HandlerFunc(s.handleDeleteKey), "mcp:read"))
	mux.Handle("GET /api/v1/config", s.auth(http.HandlerFunc(s.handleGetConfig), "admin"))
	mux.Handle("PUT /api/v1/config", s.auth(http.HandlerFunc(s.handlePutConfig), "admin"))
	mux.Handle("GET /api/v1/fares", s.auth(http.HandlerFunc(s.handleFares), "mcp:read"))
	mux.Handle("GET /api/v1/zones", s.auth(http.HandlerFunc(s.handleZones), "mcp:read"))
	mux.Handle("GET /api/v1/gtfs", s.auth(http.HandlerFunc(s.handleGTFS), "mcp:read"))
	mux.Handle("GET /api/v1/review", s.auth(http.HandlerFunc(s.handleReview), "mcp:read"))
	mux.Handle("GET /api/v1/review/export.csv", s.auth(http.HandlerFunc(s.handleReviewExport), "mcp:read"))
	mux.Handle("GET /api/v1/jobs", s.auth(http.HandlerFunc(s.handleListJobs), "admin"))
	mux.Handle("POST /api/v1/jobs", s.auth(http.HandlerFunc(s.handleEnqueueJob), "admin"))
	mux.Handle("GET /api/v1/quotas", s.auth(http.HandlerFunc(s.handleListQuotas), "admin"))
	mux.Handle("POST /api/v1/import/gtfs", s.auth(http.HandlerFunc(s.handleImportGTFS), "admin"))
	mux.Handle("POST /api/v1/import/mintrans", s.auth(http.HandlerFunc(s.handleSyncMintrans), "admin"))
	mux.Handle("POST /api/v1/import/rail", s.auth(http.HandlerFunc(s.handleSyncRail), "admin"))
	mux.Handle("GET /api/v1/admin/imports", s.auth(http.HandlerFunc(s.handleAdminImports), "admin"))
	mux.Handle("GET /api/v1/admin/logs", s.auth(http.HandlerFunc(s.handleAdminImportLogs), "admin"))
	mux.Handle("GET /api/v1/admin/audit", s.auth(http.HandlerFunc(s.handleAdminAudit), "admin"))
	mux.Handle("GET /api/v1/admin/terminals", s.auth(http.HandlerFunc(s.handleAdminListTerminals), "admin"))
	mux.Handle("PUT /api/v1/admin/terminals/{id}", s.auth(http.HandlerFunc(s.handleAdminUpdateTerminal), "admin"))
	mux.Handle("POST /api/v1/admin/external-call", s.auth(http.HandlerFunc(s.handleAdminExternalCall), "admin"))
	mux.Handle("POST /api/v1/route", s.auth(http.HandlerFunc(s.handleRoute), "mcp:read"))
	adminFS, _ := fs.Sub(webFS, "web")
	mux.Handle("GET /admin", http.HandlerFunc(s.handleAdminPage))
	mux.Handle("GET /admin/", http.StripPrefix("/admin/", http.FileServer(http.FS(adminFS))))
	mux.Handle("/mcp", s.auth(s.mcpJSONValidation(mcpHandler), "mcp:read"))

	rl := middleware.NewRateLimiter(cfg.HTTP)
	handler := rl.Middleware(mux)
	handler = s.loggingMiddleware(handler)
	handler = requestIDMiddleware(handler)
	handler = metricsMiddleware(handler)
	return handler
}

type ctxKey string

const ctxUserKey ctxKey = "user"

func (s *Server) auth(next http.Handler, requiredScope string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.logger.Debug("auth check", "path", r.URL.Path, "required", requiredScope, "remote", r.RemoteAddr)
		if s.cfg.Auth.AdminToken == "" {
			s.logger.Warn("auth open-mode: ADMIN_TOKEN empty", "path", r.URL.Path)
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
			s.logger.Info("auth admin token", "path", r.URL.Path, "remote", r.RemoteAddr)
			ctx := context.WithValue(r.Context(), ctxUserKey, &store.UserRow{ID: 0, Email: "admin", Role: "admin", Status: "active"})
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		if key == "" {
			s.logger.Warn("auth missing key", "path", r.URL.Path, "remote", r.RemoteAddr)
			w.Header().Set("WWW-Authenticate", `Bearer realm="travelmcp"`)
			writeJSONResponse(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized", "message": "API key required: Authorization: Bearer <key> or X-API-Key"})
			return
		}
		if s.store == nil {
			s.logger.Warn("auth store disabled", "path", r.URL.Path)
			w.Header().Set("WWW-Authenticate", `Bearer realm="travelmcp"`)
			writeJSONResponse(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized", "message": "invalid API key"})
			return
		}
		ak, ok := s.store.GetApiKey(r.Context(), key)
		if !ok {
			s.logger.Warn("auth invalid key", "path", r.URL.Path, "key_prefix", keyPrefix(key))
			writeJSONResponse(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized", "message": "invalid API key"})
			return
		}
		user, ok := s.store.GetUserByID(r.Context(), ak.UserID)
		if !ok || user.Status != "active" {
			s.logger.Warn("auth inactive user", "path", r.URL.Path, "user_id", ak.UserID, "status", user.Status)
			writeJSONResponse(w, http.StatusForbidden, map[string]any{"error": "forbidden", "message": "user not active or pending moderation"})
			return
		}
		if requiredScope != "" && !hasScope(ak.Scopes, requiredScope) && user.Role != "admin" {
			s.logger.Warn("auth insufficient scope", "path", r.URL.Path, "user", user.Email, "scopes", ak.Scopes, "required", requiredScope)
			writeJSONResponse(w, http.StatusForbidden, map[string]any{"error": "forbidden", "message": "insufficient scope"})
			return
		}
		s.logger.Debug("auth ok", "path", r.URL.Path, "user", user.Email, "role", user.Role, "scopes", ak.Scopes)
		_ = s.store.TouchApiKey(r.Context(), key)
		ctx := context.WithValue(r.Context(), ctxUserKey, &user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func keyPrefix(k string) string {
	if len(k) > 8 {
		return k[:8] + "..."
	}
	return "***"
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
	s.logger.Debug("healthz", "remote", r.RemoteAddr)
	writeJSONResponse(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	s.logger.Debug("readyz check", "remote", r.RemoteAddr)
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
		s.logger.Warn("readyz degraded", "providers", fmt.Sprint(statuses))
		writeJSONResponse(w, http.StatusServiceUnavailable, map[string]any{"status": "degraded"})
		return
	}
	s.logger.Info("readyz ok", "uptime", int64(time.Since(s.started).Seconds()))
	writeJSONResponse(w, http.StatusOK, map[string]any{"status": "ok", "uptime_seconds": int64(time.Since(s.started).Seconds())})
}

func (s *Server) handleProviders(w http.ResponseWriter, r *http.Request) {
	user, _ := r.Context().Value(ctxUserKey).(*store.UserRow)
	s.logger.Info("providers list", "user", userEmail(user), "remote", r.RemoteAddr)
	s.logger.Debug("providers debug", "statuses", fmt.Sprint(s.registry.HealthStatuses()))
	writeJSONResponse(w, http.StatusOK, s.registry.HealthStatuses())
}

func (s *Server) handleFares(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSONResponse(w, http.StatusOK, map[string]any{"fare_attributes": []any{}, "fare_rules": []any{}})
		return
	}
	fa, _ := s.store.ListFareAttributes(r.Context())
	fr, _ := s.store.ListFareRules(r.Context())
	writeJSONResponse(w, http.StatusOK, map[string]any{"fare_attributes": fa, "fare_rules": fr})
}

func (s *Server) handleZones(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSONResponse(w, http.StatusOK, []any{})
		return
	}
	zs, _ := s.store.ListZones(r.Context())
	writeJSONResponse(w, http.StatusOK, zs)
}

func (s *Server) handleGTFS(w http.ResponseWriter, r *http.Request) {
	region := r.URL.Query().Get("region")
	var net *model.Network
	var err error
	if s.store != nil {
		ids := make([]string, 0, len(s.registry.List()))
		for _, p := range s.registry.List() {
			ids = append(ids, p.ID())
		}
		net, err = s.store.LoadNetwork(r.Context(), ids, time.Now())
		if err != nil || net == nil || len(net.Stops) == 0 {
			net = nil
		}
	}
	if net == nil {
		app := mcp.NewWithStore(planner.NewWithConfig(s.metrics, s.cfg.Planner.Engine, s.logger, s.cfg.Planner.SemaphoreSize, s.cfg.Planner.SemaphoreEnable), s.registry, s.store, s.logger)
		_ = app
		muxNet := model.NewNetwork()
		for _, p := range s.registry.List() {
			n, e := p.Network()
			if e != nil {
				continue
			}
			for k, v := range n.Stops {
				muxNet.Stops[k] = v
			}
			for k, v := range n.Routes {
				muxNet.Routes[k] = v
			}
			for k, v := range n.Trips {
				muxNet.Trips[k] = v
			}
			muxNet.Connections = append(muxNet.Connections, n.Connections...)
			muxNet.Transfers = append(muxNet.Transfers, n.Transfers...)
			for k, v := range n.Zones {
				muxNet.Zones[k] = v
			}
			for k, v := range n.FareAttributes {
				muxNet.FareAttributes[k] = v
			}
			muxNet.FareRules = append(muxNet.FareRules, n.FareRules...)
			for k, v := range n.StopZones {
				muxNet.StopZones[k] = v
			}
		}
		net = muxNet
	}
	var data []byte
	var filename string
	if region != "" {
		comp := gtfsperregion.NewCompiler("f-ru")
		data, err = comp.BuildPerRegionFromStore(r.Context(), s.store, region, time.Now())
		if err != nil {
			data, err = gtfsCompile(net)
		}
		filename = gtfsperregion.ArchiveName(region)
	} else {
		data, err = gtfsCompile(net)
		filename = "gtfs.zip"
	}
	if err != nil {
		writeJSONResponse(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) handleReview(w http.ResponseWriter, r *http.Request) {
	writeJSONResponse(w, http.StatusOK, []any{})
}

func (s *Server) handleReviewExport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="review.csv"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("entity_type,entity_id,reason,score,created_at\n"))
}

func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSONResponse(w, http.StatusServiceUnavailable, map[string]any{"error": "storage disabled"})
		return
	}
	jobs, _ := s.store.ListJobs(r.Context(), 50)
	writeJSONResponse(w, http.StatusOK, jobs)
}

func (s *Server) handleEnqueueJob(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSONResponse(w, http.StatusServiceUnavailable, map[string]any{"error": "storage disabled"})
		return
	}
	var req struct {
		Type    string `json:"type"`
		Payload string `json:"payload"`
		Region  string `json:"region"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONDecodeError(w, err, s.logger, r.URL.Path)
		return
	}
	if req.Type == "" {
		req.Type = "import_gtfs"
	}
	payload := req.Payload
	if payload == "" {
		payload = "{}"
	}
	id, err := s.store.EnqueueJob(r.Context(), store.JobRow{Type: req.Type, Payload: payload, Region: req.Region})
	if err != nil {
		writeJSONResponse(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSONResponse(w, http.StatusCreated, map[string]any{"id": id, "type": req.Type})
}

func (s *Server) handleListQuotas(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSONResponse(w, http.StatusOK, []any{})
		return
	}
	qs, _ := s.store.ListQuotas(r.Context())
	writeJSONResponse(w, http.StatusOK, qs)
}

func (s *Server) handleImportGTFS(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSONResponse(w, http.StatusServiceUnavailable, map[string]any{"error": "storage disabled"})
		return
	}
	if strings.Contains(r.Header.Get("Content-Type"), "multipart/form-data") {
		if err := r.ParseMultipartForm(32 << 20); err == nil {
			f, hdr, err := r.FormFile("file")
			if err == nil {
				defer f.Close()
				_ = hdr
				data, _ := io.ReadAll(f)
				if len(data) == 0 {
					writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "empty file"})
					return
				}
				filename := hdr.Filename
				if filename == "" {
					filename = fmt.Sprintf("gtfs_%d.zip", time.Now().Unix())
				}
				savePath := fmt.Sprintf("data/gtfs/%s", filename)
				_ = os.MkdirAll("data/gtfs", 0755)
				_ = os.WriteFile(savePath, data, 0644)
				payload := fmt.Sprintf(`{"path":%q,"size":%d,"filename":%q}`, savePath, len(data), filename)
				id, _ := s.store.EnqueueJob(r.Context(), store.JobRow{Type: "import_gtfs", Payload: payload})
				h := fmt.Sprintf("%x", len(data))
				writeJSONResponse(w, http.StatusCreated, map[string]any{"id": id, "type": "import_gtfs", "path": savePath, "hash": h})
				return
			}
		}
	}
	id, _ := s.store.EnqueueJob(r.Context(), store.JobRow{Type: "import_gtfs", Payload: "{}"})
	writeJSONResponse(w, http.StatusCreated, map[string]any{"id": id, "type": "import_gtfs"})
}

func (s *Server) handleSyncMintrans(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSONResponse(w, http.StatusServiceUnavailable, map[string]any{"error": "storage disabled"})
		return
	}
	var payload map[string]any
	_ = json.NewDecoder(r.Body).Decode(&payload)
	b, _ := json.Marshal(payload)
	if len(b) == 0 {
		b = []byte("{}")
	}
	id, _ := s.store.EnqueueJob(r.Context(), store.JobRow{Type: "sync_mintrans", Payload: string(b)})
	writeJSONResponse(w, http.StatusCreated, map[string]any{"id": id, "type": "sync_mintrans"})
}

func (s *Server) handleSyncRail(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSONResponse(w, http.StatusServiceUnavailable, map[string]any{"error": "storage disabled"})
		return
	}
	var payload map[string]any
	_ = json.NewDecoder(r.Body).Decode(&payload)
	b, _ := json.Marshal(payload)
	if len(b) == 0 {
		b = []byte("{}")
	}
	id, _ := s.store.EnqueueJob(r.Context(), store.JobRow{Type: "sync_rail", Payload: string(b)})
	writeJSONResponse(w, http.StatusCreated, map[string]any{"id": id, "type": "sync_rail"})
}

func (s *Server) handleAdminImports(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSONResponse(w, http.StatusOK, []any{})
		return
	}
	rows, _ := s.store.ListImports(r.Context(), 20)
	writeJSONResponse(w, http.StatusOK, rows)
}

func (s *Server) handleAdminImportLogs(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSONResponse(w, http.StatusOK, []any{})
		return
	}
	rows, _ := s.store.ListImportLogs(r.Context(), 50)
	writeJSONResponse(w, http.StatusOK, rows)
}

func (s *Server) handleAdminAudit(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSONResponse(w, http.StatusOK, []any{})
		return
	}
	rows, _ := s.store.ListAuditLogs(r.Context(), 50)
	writeJSONResponse(w, http.StatusOK, rows)
}

func (s *Server) handleAdminListTerminals(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSONResponse(w, http.StatusOK, map[string]any{"items": []any{}, "total": 0})
		return
	}
	limit := 20
	offset := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	sort := r.URL.Query().Get("sort")
	if sort != "name" && sort != "is_locked" {
		sort = "id"
	}
	if lister, ok := s.store.(interface {
		ListTerminals(ctx context.Context, limit, offset int, sort string) ([]map[string]any, int, error)
	}); ok {
		items, total, _ := lister.ListTerminals(r.Context(), limit, offset, sort)
		writeJSONResponse(w, http.StatusOK, map[string]any{"items": items, "total": total, "limit": limit, "offset": offset})
		return
	}
	writeJSONResponse(w, http.StatusOK, map[string]any{"items": []any{}, "total": 0})
}

func (s *Server) handleAdminUpdateTerminal(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSONResponse(w, http.StatusServiceUnavailable, map[string]any{"error": "storage disabled"})
		return
	}
	user, _ := r.Context().Value(ctxUserKey).(*store.UserRow)
	actorID := func() *int64 {
		if user != nil && user.ID != 0 {
			return &user.ID
		}
		return nil
	}()
	idStr := r.PathValue("id")
	tid, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	var req struct {
		Name  string   `json:"name"`
		Lat   float64  `json:"lat"`
		Lon   float64  `json:"lon"`
		Names map[string]string `json:"names"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONDecodeError(w, err, s.logger, r.URL.Path)
		return
	}
	if req.Name == "" && len(req.Names) == 0 {
		writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "name required"})
		return
	}
	names := req.Names
	if names == nil {
		names = map[string]string{}
	}
	if req.Name != "" {
		names["ru"] = req.Name
	}
	now := time.Now().Unix()
	tr := store.TerminalRow{ID: tid, Lat: req.Lat, Lon: req.Lon, IsLocked: true, LastVerifiedAt: &now}
	if _, err := s.store.UpsertTerminal(r.Context(), tr, names, nil); err != nil {
		writeJSONResponse(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	_ = s.store.SaveProvenance(r.Context(), model.Provenance{EntityType: "terminal", EntityID: tid, Source: "manual", Confidence: 1.0, ObservedAt: time.Now(), ActorID: actorID})
	_ = s.store.SaveReviewQueue(r.Context(), model.ReviewQueueEntry{EntityType: "terminal", EntityID: tid, Reason: "conflicts_with_confirmed", Score: 1.0})
	_ = s.store.WriteAuditLog(r.Context(), actorID, "update_terminal", "terminal", &tid, fmt.Sprintf(`{"name":%q,"lat":%f,"lon":%f}`, req.Name, req.Lat, req.Lon))
	writeJSONResponse(w, http.StatusOK, map[string]any{"id": tid, "is_locked": true})
}

func (s *Server) handleAdminExternalCall(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSONResponse(w, http.StatusServiceUnavailable, map[string]any{"error": "storage disabled"})
		return
	}
	user, _ := r.Context().Value(ctxUserKey).(*store.UserRow)
	actorID := func() *int64 {
		if user != nil && user.ID != 0 {
			return &user.ID
		}
		return nil
	}()
	var req struct {
		Provider string `json:"provider"`
		Query    string `json:"query"`
		Lat      *float64 `json:"lat"`
		Lon      *float64 `json:"lon"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONDecodeError(w, err, s.logger, r.URL.Path)
		return
	}
	if req.Provider == "" {
		req.Provider = "yandex"
	}
	ok, _, _ := s.store.TryConsumeQuota(r.Context(), req.Provider, 1000)
	if !ok {
		writeJSONResponse(w, http.StatusTooManyRequests, map[string]any{"error": "quota exhausted", "provider": req.Provider})
		return
	}
	_ = s.store.RecordApiCall(r.Context(), req.Provider, "external_call", 1)
	if req.Query == "" && (req.Lat == nil || req.Lon == nil) {
		writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "query or lat/lon required"})
		return
	}
	if req.Query != "" && len(req.Query) < 2 {
		writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "query too short"})
		return
	}
	result := map[string]any{"provider": req.Provider, "query": req.Query, "validated": true, "actor_id": actorID}
	if req.Lat != nil && req.Lon != nil {
		if *req.Lat < -90 || *req.Lat > 90 || *req.Lon < -180 || *req.Lon > 180 {
			writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "invalid coords"})
			return
		}
		result["lat"] = *req.Lat
		result["lon"] = *req.Lon
	}
	details, _ := json.Marshal(result)
	_ = s.store.WriteAuditLog(r.Context(), actorID, "external_call", "terminal", nil, string(details))
	_ = s.store.SaveProvenance(r.Context(), model.Provenance{EntityType: "terminal", EntityID: 0, Source: req.Provider, Confidence: 0.8, ObservedAt: time.Now(), ActorID: actorID, Raw: details})
	writeJSONResponse(w, http.StatusOK, result)
}

func gtfsCompile(net *model.Network) ([]byte, error) {
	return gtfspkg.Compile(net)
}

func (s *Server) handleRoute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONResponse(w, http.StatusMethodNotAllowed, map[string]any{"error": "POST required"})
		return
	}
	var req struct {
		FromLat        *float64 `json:"from_lat"`
		FromLon        *float64 `json:"from_lon"`
		ToLat          *float64 `json:"to_lat"`
		ToLon          *float64 `json:"to_lon"`
		FromPlace      string   `json:"from_place"`
		ToPlace        string   `json:"to_place"`
		Departure      string   `json:"departure"`
		TransitModes   string   `json:"transit_modes"`
		MaxWalkMinutes *int     `json:"max_walk_minutes"`
		MaxTransfers   *int     `json:"max_transfers"`
		AllowGap       bool     `json:"allow_gap"`
		Preference     string   `json:"preference"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "invalid json", "details": err.Error()})
		return
	}
	_ = req
	writeJSONResponse(w, http.StatusNotImplemented, map[string]any{"error": "use /mcp find_route"})
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	user, _ := r.Context().Value(ctxUserKey).(*store.UserRow)
	s.logger.Info("dashboard", "user", userEmail(user))
	s.logger.Debug("dashboard debug", "counters", fmt.Sprint(s.metrics.Named()))
	writeJSONResponse(w, http.StatusOK, map[string]any{
		"uptime_seconds": int64(time.Since(s.started).Seconds()),
		"providers":      s.registry.HealthStatuses(),
		"counters":       s.metrics.Named(),
	})
}

func userEmail(u *store.UserRow) string {
	if u == nil {
		return ""
	}
	return u.Email
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	s.logger.Info("register attempt", "remote", r.RemoteAddr)
	s.logger.Debug("register debug", "headers", fmt.Sprint(r.Header))
	if s.store == nil {
		s.logger.Error("register failed: storage disabled")
		writeJSONResponse(w, http.StatusServiceUnavailable, map[string]any{"error": "storage disabled"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONDecodeError(w, err, s.logger, r.URL.Path)
		return
	}
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	s.logger.Debug("register payload", "email", req.Email)
	if _, err := mail.ParseAddress(req.Email); err != nil || req.Email == "" {
		s.logger.Warn("register invalid email", "email", req.Email, "error", err)
		writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "valid email required", "message": fmt.Sprintf("email %q невалиден: %v", req.Email, err), "field": "email"})
		return
	}
	if len(req.Password) < 8 || len(req.Password) > 72 {
		s.logger.Warn("register bad password length", "email", req.Email, "len", len(req.Password))
		writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "password 8..72 required", "message": fmt.Sprintf("password: длина %d, ожидается 8..72", len(req.Password)), "field": "password"})
		return
	}
	if _, ok := s.store.GetUserByEmail(r.Context(), req.Email); ok {
		s.logger.Warn("register user exists", "email", req.Email)
		writeJSONResponse(w, http.StatusConflict, map[string]any{"error": "user already exists", "field": "email"})
		return
	}
	role := "user"
	id, err := s.store.CreateUser(r.Context(), req.Email, hashPassword(req.Password), role)
	if err != nil {
		s.logger.Error("register create failed", "email", req.Email, "error", err)
		writeJSONResponse(w, http.StatusInternalServerError, map[string]any{"error": "create failed"})
		return
	}
	user, _ := s.store.GetUserByID(r.Context(), id)
	s.logger.Info("user registered", "email", req.Email, "id", id, "status", user.Status)
	writeJSONResponse(w, http.StatusCreated, map[string]any{"id": id, "email": req.Email, "status": user.Status, "message": "pending moderation"})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	s.logger.Info("login attempt", "remote", r.RemoteAddr)
	s.logger.Debug("login debug", "headers", fmt.Sprint(r.Header))
	if s.store == nil {
		s.logger.Error("login failed: storage disabled")
		writeJSONResponse(w, http.StatusServiceUnavailable, map[string]any{"error": "storage disabled"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONDecodeError(w, err, s.logger, r.URL.Path)
		return
	}
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	s.logger.Debug("login payload", "email", req.Email)
	user, ok := s.store.GetUserByEmail(r.Context(), req.Email)
	if !ok || !checkPassword(user.PassHash, req.Password) {
		s.logger.Warn("login invalid credentials", "email", req.Email)
		writeJSONResponse(w, http.StatusUnauthorized, map[string]any{"error": "invalid credentials", "field": "email/password"})
		return
	}
	if user.Status != "active" {
		s.logger.Warn("login inactive", "email", req.Email, "status", user.Status)
		writeJSONResponse(w, http.StatusForbidden, map[string]any{"error": "user not active", "status": user.Status, "field": "status"})
		return
	}
	keys, _ := s.store.ListApiKeys(r.Context(), user.ID)
	var ak store.ApiKeyRow
	if len(keys) > 0 {
		ak = keys[0]
		s.logger.Info("login reuse key", "email", req.Email, "user_id", user.ID, "key_id", ak.ID)
	} else {
		var err error
		ak, err = s.store.CreateApiKey(r.Context(), user.ID, "mcp:read")
		if err != nil {
			s.logger.Error("login key create failed", "email", req.Email, "error", err)
			writeJSONResponse(w, http.StatusInternalServerError, map[string]any{"error": "key create failed"})
			return
		}
		s.logger.Info("login created key", "email", req.Email, "user_id", user.ID, "key_id", ak.ID)
	}
	writeJSONResponse(w, http.StatusOK, map[string]any{"token": ak.Key, "scopes": ak.Scopes, "user_id": user.ID})
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	actor, _ := r.Context().Value(ctxUserKey).(*store.UserRow)
	s.logger.Info("list users", "actor", userEmail(actor), "remote", r.RemoteAddr)
	if s.store == nil {
		s.logger.Error("list users failed: storage disabled")
		writeJSONResponse(w, http.StatusServiceUnavailable, map[string]any{"error": "storage disabled"})
		return
	}
	users, err := s.store.ListUsers(r.Context())
	if err != nil {
		s.logger.Error("list users failed", "error", err)
		writeJSONResponse(w, http.StatusInternalServerError, map[string]any{"error": "list failed"})
		return
	}
	s.logger.Info("list users ok", "count", len(users), "actor", userEmail(actor))
	s.logger.Debug("list users debug", "users", fmt.Sprint(users))
	out := make([]map[string]any, 0, len(users))
	for _, u := range users {
		uu := u
		out = append(out, userPublic(&uu))
	}
	writeJSONResponse(w, http.StatusOK, out)
}

func (s *Server) handleModerateUser(w http.ResponseWriter, r *http.Request) {
	actor, _ := r.Context().Value(ctxUserKey).(*store.UserRow)
	s.logger.Info("moderate user", "actor", userEmail(actor), "path", r.URL.Path)
	if s.store == nil {
		s.logger.Error("moderate failed: storage disabled")
		writeJSONResponse(w, http.StatusServiceUnavailable, map[string]any{"error": "storage disabled"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		s.logger.Warn("moderate invalid id", "id", idStr)
		writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	var req struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONDecodeError(w, err, s.logger, r.URL.Path)
		return
	}
	s.logger.Debug("moderate payload", "id", id, "status", req.Status, "actor", userEmail(actor))
	if req.Status != "active" && req.Status != "blocked" && req.Status != "pending" {
		s.logger.Warn("moderate bad status", "status", req.Status)
		writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "status must be active|blocked|pending", "field": "status", "message": fmt.Sprintf("status %q недопустим, ожидается active|blocked|pending", req.Status)})
		return
	}
	if err := s.store.UpdateUserStatus(r.Context(), id, req.Status); err != nil {
		s.logger.Warn("moderate not found", "id", id, "error", err)
		writeJSONResponse(w, http.StatusNotFound, map[string]any{"error": "user not found"})
		return
	}
	s.logger.Info("moderate ok", "id", id, "status", req.Status, "actor", userEmail(actor))
	writeJSONResponse(w, http.StatusOK, map[string]any{"id": id, "status": req.Status})
}

func (s *Server) handleListKeys(w http.ResponseWriter, r *http.Request) {
	user, ok := r.Context().Value(ctxUserKey).(*store.UserRow)
	s.logger.Info("list keys", "user", userEmail(user), "remote", r.RemoteAddr)
	if !ok || user == nil {
		s.logger.Warn("list keys unauthorized")
		writeJSONResponse(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	s.logger.Debug("list keys debug", "user_id", user.ID)
	keys, err := s.store.ListApiKeys(r.Context(), user.ID)
	if err != nil {
		s.logger.Error("list keys failed", "user_id", user.ID, "error", err)
		writeJSONResponse(w, http.StatusInternalServerError, map[string]any{"error": "list failed"})
		return
	}
	s.logger.Info("list keys ok", "user_id", user.ID, "count", len(keys))
	writeJSONResponse(w, http.StatusOK, keys)
}

func (s *Server) handleCreateKey(w http.ResponseWriter, r *http.Request) {
	user, ok := r.Context().Value(ctxUserKey).(*store.UserRow)
	s.logger.Info("create key attempt", "user", userEmail(user), "remote", r.RemoteAddr)
	if !ok || user == nil {
		s.logger.Warn("create key unauthorized")
		writeJSONResponse(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	if user.ID == 0 {
		s.logger.Warn("create key with ADMIN_TOKEN synthetic user: use /api/v1/users/{id}/keys", "role", user.Role)
		writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "ADMIN_TOKEN cannot create personal key, use POST /api/v1/users/{id}/keys"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()
	var req struct {
		Scopes string `json:"scopes"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	s.logger.Debug("create key payload", "user_id", user.ID, "scopes", req.Scopes)
	if len(req.Scopes) > 256 {
		s.logger.Warn("create key scopes too large", "user_id", user.ID)
		writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "scopes too large"})
		return
	}
	if req.Scopes != "" && req.Scopes != "mcp:read" && req.Scopes != "admin" {
		s.logger.Warn("create key invalid scopes", "scopes", req.Scopes)
		writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "invalid scopes"})
		return
	}
	if req.Scopes == "admin" && user.Role != "admin" {
		s.logger.Warn("create key admin scope denied", "user_id", user.ID, "role", user.Role)
		writeJSONResponse(w, http.StatusForbidden, map[string]any{"error": "admin scope requires admin role"})
		return
	}
	ak, err := s.store.CreateApiKey(r.Context(), user.ID, req.Scopes)
	if err != nil {
		s.logger.Error("create key failed", "user_id", user.ID, "error", err)
		writeJSONResponse(w, http.StatusInternalServerError, map[string]any{"error": "create failed"})
		return
	}
	s.logger.Info("create key ok", "user_id", user.ID, "key_id", ak.ID, "scopes", ak.Scopes)
	writeJSONResponse(w, http.StatusCreated, ak)
}

func (s *Server) handleDeleteKey(w http.ResponseWriter, r *http.Request) {
	user, ok := r.Context().Value(ctxUserKey).(*store.UserRow)
	s.logger.Info("delete key", "user", userEmail(user), "path", r.URL.Path)
	if !ok || user == nil {
		s.logger.Warn("delete key unauthorized")
		writeJSONResponse(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		s.logger.Warn("delete key invalid id", "id", idStr)
		writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	s.logger.Debug("delete key", "user_id", user.ID, "key_id", id)
	if err := s.store.DeleteApiKey(r.Context(), id, user.ID); err != nil {
		s.logger.Warn("delete key not found", "user_id", user.ID, "key_id", id)
		writeJSONResponse(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	s.logger.Info("delete key ok", "user_id", user.ID, "key_id", id)
	writeJSONResponse(w, http.StatusOK, map[string]any{"deleted": id})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user, ok := r.Context().Value(ctxUserKey).(*store.UserRow)
	s.logger.Info("me", "user", userEmail(user))
	s.logger.Debug("me debug", "user", fmt.Sprint(user))
	if !ok || user == nil {
		s.logger.Warn("me unauthorized")
		writeJSONResponse(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	writeJSONResponse(w, http.StatusOK, userPublic(user))
}

func (s *Server) handleGetUser(w http.ResponseWriter, r *http.Request) {
	actor, _ := r.Context().Value(ctxUserKey).(*store.UserRow)
	s.logger.Info("get user", "actor", userEmail(actor), "path", r.URL.Path)
	if s.store == nil {
		s.logger.Error("get user storage disabled")
		writeJSONResponse(w, http.StatusServiceUnavailable, map[string]any{"error": "storage disabled"})
		return
	}
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		s.logger.Warn("get user invalid id", "id", idStr)
		writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	u, ok := s.store.GetUserByID(r.Context(), id)
	if !ok {
		s.logger.Warn("get user not found", "id", id)
		writeJSONResponse(w, http.StatusNotFound, map[string]any{"error": "user not found"})
		return
	}
	s.logger.Info("get user ok", "id", id, "email", u.Email)
	writeJSONResponse(w, http.StatusOK, userPublic(&u))
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	actor, _ := r.Context().Value(ctxUserKey).(*store.UserRow)
	s.logger.Info("delete user", "actor", userEmail(actor), "path", r.URL.Path)
	if s.store == nil {
		s.logger.Error("delete user storage disabled")
		writeJSONResponse(w, http.StatusServiceUnavailable, map[string]any{"error": "storage disabled"})
		return
	}
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		s.logger.Warn("delete user invalid id", "id", idStr)
		writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	s.logger.Debug("delete user", "id", id, "actor", userEmail(actor))
	if err := s.store.DeleteUser(r.Context(), id); err != nil {
		s.logger.Warn("delete user not found", "id", id, "error", err)
		writeJSONResponse(w, http.StatusNotFound, map[string]any{"error": "user not found"})
		return
	}
	s.logger.Info("delete user ok", "id", id, "actor", userEmail(actor))
	writeJSONResponse(w, http.StatusOK, map[string]any{"deleted": id})
}

func (s *Server) handlePatchUser(w http.ResponseWriter, r *http.Request) {
	actor, _ := r.Context().Value(ctxUserKey).(*store.UserRow)
	s.logger.Info("patch user", "actor", userEmail(actor), "path", r.URL.Path)
	if s.store == nil {
		s.logger.Error("patch user storage disabled")
		writeJSONResponse(w, http.StatusServiceUnavailable, map[string]any{"error": "storage disabled"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		s.logger.Warn("patch user invalid id", "id", idStr)
		writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	var req struct {
		Status *string `json:"status"`
		Role   *string `json:"role"`
		Config *string `json:"config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONDecodeError(w, err, s.logger, r.URL.Path)
		return
	}
	s.logger.Debug("patch user payload", "id", id, "status", req.Status, "role", req.Role, "actor", userEmail(actor))
	if req.Status == nil && req.Role == nil && req.Config == nil {
		s.logger.Warn("patch user nothing to update", "id", id)
		writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "nothing to update", "message": "укажите status, role или config"})
		return
	}
	if req.Status != nil {
		st := *req.Status
		if st != "pending" && st != "active" && st != "blocked" {
			s.logger.Warn("patch user bad status", "status", st)
			writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "status must be pending|active|blocked", "field": "status"})
			return
		}
		if err := s.store.UpdateUserStatus(r.Context(), id, st); err != nil {
			s.logger.Warn("patch user not found status", "id", id, "error", err)
			writeJSONResponse(w, http.StatusNotFound, map[string]any{"error": "user not found"})
			return
		}
	}
	if req.Role != nil {
		role := *req.Role
		if role != "user" && role != "admin" {
			s.logger.Warn("patch user bad role", "role", role)
			writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "role must be user|admin"})
			return
		}
		if err := s.store.UpdateUserRole(r.Context(), id, role); err != nil {
			s.logger.Warn("patch user not found role", "id", id, "error", err)
			writeJSONResponse(w, http.StatusNotFound, map[string]any{"error": "user not found"})
			return
		}
	}
	if req.Config != nil {
		if len(*req.Config) > 8192 {
			s.logger.Warn("patch user config too large", "id", id)
			writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "config too large"})
			return
		}
		if err := s.store.UpdateUserConfig(r.Context(), id, *req.Config); err != nil {
			s.logger.Warn("patch user config not found", "id", id, "error", err)
			writeJSONResponse(w, http.StatusNotFound, map[string]any{"error": "user not found"})
			return
		}
	}
	s.logger.Info("patch user ok", "id", id, "actor", userEmail(actor))
	u, _ := s.store.GetUserByID(r.Context(), id)
	writeJSONResponse(w, http.StatusOK, userPublic(&u))
}

func (s *Server) handleUpdateUserConfig(w http.ResponseWriter, r *http.Request) {
	actor, _ := r.Context().Value(ctxUserKey).(*store.UserRow)
	s.logger.Info("update user config", "actor", userEmail(actor), "path", r.URL.Path)
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		s.logger.Warn("update config invalid id", "id", idStr)
		writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	var req struct {
		Config string `json:"config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONDecodeError(w, err, s.logger, r.URL.Path)
		return
	}
	s.logger.Debug("update config payload", "id", id, "len", len(req.Config))
	if len(req.Config) > 8192 {
		s.logger.Warn("update config too large", "id", id)
		writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "config too large"})
		return
	}
	if err := s.store.UpdateUserConfig(r.Context(), id, req.Config); err != nil {
		s.logger.Warn("update config not found", "id", id, "error", err)
		writeJSONResponse(w, http.StatusNotFound, map[string]any{"error": "user not found"})
		return
	}
	s.logger.Info("update config ok", "id", id, "actor", userEmail(actor))
	writeJSONResponse(w, http.StatusOK, map[string]any{"id": id, "config": req.Config})
}

func (s *Server) handleListUserKeys(w http.ResponseWriter, r *http.Request) {
	actor, _ := r.Context().Value(ctxUserKey).(*store.UserRow)
	s.logger.Info("list user keys", "actor", userEmail(actor), "path", r.URL.Path)
	if s.store == nil {
		s.logger.Error("list user keys storage disabled")
		writeJSONResponse(w, http.StatusServiceUnavailable, map[string]any{"error": "storage disabled"})
		return
	}
	idStr := r.PathValue("id")
	uid, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		s.logger.Warn("list user keys invalid id", "id", idStr)
		writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	if _, ok := s.store.GetUserByID(r.Context(), uid); !ok {
		s.logger.Warn("list user keys not found", "uid", uid)
		writeJSONResponse(w, http.StatusNotFound, map[string]any{"error": "user not found"})
		return
	}
	keys, err := s.store.ListApiKeys(r.Context(), uid)
	if err != nil {
		s.logger.Error("list user keys failed", "uid", uid, "error", err)
		writeJSONResponse(w, http.StatusInternalServerError, map[string]any{"error": "list failed"})
		return
	}
	if keys == nil {
		keys = []store.ApiKeyRow{}
	}
	s.logger.Info("list user keys ok", "uid", uid, "count", len(keys))
	writeJSONResponse(w, http.StatusOK, keys)
}

func (s *Server) handleCreateUserKey(w http.ResponseWriter, r *http.Request) {
	actor, _ := r.Context().Value(ctxUserKey).(*store.UserRow)
	s.logger.Info("create user key", "actor", userEmail(actor), "path", r.URL.Path)
	if s.store == nil {
		s.logger.Error("create user key storage disabled")
		writeJSONResponse(w, http.StatusServiceUnavailable, map[string]any{"error": "storage disabled"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()
	idStr := r.PathValue("id")
	uid, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		s.logger.Warn("create user key invalid id", "id", idStr)
		writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	if _, ok := s.store.GetUserByID(r.Context(), uid); !ok {
		s.logger.Warn("create user key not found", "uid", uid)
		writeJSONResponse(w, http.StatusNotFound, map[string]any{"error": "user not found"})
		return
	}
	var req struct {
		Scopes string `json:"scopes"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	s.logger.Debug("create user key payload", "uid", uid, "scopes", req.Scopes, "actor", userEmail(actor))
	if len(req.Scopes) > 256 {
		s.logger.Warn("create user key scopes too large", "uid", uid)
		writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "scopes too large"})
		return
	}
	if req.Scopes != "" && req.Scopes != "mcp:read" && req.Scopes != "admin" {
		s.logger.Warn("create user key invalid scopes", "scopes", req.Scopes)
		writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "invalid scopes"})
		return
	}
	ak, err := s.store.CreateApiKey(r.Context(), uid, req.Scopes)
	if err != nil {
		s.logger.Error("create user key failed", "uid", uid, "error", err)
		writeJSONResponse(w, http.StatusInternalServerError, map[string]any{"error": "create failed"})
		return
	}
	s.logger.Info("create user key ok", "uid", uid, "key_id", ak.ID, "scopes", ak.Scopes, "actor", userEmail(actor))
	writeJSONResponse(w, http.StatusCreated, ak)
}

func (s *Server) handleDeleteUserKey(w http.ResponseWriter, r *http.Request) {
	actor, _ := r.Context().Value(ctxUserKey).(*store.UserRow)
	s.logger.Info("delete user key", "actor", userEmail(actor), "path", r.URL.Path)
	if s.store == nil {
		s.logger.Error("delete user key storage disabled")
		writeJSONResponse(w, http.StatusServiceUnavailable, map[string]any{"error": "storage disabled"})
		return
	}
	idStr := r.PathValue("id")
	uid, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		s.logger.Warn("delete user key invalid user id", "id", idStr)
		writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "invalid user id"})
		return
	}
	keyIdStr := r.PathValue("keyId")
	kid, err := strconv.ParseInt(keyIdStr, 10, 64)
	if err != nil {
		s.logger.Warn("delete user key invalid key id", "id", keyIdStr)
		writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "invalid key id"})
		return
	}
	s.logger.Debug("delete user key", "uid", uid, "key_id", kid, "actor", userEmail(actor))
	if err := s.store.DeleteApiKey(r.Context(), kid, uid); err != nil {
		s.logger.Warn("delete user key not found", "uid", uid, "key_id", kid, "error", err)
		writeJSONResponse(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	s.logger.Info("delete user key ok", "uid", uid, "key_id", kid, "actor", userEmail(actor))
	writeJSONResponse(w, http.StatusOK, map[string]any{"deleted": kid})
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	actor, _ := r.Context().Value(ctxUserKey).(*store.UserRow)
	s.logger.Info("get config", "actor", userEmail(actor))
	s.logger.Debug("get config debug", "cfg", fmt.Sprint(s.cfg))
	s.mu.RLock()
	defer s.mu.RUnlock()
	cfg := s.cfg
	writeJSONResponse(w, http.StatusOK, map[string]any{
		"http":      map[string]any{"addr": cfg.HTTP.Addr, "rate_limit": cfg.HTTP.RateLimit},
		"store":     map[string]any{"dsn": maskDSNShort(cfg.Store.DSN), "kind": cfg.Store.Kind},
		"cache":     cfg.Cache,
		"queue":     cfg.Queue,
		"providers": cfg.Providers,
		"planner":   cfg.Planner,
		"log":       cfg.Log,
		"telemetry": cfg.Telemetry,
		"geocoder":  map[string]any{"kind": cfg.Geocoder.Kind, "url": cfg.Geocoder.URL, "attempts": cfg.Geocoder.Attempts},
		"yandex":    map[string]any{"rasp_url": cfg.Yandex.RaspURL, "geocode_url": cfg.Yandex.GeocodeURL},
		"nominatim": cfg.Nominatim,
	})
}

func (s *Server) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	actor, _ := r.Context().Value(ctxUserKey).(*store.UserRow)
	s.logger.Info("put config", "actor", userEmail(actor), "remote", r.RemoteAddr)
	s.logger.Debug("put config debug", "headers", fmt.Sprint(r.Header))
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()
	var req map[string]any
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONDecodeError(w, err, s.logger, r.URL.Path)
		return
	}
	s.logger.Debug("put config payload", "body", fmt.Sprint(req), "actor", userEmail(actor))
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := req["providers"]; ok {
		if m, ok := v.(map[string]any); ok {
			if en, ok := m["enabled"]; ok {
				switch x := en.(type) {
				case []any:
					enabled := make([]string, 0, len(x))
					for _, e := range x {
						if s, ok := e.(string); ok {
							enabled = append(enabled, strings.TrimSpace(s))
						}
					}
					s.cfg.Providers.Enabled = enabled
				case []string:
					s.cfg.Providers.Enabled = x
				case string:
					s.cfg.Providers.Enabled = strings.Split(x, ",")
				}
			}
		}
	}
	if v, ok := req["planner"]; ok {
		if m, ok := v.(map[string]any); ok {
			if eng, ok := m["engine"].(string); ok {
				if eng != "csa" && eng != "raptor" {
					s.logger.Warn("put config invalid engine", "engine", eng)
					writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "planner.engine must be csa|raptor"})
					return
				}
				s.cfg.Planner.Engine = eng
			}
		}
	}
	if v, ok := req["log"]; ok {
		if m, ok := v.(map[string]any); ok {
			if lvl, ok := m["level"].(string); ok {
				if lvl != "debug" && lvl != "info" && lvl != "warn" && lvl != "error" {
					s.logger.Warn("put config invalid log level", "level", lvl)
					writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "log.level invalid"})
					return
				}
				s.cfg.Log.Level = lvl
			}
			if levels, ok := m["levels"].(map[string]any); ok {
				if s.cfg.Log.Levels == nil {
					s.cfg.Log.Levels = map[string]string{}
				}
				for k, v2 := range levels {
					if vs, ok := v2.(string); ok {
						s.cfg.Log.Levels[k] = vs
					}
				}
			}
		}
	}
	s.logger.Info("config updated via API", "providers", s.cfg.Providers.Enabled, "planner", s.cfg.Planner.Engine, "log_level", s.cfg.Log.Level)
	writeJSONResponse(w, http.StatusOK, map[string]any{"status": "ok", "providers": s.cfg.Providers.Enabled, "planner": s.cfg.Planner.Engine, "log": s.cfg.Log})
}

func userPublic(u *store.UserRow) map[string]any {
	return map[string]any{"id": u.ID, "email": u.Email, "status": u.Status, "role": u.Role, "created_at": u.CreatedAt, "config": u.Config}
}

func maskDSNShort(dsn string) string {
	if dsn == "" {
		return ""
	}
	if len(dsn) > 12 {
		return dsn[:6] + "***" + dsn[len(dsn)-4:]
	}
	return "***"
}

func (s *Server) handleAdminPage(w http.ResponseWriter, r *http.Request) {
	reqID := fmt.Sprint(r.Context().Value(ctxKey("request_id")))
	s.logger.Info("admin page", "method", r.Method, "path", r.URL.Path, "remote", r.RemoteAddr, "request_id", reqID)
	s.logger.Debug("admin page debug", "headers", fmt.Sprint(r.Header), "query", r.URL.RawQuery)
	data, err := fs.ReadFile(webFS, "web/index.html")
	if err != nil {
		s.logger.Error("admin page read failed", "error", err)
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(data)
	s.logger.Debug("admin page served", "bytes", len(data))
}

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
			body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
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
		telemetry.HTTPRequests.WithLabelValues(fmt.Sprint(rec.status), r.URL.Path).Inc()
		_ = time.Since(start)
	})
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
				limited := io.LimitReader(r.Body, 4096)
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
			s.logger.Error("request", attrs...)
		case rec.status >= 400:
			s.logger.Warn("request", attrs...)
		default:
			if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" || r.URL.Path == "/metrics" {
				s.logger.Debug("request", attrs...)
			} else {
				s.logger.Info("request", attrs...)
			}
		}
		if s.logger.Enabled(r.Context(), slog.LevelDebug) && rec.status >= 400 {
			s.logger.Debug("request debug", "method", r.Method, "path", r.URL.Path, "query", r.URL.RawQuery, "headers", fmt.Sprint(r.Header))
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
