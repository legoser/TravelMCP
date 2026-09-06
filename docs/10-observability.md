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

### Форматеры `internal/logger/factory.go:35`

- `log.format=json` — прод/ELK, `time RFC3339Nano`, `level INFO`, `module` атрибут, `add_source` опционально.
- `log.format=text` — консоль, человекочитаемый: `15:04:05.000` + цветной уровень `DBG/INF/WRN/ERR` (ANSI 36/32/33/31), `module` префикс, `key=value` без кавычек, пробел-разделитель, `AddSource` как `file:line` только на `debug`.
- Per-модуль уровни `log.levels` (`TRAVELMCP__LOG__LEVELS__STORE=debug`) — `Factory.levelFor` точное совпадение → `providers.intercity` → `providers` prefix → global.
- Фикс проскока дефолтного формата: `providers/intercity.go:193` переведён с `slog.Info` (global `slog.Default`) на инжектированный `logger *slog.Logger` (`internal/providers/intercity.go:96 NewIntercityWithLogger`, `cmd/mcp-server/main.go:56 NewRegistryWithLogger`), `server.go:920 writeJSONResponse` через `slog.Default().Error`. `configs/config.dev.yaml:59 format=text level=debug`, `config.example.yaml:57 json`.
- Пример `text`:
  ```
  23:34:49.056 INF main config loaded module=main path=configs/config.dev.yaml addr=:8080
  23:34:49.131 INF providers.intercity intercity build completed stops=255 trips=579 elapsed_ms=3
  23:35:28.607 INF http request method=GET path=/healthz status=200 elapsed_ms=0 request_id=...
  ```
  `json` остаётся машинно-парсимым, `text` — цветной человекочитаемый.

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

## Почему `reestr_path` в конфиге

`providers.intercity.reestr_path: "data/reestr/regions.json"` — путь к датасету Минтранса, собранному `scripts/extract-minstran.py` (XLSX → JSON + `--osm` геокодинг). Файл не коммитится (`data/` в `.gitignore`), путь меняется между контурами:
- `config.dev.yaml: "data/reestr/regions.json"` — локальный полный дамп,
- `config.prod.yaml` — тот же путь, но dataset кладётся волюмом/секретом.
`PROVIDERS_ENABLED=intercity` без пути → `intercity.Health() Up:false`. Для смены источника (`gtfs`) добавляется `providers.gtfs.path` — та же точка.

## Выбор planner engine

`planner.engine: "csa"|"raptor"` — **не переключается на лету per-запрос**, выбирается при старте из конфига (` TRAVELMCP__PLANNER__ENGINE=raptor`).

- **csa (Connection Scan Algorithm)** — сканирует `Connections` по времени, `O(conns)`. Быстрее на малых/средних сетях (`synth` 7 стопов, `intercity` 255 стопов) — `5-30 мс`, проще дебажить, Pareto `6` прогонов. Рекомендуется для `dev/test` и сетей до `10k` соединений.
- **raptor (Round-bAsed)** — раундово расслабляет маршруты, `O(rounds * routes)`, лучше на плотных частотах (`>50` активных рейсов), частотах 5-15 мин — `2-10 мс` на `intercity`. Нужен `routeTrips` индекс.

Критерий: если `active>50` рейсов/день — `raptor`; если сеть разреженная, трансфер-граф важнее — `csa`. Для `prod` с `intercity` рекомендуется `raptor` (`config.prod.yaml`), для `synth` хватит `csa`. Смена — перезапуск (`systemd`/`k8s rollout`) без перекомпиляции.

## Хардкоды defaults — исправлено

Ранее `middleware.NewRateLimiter` и `planner.NewWithConfig` содержали `100/200` и `NumCPU*2` внутри кода. Теперь единственный источник — `config.Defaults()` (`config.go:102`, `120`):
- `HTTP.RateLimit {100,200}` и `Planner.SemaphoreSize = runtime.NumCPU()*2`
- `NewRateLimiter`/`NewWithConfig` берут `cfg` значения, fallback `def = Defaults().HTTP.RateLimit` — дубликат удалён, `PLANNER_SEMAPHORE_SIZE`/`HTTP_RATE_LIMIT_*` только из `Config`.
