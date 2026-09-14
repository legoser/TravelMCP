package server

import (
	"context"
	"embed"
	"sync"
	"time"

	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"io/fs"
	"log/slog"
	"net/http"

	_ "travelmcp/internal/adapters/nominatim"
	_ "travelmcp/internal/adapters/yandex"
	"travelmcp/internal/config"
	geocoderpkg "travelmcp/internal/geocoder"
	logfactory "travelmcp/internal/logger"
	"travelmcp/internal/mcp"
	"travelmcp/internal/middleware"
	"travelmcp/internal/planner"
	"travelmcp/internal/providers"
	"travelmcp/internal/store"
	"travelmcp/internal/support/httpx"
	"travelmcp/internal/support/namesim"
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

	geocoder     geocoderpkg.Geocoder
	geocoderOnce sync.Once
}

func (s *Server) withSettlements(ctx context.Context, items []map[string]any) []map[string]any {
	tagger, _ := s.store.(interface {
		GetTerminalTags(ctx context.Context, id int64) (map[string]string, error)
	})
	for _, it := range items {
		idf, _ := it["id"].(int64)
		if tagger != nil && idf != 0 {
			if tags, err := tagger.GetTerminalTags(ctx, idf); err == nil {
				if st, ok := tags["settlement"]; ok && st != "" {
					it["settlement"] = st
					it["settlement_manual"] = true
					continue
				}
			}
		}
		if name, _ := it["name"].(string); name != "" {
			if st := namesim.ExtractSettlement(name); st != "" {
				it["settlement"] = st
			}
		}
	}
	return items
}

