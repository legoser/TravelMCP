package server

import (
	"encoding/json"
	"net/http"

	store "travelmcp/internal/store"
)

func (s *Server) identifierSchemeStore() store.IdentifierSchemeStore {
	c, _ := s.store.(store.IdentifierSchemeStore)
	return c
}

func (s *Server) handleListIdentifierSchemes(w http.ResponseWriter, r *http.Request) {
	c := s.identifierSchemeStore()
	if c == nil {
		writeJSONResponse(w, http.StatusOK, map[string]any{"items": []any{}})
		return
	}
	items, err := c.ListIdentifierSchemes(r.Context())
	if err != nil {
		writeJSONResponse(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSONResponse(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleUpsertIdentifierScheme(w http.ResponseWriter, r *http.Request) {
	c := s.identifierSchemeStore()
	if c == nil {
		writeJSONResponse(w, http.StatusServiceUnavailable, map[string]any{"error": "storage disabled"})
		return
	}
	var row store.IdentifierSchemeRow
	if err := json.NewDecoder(r.Body).Decode(&row); err != nil {
		writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}
	if row.Code == "" || row.System == "" || row.DisplayName == "" {
		writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "code, system и display_name обязательны"})
		return
	}
	if err := c.UpsertIdentifierScheme(r.Context(), row); err != nil {
		writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSONResponse(w, http.StatusOK, row)
}

func (s *Server) handleDeleteIdentifierScheme(w http.ResponseWriter, r *http.Request) {
	c := s.identifierSchemeStore()
	if c == nil {
		writeJSONResponse(w, http.StatusServiceUnavailable, map[string]any{"error": "storage disabled"})
		return
	}
	code := r.PathValue("code")
	if code == "" {
		writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "invalid code"})
		return
	}
	if err := c.DeleteIdentifierScheme(r.Context(), code); err != nil {
		writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSONResponse(w, http.StatusOK, map[string]any{"deleted": code})
}
