# 09. План рефакторинга и консолидированная дорожная карта

> Объединяет незавершённые пункты `05-roadmap.md` с замечаниями review (безопасность, универсальность источников, изоляция слоёв, БД/кэш/очередь, наблюдаемость, конфиг). i18n — в конце. Зависимости упорядочены: каждый этап зависит только от предыдущих.

## Принципы
- **Одна точка конфигураций**: `internal/config` — весь `Config` (см. §1). Новый модуль/провайдер добавляет поле в `Config`, без поиска по репо.
- **Порты и адаптеры**: ядро `model/planner/geo` не знает о внешних форматах. Внешнее → `adapter` (private raw-структуры) → `model.Network`.
- **Валидация язык-независима**: структурная (ядро) + региональная политика (адаптер).
- **Наблюдаемость**: единый HTTP-слой с логгированием (Info кратко / Debug подробно), Prometheus, OTel.
- `i18n` — последний этап.

## 1. Конфигурация — единая точка

**Проблема сейчас**: `config/config.go:73 Load = Defaults→yaml→LoadDotenv→applyEnv:95` ручной `Getenv` на каждое поле, `dotenv.go:11` рекурсивный поиск `.env` до `/`, `flag -config` обязателен `cmd/main.go:23`, секреты в `configs/*.yaml` `dev-secret`.

**Цель:**
```yaml
# configs/config.yaml — единственный файл на диске; отсутствие — ок (Defaults)
server: { addr: ":8080", readHeaderTimeout: "10s", shutdownTimeout: "10s" }
store:  { dsn: "", maxOpenConns: 10 }        # "" = memory (тест/dev)
cache:  { kind: "memory", addr: "", ttl: "5m" }  # memory|redis
queue:  { kind: "memory", url: "" }         # memory|nats
providers:
  enabled: []
  intercity: { reestr_path: "data/reestr/regions.json", bounds: "ru" }
  gtfs: { path: "data/gtfs/feed.zip" }      # пример: новый провайдер → добавить секцию
telemetry: { prometheusAddr: "" }
auth: { admin_token: "${ADMIN_TOKEN}" }     # интерполяция env, не хардкод
log:
  level: "info"          # global default
  format: "json"         # json|text
  add_source: false
  levels:                # per-модуль override (опционально)
    http: "info"
    providers.intercity: "debug"
    store: "warn"
    planner: "debug"
    httpx.yandex: "debug"
features: { planner_engine: "csa" }
yandex: { rasp_key: "${YANDEX_RASP_KEY}", geocode_key: "${YANDEX_GEOCODE_KEY}" }
```

**Реализация:**
- Заменить `applyEnv` на `knadh/koanf v2` (провайдеры: `file yaml optional` → `env` с префиксом `TRAVELMCP` и делимитром `__` → `TRAVELMCP__LOG__LEVELS__STORE=debug` → `log.levels.store`). Коанф дешевле `viper`, явные провайдеры, нет скрытых дефолтов.
- `os.ExpandEnv` после загрузки yaml для `${VAR}` (prod секреты).
- Удалить рекурсивный `DotenvPath` до `/`; оставить только явный `.env` в корне или `TRAVELMCP_DOTENV=.env` для локалки.
- Флаг только ` -config` опционален (по умолчанию `configs/config.yaml` если есть, иначе `Defaults`). Никаких `-addr/-dsn` флагов.
- Новый модуль добавляет `type XConfig struct{...}` и поле `X XConfig \`yaml:"x"\`` в `Config` — фабрика `x.New(cfg.X, logger)` читает свой подсект.
- Валидация `Config.Validate()` (порт, `engine csa|raptor`, `dsn` URL parse).

## 2. Логирование — внешние ресурсы + уровни

**Требование**: `Info` кратко (method, host, path, status, elapsed_ms), `Debug` подробно (req/resp body, headers, без секретов).

