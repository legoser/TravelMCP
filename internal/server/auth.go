package server

// Authentication: admin token / API key verification, password helpers.

import (
	"context"
	"sort"
	"strings"

	"crypto/subtle"
	"golang.org/x/crypto/bcrypt"
	"net/http"
	"net/url"

	_ "travelmcp/internal/adapters/nominatim"
	_ "travelmcp/internal/adapters/yandex"
	"travelmcp/internal/store"
)

type ctxKey string

const (
	ctxUserKey ctxKey = "user"

	defaultPageLimit    = 20
	maxPageLimit        = 100
	maxMultipartFormMem = 32 << 20
	maxMCPBodySize      = 1 << 20
	maxLogBodySize      = 4096
)

func (s *Server) auth(next http.Handler, requiredScope string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.logger.DebugContext(r.Context(), "auth check", "path", r.URL.Path, "required", requiredScope, "remote", r.RemoteAddr)
		if s.cfg.Auth.AdminToken == "" {
			s.logger.ErrorContext(r.Context(), "auth error: ADMIN_TOKEN empty", "path", r.URL.Path)
			w.Header().Set("WWW-Authenticate", `Bearer realm="travelmcp"`)
			writeJSONResponse(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized", "message": "auth system is misconfigured"})
			return
		}
		key := ""
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			key = strings.TrimPrefix(h, "Bearer ")
		} else if h := r.Header.Get("X-API-Key"); h != "" {
			key = h
		}
		if subtle.ConstantTimeCompare([]byte(key), []byte(s.cfg.Auth.AdminToken)) == 1 {
			s.logger.DebugContext(r.Context(), "auth admin token", "path", r.URL.Path)
			ctx := context.WithValue(r.Context(), ctxUserKey, &store.UserRow{ID: 0, Email: "admin", Role: "admin", Status: "active"})
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		if key == "" {
			s.logger.WarnContext(r.Context(), "auth missing key", "path", r.URL.Path, "remote", r.RemoteAddr)
			w.Header().Set("WWW-Authenticate", `Bearer realm="travelmcp"`)
			writeJSONResponse(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized", "message": "API key required: Authorization: Bearer <key> or X-API-Key"})
			return
		}
		if s.store == nil {
			s.logger.WarnContext(r.Context(), "auth store disabled", "path", r.URL.Path)
			w.Header().Set("WWW-Authenticate", `Bearer realm="travelmcp"`)
			writeJSONResponse(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized", "message": "invalid API key"})
			return
		}
		ak, ok := s.store.GetApiKey(r.Context(), key)
		if !ok {
			s.logger.WarnContext(r.Context(), "auth invalid key", "path", r.URL.Path, "key_prefix", keyPrefix(key))
			writeJSONResponse(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized", "message": "invalid API key"})
			return
		}
		user, ok := s.store.GetUserByID(r.Context(), ak.UserID)
		if !ok || user.Status != "active" {
			s.logger.WarnContext(r.Context(), "auth inactive user", "path", r.URL.Path, "user_id", ak.UserID, "status", user.Status)
			writeJSONResponse(w, http.StatusForbidden, map[string]any{"error": "forbidden", "message": "user not active or pending moderation"})
			return
		}
		if requiredScope != "" && !hasScope(ak.Scopes, requiredScope) && user.Role != "admin" {
			s.logger.WarnContext(r.Context(), "auth insufficient scope", "path", r.URL.Path, "user", user.Email, "scopes", ak.Scopes, "required", requiredScope)
			writeJSONResponse(w, http.StatusForbidden, map[string]any{"error": "forbidden", "message": "insufficient scope"})
			return
		}
		s.logger.DebugContext(r.Context(), "auth ok", "path", r.URL.Path, "user", user.Email, "role", user.Role, "scopes", ak.Scopes)
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

func userEmail(u *store.UserRow) string {
	if u == nil {
		return ""
	}
	return u.Email
}

func userPublic(u *store.UserRow) map[string]any {
	return map[string]any{"id": u.ID, "email": u.Email, "status": u.Status, "role": u.Role, "created_at": u.CreatedAt, "config": u.Config}
}

var logSafeHeaders = map[string]bool{
	"Content-Type": true, "Accept": true, "Accept-Language": true,
	"Content-Length": true, "User-Agent": true,
	"X-Request-ID": true, "X-Trace-ID": true, "X-Span-ID": true,
}

func redactedHeaders(h http.Header) map[string]string {
	out := make(map[string]string, len(logSafeHeaders))
	for k := range logSafeHeaders {
		if v := h.Get(k); v != "" {
			out[k] = v
		}
	}
	return out
}

func redactedQuery(raw string) string {
	if raw == "" {
		return ""
	}
	q, err := url.ParseQuery(raw)
	if err != nil {
		return "***"
	}
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k+"=***")
	}
	sort.Strings(keys)
	return strings.Join(keys, "&")
}

func redactMapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
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
