# 08. API и примеры запросов

Базовый URL `http://localhost:8080`, конфиг `configs/config.example.yaml`, `ADMIN_TOKEN` из `env` или `configs/*.yaml`. Логи `LOG_FORMAT=json|text` `LOG_LEVEL=info|debug` единый `EN` `slog`.

## 1. Аутентификация

- `ADMIN_TOKEN` — bootstrap `admin` (`Bearer secret` или `X-API-Key: secret`) обходит `DB`, `role=admin`.
- Пользовательские ключи `tm_<user>_<nano>` `scopes=mcp:read|admin` хранятся в `users/api_keys` `sqlite` `14ms` `LoadNetwork`. Если `ADMIN_TOKEN==""` — `open` для тестов.
- Скопы: `mcp:read` для `/mcp` `/providers` `/dashboard` `/keys` `/me`, `admin` для `/users` `/admin`.

```sh
# admin token из .env
ADMIN_TOKEN=secret DATABASE_DSN=sqlite:///tmp/app.db go run ./cmd/mcp-server
```

## 2. System

### GET /healthz — без auth
```sh
curl http://localhost:8080/healthz
# {"status":"ok"}
```

### GET /readyz — health провайдеров
```sh
curl http://localhost:8080/readyz
# {"status":"ok","uptime_seconds":12} или 503 {"status":"degraded"}
```

## 3. Auth — регистрация и модерация

### POST /api/v1/register — без auth, создаёт pending
```sh
curl -X POST http://localhost:8080/api/v1/register \
  -H "Content-Type: application/json" \
  -d '{"email":"alice@test.com","password":"pass1234"}'
# 201 {"id":1,"email":"alice@test.com","status":"pending","message":"pending moderation"}
```

### GET /api/v1/users — admin
```sh
curl http://localhost:8080/api/v1/users -H "Authorization: Bearer secret"
# [{"id":1,"email":"alice@test.com","status":"pending","role":"user"}]
```

### POST /api/v1/users/{id}/moderate — admin
```sh
curl -X POST http://localhost:8080/api/v1/users/1/moderate \
  -H "Authorization: Bearer secret" -H "Content-Type: application/json" \
  -d '{"status":"active"}' # pending|active|blocked
```

### POST /api/v1/login — без auth, активный пользователь
```sh
curl -X POST http://localhost:8080/api/v1/login \
  -H "Content-Type: application/json" \
  -d '{"email":"alice@test.com","password":"pass1234"}'
# 200 {"token":"tm_1_...","scopes":"mcp:read","user_id":1}
# 403 {"error":"user not active","status":"pending"} до модерации
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

## 4. API-ключи

### GET /api/v1/keys — mcp:read, свои ключи
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

## 5. Providers и дашборд — mcp:read

```sh
curl http://localhost:8080/api/v1/providers -H "Authorization: Bearer tm_1_..."
# {"intercity":{"up":true,"records":351,"issues":117,"excluded_stops":5}}

curl http://localhost:8080/api/v1/dashboard -H "Authorization: Bearer tm_1_..."
# {"uptime_seconds":12,"providers":{...},"counters":[{"name":"planner.planned","value":3}]}
```

## 6. Админка — admin

```sh
curl http://localhost:8080/admin -H "Authorization: Bearer secret"
# HTML с uptime, providers, metrics, ссылками на /api/v1/*
```

## 7. MCP — mcp:read, JSON-RPC 2.0 Streamable HTTP stateless

### tools/list
```sh
curl -X POST http://localhost:8080/mcp \
  -H "Authorization: Bearer tm_1_..." -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}'
```

### tools/call find_route — по координатам
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

### find_route — по населённому пункту (газетир)
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

### find_route — arrival
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

Ответ `Journey` `legs[]` `ModeFlight|bus|walk` `self_provided` для `gap`.

## 8. Логи

`LOG_FORMAT=json` для сборщика (`Loki/ELK`), `text` для `stdout`:
```sh
LOG_FORMAT=text LOG_LEVEL=debug go run ./cmd/mcp-server
# time=... level=INFO msg="config loaded" path=... log_format=text
LOG_FORMAT=json go run ./cmd/mcp-server
# {"time":"...","level":"INFO","msg":"import reading dataset","path":"..."}
```

## 9. Импорт — версионность

`store/import_intercity.go` `WithTx` `checksum sha256` `snapshot` `quality 117` — повторный `JSON` с тем же `checksum` → `import skipped`, `increment` через `upsert`. `ENABLE_GEOCODE=1` дергает `Yandex` и сохраняет навсегда в `stations` `FindStation` кэш, иначе `0,0` `quality_flags=1`.

```sh
DATABASE_DSN=sqlite:///tmp/app.db go run ./cmd/mcp-server # 231ms total_startup
# второй старт same checksum -> skipped 40ms
```
