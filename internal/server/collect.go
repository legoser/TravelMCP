// Эндпоинты интерактивного сбора (ручной режим, план §5.4) и чтения
// sync_runs для экрана сводки качества прогонов.
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"sort"
	"strconv"

	"travelmcp/internal/skeleton"
	"travelmcp/internal/store"
)

type collectRequest struct {
	Regions      []string `json:"regions"`
	Region       string   `json:"region"`
	Date         string   `json:"date"`
	Transports   []string `json:"transports"`
	StationTypes []string `json:"station_types"`
	Stations     string   `json:"stations"`
	Offline      bool     `json:"offline"`
	Force        bool     `json:"force"`
	Tag          string   `json:"tag"`
	TerminalID   int64    `json:"terminal_id"`
	Transport    string   `json:"transport"`
}

// handleCollectSkeleton — POST /api/v1/collect/skeleton
// {regions: [...], transports: [...], station_types: [...], offline, tag}
// → jobs{type: sync_collect_region, payload:{kind: skeleton, ...}}.
func (s *Server) handleCollectSkeleton(w http.ResponseWriter, reqst *http.Request) {
	s.enqueueCollectJob(w, reqst, "skeleton")
}

// handleCollectTrips — POST /api/v1/collect/trips
// {region, date, offline, tag}.
func (s *Server) handleCollectTrips(w http.ResponseWriter, r *http.Request) {
	s.enqueueCollectJob(w, r, "trips")
}

func (s *Server) enqueueCollectJob(w http.ResponseWriter, reqst *http.Request, kind string) {
	if s.store == nil {
		writeJSONResponse(w, http.StatusServiceUnavailable, map[string]any{"error": "storage disabled"})
		return
	}
	var req collectRequest
	if err := json.NewDecoder(reqst.Body).Decode(&req); err != nil {
		writeJSONDecodeError(w, err, s.logger, reqst.URL.Path)
		return
	}
	if kind == "trips" && req.Region == "" && req.TerminalID <= 0 {
		writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "region обязателен (или terminal_id для точечного сбора)"})
		return
	}
	if kind == "trips" && req.TerminalID > 0 && req.Transport == "" {
		writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "transport обязателен при terminal_id (тип транспорта терминала)"})
		return
	}
	if kind == "skeleton" && len(req.Regions) == 0 && req.Region == "" {
		writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "regions или region обязательны"})
		return
	}
	payload, err := json.Marshal(map[string]any{
		"kind":          kind,
		"regions":       req.Regions,
		"region":        req.Region,
		"date":          req.Date,
		"transports":    req.Transports,
		"station_types": req.StationTypes,
		"stations":      req.Stations,
		"offline":       req.Offline,
		"force":         req.Force,
		"tag":           req.Tag,
		"terminal_id":   req.TerminalID,
		"transport":     req.Transport,
	})
	if err != nil {
		writeJSONResponse(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	region := req.Region
	if region == "" && len(req.Regions) > 0 {
		region = req.Regions[0]
	}
	id, err := s.store.EnqueueJob(reqst.Context(), store.JobRow{Type: "sync_collect_region", Payload: string(payload), Region: region})
	if err != nil {
		writeJSONResponse(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSONResponse(w, http.StatusCreated, map[string]any{"id": id, "type": "sync_collect_region", "kind": kind, "region": region})
}

// handleCollectRegions — GET /api/v1/collect/regions: регионы Яндекс-дампа
// с количеством терминальных станций (селектор UI).
func (s *Server) handleCollectRegions(w http.ResponseWriter, reqst *http.Request) {
	path := s.cfg.Sync.YandexDumpPath
	if path == "" {
		writeJSONResponse(w, http.StatusOK, []any{})
		return
	}
	if _, err := os.Stat(path); err != nil {
		writeJSONResponse(w, http.StatusOK, []any{})
		return
	}
	yan, err := skeleton.YandexDumpSource{Path: path}.Load()
	if err != nil {
		s.logger.Warn("collect regions: yandex dump load failed", "error", err)
		writeJSONResponse(w, http.StatusOK, []any{})
		return
	}
	terminal := skeleton.FilterYandexRecords(yan, nil, map[string]bool{"bus": true, "train": true, "flight": true}, skeleton.StationClasses)
	byRegion := map[string]int{}
	for _, rec := range terminal {
		if rec.Extra == nil {
			continue
		}
		if reg := rec.Extra["region"]; reg != "" {
			byRegion[reg]++
		}
	}
	out := make([]map[string]any, 0, len(byRegion))
	for reg, n := range byRegion {
		out = append(out, map[string]any{"region": reg, "terminal_stations": n})
	}
	sort.Slice(out, func(i, j int) bool {
		ri, _ := out[i]["region"].(string)
		rj, _ := out[j]["region"].(string)
		return ri < rj
	})
	writeJSONResponse(w, http.StatusOK, out)
}

type syncRunsLister interface {
	ListSyncRuns(ctx context.Context, limit int) ([]store.SyncRunRow, error)
}

// handleListSyncRuns — GET /api/v1/sync/runs?limit=50.
func (s *Server) handleListSyncRuns(w http.ResponseWriter, reqst *http.Request) {
	if s.store == nil {
		writeJSONResponse(w, http.StatusOK, []any{})
		return
	}
	limit, _ := strconv.Atoi(reqst.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	lister, ok := s.store.(syncRunsLister)
	if !ok {
		writeJSONResponse(w, http.StatusServiceUnavailable, map[string]any{"error": "sync runs not supported"})
		return
	}
	runs, err := lister.ListSyncRuns(reqst.Context(), limit)
	if err != nil {
		writeJSONResponse(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if runs == nil {
		runs = []store.SyncRunRow{}
	}
	writeJSONResponse(w, http.StatusOK, runs)
}

// handleGetSyncRun — GET /api/v1/sync/runs/{id}: сводка одного прогона
// (summary jsonb как есть).
func (s *Server) handleGetSyncRun(w http.ResponseWriter, reqst *http.Request) {
	if s.store == nil {
		writeJSONResponse(w, http.StatusServiceUnavailable, map[string]any{"error": "storage disabled"})
		return
	}
	id, err := strconv.ParseInt(reqst.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	lister, ok := s.store.(syncRunsLister)
	if !ok {
		writeJSONResponse(w, http.StatusServiceUnavailable, map[string]any{"error": "sync runs not supported"})
		return
	}
	runs, err := lister.ListSyncRuns(reqst.Context(), 200)
	if err != nil {
		writeJSONResponse(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	for i := range runs {
		if runs[i].ID == id {
			writeJSONResponse(w, http.StatusOK, runs[i])
			return
		}
	}
	writeJSONResponse(w, http.StatusNotFound, map[string]any{"error": "run not found"})
}
