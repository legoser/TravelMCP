# 15. Текущее состояние разработки

Снимок «сейчас». Переписывается целиком при завершении каждой фазы
`14-plan.md` — истории здесь нет, только актуальное состояние. Если что-то
из описанного ниже противоречит коду — прав код, документ обновляется.

## 1. Стратегия миграций (временное правило текущей фазы)

Пока БД не в проде и не набрала production-данные, действует правило:
**одна консолидированная миграция `migrations/001_initial.sql`**, без
цепочки `ALTER`. Dev/staging пересобираются `DROP SCHEMA + миграция`.

**Это правило действует только до первого прод-деплоя с реальными данными.**
С этого момента:

- `001_initial.sql` замораживается как есть;
- дальнейшие изменения схемы — только новые инкрементальные файлы
  (`002_*.sql`, `003_*.sql`, аддитивные, без разрушающего `ALTER` на
  заполненных таблицах — см. инвариант в `AGENTS.md`);
- решения оператора (ручные правки, merges, резолюции `review_queue`)
  экспортируются датасетом overrides по внешней идентичности (OSM id,
  `yandex_code`, NK Минтранса) и применяются после миграции — привязка
  к internal ID после пересбора ляжет;
- ответственный за переход — обновить этот раздел, `14-plan.md` §7
  и `AGENTS.md`, зафиксировать инструмент миграций
  (goose/golang-migrate/самописный раннер).

## 2. Рантайм: планировщик на Store (Фаза 6, включено)

- `find_route` строит сеть из Postgres-канона (`App.networkForDay`,
  `internal/mcp` → `LoadNetwork` postgres/memory): только живые
  routes/trips (`valid_to IS NULL`), day-фильтр по `trips.service_days`
  (формат `0,1,5`, вс=0; пусто = без календаря), `Trip.ID=routeNK|tripNK`,
  времена в каноне UTC, fuzzy-стопы (`is_fuzzy`) идут с `TimeHint`.
- Кэш сети инвалидируется пакетным `canonVersion`
  (`mcp.BumpCanonVersion()` при сборах/правках/merge/reset).
- Allow-list провайдеров канона в сети: `gov-registry` + `registry` +
  `yandex` (план §5.4) — иначе LOAD молча фильтрует чужие рейсы.
- Legacy JSON-провайдер `intercity` вырезан после паритета
  (`scripts/parity-check.py`: routes 96.7% / timed 99.2%, порог 95%).
  `synth` — мок только для тестов/демо, в проде не источник.
- Реестр Минтранса читается только через standalone-коннектор
  `tools/registry-parser` → `flat_trips.json` (`meta.source=gov-registry`);
  sync-конвейер про источник не знает ничего, кроме `meta.source`.
  Провайдер в БД — `gov-registry`; типы кодов — справочник
  `identifier_schemes` (редактируется из админки).

## 3. Дерево пакетов (актуальное)

```text
cmd/mcp-server/         точка входа; collect_job.go — job sync_collect_region (skeleton|trips)
cmd/skeleton-sync/      раннер скелета (staging → чанки ≤100 → промоут → coverage)
cmd/trips-sync/         раннер attach (контракт → coverage/gate → attach → персист → sync_runs)
cmd/gtfs-import/        импорт внешних GTFS-фидов в attach-поток
cmd/gtfs-validate/      CI-gate: provenance-gate + MobilityData gtfs-validator
internal/model/         канон: Terminal/Route/Trip/StopTime, AdaptedRecord, FlatPayload, Provenance
internal/sync/          конвейер: planid/logicversion, elect/legacy_match, skeleton_promote,
                        trips_attach (+match_index/backbone/gapfill/georesolve/fuzzy),
                        trips_promote (персист), trips_jobs (барьер), trips_run (оркестрация),
                        coverage(_attach), calibrate, attribute_hygiene, staging_expiry,
                        collect_region/collect_overpass, gtfs_import, replay, overrides
internal/skeleton/      OSM/Yandex-источники → AdaptedRecord, CollapseStopArea,
                        Join/JoinPager (0.5° ячейки, ~3 мин на 177K×177K), InterpolatePosition
internal/verification/  ScorePair (оси skeleton_osm/stop_terminal/merge/dedup/legacy_match)
internal/geocoder/      cached.go (geocode_cache, TTL 90д/7д, stale-while-revalidate,
                        квота БД, пейсер) + seed.go + cached_stations/routes (Overpass cache+quota)
internal/adapters/      gtfs (Москва/СПб), yandex (Rasp schedule/thread, cache-first),
                        motis (geocode/areas), overpass (StationsAround/route-relations),
                        nominatim, osm
internal/store/         Store-интерфейсы (store.go/models.go); postgres/ (pool+tx), memory/ (тесты)
internal/planner/       CSA-поиск + Journey поверх сети из Store
internal/providers/     Provider-интерфейс, Registry, synth (мок), gtfs-обёртка
internal/gtfs/compiler/ Postgres → gtfs_{region}_{mode}.zip (provenance-gate до сборки)
internal/jobs/          очередь jobs+outbox, воркер, backoff (типы sync_*)
internal/geo/           гаверсин, walk-доступ, коды регионов, Gazetteer (поселения из канона)
internal/server/        HTTP+MCP, админка (терминалы/merge/collect/routes-поиск), web/
internal/mcp/           find_route + enrichFuzzyLegs (контакты перевозчика)
internal/config/        default → YAML → env (все knob'ы — §4)
internal/support/       classifier, httpx (таймаут 90s), namesim (Core/нормализация), timeutil, translate
internal/{cache,logger,middleware,pricing,queue,telemetry}  инфраструктура
tools/registry-parser/  standalone-коннектор реестра (stdlib-only): контракт, дни недели,
                        bootstrap NK/churn, synthetic-ключи → flat_trips.json
tools/osm-extract/      standalone: PBF → JSON гео-меток
testdata/               flat_trips.json, golden_terminals.json, golden_trips.json, overpass/
scripts/                api-demo.sh, mcp-route.sh, yandex-/overpass-collect.sh,
                        extract-minstran.py, match-reestr-yandex.py, parity-check.py, doc-lint.sh
data/                   сырьё/кэши/дампы — НЕ коммитятся
```

