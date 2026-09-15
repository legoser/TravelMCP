package server

// Administrative endpoints: jobs, quotas, imports, terminals, external calls, admin UI.

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/google/uuid"
	"io/fs"
	"net/http"
	"path/filepath"

	_ "travelmcp/internal/adapters/nominatim"
	_ "travelmcp/internal/adapters/yandex"
	geocoderpkg "travelmcp/internal/geocoder"
	"travelmcp/internal/mcp"
	"travelmcp/internal/model"
	"travelmcp/internal/store"
	"travelmcp/internal/support/httpx"
	"travelmcp/internal/support/namesim"
)

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

func (s *Server) handleResetJob(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSONResponse(w, http.StatusServiceUnavailable, map[string]any{"error": "storage disabled"})
		return
	}
	resetter, ok := s.store.(interface {
		ResetJob(ctx context.Context, id int64) error
	})
	if !ok {
		writeJSONResponse(w, http.StatusNotImplemented, map[string]any{"error": "reset not supported by store"})
		return
	}
	jid, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	if err := resetter.ResetJob(r.Context(), jid); err != nil {
		writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSONResponse(w, http.StatusOK, map[string]any{"id": jid, "state": "pending"})
}

func (s *Server) handleCancelJob(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSONResponse(w, http.StatusServiceUnavailable, map[string]any{"error": "storage disabled"})
		return
	}
	canceller, ok := s.store.(interface {
		CancelJob(ctx context.Context, id int64) error
	})
	if !ok {
		writeJSONResponse(w, http.StatusNotImplemented, map[string]any{"error": "cancel not supported by store"})
		return
	}
	jid, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	if err := canceller.CancelJob(r.Context(), jid); err != nil {
		writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	actor, _ := r.Context().Value(ctxUserKey).(*store.UserRow)
	s.logger.Info("job cancelled", "id", jid, "actor", userEmail(actor))
	writeJSONResponse(w, http.StatusOK, map[string]any{"id": jid, "state": "cancelled"})
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
		if err := r.ParseMultipartForm(maxMultipartFormMem); err == nil {
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
				baseTmp := "data/tmp/gtfs"
				if s.cfg != nil && s.cfg.GTFS.TmpDir != "" {
					baseTmp = s.cfg.GTFS.TmpDir
				}
				tmpDir := filepath.Join(baseTmp, uuid.NewString())
				_ = os.MkdirAll(tmpDir, 0755)
				savePath := filepath.Join(tmpDir, filepath.Base(filename))
				_ = os.WriteFile(savePath, data, 0644)
				sum := sha256.Sum256(data)
				hash := hex.EncodeToString(sum[:])
				payload := fmt.Sprintf(`{"path":%q,"tmp_dir":%q,"size":%d,"filename":%q,"hash":%q}`, savePath, tmpDir, len(data), filename, hash)
				id, _ := s.store.EnqueueJob(r.Context(), store.JobRow{Type: "import_gtfs", Payload: payload})
				writeJSONResponse(w, http.StatusCreated, map[string]any{"id": id, "type": "import_gtfs", "path": savePath, "tmp_dir": tmpDir, "hash": hash})
				return
			}
		}
	}
	id, _ := s.store.EnqueueJob(r.Context(), store.JobRow{Type: "import_gtfs", Payload: "{}"})
	writeJSONResponse(w, http.StatusCreated, map[string]any{"id": id, "type": "import_gtfs"})
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
	limit := defaultPageLimit
	offset := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= maxPageLimit {
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
	order := strings.ToLower(r.URL.Query().Get("order"))
	if order != "asc" && order != "desc" {
		order = "asc"
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if filtered, ok := s.store.(interface {
		ListTerminalsFiltered(ctx context.Context, limit, offset int, sort, order, q string) ([]map[string]any, int, error)
	}); ok {
		items, total, _ := filtered.ListTerminalsFiltered(r.Context(), limit, offset, sort, order, q)
		writeJSONResponse(w, http.StatusOK, map[string]any{"items": s.withSettlements(r.Context(), items), "total": total, "limit": limit, "offset": offset, "sort": sort, "order": order, "q": q})
		return
	}
	if lister, ok := s.store.(interface {
		ListTerminals(ctx context.Context, limit, offset int, sort string) ([]map[string]any, int, error)
	}); ok {
		items, total, _ := lister.ListTerminals(r.Context(), limit, offset, sort)
		writeJSONResponse(w, http.StatusOK, map[string]any{"items": s.withSettlements(r.Context(), items), "total": total, "limit": limit, "offset": offset})
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
		Name       string            `json:"name"`
		Lat        float64           `json:"lat"`
		Lon        float64           `json:"lon"`
		Names      map[string]string `json:"names"`
		Settlement string            `json:"settlement"`
		Approve    bool              `json:"approve"`
		Unapprove  bool              `json:"unapprove"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONDecodeError(w, err, s.logger, r.URL.Path)
		return
	}
	if req.Unapprove {
		unlocker, ok := s.store.(interface {
			UnlockTerminal(ctx context.Context, id int64, actorID *int64) error
		})
		if !ok {
			writeJSONResponse(w, http.StatusNotImplemented, map[string]any{"error": "unlock not supported by store"})
			return
		}
		if err := unlocker.UnlockTerminal(r.Context(), tid, actorID); err != nil {
			writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		_ = s.store.WriteAuditLog(r.Context(), actorID, "unlock_terminal", "terminal", &tid, fmt.Sprintf(`{"id":%d}`, tid))
		mcp.BumpCanonVersion()
		writeJSONResponse(w, http.StatusOK, map[string]any{"id": tid, "is_locked": false, "unapproved": true})
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
	if req.Approve {
		approver, ok := s.store.(interface {
			ApproveTerminal(ctx context.Context, terminalID int64, tr store.TerminalRow, names map[string]string) error
		})
		if !ok {
			writeJSONResponse(w, http.StatusNotImplemented, map[string]any{"error": "approve not supported by store"})
			return
		}
		now := time.Now().Unix()
		if err := approver.ApproveTerminal(r.Context(), tid, store.TerminalRow{ID: tid, Lat: req.Lat, Lon: req.Lon, LastVerifiedAt: &now}, names); err != nil {
			writeJSONResponse(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		_ = s.store.SaveProvenance(r.Context(), model.Provenance{EntityType: "terminal", EntityID: tid, Source: "manual", Confidence: 1.0, ObservedAt: time.Now(), ActorID: actorID, Channel: model.ChannelLocalFile})
		if req.Settlement != "" {
			if tagger, ok := s.store.(interface {
				SetTerminalTag(ctx context.Context, id int64, key, value string) error
			}); ok {
				_ = tagger.SetTerminalTag(r.Context(), tid, "settlement", strings.TrimSpace(req.Settlement))
			}
		}
		if resolver, ok := s.store.(interface {
			ResolveReviewQueue(ctx context.Context, entityType string, entityID int64, reason, state string) error
		}); ok {
			if entries, err := s.listTerminalReviewReasons(r.Context(), tid); err == nil {
				for _, reason := range entries {
					_ = resolver.ResolveReviewQueue(r.Context(), "terminal", tid, reason, "resolved")
				}
			}
		}
		_ = s.store.WriteAuditLog(r.Context(), actorID, "approve_terminal", "terminal", &tid, fmt.Sprintf(`{"name":%q,"lat":%f,"lon":%f}`, req.Name, req.Lat, req.Lon))
		mcp.BumpCanonVersion()
		writeJSONResponse(w, http.StatusOK, map[string]any{"id": tid, "is_locked": true, "approved": true, "last_verified_at": now})
		return
	}
	// issue #8/#14: правка залоченного терминала без approve — прямое
	// обновление (ApproveTerminal), а не UpsertTerminal, который для
	// залоченных молча не сохраняет имя и создаёт конфликт-ревью.
	now := time.Now().Unix()
	approver, ok := s.store.(interface {
		ApproveTerminal(ctx context.Context, terminalID int64, tr store.TerminalRow, names map[string]string) error
	})
	if !ok {
		writeJSONResponse(w, http.StatusNotImplemented, map[string]any{"error": "approve not supported by store"})
		return
	}
	if err := approver.ApproveTerminal(r.Context(), tid, store.TerminalRow{ID: tid, Lat: req.Lat, Lon: req.Lon, LastVerifiedAt: &now}, names); err != nil {
		writeJSONResponse(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	_ = s.store.SaveProvenance(r.Context(), model.Provenance{EntityType: "terminal", EntityID: tid, Source: "manual", Confidence: 1.0, ObservedAt: time.Now(), ActorID: actorID, Channel: model.ChannelLocalFile})
	if req.Settlement != "" {
		if tagger, ok := s.store.(interface {
			SetTerminalTag(ctx context.Context, id int64, key, value string) error
		}); ok {
			_ = tagger.SetTerminalTag(r.Context(), tid, "settlement", strings.TrimSpace(req.Settlement))
		}
	}
	_ = s.store.WriteAuditLog(r.Context(), actorID, "update_terminal", "terminal", &tid, fmt.Sprintf(`{"name":%q,"lat":%f,"lon":%f}`, req.Name, req.Lat, req.Lon))
	mcp.BumpCanonVersion()
	writeJSONResponse(w, http.StatusOK, map[string]any{"id": tid, "is_locked": true})
}

func (s *Server) handleAdminMergeTerminals(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSONResponse(w, http.StatusServiceUnavailable, map[string]any{"error": "storage disabled"})
		return
	}
	merger, ok := s.store.(interface {
		MergeTerminals(ctx context.Context, oldID, newID int64, reason string, actorID *int64) error
	})
	if !ok {
		writeJSONResponse(w, http.StatusNotImplemented, map[string]any{"error": "merge not supported by store"})
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
		OldID  int64  `json:"old_id"`
		NewID  int64  `json:"new_id"`
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONDecodeError(w, err, s.logger, r.URL.Path)
		return
	}
	if req.OldID == 0 || req.NewID == 0 || req.OldID == req.NewID {
		writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "old_id и new_id обязательны и различны"})
		return
	}
	if req.Reason == "" {
		req.Reason = "manual_merge"
	}
	if err := merger.MergeTerminals(r.Context(), req.OldID, req.NewID, req.Reason, actorID); err != nil {
		writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	_ = s.store.WriteAuditLog(r.Context(), actorID, "merge_terminal", "terminal", &req.NewID, fmt.Sprintf(`{"old_id":%d,"new_id":%d,"reason":%q}`, req.OldID, req.NewID, req.Reason))
	mcp.BumpCanonVersion()
	writeJSONResponse(w, http.StatusOK, map[string]any{"old_id": req.OldID, "new_id": req.NewID, "reason": req.Reason})
}

func (s *Server) handleAdminCanonReset(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSONResponse(w, http.StatusServiceUnavailable, map[string]any{"error": "storage disabled"})
		return
	}
	resetter, ok := s.store.(interface {
		ResetCanonicalData(ctx context.Context, actorID *int64, reason string) (map[string]int, error)
	})
	if !ok {
		writeJSONResponse(w, http.StatusNotImplemented, map[string]any{"error": "reset not supported by store"})
		return
	}
	var req struct {
		Confirm string `json:"confirm"`
		Reason  string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONDecodeError(w, err, s.logger, r.URL.Path)
		return
	}
	if req.Confirm != "RESET" {
		writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "подтверждение обязательно: {\"confirm\":\"RESET\"} — операция необратимо удаляет терминалы и рейсы"})
		return
	}
	user, _ := r.Context().Value(ctxUserKey).(*store.UserRow)
	actorID := func() *int64 {
		if user != nil && user.ID != 0 {
			return &user.ID
		}
		return nil
	}()
	counts, err := resetter.ResetCanonicalData(r.Context(), actorID, req.Reason)
	if err != nil {
		writeJSONResponse(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	_ = s.store.WriteAuditLog(r.Context(), actorID, "canon_reset", "terminal", nil, fmt.Sprintf(`{"reason":%q,"counts":%v}`, req.Reason, counts))
	mcp.BumpCanonVersion()
	writeJSONResponse(w, http.StatusOK, map[string]any{"status": "ok", "deleted": counts})
}

func (s *Server) handleAdminDeleteTerminal(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSONResponse(w, http.StatusServiceUnavailable, map[string]any{"error": "storage disabled"})
		return
	}
	deleter, ok := s.store.(interface {
		DeleteTerminal(ctx context.Context, id int64, actorID *int64) error
	})
	if !ok {
		writeJSONResponse(w, http.StatusNotImplemented, map[string]any{"error": "delete not supported by store"})
		return
	}
	user, _ := r.Context().Value(ctxUserKey).(*store.UserRow)
	actorID := func() *int64 {
		if user != nil && user.ID != 0 {
			return &user.ID
		}
		return nil
	}()
	tid, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	if err := deleter.DeleteTerminal(r.Context(), tid, actorID); err != nil {
		writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	_ = s.store.WriteAuditLog(r.Context(), actorID, "delete_terminal", "terminal", &tid, fmt.Sprintf(`{"id":%d}`, tid))
	mcp.BumpCanonVersion()
	writeJSONResponse(w, http.StatusOK, map[string]any{"id": tid, "deleted": true})
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
		Provider string   `json:"provider"`
		Query    string   `json:"query"`
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
	s.logger.Debug("external-call request", "provider", req.Provider, "query", req.Query, "lat", req.Lat, "lon", req.Lon, "actor", userEmail(user))
	// issue #10: квотный ключ ≠ геокодер-провайдер. Раньше и Rasp, и
	// геокодер писали в одну строку api_quotas('yandex') и блокировали
	// друг друга. Геокодинг ест отдельную квоту yandex_geocode (лимит —
	// geocoder.max_calls), выбор самого геокодера остаётся по req.Provider.
	quotaKey := req.Provider
	if quotaKey == "yandex" {
		quotaKey = "yandex_geocode"
	}
	quotaLimit := store.DefaultQuotaLimit
	if s.cfg != nil && s.cfg.Geocoder.MaxCalls > 0 {
		quotaLimit = s.cfg.Geocoder.MaxCalls
	}
	ok, used, qerr := s.store.TryConsumeQuota(r.Context(), quotaKey, quotaLimit)
	s.logger.Debug("quota check", "provider", quotaKey, "ok", ok, "used", used)
	if qerr != nil {
		s.logger.Error("quota check failed", "provider", quotaKey, "error", qerr)
		writeJSONResponse(w, http.StatusInternalServerError, map[string]any{"error": "проверка квоты не удалась: " + qerr.Error(), "provider": quotaKey})
		return
	}
	if !ok {
		s.logger.Warn("quota exhausted", "provider", quotaKey)
		writeJSONResponse(w, http.StatusTooManyRequests, map[string]any{"error": "quota exhausted", "provider": quotaKey})
		return
	}
	_ = s.store.RecordApiCall(r.Context(), quotaKey, "external_call", 1)
	if req.Query == "" && (req.Lat == nil || req.Lon == nil) {
		writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "query or lat/lon required"})
		return
	}
	if req.Query != "" && len(req.Query) < 2 {
		writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "query too short"})
		return
	}
	if req.Lat != nil && req.Lon != nil {
		if *req.Lat < -90 || *req.Lat > 90 || *req.Lon < -180 || *req.Lon > 180 {
			s.logger.Debug("external-call invalid coords", "lat", *req.Lat, "lon", *req.Lon)
			writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "invalid coords"})
			return
		}
		s.logger.Debug("external-call coords validated", "lat", *req.Lat, "lon", *req.Lon)
	}
	threshold := 0.8
	if s.cfg != nil && s.cfg.Verification.NameSimilarity > 0 {
		threshold = s.cfg.Verification.NameSimilarity
	}
	g := s.getGeocoder()
	if g == nil {
		writeJSONResponse(w, http.StatusServiceUnavailable, map[string]any{"error": "geocoder unavailable"})
		return
	}
	sg, err := geocoderpkg.NewSingle(*s.cfg, httpx.New(s.logger, "geocoder-admin"), req.Provider)
	if err != nil {
		writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "provider": req.Provider, "available": geocoderpkg.RegisteredKinds()})
		return
	}
	result := map[string]any{"provider": req.Provider, "query": req.Query, "actor_id": actorID, "debug": map[string]any{"quota_used": used, "threshold": threshold, "expanded_query": namesim.ExpandAbbreviations(req.Query)}}
	confidence := 0.0
	if req.Query != "" {
		s.logger.Debug("external-call geocode", "query", req.Query, "provider", req.Provider)
		var cands []geocoderpkg.Candidate
		var gerr error
		if m, ok := sg.(geocoderpkg.MultiGeocoder); ok {
			cands, gerr = m.GeocodeCandidates(r.Context(), req.Query, 5)
		} else if res, err := sg.Geocode(r.Context(), req.Query); err == nil && res != nil {
			cands = []geocoderpkg.Candidate{{Lat: res.Lat, Lon: res.Lon, Name: res.Name, Provider: req.Provider}}
		} else {
			gerr = err
		}
		if gerr != nil {
			result["errors"] = map[string]any{req.Provider: gerr.Error()}
			s.logger.Debug("external-call geocode error", "provider", req.Provider, "error", gerr.Error())
		}
		list := make([]map[string]any, 0, len(cands))
		best, bestSim := -1, 0.0
		for i, c := range cands {
			if c.Provider == "" {
				c.Provider = req.Provider
			}
			sim := namesim.Similarity(req.Query, c.Name)
			list = append(list, map[string]any{"name": c.Name, "lat": c.Lat, "lon": c.Lon, "provider": c.Provider, "similarity": sim})
			if sim > bestSim {
				bestSim, best = sim, i
			}
		}
		result["candidates"] = list
		result["found"] = len(list)
		if best < 0 {
			result["validated"] = false
			result["error"] = "not found"
		} else {
			c := cands[best]
			result["name"] = c.Name
			result["lat"] = c.Lat
			result["lon"] = c.Lon
			result["similarity"] = bestSim
			result["validated"] = bestSim >= threshold
			confidence = bestSim
		}
	} else {
		addr, err := sg.Reverse(r.Context(), *req.Lat, *req.Lon)
		if err != nil {
			result["errors"] = map[string]any{req.Provider: err.Error()}
			writeJSONResponse(w, http.StatusBadGateway, map[string]any{"error": "reverse failed", "provider": req.Provider, "details": err.Error(), "debug": result["debug"]})
			return
		}
		result["lat"] = *req.Lat
		result["lon"] = *req.Lon
		result["address"] = addr
		settlement := namesim.ExtractSettlement(addr)
		if sr, ok := sg.(geocoderpkg.SettlementReverser); ok {
			if st, serr := sr.ReverseSettlement(r.Context(), *req.Lat, *req.Lon); serr == nil && st != "" {
				settlement = st
			} else if serr != nil {
				result["errors"] = map[string]any{req.Provider + "/settlement": serr.Error()}
			}
		}
		if settlement != "" {
			result["settlement"] = settlement
		}
		result["validated"] = true
		confidence = 0.8
	}
	s.logger.Info("external-call done", "provider", req.Provider, "query", req.Query, "validated", result["validated"], "actor", userEmail(user))
	details, _ := json.Marshal(result)
	_ = s.store.WriteAuditLog(r.Context(), actorID, "external_call", "terminal", nil, string(details))
	channel := provenanceChannelForProvider(req.Provider)
	_ = s.store.SaveProvenance(r.Context(), model.Provenance{EntityType: "terminal", EntityID: 0, Source: req.Provider, Confidence: confidence, ObservedAt: time.Now(), ActorID: actorID, Raw: details, Channel: channel})
	writeJSONResponse(w, http.StatusOK, result)
}

func (s *Server) handleAdminPage(w http.ResponseWriter, r *http.Request) {
	reqID := fmt.Sprint(r.Context().Value(ctxKey("request_id")))
	s.logger.Info("admin page", "method", r.Method, "path", r.URL.Path, "remote", r.RemoteAddr, "request_id", reqID)
	s.logger.Debug("admin page debug", "headers", redactedHeaders(r.Header), "query", redactedQuery(r.URL.RawQuery))
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