func (s *Server) getGeocoder() geocoderpkg.Geocoder {
	s.geocoderOnce.Do(func() {
		if s.cfg == nil {
			return
		}
		client := httpx.New(s.logger, "geocoder")
		g, err := geocoderpkg.New(*s.cfg, client)
		if err != nil {
			s.logger.Warn("admin geocoder init failed", "error", err)
			return
		}
		s.geocoder = g
	})
	return s.geocoder
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
	app := mcp.NewWithStore(planner.NewWithConfig(metrics, cfg.Planner.Engine, plannerLogger, cfg.Planner.SemaphoreSize, cfg.Planner.SemaphoreEnable).WithDefaultMaxWalk(cfg.Planner.MaxWalkMinutes).WithMinTransfer(cfg.Planner.MinTransferMinutes, cfg.Planner.FlightCheckInMinutes), registry, st, mcpLogger)
	mcpHandler := mcpserver.NewStreamableHTTPServer(app.Server(), mcpserver.WithStateLess(true))

	mux := http.NewServeMux()
	mux.Handle("GET /healthz", http.HandlerFunc(s.handleHealthz))
	mux.Handle("GET /readyz", http.HandlerFunc(s.handleReadyz))
	mux.Handle("GET /metrics", promhttp.Handler())
	if cfg != nil && cfg.Telemetry.OTLPMetricsURL != "" {
		otlpHandler := telemetry.NewOTLPMetricsHandler(prometheus.DefaultGatherer.(*prometheus.Registry))
		mux.Handle("/api/v1/metrics/otlp", s.auth(otlpHandler, "mcp:read"))
	}
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
	mux.Handle("POST /api/v1/review/resolve", s.auth(http.HandlerFunc(s.handleReviewResolve), "admin"))
	mux.Handle("GET /api/v1/review/export.csv", s.auth(http.HandlerFunc(s.handleReviewExport), "mcp:read"))
	mux.Handle("GET /api/v1/jobs", s.auth(http.HandlerFunc(s.handleListJobs), "admin"))
	mux.Handle("POST /api/v1/jobs", s.auth(http.HandlerFunc(s.handleEnqueueJob), "admin"))
	mux.Handle("POST /api/v1/jobs/{id}/reset", s.auth(http.HandlerFunc(s.handleResetJob), "admin"))
	mux.Handle("POST /api/v1/jobs/{id}/cancel", s.auth(http.HandlerFunc(s.handleCancelJob), "admin"))
	mux.Handle("GET /api/v1/quotas", s.auth(http.HandlerFunc(s.handleListQuotas), "admin"))
	mux.Handle("POST /api/v1/import/gtfs", s.auth(http.HandlerFunc(s.handleImportGTFS), "admin"))
	mux.Handle("POST /api/v1/import/rail", s.auth(http.HandlerFunc(s.handleSyncRail), "admin"))
	mux.Handle("GET /api/v1/admin/imports", s.auth(http.HandlerFunc(s.handleAdminImports), "admin"))
	mux.Handle("GET /api/v1/admin/logs", s.auth(http.HandlerFunc(s.handleAdminImportLogs), "admin"))
	mux.Handle("GET /api/v1/admin/audit", s.auth(http.HandlerFunc(s.handleAdminAudit), "admin"))
	mux.Handle("GET /api/v1/admin/terminals", s.auth(http.HandlerFunc(s.handleAdminListTerminals), "admin"))
	mux.Handle("GET /api/v1/admin/terminals/liveness", s.auth(http.HandlerFunc(s.handleAdminListTerminalsLiveness), "admin"))
	mux.Handle("GET /api/v1/admin/terminals/{id}/card", s.auth(http.HandlerFunc(s.handleAdminTerminalCard), "admin"))
	mux.Handle("GET /api/v1/admin/terminals/{id}/schedule", s.auth(http.HandlerFunc(s.handleAdminTerminalSchedule), "admin"))
	mux.Handle("GET /api/v1/admin/identifier-schemes", s.auth(http.HandlerFunc(s.handleListIdentifierSchemes), "admin"))
	mux.Handle("PUT /api/v1/admin/identifier-schemes", s.auth(http.HandlerFunc(s.handleUpsertIdentifierScheme), "admin"))
	mux.Handle("DELETE /api/v1/admin/identifier-schemes/{code}", s.auth(http.HandlerFunc(s.handleDeleteIdentifierScheme), "admin"))
	mux.Handle("PUT /api/v1/admin/terminals/{id}", s.auth(http.HandlerFunc(s.handleAdminUpdateTerminal), "admin"))
	mux.Handle("POST /api/v1/admin/terminals/merge", s.auth(http.HandlerFunc(s.handleAdminMergeTerminals), "admin"))
	mux.Handle("POST /api/v1/admin/canon/reset", s.auth(http.HandlerFunc(s.handleAdminCanonReset), "admin"))
	mux.Handle("DELETE /api/v1/admin/terminals/{id}", s.auth(http.HandlerFunc(s.handleAdminDeleteTerminal), "admin"))
	mux.Handle("GET /api/v1/admin/routes", s.auth(http.HandlerFunc(s.handleAdminListRoutes), "admin"))
	mux.Handle("GET /api/v1/admin/trips", s.auth(http.HandlerFunc(s.handleAdminListTrips), "admin"))
	mux.Handle("GET /api/v1/admin/trips/{id}", s.auth(http.HandlerFunc(s.handleAdminGetTrip), "admin"))
	mux.Handle("POST /api/v1/admin/external-call", s.auth(http.HandlerFunc(s.handleAdminExternalCall), "admin"))
	mux.Handle("POST /api/v1/collect/skeleton", s.auth(http.HandlerFunc(s.handleCollectSkeleton), "admin"))
	mux.Handle("POST /api/v1/collect/trips", s.auth(http.HandlerFunc(s.handleCollectTrips), "admin"))
	mux.Handle("POST /api/v1/collect/routes", s.auth(http.HandlerFunc(s.handleCollectRoutes), "admin"))
	mux.Handle("GET /api/v1/collect/regions", s.auth(http.HandlerFunc(s.handleCollectRegions), "admin"))
	mux.Handle("GET /api/v1/sync/runs", s.auth(http.HandlerFunc(s.handleListSyncRuns), "admin"))
	mux.Handle("GET /api/v1/sync/runs/{id}", s.auth(http.HandlerFunc(s.handleGetSyncRun), "admin"))
	mux.Handle("POST /api/v1/route", s.auth(http.HandlerFunc(s.handleRoute), "mcp:read"))
	adminFS, _ := fs.Sub(webFS, "web")
	mux.Handle("GET /admin", http.HandlerFunc(s.handleAdminPage))
	mux.Handle("GET /admin/", http.StripPrefix("/admin/", http.FileServer(http.FS(adminFS))))
	mux.Handle("/mcp", s.auth(s.mcpJSONValidation(mcpHandler), "mcp:read"))

	rl := middleware.NewRateLimiter(cfg.HTTP)
	handler := rl.Middleware(mux)
	handler = tracingMiddleware(handler)
	handler = s.loggingMiddleware(handler)
	handler = requestIDMiddleware(handler)
	handler = metricsMiddleware(handler)
	return handler
}
