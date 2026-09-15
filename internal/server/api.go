package server

// Public API endpoints: health, providers, fares, zones, GTFS export, route, dashboard.

import (
	"context"
	"fmt"
	"time"

	"encoding/json"
	"net/http"

	_ "travelmcp/internal/adapters/nominatim"
	_ "travelmcp/internal/adapters/yandex"
	gtfspkg "travelmcp/internal/export/gtfs"
	gtfsperregion "travelmcp/internal/gtfs"
	"travelmcp/internal/mcp"
	"travelmcp/internal/model"
	"travelmcp/internal/planner"
	"travelmcp/internal/store"
)

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
		app := mcp.NewWithStore(planner.NewWithConfig(s.metrics, s.cfg.Planner.Engine, s.logger, s.cfg.Planner.SemaphoreSize, s.cfg.Planner.SemaphoreEnable).WithDefaultMaxWalk(s.cfg.Planner.MaxWalkMinutes).WithMinTransfer(s.cfg.Planner.MinTransferMinutes, s.cfg.Planner.FlightCheckInMinutes).WithAlternatives(s.cfg.Planner.AltWindowMinutes, s.cfg.Planner.AltStepMinutes).WithArrivalWindow(s.cfg.Planner.ArrivalWindowHours, s.cfg.Planner.ArrivalStepMinutes), s.registry, s.store, s.logger)
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
	resp := map[string]any{
		"uptime_seconds": int64(time.Since(s.started).Seconds()),
		"providers":      s.registry.HealthStatuses(),
		"counters":       s.metrics.Named(),
	}
	// KPI §8: устаревание полей — v_stale_attributes-осцилляция (30д-окно),
	// застрявшие staging-очереди (после expiry §5.3 живёт только свежее).
	if s.store != nil {
		if sw, ok := s.store.(interface {
			StaleAttributesCounters(ctx context.Context, olderThan time.Duration) (map[string]int, error)
			CountStagingByState(ctx context.Context) (map[string]int, error)
		}); ok {
			if stale, err := sw.StaleAttributesCounters(r.Context(), 30*24*time.Hour); err == nil {
				resp["stale_attributes_30d"] = stale
			}
			if byState, err := sw.CountStagingByState(r.Context()); err == nil {
				resp["staging_by_state"] = byState
			}
		}
	}
	writeJSONResponse(w, http.StatusOK, resp)
}
