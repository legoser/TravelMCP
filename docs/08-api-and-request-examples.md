# 08. API and Request Examples

Base URL: `http://localhost:8080`. Configuration: `configs/config.example.yaml`. `ADMIN_TOKEN` is read from `env` or `configs/*.yaml`. Logging: `LOG_FORMAT=json|text`, `LOG_LEVEL=info|debug`, unified `EN` `slog`.

## 1. Authentication

- `ADMIN_TOKEN` bootstraps the `admin` account (`Bearer secret` or `X-API-Key: secret`), bypasses the `DB`, and uses `role=admin`.
- User API keys `tm_<user>_<nano>` with `scopes=mcp:read|admin` are stored in `users/api_keys` in `sqlite`. `LoadNetwork` takes `14ms`. If `ADMIN_TOKEN==""`, authentication is `open` for testing.
- Scopes: `mcp:read` for `/mcp`, `/providers`, `/dashboard`, `/keys`, `/me`; `admin` for `/users`, `/admin`.

```sh
# admin token from .env
ADMIN_TOKEN=secret DATABASE_DSN=sqlite:///tmp/app.db go run ./cmd/mcp-server
```

## 2. System

### GET /healthz — no auth

```sh
curl http://localhost:8080/healthz
# {"status":"ok"}
```

### GET /readyz — provider health

```sh
curl http://localhost:8080/readyz
# {"status":"ok","uptime_seconds":12} or 503 {"status":"degraded"}
```

## 3. Auth — Registration and Moderation

### POST /api/v1/register — no auth, creates a pending user

```sh
curl -X POST http://localhost:8080/api/v1/register \
-H "Content-Type: application/json" \
-d '{"email":"alice@test.com","password":"pass1234"}'
# 201 {"id":1,"email":"alice@test.com","status":"pending","message":"pending moderation"}
```

### GET /api/v1/users — admin

```sh
curl http://localhost:8080/api/v1/users -H "Authorization: Bearer secret"
# [{"id":1,"email":"alice@test.com","status":"pending","role":"user","created_at":...,"config":""}]
```

### GET /api/v1/users/{id} — admin

```sh
curl http://localhost:8080/api/v1/users/1 -H "Authorization: Bearer secret"
# {"id":1,"email":"alice@test.com","status":"active","role":"user","created_at":...,"config":""}
```

### PATCH /api/v1/users/{id} — admin (generic update)

```sh
curl -X PATCH http://localhost:8080/api/v1/users/1 \
-H "Authorization: Bearer secret" -H "Content-Type: application/json" \
-d '{"status":"active","role":"admin","config":"{\"max_walk\":30}"}'
# partial update: any of status|role|config; role=user|admin, status=pending|active|blocked
```

### DELETE /api/v1/users/{id} — admin (cascade deletes API keys)

```sh
curl -X DELETE http://localhost:8080/api/v1/users/1 -H "Authorization: Bearer secret"
# {"deleted":1}
```

### POST /api/v1/users/{id}/moderate — admin (legacy)

```sh
curl -X POST http://localhost:8080/api/v1/users/1/moderate \
-H "Authorization: Bearer secret" -H "Content-Type: application/json" \
-d '{"status":"active"}' # pending|active|blocked
```

### POST /api/v1/login — no auth, active users

```sh
curl -X POST http://localhost:8080/api/v1/login \
-H "Content-Type: application/json" \
-d '{"email":"alice@test.com","password":"pass1234"}'
# 200 {"token":"tm_1_...","scopes":"mcp:read","user_id":1}
# 403 {"error":"user not active","status":"pending"} before moderation
```

### GET /api/v1/me — mcp:read

```sh
curl http://localhost:8080/api/v1/me -H "Authorization: Bearer tm_1_..."
# {"id":1,"email":"alice@test.com","status":"active","role":"user","config":""}
```

### PUT /api/v1/users/{id}/config — admin, per-user overrides

```sh
curl -X PUT http://localhost:8080/api/v1/users/1/config \
-H "Authorization: Bearer secret" -H "Content-Type: application/json" \
-d '{"config":"{\"providers\":[\"intercity\"],\"log_level\":\"debug\"}"}'
```

## 4. API Keys

### GET /api/v1/keys — mcp:read, own keys

```sh
curl http://localhost:8080/api/v1/keys -H "Authorization: Bearer tm_1_..."
```

### POST /api/v1/keys — mcp:read

```sh
curl -X POST http://localhost:8080/api/v1/keys \
-H "Authorization: Bearer tm_1_..." -H "Content-Type: application/json" \
-d '{"scopes":"mcp:read"}'
# 201 {"ID":2,"Key":"tm_1_...","Scopes":"mcp:read"}
```

### DELETE /api/v1/keys/{id} — mcp:read

```sh
curl -X DELETE http://localhost:8080/api/v1/keys/2 -H "Authorization: Bearer tm_1_..."
```

### GET /api/v1/users/{id}/keys — admin (user's keys)

```sh
curl http://localhost:8080/api/v1/users/1/keys -H "Authorization: Bearer secret"
# [{"ID":2,"Key":"tm_...","Scopes":"mcp:read",...}]
```

