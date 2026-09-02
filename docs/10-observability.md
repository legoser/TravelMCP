# 10. Наблюдаемость и интеграция внешних сервисов

## Метрики Prometheus (`/metrics`)

Эндпоинт открыт без auth (для скрапа), остальные — за `auth`/`rate-limit`.

```
GET /metrics  # Prometheus exposition
```

Метрики:
- `travelmcp_planner_duration_seconds{engine="csa|raptor"} histogram` — длительность планирования.
- `travelmcp_provider_health{provider="synth|intercity"} gauge 1/0`
- `travelmcp_http_requests_total{code="200",path="/mcp"} counter`
- `travelmcp_external_requests_total{host="geocode-maps.yandex.ru",status="200"} counter`

Пример `prometheus.yml`:
```yaml
scrape_configs:
  - job_name: travelmcp
    scrape_interval: 15s
    static_configs: [{targets: ["mcp-server:8080"]}]
```

Пример `docker-compose.yml`:
```yaml
services:
  prometheus:
    image: prom/prometheus:v2.53
    volumes: ["./prometheus.yml:/etc/prometheus/prometheus.yml"]
    ports: ["9090:9090"]
  grafana:
    image: grafana/grafana:11
    ports: ["3000:3000"]
    environment: { GF_SECURITY_ADMIN_PASSWORD: admin }
  mcp-server:
    build: .
    ports: ["8080:8080"]
```

## Request-ID и логи

Middleware `requestIDMiddleware` — `X-Request-ID` генерируется (`uuid`) если не передан, прокидывается в `ctx` и `X-Request-ID` ответа. `httpx` логирует `module`/`method`/`host`/`status`/`elapsed_ms` на `Info`, тело `8192` байт на `Debug`.

## Конфигурация rate-limit и semaphore

В единой точке `configs/config.yaml` → `internal/config/config.go:13`:

```yaml
http:
  rate_limit: { rps: 100, burst: 200 }           # дефолт для всех путей; 0 = 100/200
  rate_limit_overrides:
    "/api/v1/login": { rps: 5, burst: 10 }
    "/mcp": { rps: 20, burst: 40 }
planner:
  engine: "csa"
  semaphore_enable: true
  semaphore_size: 0  # 0 = NumCPU*2

# env override:
# TRAVELMCP__HTTP__RATE_LIMIT__RPS=200
# HTTP_RATE_LIMIT_RPS=200 (legacy)
# TRAVELMCP__PLANNER__SEMAPHORE_SIZE=8
# PLANNER_SEMAPHORE_SIZE=8
# PLANNER_SEMAPHORE_ENABLE=false (отключить)
```

Используется `internal/middleware/ratelimit.go` (token bucket без `x/time`) и `planner` `chan semaphore`.

## ELK / другие

Логи — `slog JSON` (`log.format=json`) с `request_id` полем. Для ELK: shipper `Filebeat` → `Logstash` фильтрует `request_id`, `module`.

OTel трейсинг — заглушка (интерфейс готов в `telemetry`), подключение через `OTEL_EXPORTER_OTLP_ENDPOINT`.

## Cache / Queue (переключение)

```yaml
cache: { kind: "memory", addr: "", ttl: "5m" }  # memory|redis
queue: { kind: "memory", url: "" }              # memory|nats
```

Фабрики `internal/cache` / `internal/queue` — `memory` по умолчанию, `redis`/`nats` подключаются по `kind`.