## 4. Env-переменные (текущий полный список)

Группировка по подсистемам; конфиг-ключи в скобках. `doc-lint.sh`
требует присутствия здесь: `HTTP_ADDR DATABASE_DSN PROVIDERS_ENABLED
SYNC_FLAT_TRIPS_PATH ADMIN_TOKEN SYNC_LOG_DIR SYNC_COVERAGE_GATE
SYNC_ATTACH_WAIT SYNC_TRIPS_CHURN_THRESHOLD SYNC_TRIPS_MAX_SPEED_KMH
VERIFICATION_SCORE_MARGIN VERIFICATION_SCORE_AMBIGUITY
GEOCODER_TTL_VERIFIED GEOCODER_TTL_DISPUTED`.

| Переменные | Назначение |
|---|---|
| `HTTP_ADDR` / `DATABASE_DSN` / `PROVIDERS_ENABLED` / `ADMIN_TOKEN` | адрес, DSN Postgres, активные Provider, токен админки (до JWT Фазы 5) |
| `LOG_LEVEL` / `LOG_FORMAT` / `LOG_ADD_SOURCE` / `LOG_LOKI_*` | уровень/формат slog, Loki-бэкенд |
| `HTTP_RATE_LIMIT_RPS` / `HTTP_RATE_LIMIT_BURST` | in-process лимит HTTP (не квота API — квоты только через БД) |
| `PLANNER_MAX_WALK_MINUTES` / `PLANNER_MIN_TRANSFER_MINUTES` / `PLANNER_FLIGHT_CHECK_IN_MINUTES` | дефолт пешей доступности (30), буфер стыковок (15), регистрация на рейс (120) |
| `PLANNER_ALT_WINDOW_MINUTES` / `PLANNER_ALT_STEP_MINUTES` | окно (360) и шаг (60) альтернативных отправлений Парето-эвристики |
| `PLANNER_ENGINE` / `PLANNER_SEMAPHORE_*` | движок планировщика, параллелизм |
| `PRICING_DEFAULT_CURRENCY` / `DEFAULT_CURRENCY` | валюта цен по умолчанию |
| `GTFS_TMP_DIR` | временные файлы сборки GTFS |
| `SYNC_OSM_PATH` / `SYNC_YANDEX_DUMP_PATH` / `SYNC_SKELETON_REGION` / `SYNC_BBOX` / `SYNC_REGION_BBOX` / `SYNC_FLAT_TRIPS_PATH` | входы скелета/attach: OSM-станции, дамп Яндекса, регион, bbox, flat_trips.json (`sync.*`) |
| `SYNC_SKELETON_CHUNK_SIZE` / `SYNC_LOG_DIR` | терминалов на чанк (потолок 100), каталог ops-логов `sync_<run>.log.jsonl` |
| `SYNC_COVERAGE_GATE` / `SYNC_LEGACY_THRESHOLD` / `SYNC_COVERAGE_SOFT_SCORE` | порог coverage-gate, порог legacy_match (0.6), мягкий порог coverage (0.4) |
| `SYNC_ATTACH_WAIT` / `SYNC_TRIPS_CHURN_THRESHOLD` / `SYNC_TRIPS_MAX_SPEED_KMH` | таймаут барьера attach, churn-алерт (старт 0.2), предел скорости hard-валидатора (200) |
| `SYNC_STAGING_EXPIRY_DAYS` | dead-letter порог staging (14д, `jobs{cleanup}`) |
| `VERIFICATION_SCORE_MARGIN` / `VERIFICATION_SCORE_AMBIGUITY` | полоса low_confidence (0.1), зазор duplicate_ambiguous (0.05) |
| `VERIFICATION_CONFIDENCE_THRESHOLD` / `VERIFICATION_DISTANCE_M` / `VERIFICATION_STRONG_DISTANCE_M` / `VERIFICATION_NAME_SIMILARITY` / `VERIFICATION_LEV_THRESHOLD` | пороги ScorePair (дефолт verified 0.6, калибровка §3.8) |
| *(конфиг-файл, не env)* | `density_thresholds` + `trust[pair]` ScorePair — путь в `configs/` |
| `GEOCODER_KIND` / `GEOCODER_LIMIT` / `GEOCODER_ATTEMPTS` / `GEOCODER_MAX_CALLS` | бэкенд геокодера, лимит квоты `api_quotas`, попытки |
| `GEOCODER_TTL_VERIFIED` / `GEOCODER_TTL_DISPUTED` | 90д / 7д для `geocode_cache` |
| `OVERPASS_URL` / `OVERPASS_MIRROR_URL` | основной и зеркало Overpass (kumi исключён) |
| `MOTIS_URL` / `MOTIS_BASE` / `NOMINATIM_URL` | endpoints MOTIS и Nominatim (1 rps) |
| `YANDEX_RASP_KEY` / `YANDEX_RASP_URL` / `YANDEX_RASP_CACHE_DIR` / `YANDEX_RASP_QUOTA_LIMIT` | ключ/URL/кэш/лимит Rasp (квота `yandex_rasp`, дефолт 500) |
| `YANDEX_GEOCODE_KEY` / `YANDEX_GEOCODE_URL` | ключ/URL геокодера (квота `yandex_geocode`, лимит `geocoder.max_calls`) |

