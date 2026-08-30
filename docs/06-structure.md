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
│   ├── model/                 # каноническая модель данных (ядро без зависимостей)
│   ├── providers/             # адаптеры источников + Registry (synth, intercity)
│   ├── geo/                   # гаверсин, ближайшие остановки, время пешего доступа
│   ├── planner/               # CSA-поиск и сборка Journey (ядро бизнес-логики)
│   ├── mcp/                   # MCP-сервер: инструменты find_route, list_providers
│   ├── server/                # http-роутер: /healthz, /readyz, /api/v1/*, монтаж /mcp
│   ├── telemetry/             # потокобезопасные счётчики метрик
│   └── config/                # конфиг: default → YAML → env-переопределение
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
└── docs/                      # проектная документация (01–06)
```

Правило размещения: приложение — в `internal/` (нельзя импортировать извне
модуля), тесты, которым нужен готовый код и реальные запуски, — в `test/`.

## 2. Граф зависимостей пакетов

Зависимости направлены «сверху вниз», `model` — лист без внешних зависимостей.
Стрелка `A → B` означает «package A импортирует package B».

```
cmd/mcp-server ──→ config, providers, server, telemetry
internal/server ──→ config, telemetry, providers, planner, mcp, mcp-go/server
internal/mcp    ──→ planner, providers, model, mcp-go{server,mcp}
internal/planner─→ model, geo, telemetry
internal/providers ─→ model, geo
internal/geo    ──→ model
internal/model  ──→ (никого)
test/smoke      ──→ internal/providers, test/common
test/integration──→ internal/{config,providers,server,telemetry,model}, test/common
```

Внешние зависимости: `github.com/mark3labs/mcp-go` (протокол MCP),
`gopkg.in/yaml.v3` (конфиг). Никто не импортирует `server`, `mcp` и `cmd`
наверх — интерфейсы объявлены там, где вызываются.

## 3. Поток запроса (рантайм)

```
MCP-клиент (агент)
   │ POST /mcp  JSON-RPC 2.0, Streamable HTTP
   ▼
internal/server (mux) → NewStreamableHTTPServer → internal/mcp.App
   │ handleFindRoute
   │  · парсинг аргументов (lat/lon, время, лимиты)
   │  · internal/mcp.network()  — собрать граф из Registry (слить провайдеров,
   │    проверить конфликты id)
   ▼
internal/planner.Plan(net, from, to, params)
   │ 1. geo.NearestStops → access/egress (Haversine, WalkTimeMinutes)
   │ 2. CSA по Connections и Transfer (пешая стыковка) → цепочка шагов
   │ 3. buildLegs → Leg[] + Journey (двери-в-двери, пересадки)
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
| `Coords`, `Mode` (+ `ModeFlight`), `Stop`, `Route`, `StopTime`, `Trip`, `Transfer`, `Connection`, `Network`, `SearchParams`, `LegPoint`, `Leg`, `Cost`, `Journey` | `internal/model/model.go` | каноническая модель; `Cost` — шов под будущий cost-движок |
| `Provider` (ID/Health/Network), `Registry`, `HealthStatus`, `Synth` | `internal/providers/registry.go`, `internal/providers/synth.go` | synth — детерминированная сеть-фикстура, мок только для тестов/демо (не продакшн-источник) |
| `Intercity` (разбор JSON-датасета, пешие стыковки между близкими терминалами) | `internal/providers/intercity.go`, `internal/providers/intercity_test.go` | реальный источник exact-времён из реестра Минтранса; тест на `testdata/reestr/mini.json` |
| `Haversine`, `WalkTimeMinutes`, `NearestStops` | `internal/geo/geo.go` | скорость пешком 5 км/ч; порог пешей доступности 30 мин |
| `Planner.Plan`, `csa`, `reconstruct`, `buildLegs` | `internal/planner/planner.go` | CSA по медицинским правилам: сортировка по отправлению, пересадки по пешим стыковкам, лимит `MaxTransfers` |
| `App.Server`, `handleFindRoute`, `handleListProviders`, `network()` | `internal/mcp/mcp.go` | инструменты MCP; `network()` склеивает провайдеров |
| `Server.New` (mux, эндпоинты, монтаж `/mcp`) | `internal/server/server.go` | `NewStreamableHTTPServer` |
| `Metrics.Inc`, `Snapshot`, `Named` | `internal/telemetry/telemetry.go` | мутекс-защищённые счётчики; в JSON для админки |
| `Load` (default → YAML → env) | `internal/config/config.go` | env: `HTTP_ADDR`, `DATABASE_DSN`, `PROVIDERS_ENABLED`, `INTERCITY_REESTR_PATH`, `ADMIN_TOKEN` |
| main: конфиг → реестр → server → graceful shutdown | `cmd/mcp-server/main.go` | `signal.NotifyContext` + `http.Server.Shutdown` |

## 5. Жизненный цикл процесса

1. `main` читает конфиг (`-config`, по умолчанию `configs/config.example.yaml`).
2. Создаются `telemetry.Metrics`, `providers.Registry` (по `providers.enabled`).
3. `server.New` собирает http-роутер: health-эндпоинты, REST `/api/v1/*`,
   и монтирует MCP (`StreamableHTTPServer`) на `/mcp`.
4. `http.Server` стартует в горутине; `SIGTERM`/`SIGINT` → graceful shutdown
   с таймаутом 10 c.
5. Провайдеры ленивы: сеть строится на каждый запрос (`Registry → Network()`).
   Для `synth` (мок) это дёшево; `intercity` читает и парсит JSON-датасет на
   каждый вызов — кэш/импорт в БД появится позже, интерфейс не изменится.

## 6. Тестирование (точка входа — `make test`)

- `make unit` — модульные тесты: `config_test.go`, `geo_test.go`,
  `planner_test.go` (кросс-город, пешая стыковка, лимит пересадок, перелёт,
  белое пятно).
- `make integration` — `test/integration`: полностью собранный HTTP+MCP handler
  in-process (`httptest`), фиксированные сценарии из `test/common`
  (`AssertGroundJourney`, `AssertFlightJourney`, `AssertNoRoute`, `AssertBadArgs`).
- `make smoke` — `test/smoke`: собирает реальный бинарник, запускает процесс,
  прогоняет те же сценарии поверх живого сокета.
- `make test` — всё сразу: `go test ./... -count=1`.

Заготовленные сценарии в `test/common` не генерируются на лету: они описывают
точные входные координаты и ожидаемые результаты (например, прибытие
`09:36` для Пермь→Екатеринбург наземкой и `09:05` — перелётом). Один сценарий —
одинаковая проверка и в integration, и в smoke.

## 7. Связь с целевой архитектурой (`docs/03`)

Реализовано сейчас | Из целевой архитектуры | Статус
---|---|---
`model` | (в 03.1) | реализовано полностью
`providers` (synth, intercity) | (03.2: gtfs, govregistry, osm…) | интерфейс готов, 2 реализации (мок + реестр Минтранса); остальные — roadmap
`geo` (посчитать на лету) | (03.3: PostGIS/H3) | для каркаса достаточно; индексация — позже
`planner` (CSA) | (03.4, п.3) | RAPTOR multi-criteria — дальнейший этап
`config` (файл+env) | (03.5: третий уровень — БД, hot-reload) | БД-конфиг и hot-reload — позже
`server` (health, /api/v1/providers, /api/v1/dashboard) | (03.6–03.7: auth, users, keys, веб-страница) | auth/админка — на следующих этапах
`telemetry` (счётчики) | (03.8: OTel, Prom, трейсы) | остов готов