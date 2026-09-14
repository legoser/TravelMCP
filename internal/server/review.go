package server

// Review endpoints and terminal/trip snapshot helpers for manual data verification.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"encoding/json"
	"net/http"

	_ "travelmcp/internal/adapters/nominatim"
	_ "travelmcp/internal/adapters/yandex"
	"travelmcp/internal/model"
	"travelmcp/internal/store"
	"travelmcp/internal/support/namesim"
	syncpkg "travelmcp/internal/sync"
)

func (s *Server) handleReview(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSONResponse(w, http.StatusOK, []any{})
		return
	}
	if lister, ok := s.store.(interface {
		ListReviewQueue(ctx context.Context, limit int) ([]store.ReviewQueueRow, error)
	}); ok {
		rows, _ := lister.ListReviewQueue(r.Context(), 50)
		out := make([]map[string]any, 0, len(rows))
		for _, rq := range rows {
			item := map[string]any{"entity_type": rq.EntityType, "entity_id": rq.EntityID, "reason": rq.Reason, "score": rq.Score, "created_at": rq.CreatedAt, "fingerprint": rq.Fingerprint}
			if rq.EntityType == "terminal" {
				if snap := s.terminalSnapshot(r.Context(), rq.EntityID); snap != nil {
					item["terminal"] = snap
					if st, _ := snap["settlement"].(string); st != "" {
						item["duplicates"] = s.findDuplicates(r.Context(), rq.EntityID, snap)
					}
				}
			}
			if rq.EntityType == "trip" {
				item["trip"] = s.tripSnapshot(r.Context(), rq.Fingerprint)
			}
			out = append(out, item)
		}
		writeJSONResponse(w, http.StatusOK, out)
		return
	}
	writeJSONResponse(w, http.StatusOK, []any{})
}

func (s *Server) terminalSnapshot(ctx context.Context, id int64) map[string]any {
	getter, ok := s.store.(interface {
		GetTerminal(ctx context.Context, id int64) (map[string]any, error)
	})
	if !ok {
		return nil
	}
	t, err := getter.GetTerminal(ctx, id)
	if err != nil {
		return map[string]any{"id": id, "missing": true}
	}
	s.withSettlements(ctx, []map[string]any{t})
	return t
}

// Возвращает откуда/куда/когда (терминалы, координаты, города, времена).
func (s *Server) tripSnapshot(ctx context.Context, fingerprint string) map[string]any {
	out := map[string]any{"fingerprint": fingerprint}
	source, routeNK, tripNK, ok := syncpkg.ParseTripReviewFingerprint(fingerprint)
	if !ok {
		out["missing"] = true
		out["detail"] = "нечитаемый fingerprint"
		return out
	}
	out["source"] = source
	out["route_nk"] = routeNK
	out["trip_nk"] = tripNK
	ts, ok := s.store.(interface {
		FindRouteID(ctx context.Context, source, routeCode string) (int64, bool)
		FindTrip(ctx context.Context, routeID int64, tripCode string) (store.TripRow, bool)
	})
	if !ok {
		out["missing"] = true
		return out
	}
	// живой трип в каноне: полный маршрут с временами
	if routeID, found := ts.FindRouteID(ctx, source, routeNK); found {
		if trip, found := ts.FindTrip(ctx, routeID, syncpkg.SplitTripNK(tripNK)); found && trip.ValidTo == nil {
			out["trip_id"] = trip.ID
			out["service_days"] = trip.ServiceDays
			if st, err := s.tripStopsFromCanon(ctx, trip.ID); err == nil && len(st) > 0 {
				out["stops"] = st
				out["from"], out["to"] = st[0], st[len(st)-1]
				return out
			}
		}
	}
	// staging: трип не в каноне, но matched_stop_times хранит матчинг
	st := s.tripStopsFromStaging(ctx, source, routeNK, syncpkg.SplitTripNK(tripNK))
	if len(st) > 0 {
		out["staged"] = true
		out["stops"] = st
		out["from"], out["to"] = st[0], st[len(st)-1]
		return out
	}
	out["missing"] = true
	out["detail"] = "трип не найден ни в каноне, ни в staging (пересбор?)"
	return out
}