Новые переменные — добавлять в эту таблицу по мере появления, не хранить
список только в коде/`configs/`.

## 5. Референс тестовых данных

- `testdata/flat_trips.json` — вход attach и planner-тестов (генерирована
  `tools/registry-parser`, замена `test.json`).
- `testdata/golden_terminals.json` / `golden_trips.json` — CI требует
  precision/recall 1.0 по обоим осям ScorePair (identity+merge,
  стоп→терминал + sequence/монотонность/интерполяция + межзонный кейс).
- `testdata/overpass/` — фикстуры stations/route (порядок членов,
  геометрия chained/reversed/broken).
- `synth`-сеть (только тесты/демо): кластеры A «Пермь» / B «Екатеринбург» /
  C «ПГУ»; стабильные ожидания — Пермь→Екб 06:00 прибытие 09:36 (5 легов,
  2 пересадки), перелёт 07:00→09:05, C→B — «маршрут не найден».

## 6. Статус фаз

| Фаза | Статус | Итог (файл-точки входа) |
|---|---|---|
| 0. Миграция данных | done (внутри Фазы 3) | `ElectField` per-field конкурс, `MatchLegacy` → `review_queue{legacy_unmatched}` (`internal/sync/elect.go`, `legacy_match.go`) |
| 1. DDL и инфраструктура | done | `migrations/001_initial.sql` (канон+staging+quotas+review+sync_runs/chunks, provenance.channel, identifier_schemes); `planid`/`LogicVersion="3"` (`internal/sync/logicversion.go`); replay-diff, overrides, `doc-lint.sh` |
| 2. Кэш геокодера | done | `CachedGeocoder` + seed `origin='seed'` (`internal/geocoder/cached.go`, `seed.go`) |
| 3. Skeleton | done | `OSMSource`/`YandexDumpSource`/`CollapseStopArea`/`Join`+`JoinPager`/`InterpolatePosition` (`internal/skeleton`); промоут чанками (`internal/sync/skeleton_promote.go`); СФО-пилот: 55 751 терминал, coverage reestr42 148/148 |
| 4. Trips | done | attach-движок (`trips_attach.go` + `match_index`/exclusivity/backbone/gapfill/georesolve/fuzzy), персист per-trip (`trips_promote.go`), барьер+jobs (`trips_jobs.go`), раннер (`cmd/trips-sync`, `trips_run.go`), калибровка 0.6/0.7 (`calibrate.go`), коды 4.5, `tools/registry-parser` |
| 5. Freshness | done | `RunHygieneSweep` + staging-expiry в `jobs{cleanup}` (`attribute_hygiene.go`, `staging_expiry.go`); stale-KPI 30д, possible_merge-аудит, transport_types-union, FP-сэмплинг |
| 6. Депрекация/масштаб | done | планировщик на Store, паритет + вырезка legacy, GTFS provenance-gate + validator, `LogicVersion`, Overpass route-relations + cache+quota, реестр как плагин |
| O/S/C/D-блоки | done | O-1–O-10 (Overpass), S-1–S-4 (settlement, gate=soft-rate), C-1–C-3 (provisional, glued-валидаторы, реран), D-1–D-5 (blocking, exclusivity, backbone, match_score, единая формула) |
| 7. Качество/безопасность | backlog | план §11, см. §8 ниже |