**Реализация:**
- `internal/httpx/client.go` — единый обёрточный `Client{base *http.Client, logger *slog.Logger, redact}`. Метод `Do(ctx, req) (*http.Response, error)` логирует: `Info` всегда, `Debug` — `LimitReader 4KB req / 8KB resp` через `io.TeeReader`, `url.Query(apikey=***)`, `Authorization: Bearer ***`.
- Подмена: `store/import_intercity.go:421 geocodeStation` (сейчас `http.Get` без `ctx` + вторичное чтение `.env`) и будущие адаптеры получают `httpx.Client` по DI (`providers.NewIntercityWithClient(path, client, logger)`). `providers/intercity.go:190 slog.Info` (глобальный `slog.Default`) → инжектированный `logger`.
- **Per-модуль уровни**: `internal/logger/factory.go` — `Factory{global Level, overrides map[string]Level}`. `NewFactory(cfg.Log)` парсит `log.levels`. `For(module string) *slog.Logger` — exact match → prefix fallback (`providers.intercity` → `providers`) → global. Реализуется через `slog.LevelVar` per-module. `cmd/main.go:108 newLogger` заменить на `loggerFactory := logger.NewFactory(cfg.Log); logger := loggerFactory.For("main")`; `server` ← `For("http")`, `store` ← `For("store")` и т.д. `TRAVELMCP__LOG__LEVELS__PROVIDERS_INTERCITY=debug` работает.

## 3. Консолидированный план по фазам (зависимости → порядок)

### Фаза 0 — Hotfix безопасности (1–2 дня, без зависимостей)
*Зависимость: нет. Блокирует прод.*
- [ ] `server.go:132 hashPassword` → `golang.org/x/crypto/bcrypt` (cost 10) + миграция `users.pass_hash`.
- [ ] `store/sqlite.go:415 CreateApiKey` → `crypto/rand 32B base64url`, хранить `sha256(key)` (голый отдаётся один раз).
- [ ] `server.go:78 open-mode` — требовать `ADMIN_TOKEN` в prod (env `APP_ENV=prod` → 401 если пусто), не `next.ServeHTTP`; `server.go:88` → `subtle.ConstantTimeCompare`.
- [ ] `server.go:191 email=="admin"` автоповышение — удалить; админ через `seed` миграция + `ADMIN_EMAIL` env, регистрация всегда `pending/user`.
- [ ] `server.go:372 handleAdminPage` → `html/template` + `html.EscapeString`; `server.go:177,213` → `http.MaxBytesReader 1MiB` + `r.Body.Close()`.
- [ ] `sqlite.go:50 WithTx` silent fallback → возвращать ошибку; `PRAGMA foreign_keys=ON`, проверять `PRAGMA/rows.Err`.
- [ ] `main.go:34` логирование `DSN` — маскировать `url.Parse → ***`; удалить `dev-secret` из `configs/*.yaml` → плейсхолдеры.
- [ ] Валидация входа `mcp/mcp.go:142,197` — `max_walk 0..180`, `max_transfers -1..10`, `lat -90..90 lon -180..180`, `argFloat` поддержка `int/json.Number`.

**Критерий**: `go vet`, `govulncheck`, `make test`, ручной `curl /register admin` не даёт админа.

### Фаза 1 — Конфиг + логгер + кэш readyz (3–4 дня)
*Зависит от Фазы 0 (маскировка, уровни). Независима от провайдеров.*
- [ ] Рефактор `internal/config` → единая точка (koanf, `Config` схема §1), удаление `applyEnv`, рекурсивного `dotenv`.
- [ ] `internal/logger/factory.go` per-модуль уровни + `internal/httpx/client.go` (Info/Debug).
- [ ] `providers/intercity.go:421` geocode переключить на `httpx.Client` с `context.WithTimeout 10s`.
- [ ] `readyz` `server.go:141` → снапшот: `Registry` хранит `atomic.Value map[string]HealthStatus` + `LastCheck`, фоновый `ticker 10s` + `singleflight` обновляет; `/readyz` читает снапшот без `load()+build()`; `/healthz` без изменений.

**Критерий**: `TRAVELMCP__LOG__LEVELS__PROVIDERS_INTERCITY=debug` включает debug только для intercity; `GET /readyz` <5мс без сборки сети; логи внешних запросов в `Info/Debug`.