func (s *Server) tripStopsFromCanon(ctx context.Context, tripID int64) ([]map[string]any, error) {
	getter, ok := s.store.(interface {
		GetTripStopTimes(ctx context.Context, tripID int64) ([]map[string]any, error)
	})
	if !ok {
		return nil, nil
	}
	items, err := getter.GetTripStopTimes(ctx, tripID)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		st := map[string]any{
			"seq": it["seq"], "name": it["name"],
			"arrival": it["arrival"], "departure": it["departure"],
			"arrival_hhmm": secsToHHMM(it["arrival"]), "departure_hhmm": secsToHHMM(it["departure"]),
			"is_provisional": it["is_provisional"], "match_score": it["match_score"],
		}
		out = append(out, st)
	}
	return out, nil
}

func (s *Server) tripStopsFromStaging(ctx context.Context, source, routeCode, tripCode string) []map[string]any {
	lister, ok := s.store.(interface {
		ListStagingTrips(ctx context.Context, region string, limit int) ([]store.StagingTripRow, error)
	})
	if !ok {
		return nil
	}
	rows, err := lister.ListStagingTrips(ctx, "", 5000)
	if err != nil {
		return nil
	}
	var raw string
	for _, r := range rows {
		if r.Source == source && r.ExternalRouteCode == routeCode && r.ExternalTripCode == tripCode {
			raw = r.MatchedStopTimes
			break
		}
	}
	if raw == "" {
		return nil
	}
	var matched []struct {
		Seq        int     `json:"seq"`
		TerminalID int64   `json:"terminal_id"`
		ArrivalS   int     `json:"arrival_s"`
		DepartureS int     `json:"departure_s"`
		IsFuzzy    bool    `json:"is_fuzzy"`
		MatchScore float64 `json:"match_score"`
	}
	if err := json.Unmarshal([]byte(raw), &matched); err != nil || len(matched) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(matched))
	for _, mst := range matched {
		st := map[string]any{
			"seq": mst.Seq, "terminal_id": mst.TerminalID,
			"arrival": mst.ArrivalS, "departure": mst.DepartureS,
			"arrival_hhmm": secsToHHMM(mst.ArrivalS), "departure_hhmm": secsToHHMM(mst.DepartureS),
			"is_fuzzy": mst.IsFuzzy, "match_score": mst.MatchScore,
		}
		if snap := s.terminalSnapshot(ctx, mst.TerminalID); snap != nil {
			if _, missing := snap["missing"]; !missing {
				st["name"] = snap["name"]
				st["lat"] = snap["lat"]
				st["lon"] = snap["lon"]
				if v, ok := snap["settlement"]; ok {
					st["settlement"] = v
				}
			} else {
				st["missing_terminal"] = true
			}
		}
		out = append(out, st)
	}
	return out
}

func secsToHHMM(v any) string {
	n, ok := toInt(v)
	if !ok {
		return ""
	}
	n %= 86400
	if n < 0 {
		n += 86400
	}
	return fmt.Sprintf("%02d:%02d", n/3600, (n%3600)/60)
}

func toInt(v any) (int, bool) {
	switch x := v.(type) {
	case int:
		return x, true
	case int64:
		return int(x), true
	case float64:
		return int(x), true
	}
	return 0, false
}

func (s *Server) findDuplicates(ctx context.Context, selfID int64, snap map[string]any) []map[string]any {
	name, _ := snap["name"].(string)
	settlement, _ := snap["settlement"].(string)
	if name == "" {
		return nil
	}
	lister, ok := s.store.(interface {
		ListTerminalsFiltered(ctx context.Context, limit, offset int, sort, order, q string) ([]map[string]any, int, error)
	})
	if !ok {
		return nil
	}
	q := settlement
	if q == "" {
		if core := namesim.Core(name); core != "" {
			parts := strings.Fields(core)
			if len(parts) > 0 {
				q = parts[len(parts)-1]
			}
		}
	}
	items, _, err := lister.ListTerminalsFiltered(ctx, 50, 0, "id", "asc", q)
	if err != nil {
		return nil
	}
	var out []map[string]any
	for _, it := range items {
		idf, _ := it["id"].(int64)
		if idf == 0 || idf == selfID {
			continue
		}
		iname, _ := it["name"].(string)
		if sim := namesim.Similarity(name, iname); sim >= 0.6 {
			it["similarity"] = sim
			out = append(out, it)
			if len(out) >= 3 {
				break
			}
		}
	}
	return out
}

