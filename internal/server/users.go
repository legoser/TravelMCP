package server

// User and API-key management endpoints, runtime configuration API.

import (
	"fmt"
	"strconv"
	"strings"

	"encoding/json"
	"log/slog"
	"net/http"
	"net/mail"

	_ "travelmcp/internal/adapters/nominatim"
	_ "travelmcp/internal/adapters/yandex"
	"travelmcp/internal/store"
)

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
		"http":          map[string]any{"addr": cfg.HTTP.Addr, "rate_limit": cfg.HTTP.RateLimit},
		"store":         map[string]any{"dsn": maskDSNShort(cfg.Store.DSN), "kind": cfg.Store.Kind},
		"providers":     cfg.Providers,
		"planner":       cfg.Planner,
		"log":           cfg.Log,
		"telemetry":     cfg.Telemetry,
		"geocoder":      map[string]any{"kind": cfg.Geocoder.Kind, "attempts": cfg.Geocoder.Attempts, "limit": cfg.Geocoder.Limit},
		"yandex":        map[string]any{"rasp_url": cfg.Yandex.RaspURL, "geocode_url": cfg.Yandex.GeocodeURL, "geocode_key": ""},
		"nominatim":     map[string]any{"url": cfg.Nominatim.URL},
		"motis":         map[string]any{"url": cfg.Motis.URL},
		"verification":  cfg.Verification,
		"deduplication": cfg.Deduplication,
		"pricing":       cfg.Pricing,
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
	if v, ok := req["store"]; ok {
		if m, ok := v.(map[string]any); ok {
			if dsn, ok := m["dsn"].(string); ok && dsn != "" {
				s.cfg.Store.DSN = dsn
			}
		}
	}
	if v, ok := req["motis"]; ok {
		if m, ok := v.(map[string]any); ok {
			if u, ok := m["url"].(string); ok {
				s.cfg.Motis.URL = u
			}
		}
	}
	if v, ok := req["geocoder"]; ok {
		if m, ok := v.(map[string]any); ok {
			if k, ok := m["kind"].(string); ok {
				s.cfg.Geocoder.Kind = k
			}
		}
	}
	if v, ok := req["nominatim"]; ok {
		if m, ok := v.(map[string]any); ok {
			if u, ok := m["url"].(string); ok {
				s.cfg.Nominatim.URL = u
			}
		}
	}
	if v, ok := req["deduplication"]; ok {
		if m, ok := v.(map[string]any); ok {
			if d, ok := m["distance_m"].(float64); ok {
				s.cfg.Deduplication.DistanceM = int(d)
			}
		}
	}
	if v, ok := req["verification"]; ok {
		if m, ok := v.(map[string]any); ok {
			if ct, ok := m["confidence_threshold"].(float64); ok {
				s.cfg.Verification.ConfidenceThreshold = ct
			}
			if dm, ok := m["distance_m"].(float64); ok {
				s.cfg.Verification.DistanceM = int(dm)
			}
		}
	}
	s.logger.Info("config updated via API", "providers", s.cfg.Providers.Enabled, "planner", s.cfg.Planner.Engine, "log_level", s.cfg.Log.Level, "store", s.cfg.Store.DSN, "motis", s.cfg.Motis.URL)
	if s.cfg.Log.Level == "debug" {
		s.logger.Debug("config debug after update", "cfg", fmt.Sprint(s.cfg))
	}
	slog.SetDefault(s.logger)
	writeJSONResponse(w, http.StatusOK, map[string]any{"status": "ok", "providers": s.cfg.Providers.Enabled, "planner": s.cfg.Planner.Engine, "log": s.cfg.Log, "store": s.cfg.Store, "motis": s.cfg.Motis, "geocoder": s.cfg.Geocoder, "nominatim": s.cfg.Nominatim, "verification": s.cfg.Verification, "deduplication": s.cfg.Deduplication})
}
