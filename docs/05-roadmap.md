# 05. Дорожная карта

Этапы с оценкой трудозатрат (один разработчик, Go-стек). Оценки ориентировочные; каждая строка — с критерием готовности.

## Этапы

### Этап 0 — Скелет сервиса (2–3 дня)
- Модуль `travelmcp`, структура `cmd/mcp-server` + `internal/*`.
- MCP-сервер на `modelcontextprotocol/go-sdk` + `mark3labs/mcp-go`, транспорт Streamable HTTP.
- Динамический конфиг (env + YAML + БД, hot-reload, per-user overrides).
- Health: `/healthz`, `/readyz`.
- Телеметрия: OpenTelemetry + Prometheus-экспорт, JSON-логи с request-id.
- Docker Compose: `mcp-server` + PostgreSQL(+PostGIS).

**Готово, когда**: сервер отвечает на `initialize`/`server/discover` клиентом MCP по HTTP; метрики пишутся; конфиг меняется без перезапуска.

### Этап 1 — Ядро планирования (4–6 дней) — **выполнен**
- Каноническая модель (`internal/model` `StopType/ValidateNetwork/BuildIndexes/SpatialIndex`) и интерфейс Provider.
- Синтетический провайдер (детерминированная сеть `synth`: Пермь→Екб 09:36, аэропорт 09:05).
- CSA/RAPTOR (`engine: csa|raptor`, `arrival-by`, `Pareto Alternatives ≤2`, `gap self_provided`) на нормализованных соединениях; `find_route` `departure/arrival/allow_gap`.
- Nearest stop `SpatialIndex 0.01°` + `findStopByPlace` `Hub`.

**Готово, когда**: MCP-инструмент `find_route(A, B, departure)` возвращает валидную цепочку на синтетике; юнит-тесты ядра. — **ok** `planner_test.go` 7/7.

### Этап 2 — Первые реальные данные (4–5 дней) — **частично**
- GTFS Москвы/СПб — отложен (intercity приоритет).
- Адаптер OSM: `tools/osm-extract` PBF→JSON 24895, `geocode` OSM+газетир+Yandex (`extract-minstran.py --osm`), `NearbyPairs` 0.4км, `SpatialIndex`.
- Метрики прочности: `ValidateNetwork` `issues/excluded`, `HealthStatus` `records/issues` в `list_providers`/`readyz`.
- Store `memory/sqlite modernc` + `ImportIntercity` (station-группировка, `no_geo` обогащение).

**Готово, когда**: внутри одного города строится «адрес → адрес» на реальных GTFS. — межгород покрыт, GTFS город — позже.

### Этап 3 — Межрегиональная связность (2–3 дня) — **выполнен**
- Адаптер реестров Минтранса РФ XLSX 5 листов → `services/service_days` + exact `dep/arr` `+1440`, 351 маршрут 255 стопов → `store` 589 рейсов.
- CSA → RAPTOR (`raptor.go` frequency-based, `engine` `dev:raptor`), multi-criteria `Pareto`.
- Gap-детекция: `AllowGap` `self-leg` `Haversine` → дверь-в-дверь всегда.

**Готово, когда**: `Юрга→Горно-Алтайск` `5 legs` `НСК→Барнаул` `3 legs` с межрегиональной стыковкой и `self_provided` где нет покрытия — **ok** `TestFinalVerify2`.

### Этап 4 — Пользователи и админка (5–7 дней)
- API-ключи со скоупами; саморегистрация + модерация.
- REST API: пользователи, ключи, источники, конфиг.
- Минимальная веб-страница админки + настраиваемый дашборд (здоровье компонентов, нагрузка, внешние запросы).

**Готово, когда**: новый пользователь регистрируется, проходит модерацию, получает ключ и использует MCP с персональными настройками; админ видит источники и метрики.

### Этап 5 — Стоимость (будущее)
- Расширение `Leg.Cost`, реализация cost-движка (по оценке/по тарифам провайдеров).
- Многокритериальный выбор с учётом цены.

### Этап 6 — Скрейпинг (по необходимости, 2–4 дня за первый сайт)
- Модульный скрейпер + нормализатор + валидатор + метрики здоровья.

## Консолидированный план рефакторинга (детально — docs/09-refactor-plan.md)

Зафиксирован 2026-09-02, объединяет незавершённое выше с review. Зависимости по фазам.

| Фаза | Тема | Зависит от | Срок | Критерий |
|---|---|---|---|---|
| 0 | Hotfix безопасности (`bcrypt`, `crypto/rand` ключи, `ConstantTimeCompare`, `html/template`, `MaxBytesReader`, `WithTx`/`foreign_keys`, маскировка DSN) | — | 1–2д | `curl /register admin` не даёт админа, `make test` |
| 1 | Единая точка конфигураций (koanf `TRAVELMCP__` prefix, один `Config`, только `-config` флаг) + логгер `Factory` per-модуль `log.levels` + `httpx.Client` (Info кратко / Debug body) + `readyz` снапшот | 0 | 3–4д | `PROVIDERS_ENABLED` через `Config`, `/readyz` <5мс, логи внешних запросов |
| 2 | Порты/адаптеры (`intercity/raw→mapper`, `Factory` реестр, `timeutil` дедуп), валидация split (ядро коды / политика `BBox`), `StopClassifier` модульный (ru.yaml), `NetworkCache` | 1 | 1.5н | Новый `gtfs` — только пакет + поле `Gtfs` в `Config`, `model` без кириллицы |
| 3 | Хранилище `postgres PostGIS`, `cache redis/memory`, `queue memory→nats` интерфейсы, `semaphore`+`rate limit` поиска | 2 | 1н | `DATABASE_DSN`/`CACHE_KIND` переключаются конфигом |
| 4 | Наблюдаемость `GET /metrics` prom, `otel` трейсы, `request-id`, `telemetry` на prom | 1,3 | 4д | `curl /metrics` prom формат |
| 5 | Универсальные источники `adapters/gtfs`, `Geocoder`, `osm` (из 05 Этап 2/3) | 2,4 | 1–2н | `PROVIDERS_ENABLED=gtfs,intercity` merged сеть |
| 6 | Пользователи/админка/стоимость (05 Этап 4/5), скрейпер (05 Этап 6) | 3 | 5–7д | per-user `config`, `/api/v1/config` hot-reload |
| 7 | i18n последняя (`locales`, `Classifier` per-регион) — **backlog, отложена 2026-09-02** (приоритет — логирование и данные `docs/11-data-import-strategy.md`) | все | 3–5д | — |

См. `docs/09-refactor-plan.md` §1–2 для схемы `Config`/`log.levels`/`httpx`.

## Итоговые ориентиры

- MVP (этапы 0–2): ~2–3 недели.
- Полноценный контур с пользователями и админкой (0–4): ~4–6 недель.

## Риски и сглаживание

| Риск | Сглаживание |
|---|---|
| Пустые/устаревшие данные в реестрах Минтранса | Использование реестра как каркаса связности; времена брать из GTFS/скрейпинга |
| Хрупкость скрейпинга | Точечно, метрики здоровья, валидация, осознание ToS-рисков |
| Санкционные нестабильности внешних доменов | Адаптеры переживают сбой; метрики; переключение источников конфигом |
| Производительность ядра на больших сетях | CSA → RAPTOR; in-memory numpy/parquet; при необходимости выделить тяжёлые куски в отдельный сервис |
| 152-ФЗ и приватность | Минимум персональных данных; маскировка/ограничение логов |