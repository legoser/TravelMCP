package server

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"travelmcp/internal/model"
	store "travelmcp/internal/store"
)

func (s *Server) browserStore() store.BrowserStore {
	b, _ := s.store.(store.BrowserStore)
	return b
}

func adminLimitOffset(r *http.Request) (int, int) {
	limit, offset := 20, 0
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
	return limit, offset
}

func (s *Server) handleAdminListRoutes(w http.ResponseWriter, r *http.Request) {
	b := s.browserStore()
	if b == nil {
		writeJSONResponse(w, http.StatusOK, map[string]any{"items": []any{}, "total": 0})
		return
	}
	limit, offset := adminLimitOffset(r)
	q := r.URL.Query().Get("q")
	items, total, err := b.ListRoutesAdmin(r.Context(), limit, offset, q)
	if err != nil {
		writeJSONResponse(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSONResponse(w, http.StatusOK, map[string]any{"items": items, "total": total, "limit": limit, "offset": offset, "q": q})
}

func (s *Server) handleAdminListTrips(w http.ResponseWriter, r *http.Request) {
	b := s.browserStore()
	if b == nil {
		writeJSONResponse(w, http.StatusOK, map[string]any{"items": []any{}, "total": 0})
		return
	}
	routeID, err := strconv.ParseInt(r.URL.Query().Get("route_id"), 10, 64)
	if err != nil || routeID <= 0 {
		writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "route_id required"})
		return
	}
	limit, offset := adminLimitOffset(r)
	items, total, err := b.ListTripsAdmin(r.Context(), routeID, limit, offset)
	if err != nil {
		writeJSONResponse(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSONResponse(w, http.StatusOK, map[string]any{"items": items, "total": total, "route_id": routeID, "limit": limit, "offset": offset})
}

func (s *Server) handleAdminGetTrip(w http.ResponseWriter, r *http.Request) {
	b := s.browserStore()
	if b == nil {
		writeJSONResponse(w, http.StatusOK, map[string]any{"items": []any{}})
		return
	}
	tripID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || tripID <= 0 {
		writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	items, err := b.GetTripStopTimes(r.Context(), tripID)
	if err != nil {
		writeJSONResponse(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSONResponse(w, http.StatusOK, map[string]any{"trip_id": tripID, "items": items})
}

func (s *Server) handleAdminTerminalCard(w http.ResponseWriter, r *http.Request) {
	b := s.browserStore()
	if b == nil || s.store == nil {
		writeJSONResponse(w, http.StatusServiceUnavailable, map[string]any{"error": "storage disabled"})
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	ctx := r.Context()
	card, err := s.store.GetTerminal(ctx, id)
	if err != nil {
		writeJSONResponse(w, http.StatusNotFound, map[string]any{"error": err.Error()})
		return
	}
	if tags, err := s.store.GetTerminalTags(ctx, id); err == nil && len(tags) > 0 {
		card["tags"] = tags
	}
	if stats, err := b.GetTerminalStats(ctx, id); err == nil {
		card["stats"] = stats
	}
	if aliases, err := b.ListTerminalAliases(ctx, id); err == nil && len(aliases) > 0 {
		list := make([]map[string]any, 0, len(aliases))
		for _, a := range aliases {
			list = append(list, map[string]any{"alias": a.Alias, "lang": a.Lang, "source": a.Source})
		}
		card["aliases"] = list
	}
	if reviews, err := b.ListTerminalReviewEntries(ctx, id); err == nil && len(reviews) > 0 {
		list := make([]map[string]any, 0, len(reviews))
		for _, rq := range reviews {
			list = append(list, map[string]any{"reason": rq.Reason, "score": rq.Score, "created_at": rq.CreatedAt, "fingerprint": rq.Fingerprint})
		}
		card["review"] = list
	}
	if idents, err := s.listTerminalIdentifiers(ctx, id); err == nil && len(idents) > 0 {
		card["identifiers"] = idents
	}
	writeJSONResponse(w, http.StatusOK, card)
}

func (s *Server) listTerminalIdentifiers(ctx context.Context, terminalID int64) ([]map[string]any, error) {
	lister, ok := s.store.(interface {
		ListTerminalCodes(ctx context.Context, terminalID int64, system string) ([]model.AdaptedIdentifier, error)
	})
	if !ok {
		return nil, nil
	}
	schemes, ok := s.store.(store.IdentifierSchemeStore)
	if !ok {
		return nil, nil
	}
	systems, err := schemes.ListIdentifierSystems(ctx)
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, sys := range systems {
		codes, err := lister.ListTerminalCodes(ctx, terminalID, sys)
		if err != nil {
			continue
		}
		for _, c := range codes {
			out = append(out, map[string]any{"system": sys, "code_type": c.CodeType, "code": c.Code})
		}
	}
	return out, nil
}

func (s *Server) handleAdminTerminalSchedule(w http.ResponseWriter, r *http.Request) {
	b := s.browserStore()
	if b == nil {
		writeJSONResponse(w, http.StatusOK, map[string]any{"items": []any{}})
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	date := time.Now()
	if v := r.URL.Query().Get("date"); v != "" {
		parsed, err := time.Parse("2006-01-02", v)
		if err != nil {
			writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "invalid date, YYYY-MM-DD expected"})
			return
		}
		date = parsed
	}
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	items, err := b.GetTerminalSchedule(r.Context(), id, date, limit)
	if err != nil {
		writeJSONResponse(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSONResponse(w, http.StatusOK, map[string]any{"terminal_id": id, "date": date.Format("2006-01-02"), "items": items})
}

func (s *Server) handleAdminListTerminalsLiveness(w http.ResponseWriter, r *http.Request) {
	b := s.browserStore()
	if b == nil {
		writeJSONResponse(w, http.StatusOK, map[string]any{"items": []any{}, "total": 0})
		return
	}
	dead := r.URL.Query().Get("dead")
	if dead != "yes" && dead != "no" && dead != "" {
		writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "dead must be yes|no|empty"})
		return
	}
	limit, offset := adminLimitOffset(r)
	items, total, err := b.ListTerminalsByLiveness(r.Context(), limit, offset, dead)
	if err != nil {
		writeJSONResponse(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSONResponse(w, http.StatusOK, map[string]any{"items": s.withSettlements(r.Context(), items), "total": total, "dead": dead, "limit": limit, "offset": offset})
}