func (s *Server) handleReviewResolve(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSONResponse(w, http.StatusServiceUnavailable, map[string]any{"error": "storage disabled"})
		return
	}
	if r.Method != http.MethodPost {
		writeJSONResponse(w, http.StatusMethodNotAllowed, map[string]any{"error": "POST required"})
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
		EntityType string `json:"entity_type"`
		EntityID   int64  `json:"entity_id"`
		Reason     string `json:"reason"`
		Action     string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONDecodeError(w, err, s.logger, r.URL.Path)
		return
	}
	if req.Action != "approve" && req.Action != "dismiss" {
		writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "action must be approve or dismiss"})
		return
	}
	resolver, ok := s.store.(interface {
		ResolveReviewQueue(ctx context.Context, entityType string, entityID int64, reason, state string) error
	})
	if !ok {
		writeJSONResponse(w, http.StatusServiceUnavailable, map[string]any{"error": "review not supported"})
		return
	}
	if req.Action == "approve" && req.EntityType == "terminal" {
		if snap := s.terminalSnapshot(r.Context(), req.EntityID); snap != nil {
			if _, missing := snap["missing"]; !missing {
				lat, _ := snap["lat"].(float64)
				lon, _ := snap["lon"].(float64)
				tr := store.TerminalRow{ID: req.EntityID, Lat: lat, Lon: lon, IsLocked: true}
				if _, err := s.store.UpsertTerminal(r.Context(), tr, nil, nil); err != nil {
					writeJSONResponse(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
					return
				}
				_ = s.store.SaveProvenance(r.Context(), model.Provenance{EntityType: "terminal", EntityID: req.EntityID, Source: "manual", Confidence: 1.0, ObservedAt: time.Now(), ActorID: actorID, Channel: model.ChannelLocalFile})
			}
		}
	}
	// issue #8/#14: sticky-резолв вместо DELETE — закрытая запись не
	// пере-открывается повторным импортом того же конфликта.
	resolveState := "resolved"
	if req.Action == "dismiss" {
		resolveState = "rejected"
	}
	if err := resolver.ResolveReviewQueue(r.Context(), req.EntityType, req.EntityID, req.Reason, resolveState); err != nil {
		writeJSONResponse(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	_ = s.store.WriteAuditLog(r.Context(), actorID, "review_"+req.Action, req.EntityType, &req.EntityID, fmt.Sprintf(`{"reason":%q}`, req.Reason))
	writeJSONResponse(w, http.StatusOK, map[string]any{"status": "ok", "action": req.Action})
}

func (s *Server) handleReviewExport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="review.csv"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("entity_type,entity_id,reason,score,created_at\n"))
}

func (s *Server) listTerminalReviewReasons(ctx context.Context, terminalID int64) ([]string, error) {
	lister, ok := s.store.(interface {
		ListTerminalReviewEntries(ctx context.Context, terminalID int64) ([]store.ReviewQueueRow, error)
	})
	if !ok {
		return nil, nil
	}
	entries, err := lister.ListTerminalReviewEntries(ctx, terminalID)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := []string{}
	for _, e := range entries {
		if !seen[e.Reason] {
			seen[e.Reason] = true
			out = append(out, e.Reason)
		}
	}
	return out, nil
}

// provenanceChannelForProvider — канал доставки внешнего вызова (план §3.3):
// MOTIS-коннекторы ходят в локальный инстанс, остальные внешние геокодеры —
// live HTTP. Канон значений в model.Channel*.
func provenanceChannelForProvider(provider string) string {
	switch provider {
	case "motis", "transitous":
		return model.ChannelLocalMotis
	default:
		return model.ChannelLocalFile
	}
}
