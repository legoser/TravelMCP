# 06. Структура репозитория и связи

Этот документ описывает фактическое состояние каркаса: дерево кода, граф
зависимостей пакетов, поток запроса в рантайме, соответствие целевой архитектуре
из `docs/03-architecture.md` и способы тестирования.

## 1. Карта репозитория

```
Travel_MCP/
├── cmd/
│   └── mcp-server/            # точка входа: конфиг → реестр → HTTP+MCP → shutdown
├── internal/
│   ├── model/                 # каноническая модель данных (ядро без зависимостей) + ValidateNetwork, StopType
│   ├── providers/             # адаптеры источников + Registry (synth, intercity) + Health Issues
│   ├── geo/                   # гаверсин, SpatialIndex, NearestStops, WalkTimeMinutes
│   ├── planner/               # CSA/RAPTOR + arrival-by + Pareto + gap/self-leg
│   ├── store/                 # Store (memory/sqlite modernc, pg stub) + ImportIntercity (station-группировка, no_geo обогащение)
│   ├── mcp/                   # MCP-сервер: find_route (departure/arrival, allow_gap), list_providers (Issues)
│   ├── server/                # http-роутер: /healthz, /readyz, /api/v1/*, монтаж /mcp (Store)
│   ├── telemetry/             # потокобезопасные счётчики метрик
│   └── config/                # конфиг: default → YAML → env (planner.engine, database.dsn)
├── test/
│   ├── common/                # заготовленные хелперы и фиксированные сценарии
│   ├── integration/           # HTTP+MCP in-process через httptest
│   └── smoke/                 # сборка бинарника + запуск отдельного процесса
├── testdata/
│   └── reestr/                # мини-фикстура реестра Минтранса (mini.json)
├── configs/config.example.yaml
├── docker-compose.yml         # mcp-server + PostgreSQL/PostGIS
├── Dockerfile
├── Makefile                   # make build / test / unit / integration / smoke / run
├── go.mod / go.sum
└── docs/                      # проектная документация (01–06, 08-api)
```

Правило размещения: приложение — в `internal/` (нельзя импортировать извне
модуля), тесты, которым нужен готовый код и реальные запуски, — в `test/`.

## 2. Граф зависимостей пакетов

Зависимости направлены «сверху вниз», `model` — лист без внешних зависимостей.
Стрелка `A → B` означает «package A импортирует package B».

```
cmd/mcp-server ──→ config, providers, server, store, telemetry
internal/server ──→ config, telemetry, providers, planner, mcp, store, mcp-go/server
internal/mcp    ──→ planner, providers, store, model, mcp-go{server,mcp}
internal/planner─→ model, geo, telemetry
internal/providers ─→ model, geo
internal/store   ──→ model, geo
internal/geo    ──→ model
internal/model  ──→ (никого)
test/smoke      ──→ internal/providers, test/common
test/integration──→ internal/{config,providers,server,store,telemetry,model}, test/common
```

Внешние зависимости: `github.com/mark3labs/mcp-go` (протокол MCP),
`gopkg.in/yaml.v3` (конфиг). Никто не импортирует `server`, `mcp` и `cmd`
наверх — интерфейсы объявлены там, где вызываются.

## 3. Поток запроса (рантайм)

```
MCP-клиент (агент)
   │ POST /mcp  JSON-RPC 2.0, Streamable HTTP
   ▼
internal/server (mux) → NewStreamableHTTPServer → internal/mcp.App (Store)
   │ handleFindRoute
   │  · парсинг (lat/lon/place, departure/arrival, allow_gap, лимиты)
   │  · internal/mcp.networkForDay() — ValidateNetwork → FilterExcluded → суффикс p:id при коллизии → (Store.LoadNetwork(day) || Registry)
   ▼
internal/planner.Plan(net, from, to, params)
   │ 1. SpatialIndex.Nearest → access/egress (Haversine, WalkTimeMinutes) + findStopByPlace (StopType Hub)
   │ 2. CSA/RAPTOR (engine) по Connections/TransfersByStop + arrival-by (planArrival) → gap/self-leg если AllowGap
   │ 3. buildLegs → Leg[] + Journey (+ Pareto Alternatives ≤2, Transfers, self_provided)
   ▼
internal/telemetry: planner.planned++        → /api/v1/dashboard
   ▼
mcp.NewToolResultJSON(Journey) → ответ клиенту
```

Простой эндпоинт `/api/v1/providers` идёт напрямую из `server` в `Registry`
без планировщика. `/healthz` не трогает источники, `/readyz` — проверяет все
включённые провайдеры и отвечает 503 при деградации.

## 4. Ключевые символы: файл-карта