### Фаза 2 — Изоляция слоёв: порты, адаптеры, валидация (1.5 недели)
*Зависит от Фазы 1 (конфиг как DI).*
- [ ] Ввести `internal/ports` (интерфейсы `Provider` уже `providers/registry.go:18` + добавить `Capabilities`, `RawSource Fetcher`, `Cache[K,V]`, `Queue`).
- [ ] Выделить `internal/adapters/intercity/{raw/schema.go, fetch.go, mapper.go, validator.go}` — `reestr*` структуры `intercity.go:24` приватизировать, `fetch` с `LimitReader 20MiB + sha256`, `mapper` → `model.Network` через модульный `StopClassifier` (§4). `providers/intercity.go` — тонкая обёртка.
- [ ] Устранить дублирование `parseTimeMinutes/pickPeriod/runsCount/timeAt` `intercity.go:253`/`import_intercity.go` → `internal/adapters/timeutil`.
- [ ] `model/validate.go:22` разделить: инвариантная `ValidateNetwork` (ref integrity, `Arrival<Departure`, `Seq`, `duplicates`, `Lat/Lon` глобально) — коды `Code` без текста; региональная `PolicyValidator{BBox}` для `intercity` `BBox{35,85,19,190}` по конфигу `providers.intercity.bounds`; `FilterExcludedStops` по `Code`.
- [ ] `model.InferStopType` `model.go:55` → `StopClassifier` интерфейс (`Classify(name, tags) StopType`), `RuClassifier` с `configs/classifiers/ru.yaml` (keywords), регистрация в адаптере, ядро без кириллицы.
- [ ] `Registry` `registry.go:45 switch` → `map[string]Factory func(Config, Logger, Httpx) Provider` + `RegisterFactory`.
- [ ] `NetworkCache` (in-memory `sync.Map` + `TTL 5m` + `singleflight`) для `intercity.Network()` и `store.LoadNetwork`, `context` везде, `LimitReader`.

**Критерий**: добавление `providers.gtfs` — только новый пакет `adapters/gtfs` + поле `Gtfs` в `Config` + `RegisterFactory`; `model` без `strings.Contains("вокзал")`.

### Фаза 3 — Хранилище/кэш/очередь — переключаемость (1 неделя)
*Зависит от Фазы 2 (порты).*
- [ ] `store/postgres` реализовать `pgx` + `PostGIS` миграции (заменить `pgStub` `store/store.go:45`), `docker-compose.yml:2` уже есть; `store.New(cfg StoreConfig)` фабрика по `dsn` (`memory|sqlite://|postgres://`).
- [ ] `internal/cache` интерфейс `Get/Set/Delete with TTL` + реализации `memory` (sync.Map) и `redis` (`go-redis`); `queue` интерфейс `Enqueue(Job)` + `memory` (chan+workers) → `nats` (опционально). Пока поиск синхронный, очередь для фоновых `ImportIntercity`/`geocode`.
- [ ] Поиск: `semaphore Weighted(NumCPU*2)` перед `planner.Plan` `planner/planner.go:60` + `context.WithTimeout 2s`.
- [ ] **RateLimit middleware** (перенесено из Фазы 0 review): `internal/middleware/ratelimit.go` — `Token bucket` per-IP (`golang.org/x/time/rate`), дефолт `RPS/burst` из `config.server.rate_limit {rps,burst}` + `overrides map[path]RateLimit` (напр. `POST /api/v1/login: 5/10`, `POST /mcp: 20/40`). Middleware на все методы, переопределяемые лимиты, `429 Retry-After`.

**Критерий**: `DATABASE_DSN=postgres://...` и `CACHE_KIND=redis` переключаются конфигом без правки кода, `make integration` проходит на `memory` и `sqlite`; `429` на превышение лимита.