Ключевые решения (не переопределять точечно, см. план §0):

- Направление — свойство рейса (`direction_id`), не терминала; стороны
  дороги — разные `stops_canonical` с одним `terminal_id`.
- Безымянный стоп (поселение не извлеклось) — только полные тёзки по
  Core (`internal/sync/trips_georesolve.go`); заимствованная геометрия
  (`FlatStop.CoordsBorrowed`) не проходит dist-guard ScorePair.
- Coverage-gate меряет soft-rate, attach требует verified (план §10 S-4).
- API-ключи внешних сервисов — read-only, шифрование at-rest не требуется
  (решение 2026-09-15).

Backlog (не Фаза 7): D-6 sequence-context tie-break; McRAPTOR вместо
эвристики Парето; demand-driven и TTL-ресинк расписаний (§3.4);
проводка GTFS/MOTIS-кодов в attach; 98 stale `incomplete_trip` старых
прогонов (закрыть/оставить — открыто).

## 7. Чек-лист устаревания

Прогонять `make check-deprecated` после закрытия каждого пункта
`14-plan.md`. Сигналы: `Deprecated`/`TODO.*phase` в коде; привязки
к удалённым путям (`seed_pilot`, `places_sqlite`, `import_intercity`).
Актуальные grep-паттерны — в `Makefile`, этот раздел только поясняет
зачем; напоминание о сверке со §2 — в цели `make`.

## 8. Фаза 7 (частично выполнена)

Зафиксирована в `14-plan.md` §11. Done: 7.2 (санитизация секретных логов,
allow-list redact), 7.3 частично (race-тесты квоты/воркера/singleflight,
`make unit` с `-race` зелёный). Done 2026-09-15 (вторая волна): CI-gate
`.forgejo/workflows/ci.yaml` (fmt/vet/unit/check-layers/check-deprecated/
doc-lint + integration на postgis + tools-модуль); контракт листинга
`test/common/browser_contract.go` (memory+postgres: clamp страниц, tiebreak
имени, LIKE-escape `%`/`_`, injection-строки); краевые `find_route`
(таблица 14→25 кейсов), `adminLimitOffset`, hard/soft-валидаторов
(секундное разрешение скорости), импортов (overnight-развёртка реестра,
single-time pairing, frequency-only, контракт датасета, `SkippedStopTimes`);
минимальные краевые `httpx/timeutil/classifier/jobs.Backoff/nominatim/osm`.
Done 7.4: `yandex.rasp_quota_limit`, `planner.alt_window/step_minutes`,
`planner.arrival_window_hours/arrival_step_minutes` (24ч/30м),
`sync.jobs_poll_interval` ("5s") — всё через `default → YAML → env`.
Done 7.1: `config.go` → `sections/defaults/env`; server/sync уже были
пофайлово разделены (субпакеты отвергнуты, см. 14-plan §11).
Done smoke-паритет: `AssertBadArgsTable` в `TestSmokeMCPFlow`.
Трек Г: stale закрывается expiry (incl. `NULL last_attempt_at`); McRAPTOR
и demand-driven — backlog отдельными фазами. Находка закрыта: автошедулер
`cleanup` (`sync.cleanup_interval`, дефолт 24ч, дедуп) — sweep больше не
только ручной; `hygiene_retention_days`/`quota_history_keep_days` в конфиге.
Нюанс закрыт: точный `isRateLimited` (был вечный ретрай на «generate»).
Комментарии — только английские в файлах, тронутых текущей задачей.