### POST /api/v1/users/{id}/keys — admin

```sh
curl -X POST http://localhost:8080/api/v1/users/1/keys \
-H "Authorization: Bearer secret" -H "Content-Type: application/json" \
-d '{"scopes":"mcp:read"}' # mcp:read|admin
# 201 {"ID":3,"Key":"tm_...","Scopes":"mcp:read"}
```

### DELETE /api/v1/users/{id}/keys/{keyId} — admin

```sh
curl -X DELETE http://localhost:8080/api/v1/users/1/keys/2 -H "Authorization: Bearer secret"
```

### GET /api/v1/config — admin (hot-config snapshot)

```sh
curl http://localhost:8080/api/v1/config -H "Authorization: Bearer secret"
# {"providers":{"enabled":["synth"],"intercity":{...}},"planner":{"engine":"csa"},...} DSN is masked
```

### PUT /api/v1/config — admin (hot reload without restart)

```sh
curl -X PUT http://localhost:8080/api/v1/config \
-H "Authorization: Bearer secret" -H "Content-Type: application/json" \
-d '{"providers":{"enabled":["synth","intercity"]},"planner":{"engine":"raptor"},"log":{"level":"debug","levels":{"http":"debug"}}}'
# {"status":"ok","providers":["synth","intercity"],"planner":{"engine":"raptor"},"log":{...}}
```

## 5. Providers and Dashboard — mcp:read

```sh
curl http://localhost:8080/api/v1/providers -H "Authorization: Bearer tm_1_..."
# {"intercity":{"up":true,"records":351,"issues":117,"excluded_stops":5}}

curl http://localhost:8080/api/v1/dashboard -H "Authorization: Bearer tm_1_..."
# {"uptime_seconds":12,"providers":{...},"counters":[{"name":"planner.planned","value":3}]}
```

## 6. Admin UI — admin

```sh
curl http://localhost:8080/admin -H "Authorization: Bearer secret"
# HTML with uptime, providers, metrics, and links to /api/v1/*
```

## 7. MCP — mcp:read, JSON-RPC 2.0 Streamable HTTP stateless

### tools/list

```sh
curl -X POST http://localhost:8080/mcp \
-H "Authorization: Bearer tm_1_..." -H "Content-Type: application/json" \
-d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}'
```

### tools/call find_route — by coordinates

```sh
curl -X POST http://localhost:8080/mcp \
-H "Authorization: Bearer tm_1_..." -H "Content-Type: application/json" \
-d '{
  "jsonrpc":"2.0","id":2,"method":"tools/call",
  "params":{"name":"find_route","arguments":{
    "from_lat":55.7558,"from_lon":37.6173,
    "to_lat":55.0084,"to_lon":82.9357,
    "departure":"2026-09-02T06:00:00Z"
  }}
}'
```

### find_route — by place name (gazetteer)

```sh
curl -X POST http://localhost:8080/mcp \
-H "Authorization: Bearer tm_1_..." -H "Content-Type: application/json" \
-d '{
  "jsonrpc":"2.0","id":3,"method":"tools/call",
  "params":{"name":"find_route","arguments":{
    "from_place":"Юрга","to_place":"Горно-Алтайск",
    "departure":"2026-09-02T06:00:00Z","allow_gap":true
  }}
}'
```

### find_route — arrival constraint

```sh
curl -X POST http://localhost:8080/mcp \
-H "Authorization: Bearer tm_1_..." -H "Content-Type: application/json" \
-d '{
  "jsonrpc":"2.0","id":4,"method":"tools/call",
  "params":{"name":"find_route","arguments":{
    "from_lat":53.35,"from_lon":83.75,"to_lat":51.95,"to_lon":85.94,
    "arrival":"2026-09-02T12:00:00Z"
  }}
}'
```

The response is a `Journey` with `legs[]`, `ModeFlight|bus|walk`, and `self_provided` for a `gap`.

## 8. Logging

`LOG_FORMAT=json` is intended for log collectors (`Loki/ELK`), while `text` is intended for `stdout`:

```sh
LOG_FORMAT=text LOG_LEVEL=debug go run ./cmd/mcp-server
# time=... level=INFO msg="config loaded" path=... log_format=text

LOG_FORMAT=json go run ./cmd/mcp-server
# {"time":"...","level":"INFO","msg":"import reading dataset","path":"..."}
```

## 9. Import — Versioning

`store/import_intercity.go` uses `WithTx`, a `sha256` checksum, a `snapshot`, and `quality 117`. Re-importing the same JSON with the same checksum results in `import skipped`; `increment` is performed via `upsert`.

`ENABLE_GEOCODE=1` calls `Yandex` and persists the result permanently in `stations` as a `FindStation` cache. Otherwise, coordinates remain `0,0` and `quality_flags=1`.

```sh
DATABASE_DSN=sqlite:///tmp/app.db go run ./cmd/mcp-server # 231ms total_startup
# second start with the same checksum -> skipped, 40ms
```