### Фаза 4 — Наблюдаемость (4 дня)
*Зависит от Фазы 1 (логгер) и Фазы 3 (метрики на всех слоях).*
- [ ] `prometheus/client_golang` `GET /metrics` (`promhttp`), метрики `travelmcp_planner_duration_seconds{engine,mode}`, `travelmcp_provider_health{provider}`, `travelmcp_http_requests_total{code,path}`, `travelmcp_external_requests_total{host,status}` (из `httpx`).
- [ ] `otel-go` `TraceProvider` + `otelhttp.NewHandler` для `server`, `httpx` трейсы; `request-id` middleware `X-Request-ID` → `context`.
- [ ] `telemetry.Metrics` `telemetry/telemetry.go:10` обернуть `prom.CounterVec` (совместимость `Named()`).
- [ ] Дашборд `GET /api/v1/dashboard` + `/admin` `server.go:370` без XSS, метрики health `records/issues/excluded`.

**Критерий**: `curl /metrics` отдаёт `prom` формат; логи — JSON с `trace_id`, `request_id`.

### Фаза 5 — Универсальные источники (1–2 недели)
*Зависит от Фазы 2 (адаптеры) и Фазы 4 (метрики здоровья). Незавершённое из 05 Этап 2/3.*
- [ ] `adapters/gtfs` (Москва/СПб) как второй пример универсальности: `Fetcher` (zip CSV) → `Mapper` → `model`; `Capabilities{SupportsFares}`.
- [ ] `Geocoder` интерфейс (`Yandex` — одна реализация за `httpx`, конфиг `yandex` §1).
- [ ] `tools/osm-extract` уже `24895` записей — подключить как `adapter/osm` для геометрии, не провайдер маршрутов.
- [ ] `Planner` доработка `AllowGap` `self_provided` уже `planner.go:70` — расширить тест на межгород `Юрга→Горно-Алтайск` (05 Этап 3 — выполнено, закрепить фикстурой).

**Критерий**: `PROVIDERS_ENABLED=gtfs,intercity` собирает объединённую сеть `Merge` (`mcp/mcp.go:380` суффикс коллизий) с метриками по каждому.

### Фаза 6 — Пользователи/админка/стоимость (из 05 Этап 4/5, 5–7 дней)
*Зависит от Фазы 3 (store postgres).*
- [ ] `REST` полное покрытие `05 §7` (`/api/v1/config` hot-reload без рестарта), per-user `store.UpdateUserConfig`.
- [ ] Стоимость `model.Cost` `Leg.Cost` `model.go:272` — `CostBasis fare|estimate`, движок тарифа.
- [ ] Скрейпер модульный (по необходимости, 2–4д/сайт) с `Health` метриками (05 Этап 6).

### Фаза 7 — i18n (последняя, 3–5 дней) — **backlog 2026-09-02, отложена** (см. `docs/11-data-import-strategy.md`)
*Зависит от всех предыдущих (классификатор уже модульный).*
- [ ] `locales/ru.yaml,en.yaml` (`go-i18n` или `x/text`) — перевод кодов `validate` и описаний MCP `mcp/mcp.go:50`.
- [ ] `StopClassifier` per-регион (ru/en) из `configs/classifiers/*.yaml`.
- [ ] `Gazetteer` `geo/places.go` → `GazetteerProvider` с OSM/Nominatim + `x/text` транслит.

## Проверка каждой фазы
`gofmt -w . && go vet ./... && govulncheck ./... && make test` (`unit+integration+smoke`), `scripts/api-demo.sh` + `scripts/mcp-route.sh place/coords` с `PROVIDERS_ENABLED=synth`.

## Связь с 05-roadmap.md
- Этап 0–1 (05) — выполнено (каркас, ядро CSA/RAPTOR).
- Этап 2 GTFS Москва/СПб — перенесён в Фазу 5.
- Этап 3 межрегиональная связность — выполнено, закрепляется Фазой 5.
- Этап 4–6 (пользователи/стоимость/скрейпинг) — Фазы 6–7.

## Открытые решения до старта Фазы 1
- `knadh/koanf` vs `spf13/viper` — рекомендация `koanf`.
- Ключи `log.levels` — `providers.intercity` (точка) для yaml, `__` для env.
- Лимит тела в Debug — `4KB req / 8KB resp` (настраиваемо `log.max_body_bytes`).