| Сущность | Где | Примечания |
|---|---|---|
| `Coords`, `Mode`, `StopType`, `Stop`, `Station`, `ProviderStop`, `Route`, `StopTime`, `Trip`, `Transfer` (+DistanceM/MinTransferTime), `Connection` (+DistanceM), `Network` (+BuildIndexes), `SearchParams` (+Arrival/AllowGap), `Journey` (+Alternatives) | `internal/model/model.go` | канон. модель + ValidateNetwork/FilterExcluded, InferStopType |
| `Provider` (ID/Health/Network), `Registry`, `HealthStatus` (+Issues/Excluded), `Synth` | `internal/providers/registry.go`, `internal/providers/synth.go` | synth — детерминированная сеть-фикстура, мок только для тестов/демо |
| `Intercity` (разбор JSON-датасета, NearbyPairs 0.4км, serviceActive) | `internal/providers/intercity.go`, `internal/providers/intercity_test.go` | реальный источник exact-времён; тест на `mini.json` |
| `Haversine`, `WalkTimeMinutes`, `SpatialIndex`, `NearbyPairs` | `internal/geo/geo.go`, `internal/geo/index.go` | 5 км/ч, 30 мин; индекс 0.01° для Nearest/трансферов |
| `Planner.Plan`, `csa/raptor`, `planArrival`, `paretoAlternatives`, `gap` | `internal/planner/planner.go`, `internal/planner/raptor.go` | CSA/RAPTOR, arrival-by, Pareto ≤2, gap/self-leg |
| `Store` (memory/sqlite modernc, pg stub), `ImportIntercity` | `internal/store/store.go`, `internal/store/sqlite.go`, `internal/store/memory.go`, `internal/store/import_intercity.go` | station-группировка, no_geo обогащение (Yandex) |
| `App.Server`, `handleFindRoute` (departure/arrival/allow_gap), `networkForDay` | `internal/mcp/mcp.go` | валидация, суффикс коллизий, Store.LoadNetwork(day) |
| `Server.New/NewWithStore` | `internal/server/server.go` | `NewStreamableHTTPServer` + Store |
| `Metrics.Inc`, `Snapshot`, `Named` | `internal/telemetry/telemetry.go` | мутекс-защищённые счётчики |
| `Load` (default → YAML → env, planner.engine) | `internal/config/config.go` | env: `HTTP_ADDR`, `DATABASE_DSN`, `PROVIDERS_ENABLED`, `INTERCITY_REESTR_PATH`, `ADMIN_TOKEN`, `PLANNER_ENGINE`, `YANDEX_*` |
| main: конфиг → Store(Migrate/Import) → реестр → server → graceful shutdown | `cmd/mcp-server/main.go` | `signal.NotifyContext` + `http.Server.Shutdown` |

## 5. Жизненный цикл процесса

1. `main` читает конфиг (`-config`, по умолчанию `configs/config.example.yaml` + `dev/prod/test` контуры).
2. Создаются `telemetry.Metrics`, `Store` (`memory`/sqlite `modernc.org/sqlite`/pg stub) → `Migrate` → `ImportIntercity` (если `intercity` enabled), `providers.Registry`.
3. `server.NewWithStore` собирает http-роутер: health-эндпоинты, REST `/api/v1/*`,
   и монтирует MCP (`StreamableHTTPServer` с `Store`) на `/mcp`.
4. `http.Server` стартует в горутине; `SIGTERM`/`SIGINT` → graceful shutdown
   с таймаутом 10 c.
5. Сеть: если `Store` задан — `Store.LoadNetwork(day)` (проиндексированная, `BuildIndexes`), иначе лениво `Registry → NetworkForDay(day)` (с `ValidateNetwork`/`FilterExcluded`). `synth` дёшево; `intercity` без Store парсит JSON на каждый вызов, со Store — из БД.

## 6. Тестирование (точка входа — `make test`)

- `make unit` — модульные: `config_test.go`, `geo_test.go`, `planner_test.go` (кросс-город, пешая стыковка, лимит, перелёт, белое пятно, `StopType`, `arrival`/`Pareto`/`gap`), `model/validate_test.go`, `mcp/merge_test.go`, `store` (sqlite `memory` импорт).
- `make integration` — `test/integration`: HTTP+MCP `httptest`, сценарии `test/common` (`AssertGroundJourney` `09:36`, `AssertFlightJourney` `09:05`, `AssertNoRoute`, `AssertBadArgs`, `Place`).
- `make smoke` — `test/smoke`: бинарник + живой сокет, те же сценарии.
- `make test` — всё: `go test ./... -count=1` (`vet` 0, `gofmt`).

Заготовленные сценарии не генерируются на лету: точные координаты/ожидаемые `arrival`/`transfers`. Один сценарий — интеграция и smoke.

## 7. Связь с целевой архитектурой (`docs/03`)

Реализовано сейчас | Из целевой архитектуры | Статус
---|---|---
`model` (+Validate/StopType/Alternatives) | (в 03.1) | полностью + RAPTOR/Pareto/gap |
`providers` (synth, intercity +NearbyPairs/serviceActive) | (03.2: gtfs, govregistry, osm…) | 2 реализации, health Issues, изоляция коллизий |
`store` (memory/sqlite modernc, pg stub, ImportIntercity) | (03.2 DB) | межгород 351 маршрут в БД, dev/prod контуры |
`geo` (Haversine + SpatialIndex) | (03.3: PostGIS/H3) | индекс 0.01° для Nearest/трансферов |
`planner` (CSA/RAPTOR, arrival-by, Pareto, gap) | (03.4, п.3) | `engine: csa|raptor`, `AllowGap` |
`config` (файл+env, planner.engine) | (03.5: третий уровень — БД, hot-reload) | `dev/test/prod.yaml`, `DATABASE_DSN` |
`server` (health/readyz, /api/v1/providers+dashboard, Store) | (03.6–03.7: auth, users, keys) | `X-API-Key`, `Store` |
`telemetry` (счётчики) | (03.8: OTel, Prom) | остов |