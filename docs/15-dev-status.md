# 15. Текущее состояние разработки

Снимок «сейчас». Переписывается целиком при завершении каждой фазы
`14-plan.md` — в отличие от `13-mission.md`/`14-plan.md`, здесь нет
намерения хранить историю, только актуальное состояние. Если что-то из
описанного ниже противоречит коду — прав код, документ обновляется.

## 1. Стратегия миграций (временное правило текущей фазы)

Пока БД не в проде и не набрала production-данные, действует правило:
**одна консолидированная миграция `migrations/001_initial.sql`**, без
цепочки `ALTER`. Причина — на этапе Фаз 0–5 схема меняется быстрее, чем
имеет смысл писать инкрементальные миграции поверх пустых таблиц; проще
пересобирать один файл и прогонять `DROP SCHEMA + миграция` на dev/staging.

**Это правило действует только до первого прод-деплоя с реальными данными.**
С этого момента:
- `001_initial.sql` замораживается как есть;
- дальнейшие изменения схемы — только новые инкрементальные файлы
  (`002_*.sql`, `003_*.sql`, аддитивные, без разрушающего `ALTER` на
  заполненных таблицах — см. инвариант в `AGENTS.md`);
- ответственный за этот переход — обновить и этот раздел, и `AGENTS.md`,
  если общий инвариант аддитивности нужно уточнить под конкретный
  инструмент миграций (goose/golang-migrate/самописный раннер — выбор
  зафиксировать здесь, когда встанет вопрос).

## 2. Статус перехода JSON → Postgres-канон

Сейчас в кодовой базе одновременно существуют два поколения архитектуры:

- **Legacy / текущий рантайм планировщика**: `internal/providers` с
  `Provider`-интерфейсом. `intercity` читает `data/reestr/regions.json`
  заново при каждом вызове `Network()`/`Health()` — кэша/TTL нет. `synth` —
  мок-сеть, включается явно (`PROVIDERS_ENABLED=synth`), используется
  только в тестах/демо, никогда не должна быть источником по умолчанию в
  проде.
- **Целевая архитектура (проектируется/строится по `14-plan.md`)**: канон в
  Postgres+PostGIS (`places/terminals/stops_canonical/routes/trips/...`),
  конвейер `AdaptedRecord` → нормализация → обогащение → дедуп →
  верификация → канон, коннекторы внешних источников — standalone-модули
  `tools/*` (реестр: `tools/registry-parser` → flat_trips.json) +
  `internal/adapters/{yandex, motis,gtfs}`, компилятор `internal/gtfs/compiler`
  (per-region `gtfs_{code}_{mode}.zip`), очередь `internal/jobs` + `outbox`.

Пока обе архитектуры сосуществуют: `internal/providers` обслуживает текущий
`find_route`, канон в Postgres наполняется параллельно по фазам плана.
Точка переключения планировщика с `Provider`(JSON) на `Store`(Postgres) —
отдельная задача, не начатая на момент этого снимка; при переключении
обновить этот раздел и убрать `internal/providers/intercity` в `deprecated`
по чек-листу ниже.

## 3. Дерево пакетов (текущее, полное)

```
cmd/trips-sync/        раннер массового attach Фазы 4.4 (конфиг → контракт → план → coverage/gate → attach → персист → sync_runs + ops-лог; флаги --regions/--wait/--force/--dry-run/--trust-nk)
cmd/mcp-server/collect_job.go   job sync_collect_region (ручной сбор региона §5.3-dev: skeleton|trips из Яндекс-дампа/кэша Rasp через квоты)
cmd/gtfs-validate/    CI-gate производственного GTFS: fixture-zip через provenance-gate + MobilityData gtfs-validator (make gtfs-validate, GTFS_VALIDATOR_BIN)
internal/providers/     Provider-интерфейс, Registry, synth (мок), gtfs (адаптер-обёртка; legacy intercity вырезан, Фаза 6)
internal/geo/           гаверсин, ближайшие остановки, время пешего доступа, таблица кодов регионов (общие геодезические константы: MetersPerDegree, JoinCellSizeDeg, JoinGeoWindowM)
internal/sync/          Фазы 0–1: `planid` (plan_id от `LogicVersion` + ChunkFresh; логик-версия вместо sha(binary) — 2026-09-10), `replay` (каноническая проекция + diff), `overrides` (экспорт/импорт решений оператора), `elect` (per-field конкурс + legacy-матчинг); Фаза 4: `trips_attach` (движок 4.2) + `trips_promote` (персист 4.3: TripsStore/PersistAttachReport/outbox, tagging intercity, provenance route+trip) + `trips_jobs` (payload sync_trips_attach, барьер) + `coverage_attach` (coverage осью stop_terminal, gate) + `calibrate` (sweep порога, рекомендация) + `trips_run` (оркестрация массового attach 4.4)
internal/skeleton/      Фаза 3: источники отдают `model.AdaptedRecord` (`Kind=AdaptedTerminal`, идентификаторы `osm_id`/`yandex_code`/`esr_code`, `Extra`: settlement/region/transport_type) — своего типа терминала нет; `OSMSource` + `YandexDumpSource` (только локальные файлы, 0 API), `CollapseStopArea` (иерархия OSM на экстракции), `Join` (скоринг OSM↔Yandex: код > гео+имя; `Enrichment`/`Score` — в результате join'а, не в записи; Yandex-only → unverified, не канон), `InterpolatePosition` (seq/долевой кандидат, origin seed), `CoverageByRegion`+`GatePass` (coverage-gate)
internal/geocoder/      fallback + `cached.go` (lookup `geocode_cache`, TTL 90д/7д, stale-while-revalidate, квота БД) + `seed.go` (загрузка `{yandex,nominatim}_geo.json` с `origin='seed'`) + `cached_stations.go`/`cached_routes.go` (O-4/O-5: путь cache+quota Overpass, общий пейсер 1.2с, ключи `overpass:around:*`/`overpass:route:*`)
internal/planner/       CSA-поиск + сборка Journey (работает поверх Provider, не Store)
internal/store/         Store/PlaceStore и т.п. интерфейсы; postgres/, sqlite/ (legacy, к удалению), memory/ (тесты)
internal/model/adapted.go   AdaptedRecord — единый промежуточный формат коннекторов
internal/verification/  ScorePair — единый скоринг (§3.8: stop_terminal/skeleton_osm/merge/dedup/legacy_match); `verify.go`/`VerifyTerminal` удалены, `haversineMeters` переехал в `scorepair.go`
internal/adapters/gtfs/      коннектор внешних GTFS (Москва/СПб)
internal/adapters/yandex/    коннектор Яндекс.Расписание (квоты, ротация)
internal/adapters/motis/     коннектор MOTIS (geocode/reverse-geocode/areas)
tools/registry-parser/       standalone-коннектор реестра Минтранса РФ (отдельный Go-модуль, stdlib-only): срез reestr.json → flat_trips.json; контракт, дни недели, bootstrap NK-стабильности, synthetic-ключи и --trust-nk живут здесь; конвейер sync про источник не знает
internal/gtfs/compiler/ Postgres → gtfs_{region}_{mode}.zip, per-region
internal/jobs/           очередь (jobs+outbox), воркер, backoff, ротация провайдеров; типы `sync_stations/sync_refresh/sync_terminals_chunk/sync_trips_attach` (Фаза 4.3)
tools/osm-extract/       отдельный Go-модуль: PBF (OSM) → JSON гео-меток
data/                    сырьё и датасеты сбора (НЕ коммитятся)
scripts/api-demo.sh          ручное демо/обследование API (curl+jq)
scripts/mcp-route.sh         шаблоны запросов MCP
scripts/yandex-collect.sh    точечный сбор фикстур Яндекса
scripts/extract-minstran.py  XLSX-реестр Минтранса → JSON датасет (вход trips-sync; legacy-путь провайдера закрыт)
scripts/parity-check.py      паритет store vs legacy-срез реестра (Фаза 6)
```

## 4. Env-переменные (текущий полный список)

| Переменная | Назначение |
|---|---|
| `HTTP_ADDR` | адрес HTTP-сервера |
| `DATABASE_DSN` | строка подключения к Postgres |
| `PROVIDERS_ENABLED` | список активных `Provider` (пусто по умолчанию; `synth` — тесты/демо; сеть строит Store из канона — legacy `intercity` вырезан, Фаза 6) |
| `ADMIN_TOKEN` | токен админки текущей фазы (до внедрения полноценного JWT/ролей по плану Фазы 5) |
| `PLANNER_MAX_WALK_MINUTES` | дефолт пешей доступности (мин, 1..180), если запрос `find_route` не задал `max_walk_minutes`; междугородние автовокзалы часто за окраиной 2.5 км (конфиг `planner.max_walk_minutes`, дефолт 30) |
| `SYNC_STAGING_EXPIRY_DAYS` | порог dead-letter для протухших `staging_trips` (дней, дефолт 14; `jobs{cleanup}` + `sync.staging_expiry_days`, план §5.3) |
| `SYNC_LOG_DIR` | каталог файлов операций `sync_<run>.log.jsonl` (план, Фаза 1; конфиг `sync.log_dir`) |
| `SYNC_COVERAGE_GATE` | порог N% coverage-gate скелета (план, Фаза 4; конфиг `sync.coverage_gate`) |
| `SYNC_SKELETON_CHUNK_SIZE` | терминалов на чанк промоушена скелета, потолок 100 (план §4.2; конфиг `sync.skeleton_chunk_size`) |
| `SYNC_OSM_PATH` / `SYNC_YANDEX_DUMP_PATH` / `SYNC_SKELETON_REGION` / `SYNC_BBOX` / `SYNC_FLAT_TRIPS_PATH` | входы пилота скелета: OSM-станции, дамп Яндекса, регион-фильтр, bbox, flat_trips.json (генерирует `tools/registry-parser`) для coverage и trips-sync (конфиг `sync.osm_path`, `sync.yandex_dump_path`, `sync.skeleton_region`, `sync.bbox`, `sync.flat_trips_path`) |
| `SYNC_LEGACY_THRESHOLD` / `SYNC_COVERAGE_SOFT_SCORE` | порог `legacy_match` Фазы 0 (0.6) и мягкий порог ScorePair для coverage (0.4); конфиг `sync.legacy_threshold` / `sync.coverage_soft_score` |
| `SYNC_TRIPS_CHURN_THRESHOLD` | churn-alert attach трипов: доля added+removed+changed за прогон, стоп при превышении, старт 0.2 (план, Фаза 4; конфиг `sync.trips_churn_threshold`) |
| `SYNC_ATTACH_WAIT` | таймаут барьера attach: ждать прохождения coverage-gate до истечения (например `5m`), пусто — не ждать, пропущенные регионы — в `skipped_routes` + starvation-warning (план §4.2/§5.4; конфиг `sync.attach_wait`, Фаза 4.4) |
| `VERIFICATION_SCORE_MARGIN` / `VERIFICATION_SCORE_AMBIGUITY` | полоса ниже порога → `low_confidence` (0.1) и зазор top1−top2 → `duplicate_ambiguous` (0.05); конфиг `verification.score_margin` / `verification.score_ambiguity` (ScorePair, Фаза 4.1) |
| `SYNC_TRIPS_MAX_SPEED_KMH` | физический предел скорости автобуса для hard-валидатора, старт 200 (план §5.1; конфиг `sync.trips_max_speed_kmh`, Фаза 4.2) |
| `YANDEX_RASP_CACHE_DIR` | каталог дискового кэша Rasp API (`schedule_*`/`thread_*`, cache-first; конфиг `yandex.rasp_cache_dir`, дефолт `data/yandex/cache`) |
| `GEOCODER_TTL_VERIFIED` / `GEOCODER_TTL_DISPUTED` | 90д / 7д для `geocode_cache` (план, Фаза 2; конфиг `geocoder.ttl_*`) |
| *(конфиг-файл, не env)* | `density_thresholds` + `trust[pair]` ScorePair — путь в `configs/`, не env (план §3.8) |

Новые переменные (квоты Яндекса, пороги verification, JWT-секрет для
Фазы 5 и т.п.) — добавлять в эту таблицу по мере появления, не хранить
список только в коде/`configs/`.

## 5. Референс тестовых данных

### 5.0 Фазы 0–2 (выполнено)

- **Фаза 1:** `migrations/001_initial.sql` — единая миграция: `terminals.address/address_parts/transport_types/object_type/enrichment_status`, `terminal_aliases`, `terminal_merges`, `routes.external_route_code` + `UNIQUE(source_provider, external_route_code)`, `trips.external_trip_code/direction_id/duration_s/distance_m/method/valid_to` + `UNIQUE(route_id, external_trip_code)`, `staging_trips`, `trip_sources`, `attribute_state` (+`value`,`origin`,`sync_run_id`), `geocode_cache` (+`origin`), `review_queue` (+`state`,`fingerprint`,`count`,`observed_at`, enum-канон §3.10), `provenance_history.sync_run_id` + индекс, `sync_runs` (+`summary`)/`sync_chunks`, типы джоб `sync_*` в `jobs`, `v_stale_attributes`. Без `ALTER` — всё в исходных `CREATE TABLE`. Инфраструктура: `internal/sync` (plan_id, replay-diff, overrides), `scripts/doc-lint.sh`, `testdata/golden_*.json`.
- **Фаза 2:** `CachedGeocoder` (hit без квоты, stale-while-revalidate, singleflight на ключ), TTL из конфига `geocoder.ttl_verified/ttl_disputed` (90д/7д, env `GEOCODER_TTL_*`), seed `origin='seed'` из `data/reestr/{yandex,nominatim}_geo.json` (не голос за finalize), квота БД через инжектируемый `QuotaFunc`, лимит квоты — `geocoder.max_calls` (env `GEOCODER_MAX_CALLS`), `GeoResolver` удалён (таблица регионов живёт в `internal/geo/regions.go`).
- **Фаза 0:** per-field конкурс `ElectField` (`geom` — повышенный вес скелета OSM/MOTIS, остальное `confidence → observed_at`, `actor_id` — sticky-барьер поля, `seed` — только fallback), без пары — `MatchLegacy` → `review_queue{legacy_unmatched}`. Исполняется внутри Фазы 3 после построения скелета.
- **Фаза 3 (скелет, без БД-зависимостей):** `internal/skeleton` — `OSMSource` (парсит `stations.json` экстрактора, безымянные отбрасываются), `YandexDumpSource` (офлайн-разбор `global_stations_list.json`: 159 167 станций, Кузбасс — 1684; координаты бывают пустыми строками → указатель `nil`), `CollapseStopArea` (одно имя + геоклетка → один терминал, агрегат `transport_types`, слияние идентификаторов), `Join` (verified только при гео-паре в пороге или код-матче; geom-null по одному имени — никогда verified; несопоставленный Yandex → `Unverified`/review, не канон; вариант названия Яндекса сохраняется в `Extra[yandex_title]` для алиасов), `InterpolatePosition` (кандидат между сматченными соседями по `seq`, долевой при наличии весов, `origin='seed'`, confidence < 0.6), `CoverageByRegion`/`GatePass` (порог — `sync.coverage_gate`/`SYNC_COVERAGE_GATE`, старт 0 до калибровки на Кузбассе).
- **Фаза 3 (скелет, Postgres-часть):** `internal/sync/skeleton_promote.go` — узкий `SkeletonStore` (терминал + алиас + `attribute_state` + `provenance`/`review_queue` + `sync_runs`, без расширения широкого `Store`), `ChunkSkeleton` (детерминировано регион→транспорт→код, чанк ≤ 100, потолок в `sync.skeleton_chunk_size`), `PromoteSkeletonChunk` (один чанк = одна транзакция через `WithTx`; row-reconciliation `in == written + review`, fail-loud; без координат → `review_queue{low_confidence}`, Yandex-only → `review_queue{skeleton_unverified}` с синтетическим отрицательным `entity_id` от fnv-внешней идентичности и `fingerprint=source:code`), `Begin/FinishSkeletonRun` (вид `skeleton`, summary-JSON). DDL в `001_initial.sql`: `staging_terminals(run_id, source, source_code, ...)` + GiST по `geom` (кандидаты — `ST_DWithin`, точный скоринг — в Go: `NearbyStagedCandidates` в `internal/store/postgres/skeleton.go`); `review_queue.fingerprint` пишется `SaveReviewQueue` (пустой не затирает); `attribute_state`-апсерт бампает `observed_at` только при смене value/confidence (no-op-конвергенция §4.3). Реализации: postgres (pool + tx) и memory.
- **Фаза 0 + пилот Кузбасс (прогон на живой БД `travelmcp_pilot`, 2026-09-06):** раннер `cmd/skeleton-sync` (конфиг `sync.*`, `plan_id` из версии + конфига + sha входов, staging → чанки `sync_chunks` с пропуском свежих по `plan_id_done`, resume флагом `-run`, ops-лог `sync_<run>.log.jsonl`, `-dry-run` без записи). OSM-экстракт СФО — 27 257 станций (в т.ч. 2360 ж/д: фильтр экстрактора расширен на `railway=station/halt`, иначе rail-терминалы не матчились бы вовсе); bbox пилота `53.5,84.0,57.0,88.5` (Новокузнецк внутри). Join OSM(2081 в bbox)×Yandex(1684, `Кемеровская область - Кузбасс`): канон 1982 после дистанционного схлопывания (было 2081 до фикса границы геоклетки), 1346 Yandex-only → `review_queue{skeleton_unverified}`; summary `in == written + review` сошёлся. Фаза 0 на 3 seed-legacy: 1 matched (ручной geom sticky — цел, значения конкурентов совпали), 2 → `review_queue{legacy_unmatched}` с fingerprint. Resume `-run` — все чанки пропущены как свежие, канон стабилен. Coverage по срезу реестра-42 (148 стоп, 8 регионов, мягкий порог 0.4): 42 — 37/46 = 0.80; 22 — 0.73; 54 — 0.95; 04 — 0.48; gate при 0 — pass. Покрытие 04 низкое: маршрут 04.42.010 уходит в Горный Алтай вне bbox/OSM-экстракта СФО — расширять скелет, а не тянуть рейсы (§5.4).
- **Политика сторон дороги (решение):** флага направления на терминале нет и не будет — направление это свойство рейса (`direction_id`), а не места (GTFS-семантика). Остановки с разных сторон дороги — разные строки `stops_canonical` с одним `terminal_id` (parent_station-семантика); заселяются на attach Фаза 4. Дубли вида «Собор» 8/9 (61м, разные геоклетки) устранены заменой ключа `имя+геоклетка` на дистанционный кластер: одно нормализованное имя + single-linkage ≤ 300м (`collapseMaxM`, калибруется с ScorePair). Без координат — отдельная группа на имя (как раньше).
- **Мульти-тип `{bus,rail}` (аудит пилота):** 18 шт, порядок нормализован сортировкой. Разбор: узлы типа «Вокзал Томск I», «Белово» — настоящие интермодальные хабы (ж/д + автобусные остановки «Станция X» в радиусе 300м — корректный агрегат); кейс 2078 «Городская» (ДЖД) был багом классификации — `building=train_station` без `railway=*` считался bus, исправлено (`osmTransportType`), теперь `{rail}`. Потеря вторых кодов при схлопывании (PK без `code`) исправлена: PK `(terminal_id, system, code_type, code)` + `ON CONFLICT DO NOTHING`, «Собор» хранит оба `osm_id`. Остаток 18 — кандидаты авто-аудита `possible_merge` Фазы 5 (план §2).
- **Приоритет межгорода (решение):** городской/межгород отдельной метки пока нет; частичное покрытие — `object_type` (`station|stop|platform|airport`). Межгород Tagging — на attach Фаза 4 (`terminal_tags['intercity']` для терминалов, сматченных на стопы реестра); скелет строится полностью, фильтр — на чтении/attach, чтобы городские данные не потерять.
- **`configs/cities.yaml` удалён** (Go-конфиг `Cities`, env `CITIES_PATH/DATA_PATH`, мост в `main.go`, fallback в `extract-minstran.py` — всё вычищено; газетир Go — только встроенный `internal/geo/places.json` до переезда данных в БД `places`).
- **Справочники:** `regions` пуст (заполнение — геометрии границ + `density_class` перед рекалибровкой Фазы 4; кандидаты источников — OSM boundary relations, MOTIS areas); `places` строится из иерархии Yandex-дампа (страна→регион→поселение) на фазе дообогащения; `tz` — статическая карта регион→tz (регион целиком в одном tz), без геокодинга каждой точки.
 - **Фаза 4.3 (персист, выполнено):** `internal/sync/trips_promote.go` — `TripsStore` (узкий интерфейс: carrier/route/trip/stop/stop_times + `trip_sources` + `staging_trips` + tombstone + outbox + review) и `PersistAttachReport` (промоушен-транзакция = трип: route-NK → service-заглушка → trip-NK с `direction_id`/`duration_s`/winner-period → атомарный replace `stop_times` → `trip_sources` → удаление staging; несходимость `in == promoted+staged+dead` — fail-loud; tombstone `valid_to`, ресинк воскрешает тем же ID через `valid_to=NULL` в апсертах). Staging — upsert по NK `(source, external_route_code, external_trip_code)` с `retry_count+1`; `trip_sources.source` — базовый источник (winner-период — в `trips.period`, per-stop микс запрещён §3.13). Outbox: `PublishTerminalCreated` + `ConsumeTerminalCreated` (коалесценция по регионам, события вычищаются после обработки). `internal/sync/trips_jobs.go` — payload `sync_trips_attach` (job=route — батчинг, семантика per-trip), `EnqueueTripsAttachJobs`/`HandleTripsAttachJob`, чистый `CheckAttachBarrier` (скелет-барьер §4.2). `RouteRow`/`TripRow` выровнены под DDL (`direction_id/duration_s/distance_m/method/valid_to`, `routes.valid_to`); новые `StagingTripRow/TripSourceRow/OutboxEvent`. Реализации: `internal/store/postgres/trips.go` + `internal/store/memory/trips.go` (+tx-варианты); `EnsureStopForTerminal` (один `stops_canonical` на терминал — parent_station-семантика, стороны дороги — позже на attach). Джобы `sync_stations/sync_refresh/sync_terminals_chunk/sync_trips_attach` добавлены в `internal/jobs` (DDL CHECK их уже знал). Проверено на живой scratch-БД (миграция с 0 ошибок, promote/staging-retry/tombstone→resurrect/outbox/jobs — всё сошлось) + memory-тесты `trips_persist_test.go` (идемпотентный ресинк, tombstone/resurrect, outbox-автопромоушн, jobs-roundtrip, барьер). Попутно закрыты два латентных FK-дефекта, вскрытых живой проверкой: `UpsertRouteRegion` — stub-строка `regions` при пустом справочнике; `UpsertService` — `NULL` вместо `""` для дат (оба impl'а + tx).
 - **Фаза 4.4 (coverage-gate + калибровка + раннер, выполнено):** coverage переведён на ось attach (`internal/sync/coverage_attach.go`: `MeasureAttachCoverage` — ScorePair `stop_terminal` вместо старой skeleton-оси, `GatePassRegions`; общие конструкторы `PairItemFromTerminal/PairItemFromStop` с движком). Калибровка (`internal/sync/calibrate.go`): sweep порога 0.3–0.8 на чистых и зашумлённых (+555м) данных фикстуры — плато 16/16 до 0.7 включительно, обрыв на 0.8; `RecommendThreshold` фиксирует **0.7** как верх робастности, дефолт **0.6** подтверждён с шагом запаса (CI-тест `TestCalibrateAttachFixture`). Раннер `cmd/trips-sync` (конфиг → контракт → план → coverage → gate → attach → персист → `sync_runs{trips_attach}` + ops-лог): gate блокирует регионы (`skipped_routes`, starvation-warning), `--wait` — барьер с таймаутом (poll скелета, затем пропуск заблокированных — штатный staging-путь), `--force` — эскалация, `--regions` — фильтр, `--dry-run` — без записи, `--trust-nk` (дефолт true; bootstrap-сличение двух срезов — операторская процедура 4.0). Оркестрация — тестируемый `RunTripsSync` (`internal/sync/trips_run.go`): `prevCanon` из `ListCanonTrips` (новое в `TripsStore`: только живые канонические рейсы), сводка `by_route` + `% полных рейсов`. Tagging `terminal_tags['intercity']='1'` — в промоушен-транзакции 4.3. Gate-дефолт остаётся **0**: численный N фиксируется замером новой осью на пилоте (пилотный срез реестра и полная БД здесь недоступны). Проверено на живой scratch-БД: dry (4 promoted/2 staged/2 dead, full_rate 0.5, без записи) → real (канон 4, staging 2, tags 9, `sync_runs` + per-route ops-лог) → rerun (конвергенция, без dupes и алертов). Попутно закрыты два дефекта, вскрытые проверками: `SkeletonRowToAttachTerminals` маппил `(0,0)` в валидную гео-фичу (теперь nil — как «без координат»); tombstone-diff считал staged/dead «добавленными» и вечно триггерил churn-alert на ресинге (теперь diff только promoted vs канон, тест `currentNKs` выровнен); счётчик `allocID` memory-стора затирал явные ID (теперь догоняет `max+1`).
 - **Фаза 4.5 (накопление идентификаторов, выполнено):** петля замкнута — `FlatStop.OpCode` (`reestr.OpReg`) → `StopCodes`/`PairItemFromStop.Codes` → `MatchedStopTime.Codes` → write-back в `persistPromotedTrip` (внутри per-trip транзакции). Код-матч — сильнейший сигнал ScorePair (пробивает urban-guard без гео; golden-тест: тёзка за 79 км верифицируется по коду). Новое в `TripsStore`: `AttachTerminalIdentifier` (кросс-терминальная коллизия по `UNIQUE(system, code)` → владелец + `possible_merge`, рейс не падает) и `ListTerminalCodes` (stale-дивергенция: код терминала не встречен ни в одном матче прогона → `possible_merge`; легитимные мульти-коды одного прогона молчат; повторный ресинк тех же кодов молчит). Заморозка: alerted-отчёт персистить запрещено (`PersistAttachReport` фейлит на `rep.Alert` — перенумерация не отравляет мешок). Сводка: `codes_attached`/`code_conflicts`. Конвенция кода — `OpReg` (как legacy, входит в CHECK-`code_type`); `is_primary` всегда false, выборы primary — позже. GTFS: коннектор не эмитит идентификаторы и не входит в attach-поток — механизм source-agnostic, проводка GTFS/MOTIS-кодов отдельно.
 - **Пилот 4.6 (main-БД, выполняется):** раздвоение схлопнуто — `travelmcp_pilot` и старая `travelmcp` дропнуты, `travelmcp` пересоздана и мигрирована с 0 ошибок; скелет пересобран свежим кодом (1982/1346 — сошлось с пилотом). Срез `data/scratch/reestr42.json` (150 маршрутов/148 стоп/300 расписаний) провалил контракт в трёх местах — все три оказались живым форматом, контракт расширен: `reg` с суффиксом `/N` (9 шт), wrap-сервисы через год (14 шт, `wrapsYear`: старт ≥июль + конец ≤июль), времена `HH:MM (дни)` (1661 ячейка, 27%) + `Days` блоков + маркер `Нет отправления` (2 ячейки). Конвенция дней недели — **вс=0..сб=6** (верифицировано по `service_days`); сами `service_days` именам не соответствуют (напр. `ежедневно;пт,вс` → `{0,5}`) — attach их для решений не использует. Движок считает дни: пересечение block⊓cell по прогону, `FlatTrip.Weekdays` (nil = вся неделя), противоречивые прогоны дропаются со счётчиком, `TripRow.ServiceDays` (`0,5,6` или пусто), parity-блоки (`через день`, 54 шт) — со счётчиком, моделирование паритета отложено. Замер среза: 335 рейсов, 27 restricted, 15 dropped, 56 frequency-only. Координаты стопов: OSM-геокодинг экстрактора даёт ложняки до 471 км (без sanity) — не используется; привязка скриптом `scripts/match-reestr-yandex.py` (токены + бонус поселения + sanity-порог + OVERRIDES): 122 + 3 оверрайда, 23 unsure. Сырьё не тронуто, дериват — `data/scratch/reestr42.geo.json` (не коммитится). Итог attach на main: **0/391 promoted** — дамба держит честно: verified-решений 224/1531, сельские стопы без терминалов скелета (вне bbox 89, тёзки, развороты). Следующее: узкий пилот (маршруты полной верификации + `--routes`), решение по методу gate (verified-rate vs soft) открыто.
   - **Мультирегион-скейл скелета (выполнено):** `internal/skeleton/join_stream.go` — `JoinPager`: пагинированный join OSM×Yandex по гео-ячейкам 0.5° (страница = 100 OSM-записей; кандидаты — Yandex своей и ±1 соседних ячеек + бескординатные; pre-filter 2км без код-матча до дорогого `PairScore`; мемоизация nameSim). Эквивалентность `Join` закреплена тестом `TestJoinPagerEquivalence` (verified-пары/unverified множества совпадают; граница ячейки — `TestJoinPagerAcrossCellBoundary`). Замер на реальном экстракте РФ (`stations_rf.json`, 177 466 named): полный прогон 177K×177K — **~3 мин, 1775 страниц** (наивный `Join` = 31 млрд пар × 9µс ≈ 78 ч). Раннер `cmd/skeleton-sync`: `-region "Р1,Р2"` (список; `all`/`*` — без фильтра), bbox опционален (`SYNC_BBOX=none` — снять дефолт Кузбасса; пустой при заданных регионах — **auto-bbox из географии Yandex-регионов** +1° запас, чтобы OSM-экстракт РФ не попадал в канон целиком). Dry-run 3 региона (Кузбасс+Нск+Томск): OSM 16 679 в auto-bbox, Yandex 3331, канон 16 629/unverified 2398/ambiguous 50; coverage-gate по срезу reestr42 — полный матч 148/148 стоп (было 42-регион 37/46). `ChunkSkeleton` + промоутер: новый тип чанка `ambiguous:` (O-9 `DuplicateAmbiguous` → `review_queue{duplicate_ambiguous}` с fingerprint), O-10 — unverified с координатами → канон confidence 0.4.
   - **Фаза 6 (планировщик на Store — включено, 2026-09-08):** `find_route` строит сеть из канонической БД, а не из legacy JSON реестра. Точка переключения — `App.networkForDay` (mcp/mcp.go): при живом сторе allow-list = ID реестра **+ `mintrans`** (канонические трипы в БД всегда под этим провайдером, независимо от legacy-реестра); fallback на реестр остаётся для тестов (`PROVIDERS_ENABLED=synth`). `LoadNetwork` (postgres+memory, синхронно): фильтр `valid_to IS NULL` для routes/trips (43 tombstoned СФО-трипа не воскресают), чтение `trips.service_days` + day-фильтрация сети по запрошенному дню (форматы совпадают с sync: `0,1,5`, вс=0; пусто = без календаря, включаем всегда), загрузка `services/service_days/service_exceptions` в `net.Services/ServiceDays/ServiceExceptions` (GTFS-компилятор теперь получает calendar.txt из БД); `Trip.ID` = `routeNK|tripNK` (раньше `route:direction` — коллизия при `backward:4:0` и `backward:4:1`, трипы молча терялись в map). Починен найденный живым тестом дефект: `EnsureStopForTerminal` писал `stops_canonical.geom=NULL` при вызове без координат (trips_promote звал с 0,0) — теперь fallback на геометрию терминала + self-healing UPDATE существующих NULL-geom стопов; 57 существующих стопов забэкфиллены. Живая проверка (docker, реальная БД): Славгород→Белово — walk+bus+walk 08:00→05:02 (трип 22.42.029, service_days пн–чт: во вторник находится, в пятницу честно «нет расписаний»); Новосибирск-автовокзал→Белово — 08:00→14:00, 0 пересадок (54.42.154). Сеть: 57 стопов / 98 трипов (вторник) / 222 связи из stops_canonical+trips+stop_times. Integration+smoke зелёные, check-layers чистый. Legacy `intercity`-провайдер остаётся в реестре как fallback (депрекация — по плану §6 после паритета покрытия).
  - **Фаза 6 (GTFS hard-gate provenance, выполнено 2026-09-10).** Канон каналов §3.3 стал обязательным полем: `provenance.channel` (+history + триггер, CHECK по четырём значениям), `model.Provenance.Channel` + `ValidProvenanceChannel`; все писатели (postgres pool/tx, memory) fail-loud на пустом/неканоническом значении — дефолтов нет, иначе gate молча зеленеет. Проставлены все пути записи: скелет/legacy — `local_file` (OSM-экстракт, дамп, реестр — локальные файлы), ручные правки админки — `local_file`, external-call — `local_motis` для MOTIS-коннекторов / `local_file` для остальных, персист трипов 4.3 пишет provenance route+trip в промоушен-транзакции (слои рейсов исторически жили без provenance — теперь полная пара source/channel). Gate: `store.ProvenanceCompletenessChecker.CheckProvenanceCompleteness` — живые routes/trips без пары → список нарушителей (postgres одним UNION-запросом, memory-зеркало с тестами); `Compiler.writeFromStore` зовёт gate **до** сборки zip — неполный канон = ошибка 500, тихий фолбэк `gtfsCompile(net)` в `/api/v1/export` убран. Раннер `cmd/gtfs-validate` + `make gtfs-validate` (`GTFS_VALIDATOR_BIN` — путь к локальному MobilityData gtfs-validator): fixture-zip собирается через тот же gate-путь, затем валидатор; без бинарника — только gate. Тесты: `TestCompilerProvenanceGateBlocks/Pass`, `TestSaveProvenanceRequiresChannel`, `TestCheckProvenanceCompleteness`, `TestListProvenanceChannels`. Миграция на live-БД — при следующем пересборе (столбец NOT NULL: старые строки пересобираемой схемы несовместимы — dev-БД переезжает DROP+миграция по правилу §7 до прод-деплоя).
  - **Фаза 6 (`plan_id` → logic version, выполнено 2026-09-10).** `internal/sync/logicversion.go`: `LogicVersion="2"` + `LogicVersionID()` — единый вход всех раннеров (`skeleton-sync`, `trips-sync`, `gtfs-import`) вместо разрозненных `appVersion`-констант ("skeleton-sync/1" и т.п.) и sha(binary): деплой без изменения логики не инвалидирует чанки, бамп версии — предсказуемый full-resync. Правило бампа — при семантических изменениях обработчика (стадии, формат записи, ключи матчинга), зафиксировано в комментарии константы + §4.3 плана; тест `TestPlanIDLogicVersion` (стабильность/бамп/конфиг).
  - **§10 O-5/O-6 (Overpass route-relations, выполнено 2026-09-10).** `Adapter.FetchRouteRelations` (QL relation route bus/trolleybus + рекурсия `>>`; execQL main→mirror) + мост `FetchRoutes` → `geocoder.CachedRoutesProvider` (`cached_routes.go`, по лекалу O-4: ключи `overpass:route:<ref>[:bbox]`, квота `api_quotas{osm}`, общий пейсер ≥1.2с — нового пути вызова нет, §3.7). Тесты: фикстура route.json (порядок 5 членов сохранён, identity ref+operator), cache-hit без квоты, вежливый отказ при исчерпании. `scripts/overpass-collect.sh` (O-6, по лекалу yandex-collect): пейсер 1.2с, fallback main→openstreetmap.fr (живой: route_101 ушёл на зеркало 157КБ), resume по слагам, дефолтные точки — реальные автовокзалы OSM (Кемерово 55.341592,86.061353; Красноярск МКАВ; Барнаул; Томск). Живая пачка: №101 Поросино↔Ленина (Томск, 161 member, двусторонний relation) + Асино; сводный дамп 1349 elements; сырьё в gitignored `data/overpass/`. Офлайн-скретч-тест на живую пачку — skip в CI без сырья.
   - **§10 C-2/C-3 (two-tier на склейке, выполнено 2026-09-10).** C-2 `internal/sync/trips_glued_test.go`: hard-валидаторы доказаны на форме «порядок стопов из Overpass × времена из Яндекса» — (a) монотонность по relation-порядку ловит убывание → dead; (b) overspeed на финализированной геометрии склейки → dead; (c) тот же перегон при identity_only конце → soft needs_review (guard §5.1); (d) интерполированный конец → review, не dead; полный `AttachTrips`-проход сохраняет Overpass-порядок в seq. C-3 живой прогон на scratch-БД (DROP+миграция 0 ошибок с `provenance.channel`): скелет Кузбасса 5755/26 → attach: run1 179 promoted (full_rate 0.46; provenance route+trip с channel; gate-запрос 0 нарушителей) → run2 167+12 tombstoned (персист run1 добавил terminal_identifiers → код-кандидаты изменили exclusivity-исходы — единичная эволюция после накопления кодов 4.5, честный дифф) → run3=run4 167/224/0/0, dupes 0, churn молчит — конвергенция. Scratch-БД дропнута.
  - **Реестр как плагин: standalone-коннектор + идентификаторы как данные (выполнено 2026-09-10, ревизия по внешнему ревью).** Специфика Минтранса вынесена из приложения в `tools/registry-parser` — отдельный Go-модуль (stdlib-only, по лекалу `tools/osm-extract`): контракт среза реестра, кириллические дни недели/сезонные блоки, bootstrap NK-стабильности (`--bootstrap`/`--churn`), synthetic-ключи и `--trust-nk` — всё в коннекторе; выход — универсальный `flat_trips.json` (`{meta:{source,snapshot},stats,trips[]}`), `meta.source = gov-registry`. Конвейер про источник не знает: `cmd/trips-sync` читает flat-формат (`sync.flat_trips_path`, env `SYNC_FLAT_TRIPS_PATH`), `RouteNK` приходит готовым из коннектора, коды стопов — `FlatStop.Codes []AdaptedIdentifier` (вместо `OpCode` — устранена source-зависимая ветка `StopCodes`), source — fail-loud во всех 9 точках (дефолтов `"mintrans"` больше нет), `model.FlatPayload/FlattenStats/FormatWeekdays` — контракт в модели. `internal/adapters/mintrans` удалён; planner-тесты — на flat-фикстуре `testdata/flat_trips.json` (генерирована парсером, golden-эквивалент test.json). БД: провайдер `('gov-registry','Реестр Минтранса РФ')` (замена `'mintrans'`), новая `identifier_schemes` (code PK, system FK→providers, display_name, priority, is_resolvable, is_merge_key; 13 сидов), `terminal_identifiers`/`carrier_identifiers` — CHECK на system/code_type заменён FK (providers/identifier_schemes), мёртвый `sync_mintrans` вырезан из jobs-CHECK; primary-ранг терминала — из `identifier_schemes.priority` (SQL, не хардкод `identifierSystemRank`); Dev-БД пересоздана DROP+миграция (0 ошибок, правило §7 — до прод-деплоя). Справочник редактируем из админки: `GET/PUT /api/v1/admin/identifier-schemes`, `DELETE .../{code}` (FK-защита: используемый тип не удаляется), systems-список карточки терминала — из schemes, не хардкод. `LogicVersion="3"` (семантика входа trips-sync изменилась). Ручка `POST /api/v1/import/mintrans` удалена (404). Backlog: trust-матрица источников как данные (`provider_trust_pairs`), интерактивный сбор по кнопке (`sync_collect_region`).
  - **Пилот СФО «терминалы→рейсы» с нуля (выполнено 2026-09-08):** БД пересоздана (миграция 0 ошибок). Скелет: 8 регионов СФО через мультирегион-раннер (`-region` список, `SYNC_BBOX=none` → auto-bbox 43–74°/39–112° из географии Яндекса), полный экстракт РФ `stations_rf.json`: OSM 51 245 в bbox × Yandex 6328 → канон 51 131 + unverified 4532 + ambiguous 114; 560 чанков промоутнуто; итог в БД **55 751 терминал** (1682 enriched, O-10: Yandex-only с координатами в каноне), review всего 126 (было 1681 на старом коде). Coverage по reestr42: **148/148 стопов, все 8 регионов 100%**, gate pass. Трипы: `trips-sync` на `reestr42.geo.json` — **154 promoted** первым прогоном (full_rate 0.39), реран после полного скелета — 111 живых + 43 tombstoned (честнее: на полном скелете exclusivity режет тёзки, run3 частично матчился на неполном наборе терминалов 5400/55751); 68 маршрутов, 627 stop_times (453 provisional — identity_only-терминалы, D-3; все с `match_score`), 237 staged (56 дыры времён + 17 skeleton_gap + 207 неполные — добор покрытия), 57 intercity-тегов, 0 dead. Попутно закрыто 3 дефекта: `terminal_identifiers` UNIQUE(system,code)-коллизия между чанками → `ON CONFLICT DO NOTHING` (код остаётся у владельца, прогон не падает); staging 57K row-INSERT → `pgx.Batch` по 1000 (53 чанка/15мин → 560 чанков/90с); несходимость итога `In` в раннере (не учтен DuplicateAmbiguous). Churn-алерт на идентичном ресинке — диагностирован: prevCanon vs current на изменившемся скелете (5400→55751 терминалов меняет исходы матчинга), не баг диффа; порог для рерана — `SYNC_TRIPS_CHURN_THRESHOLD=0.5`, канон стабилен (111+43 tombstoned, повторный прогон — конвергенция).
  - **D-блок: entity-resolution пробелы attach (выполнено, по внешнему ревью).** Внешнее ревью сопоставило attach-пайплайн с эталонным record-linkage: архитектура подтверждена, вскрыты 4 структурных пробела + 1 дубль. Все закрыты: **D-1** `matchIndex` (`trips_match_index.go`) — blocking пул = гео-окно ∪ код-совпадения ∪ no-coords (код-матч проходит окно — golden «тёзка 79км» держит), hoist per-run; пилот 3.1М пар, РФ был бы 7.2 млрд — теперь O(T×S×K). **D-2** greedy exclusivity per-trip (`used` по terminalID; кольцевой возврат на первом терминале легитимен, 2-позиционный дубль — нет; тесты exclusivity+circular). **D-3** backbone-промоушен: verified-концы + дырки в середине (≥2/3 verified) → промоут без непроверенных стопов вместо staging целиком; валидаторы на эффективных соседях (склеенный перегон честно считается, overspeed ловится — `TestBackboneGapSpeedOnEffectiveNeighbors`); счётчики `mid_gaps`/`gapped_promoted`. **D-4** `stop_times.match_score/match_method` — трассируемость решает attach и персистит (round-trip тест; вход для §8 FP-аудита; миграция 0 ошибок). **D-5** единая `scorePairBody` для `PairScore`/`pairScoreCached` (две копии формулы устранены; `Join()` жив — потребитель `legacy_match`, 1×N без blocking). Причина пробелов задокументирована: порог боли не был перейдён на пилоте (28с на 3.1М пар), при цели РФ ×2400. D-6 (sequence-context tie-break, McRAPTOR) — backlog.
   - **Разрез дуализма + чистка (выполнено):** legacy-писатель вырезан из сервера — стартовый `ImportIntercity*` и тело воркера `sync_mintrans` удалены (`cmd/mcp-server/main.go`), `POST /api/v1/import/mintrans` отвечает 409, ручная джоба падает с объяснением; геокодер-инит из main удалён (потребителей не осталось), чтение реестра через `providers.Registry` (fallback `find_route`) — живо. `verify.go` + тест удалены — в проде один скоринг. Политика ревью: ревью только на сомнения (`duplicate_ambiguous`, `low_confidence`, `needs_review`, конфликты кодов) — `skeleton_gap` (нет верифицированного терминала) и дыры времён идут в staging без ревью; счётчик `skeleton_gap` в сводке персиста. Primary-правило `manual > yandex > osm > mintrans` — единый primary на терминал (`applyIdentifierPrimaryRule`, 4 точки записи postgres: `UpsertTerminal` pool/tx + `AttachTerminalIdentifier` pool/tx; memory — без primary); проверено на scratch-копии БД (yandex побеждает osm/mintrans, osm-attach не сносит, коллизия возвращает владельца). Nominatim-reverse подключён к промоушену скелета (до чанкинга и транзакций): только записи без адреса и с координатами, квота `api_quotas/nominatim` (лимит `geocoder.max_calls`, дефолт 500, атомарный `TryConsumeQuota`), таймаут 10с на вызов. Живой прогон `trips-sync` run_id=5: 0/391, ревью прогона 237 (все `low_confidence`), `skeleton_gap` 11; в очереди лежат 98 stale `incomplete_trip` от старых прогонов — новые прогоны их не создают, решение (закрыть/оставить) открыто.
  - **Рантайм-георезолв городов из канона (выполнено 2026-09-09).** Диагностика вскрыла, что `from_place/to_place` замыкались на 102 статические записи `places.json` (координаты = автовокзалы старого реестра), Postgres Gazetteer не видел вовсе: 67% городов с живыми минтранс-рейсами (27 из 40: Новокузнецк — 16 трипов! — Прокопьевск, Белово, Шерегеш, Шира…) не резолвились, а края walk-radius 2.5 км срезали легитимные окраинные автовокзалы (Бийск — 2.97 км). Фиксы: (a) `Gazetteer.LoadSettlements` (internal/geo/settlements.go) наполняет справочник из канона через узкий интерфейс `SettlementSource` (реализация — `PostgresStore.ListSettlements`: представитель поселения = терминал с живыми stop_times → bus_station → station; ~630 записей, ~2.9K мусорных тегов отфильтровано `isPlausibleSettlement`); запись канона ЗАМЕНЯЕТ одноимённую статическую (upsert — БД источник истины); (b) substring-фолбэк Gazetteer получил границы слова (`wordBoundContains` — «Омск» больше не матчит «Томск» на уровне Gazetteer, зеркально planner'у) + `normQuery` нормализует ё→е; (c) `ResolveDetailed` логирует источник решения (exact/token/substring + matched-имя) в `resolvePoint`; (d) конфигурируемый дефолт `planner.max_walk_minutes` (`PLANNER_MAX_WALK_MINUTES`, 1..180, дефолт 30) через `Planner.WithDefaultMaxWalk`; (e) диагностика провалов: findStopByPlace при промахе логирует ближайший name-matched стоп сети с дистанцией и hint'ом, ошибка «нет остановок, достижимых пешком» дополнена ближайшей остановкой и нужным лимитом минут. Живая проверка: Новокузнецк/Бийск/Красноярск/Шерегеш/Прокопьевск/Шира/Томск/Юрга — строятся; «Барнаул→Кемерово нет маршрута» — честный ответ (в срезе реестра нет сквозного трипа 76↔65, ни одного общего). Попутно trace_id дотянут до Loki-строк: `loggingMiddleware`/auth/mcp пишут через `*Context`-варианты slog, `resolvePointWithPlace`/`networkForDay` принимают ctx (LoadNetwork больше не `context.Background()` — отмена запроса уважается), мёртвый `App.resolvePoint` удалён.
  - **Фаза 5 (freshness-sweep, выполнено 2026-09-09).** `RunHygieneSweep` (internal/sync/attribute_hygiene.go) — один прогон ночного sweep в `jobs{cleanup}` (payload `attribute_retention_days` перекрывает дефолт 90): (a) **finalize/GC attribute_state** — конкуренты без actor_id не seed старше retention → append в provenance_history + DELETE (несходимость history/DELETE — fail-loud), seed вытесненный live-конкурентом — GC; ручные правки не трогаются (барьер поля §3.1); на текущих данных 0 (моложе retention), механизм готов. (b) **stale-счётчики 30д** — `StaleAttributesCounters` → KPI `stale_attributes_30d` в `/api/v1/dashboard` (+ `staging_by_state` после expiry §5.3). (c) **аудит possible_merge** — `AuditPossibleMerges` (300м, пересечение transport_types, лимит 500 ближайших; живой замер: 22.6K пар в СФО, sweep берёт топ по дистанции) → `review_queue{possible_merge}` fingerprint=merge:a:b (идемпотентно, count-бамп). (d) **recompute transport_types** — правило **union** (stored ∪ fact): факт стопов/режимов ДОБАВЛЯЕТ типы, скелетные rail/flight не срезаются временным отсутствием рейсов (замер дрейфа: 2289 терминалов, включая flight→flight+bus аэропортов и bus+rail хабов); пустой факт — no-op. (e) **FP-аудит** — `SampleMarginalMatches(match_score ∈ [lo,hi))` (acceptance sampling §8): оператор проверяет N сэмплов, FP-рейт в KPI; полоса по дефолту [threshold, threshold+margin). (f) **golden_terminals.json** — 6 identity/merge-кейсов оси stop_terminal в CI (`TestGoldenTerminalsScorepair`, precision/recall 1.0), калибровка Кузбасса не пересекается. Типы sweep-контракта — `store.AttributeSweepResult/MergeAuditCandidate/TransportTypesRecomputeResult/MarginalMatchSample` (row-модели в store, реализация postgres, интерфейс потребителя в sync — SOLID). Cleanup-джоб последовательный: staging-expiry §5.3 затем sweep, сбой одного не роняет другой (частичность в логах).
  - **Фаза 5 (гигиена review/staging §5.3, выполнено 2026-09-09).** Три механизма закрывают структурный долг «код создаёт review/staging, но не закрывает»: (a) **автозакрытие review при промоушене трипа** — `ResolveTripReviewsByFingerprint` (postgres pool+tx, memory) вызывается в `persistPromotedTrip` рядом с `DeleteStagingTrip` по fingerprint NK (`source:routeNK|tripNK`); причина low_confidence/incomplete_trip снята фактом промоушена; счётчик `reviews_resolved` в `PersistSummary`; живая БД: 39 открытых low_confidence-записей соответствуют уже-promoted трипам и закроются первым ресинком (ROLLBACK-проверка: 39/39). (b) **upsert review бампает `observed_at`/`count`** (postgres pool+tx, memory-зеркало `Count++`) — пере-детекция = живое наблюдение, expiry видит актуальный возраст, а не время первой вставки. (c) **expiry job §5.3** — `internal/sync/staging_expiry.go`: `ExpireStaleStagingTrips` (незавершённые `incomplete_trip/awaiting_times/needs_review/skeleton_gap` старше порога → `state='expired'`, dead-letter: данные остаются для операторского разбора, идемпотентно), `HandleCleanupJob` → `jobs{type=cleanup}` (зарегистрирован в `newJobsWorker`; payload `staging_expiry_days` перекрывает конфиг `sync.staging_expiry_days` / env `SYNC_STAGING_EXPIRY_DAYS`, дефолт 14 дней); Store-методы `ExpireStagingTripsOlderThan`/`CountStagingByState` (KPI §8 — счётчики по состояниям). Живая БД (порог 1 день, ROLLBACK-демо): 189 staging-строк уйдут в expired при первом cleanup-прогоне (87 untimed-«awaiting_times» — вечные без источника времён §3.13, теперь явно dead-letter вместо «висят непонятно зачем»). Тесты: `staging_expiry_test.go` (resolve идемпотентен, count-бамп, expiry свежие/старые/повтор — 4 кейса). Оставшееся Фазе 5: finalize/GC `attribute_state` (retention 90д), `v_stale_attributes`-дашборд, аудит possible_merge (18 мульти-тип хабов), ночной recompute transport_types, golden_terminals.json, FP-аудит по match_score (D-4).
- **Фаза 4.2 (attach-движок, выполнено, без записи в БД):** `internal/adapters/mintrans/trips.go` — `ParseDataset` (контракт 4.0) + `FlattenTrips` (победитель времён winter→summer, развёртка runs с day-offset, frequency-only и untimed-стопы без фабрикации); `internal/sync/trips_attach.go` — `AttachTrips`: матчинг стопов осью ScorePair `stop_terminal` (только verified идёт дальше), схлопывание соседних дублей терминала, two-tier валидаторы (hard: монотонность, arr≤dep, скорость выше предела при финализированной геометрии обоих концов → `dead` с loud-причиной; soft: та же скорость на неподтверждённой геометрии → staging `needs_review`), frequency-only и дыры времён → staging (`awaiting_times`/`incomplete_trip`, без review для ожидания), winner времён `mintrans:winter|summer` на весь трип, tombstone-дифф против прошлого канона + churn-alert по `sync.trips_churn_threshold` (первая загрузка не алертит), баланс `in == promoted+staged+dead` fail-loud, review-записи на трип с fingerprint из внешних NK. Прогон на `testdata/test.json`: 4 promoted, 2 staged (дыры 54.22.049), 2 dead (overspeed 266/304 км/ч — кривая геопривязка Безменово в фикстуре, дамба ловит реальный дефект данных). Golden `sequence-monotonic`/`interpolated-speed-soft` теперь исполняются тестом валидаторов, legacy-кейс `stop-matches-terminal` переведён в `matcher=scorepair` (9/9 в CI).
- **Фаза 4.1 (ScorePair ось `stop_terminal`, выполнено):** `internal/verification/scorepair.go` — единый скоринг по §3.8: `PairItem` (имя/гео/транспорт/settlement/коды/источник), веса-приоры `0.45/0.3/0.15/0.1` с ренормализацией при null-фичах, код-матч — сильнейший сигнал, `trust[pair]` (один голос 0.6, OSM/MOTIS — один голос, seed 0.4, legacy 0.7, manual 1.0), hard-guard (гео-пара в пороге ИЛИ код ИЛИ имя+settlement вне urban с margin и trust 1.0; urban geom-null — никогда verified; оба-из-Минтранса без гео — никогда verified), решения verified/low_confidence/duplicate_ambiguous/rejected через `MatchStopToTerminal` (top1−top2 против ambiguity). Пороги/margin — `verification.*` + per-class override `density_thresholds`, калибровка весов/trust на Кузбассе — перед массовым attach (gate Фазы 4). Golden: `testdata/golden_trips.json` — 8 `matcher=scorepair` кейсов, CI-тест `TestGoldenTripsScorepair` требует accuracy/precision/recall 1.0; старые 3 кейса (sequence/validate) — пропуск до 4.2.
- **Фаза 4.0 (bootstrap NK + контракт сырого входа, выполнено):** `internal/adapters/mintrans/contract.go` — `ValidateDatasetContract` (fail-loud на дрейф формата: обязательные поля, `source='minstran_reestr'`, `snapshot` YYYY-MM-DD, `reg` NN.NN.NNN + уникальность, стопы `region` NN + диапазон координат, расписания `forward|backward` + ссылки на routes/stops/services + форматы `HH:MM`/dwell, `service_days` 0..6); `bootstrap.go` — `CompareDatasets` (сигнатура маршрута = carrier + стопы forward/backward, `churn=(added+removed+changed)/max`, `disappearance=removed/old`, `TrustNK` при обоих ≤ порога, иначе `Alert` + рекомендация synthetic-ключа `synthetic:region:number:from:to:carrier`), sha обоих срезов в отчёте. Порог — `sync.trips_churn_threshold` / `SYNC_TRIPS_CHURN_THRESHOLD`, старт 0.2. Тесты — `contract_bootstrap_test.go` на `testdata/test.json` (13 кейсов дрейфа, stable/churn/carrier/synthetic).
- **Фикс миграции:** `users`/`api_keys` перенесены в `001_initial.sql` выше `provenance` — иначе на чистой БД цепочка `provenance`/`provenance_history` не создавалась (FK на несуществующих `users`, тихий ERROR без `ON_ERROR_STOP`). Пилотная БД `travelmcp_pilot` (docker, `localhost:5442`) пересоздана и мигрируется с 0 ошибок; `travelmcp` (основная dev-БД) осталась со старой схемой без `provenance` — перед следующим использованием пересоздать.
 - **Дерево (новое):** `cmd/skeleton-sync/` — раннер пилота (конфиг → план → staging → Nominatim-reverse адресов → чанки → Фаза 0 → coverage; флаг `--no-reverse` для офлайна); `internal/sync/legacy_match.go` (Фаза 0: `Join` как `legacy_match`, конкурс `ElectField`, review), `internal/sync/coverage.go` (чистый `MeasureCoverage` + `GatePass`), `internal/store/postgres/skeleton.go` (staging/chunks/legacy-чтения), `internal/store/postgres/identifiers.go` (primary-правило кодов), `tools/osm-extract` — фильтр расширен на ж/д станции.
- **Имена/алиасы (решение):** канон — только `terminal_names` (ru/en, одно значение на язык) + `terminal_aliases` (варианты с источником; пока DDL без писателей). Убраны дубли: `AdaptedRecord.NormalizedRu` (выводится `namesim.Normalize` на лету), `terminals.osm_compatible_name` + триггер `trg_sync_osm_name` + индекс (ничего не читало; при нужде — `lower(unaccent())` в запросе), `skeleton.Terminal` (заменён `AdaptedRecord`). Оставлено: `NameRu/NameEn` (транспорт имён источника), `identifiers.is_primary` (живой — читается во `v_review_stops`). Запись алиасов — в промоушене Postgres-части Фазы 3.
- **Фикстура `testdata/test.json` (закоммичена):** нарезана из живого реестра (`scripts/extract-minstran.py --regions 22,54,70 --snapshot 2026-05-10` + `scripts/make-test-fixture.py`): 4 маршрута (`54.22.078`, `54.70.040`, `54.22.049`, `54.22.030`) × forward/backward = 8 рейсов, 16 остановок. Координаты — из офлайн-дампа Яндекса (автоматч по токенам + 5 ручных привязок в `OVERRIDES`: Речной вокзал НСК, Томск-АВ, Крутиха, Камень-на-Оби). Ожидания: `54.22.078` forward 12:30→17:00 / backward 19:59→… (7 стопов), НСК→Томск через `54.70.040` (10:00→15:00), старт journey с автовокзала `op:54:54099`. `make test` полностью зелёный.
- Ревю схемы (все 11 пунктов закрыты в `001_initial.sql` без `ALTER`): NK `external_route_code`/`external_trip_code` — `NOT NULL`, дубль `routes.external_code` удалён, `source/provider` новых таблиц — `REFERENCES providers(code)` (+seed `manual`), `geocode_cache.ttl_class` (`verified`/`disputed`), `terminal_merges.old_id` — FK (старые строки тумстоунятся, не удаляются), `esr_code` в `code_type`, `method` — CHECK по закрытому списку, `api_quotas.quota_limit` (не `limit`), `services.id` — `bigserial` (+`bigint` FK), `trips.valid_to` — `timestamptz` с комментарием, `staging_trips.is_synthetic_key`. Go-код синхронизирован (апсерты по NK, synthetic-код трипа в импортёре, идемпотентность memory-store + тесты).

### 5.1 synth (мок-сеть, только тесты/демо)

- Кластеры: A «Пермь» (`a-cen`, `a-bus`, `a-air`), B «Екатеринбург»
  (`b-bus`, `b-mkt`, `b-apt`), C «ПГУ» (`c1`, `c2`, `c2x`, `c3`), аэропорт
  `a-apt`.
- Виды транспорта: `walk`, `bus`, `tram`, `rail`, `flight`. Регулярки:
  автобус «a» (10 мин), межгород «900» (60 мин), трамвай «b» (6 мин),
  шаттлы «s1»/«s2» (30 мин), перелёт «fly» (120 мин).
- Пешие стыковки: `b-bus ↔ b-mkt` (6 мин), `c2 ↔ c2x` (5 мин).
- Стабильные ожидания тестов: Пермь→Екб наземкой (06:00) — прибытие
  `09:36`, 5 legов, 2 пересадки; перелёт аэропорт→аэропорт (07:00) —
  прибытие `09:05`, 0 пересадок; кластер C→B — ошибка «маршрут не найден».
- Порог пешей доступности по умолчанию — 30 мин (5 км/ч).

### 5.2 intercity — вырезан (Фаза 6, 2026-09-10)

- **Legacy JSON-провайдер `internal/providers/intercity` вырезан после паритета**
  (`scripts/parity-check.py`: пилот-регион 42 — routes 96.7% / timed 99.2%,
  порог 95%; срез реестра `scripts/extract-minstran.py` vs канон Postgres:
  маршрутом считается трасса со стопом в регионе пилота, `staging_trips` —
  честная промежуточная станция конвейера). Сеть строит Store из канона
  (`LoadNetwork`, минтранс-трипы конвейера trips-sync); live-проверка:
  Кемерово → Новосибирск строится по канон-терминалу «Кемерово, автовокзал».
- Вместе с провайдером вырезаны: `Registry.WithDedupKm`/`intercityPath`,
  `providers.intercity`-секция конфига, env `INTERCITY_REESTR_PATH`,
  `mintrans/importer.go` (JSON-типы контракта → `contract.go`, helpers
  `pickPeriod/runsCount/blockOf/cellAt` → `trips.go`), `internal/import/pipeline.go`.
- Planner-тесты, использовавшие провайдер как network-builder, перешли на
  локальный билдер из flat-фикстуры `testdata/flat_trips.json`
  (`reestrTestNetwork`, модель `model.FlatPayload`).
- Датасет реестра (`data/reestr/regions.json`, не коммитится) больше не вход
  `cmd/trips-sync`: конвертацией в `flat_trips.json` занимается standalone-
  коннектор `tools/registry-parser` (`sync.flat_trips_path`); sync-конвейер
  про источник не знает ничего, кроме `meta.source`.
- **Канон transport-типов расширен** (§3.1 плана): `subway` (единообразно
  с OSM `station=subway` и GTFS `route_type=1`; исправлено схлопывание
  metro→rail в OSM/GTFS-адаптерах) + полноценные `taxi, car, bicycle,
  scooter` (модель `model.Mode`, справочник `transport_modes`, скорости:
  car/taxi 130, bicycle/scooter 25 км/ч; MCP `transit_modes` принимает
  SUBWAY,TAXI,CAR,BICYCLE,SCOOTER). Правило расхождения «GTFS считает метро
  rail»: OSM-скелет — канонический гео-факт, route_type фида — claim
  маршрута; recompute `transport_types` объединяет (union), subway-терминал
  с rail-маршрутом честно носит `rail+subway`.

### 5.3 Ручной режим сбора «Яндекс-первыми» (выполнено 2026-09-11)

Пилот одного источника: дамп Яндекс-станций → unverified-скелет →
cache-first сбор расписаний → attach по code-match → канон + live-пробы
`find_route`. Валидация и одобрение терминалов — руками оператора в UI
(CRM-карточка), автопереверификация залоченное не трогает (инвариант §5).

- **Режимы конвейера разделены:** ручной (single-source, терминалы
  score 0.4 IdentityOnly, не залочены; оператор валидирует через
  `POST /api/v1/admin/external-call` и одобряет `PUT .../terminals/{id}`) и
  автоматический (skeleton-sync join + coverage-gate, без изменений).
- **`cmd/skeleton-sync`:** флаги `-no-osm` (Yandex-only скелет, все записи —
  `Unverified` через `FlushUnverified`, путь уже существовал; юнит
  `TestJoinPagerEmptyOSM`), `-transport`, `-station-type` (дефолт —
  терминальные классы `bus_station,station,train_station,airport`; без
  городских `bus_stop/stop`). Dry-run без flat-файла печатает сводку join.
- **Фикс `OverpassEnrich`:** апгрейд unverified-записи через
  `StationsAround` больше не теряет идентификаторы исходника
  (`yandex_code`) — мердж `rec.Identifiers ∪ osm.Identifiers` + перенос
  settlement/region (`internal/skeleton/overpass_enrich.go`, тест
  `TestOverpassEnrichKeepsIdentifiers`); без кода терминал терял точный
  code-match на attach.
- **Адаптер `internal/adapters/yandex/rasp.go`:** клиент `/schedule`
  (догон пагинации: существующий кэш-файл = первая страница, дополнительные
  сохраняются `schedule_<code>_<date>_<offset>.json` и мержатся) и
  `/thread`; cache-first (кэш = валидные данные), miss → API строго через
  `TryConsumeQuota("yandex", 500)` (`QuotaFunc` инжектится, адаптер не
  зависит от store), ответ сохраняется в кэш. Конвертер `rasp_flat.go`:
  thread → `FlatTrip` (RouteNK — синтетический из title+перевозчик, номера
  у ниток пустые; дни «ежедневно/кроме/список» — парсер дней недели,
  календарные даты и чётность → `restricted_days` со счётчиком в stats,
  решение: дропать честно; времена — минуты от первого отправления,
  переход через полночь +1440; стоп-коды → `FlatStop.Codes
  {yandex, yandex_code}`).
- **Фикс `matchStops` (GTFS-конвенция):** стоп с единственным временем
  (конечный — только arrival, первый — только departure) получает парное
  время `arr=dep`; раньше конечный стоп с одним arrival ронял hard-валидатор
  монотонности (`прибытие после отправления`) — 179 ложных dead на пилоте
  Кузбасса, после фикса — 4.
- **Churn-эскалация:** `AttachInput.AllowChurnGrowth` (передаётся из
  `Force`): подавляет churn-алерт от чистого роста канона
  (added>>removed, disappearance=0) — первый прогон после частичного
  наследия давал churn 0.917 при 0 исчезновений; disappearance-алерт
  остаётся hard и эскалацией не гасится (`TestAttachChurnGrowthSuppressedByEscalation`).
- **Job `sync_collect_region`** (`cmd/mcp-server/collect_job.go`, в CHECK
  `jobs.type` 001 — БД dev пересоздана DROP+миграцией по §6): payload
  `{kind: skeleton|trips, regions/region, date, transports,
  station_types, offline, force, tag}`. skeleton — `RunCollectSkeleton`
  (фильтры дампа → чанковый промоут unverified), trips — `runTrips`
  (терминальные станции региона → `Schedule` → уникальные uid → `Thread` →
  `FlattenRaspThread` → `RunCollectTrips` = полный attach-конвейер).
- **API:** `POST /api/v1/collect/skeleton|trips` → job,
  `GET /api/v1/collect/regions` (регион-лист дампа с терминальными
  станциями), `GET /api/v1/sync/runs[?limit]` и `/{id}` — сводки качества
  (`store.ListSyncRuns`, postgres+memory). Конфиг: `yandex.rasp_cache_dir`
  (env `YANDEX_RASP_CACHE_DIR`, дефолт `data/yandex/cache`).
- **UI (админка):** вкладка «Сбор» (регион-селектор, дата, чекбоксы
  транспорта, offline-флаг, две кнопки → jobs), вкладка «Прогоны»
  (sync_runs + развёрнутая сводка: coverage, promoted/staged/dead, квоты),
  CRM-карточка терминала: панель валидации через внешний API
  (overpass/nominatim/yandex, кандидаты с similarity → «в поля правки»),
  полный доступ ко всем атрибутам (имена/алиасы, идентификаторы, теги,
  ревью, liveness, расписание).
- **`networkForDay`:** allow-list провайдеров канона расширен
  `yandex` — рейсы, прикреплённые источником `yandex`, попадают в сеть
  планировщика (до этого фильтровались на LOAD, сеть собиралась с trips=0).
- **Пилот Кузбасс (offline, кэш 2026-09-04):** скелет СФО 9 регионов —
  195 терминальных станций → 192 канон + 3 review (0 квот); расширенный
  скелет Кузбасса с bus_stop — 1341 терминал; рейсы — 264 flat-трипа
  (276 ниток, 12 restricted) → **147 promoted / 113 staged / 4 dead,
  full_rate 0.557, coverage 621/632 = 98.3%, gate_pass=true**;
  конвергенция повторного прогона — идентичный persist, tombstoned=0.
  Live-проба: Кемерово → Томск по канону `find_route` находит
  автобусный маршрут (белово—томск, 13→106, 0 пересадок).

### 5.4 Правка терминалов, merge/delete, дедуп скелета (2026-09-11)

- **Фикс pgx-encode `*int64`→timestamptz:** `TerminalRow.LastVerifiedAt`
  (unix-сек) передавался напрямую в колонку timestamptz в 4 INSERT-путях
  `UpsertTerminal` (pool+tx) — pgx не имеет encode-плана int64→timestamptz;
  до UI-правки поле всегда было nil и кодирование не требовалось. Хелпер
  `unixTsOrNull` (skeleton.go) → `time.Unix(...)`; `ApproveTerminal`
  перешёл с `to_timestamp($4)` на тот же хелпер (голый int64 не кодируется
  и во float8). Conflict-ветка INSERT'а более не теряет операторские
  `last_verified_at`/`is_locked` (COALESCE + OR) и не затирает geom нулями
  при правке без координат (guard ST_X/ST_Y=0 → сохранить прежний).
- **Дедуп промоута скелета по внешнему коду:** повторный прогон
  `PromoteSkeletonChunk` промоутит существующий терминал (резолв
  `ListTerminalIDByCode` по любому идентификатору записи → идемпотентный
  UPDATE), а не создаёт дубликат. Баг пилота: дубль «Томск, автовокзал»
  (180/1561 — идентичные имя+координаты, код UNIQUE остался за старым,
  новый терминал остался без идентификатора). `SkeletonStore` расширен
  `ListTerminalIDByCode`. Тест `TestPromoteSkeletonChunkDedupByCode`.
- **MergeTerminals (план §3.1, internal/store/postgres/merge.go):**
  транзакция — flatten карты redirect (`terminal_merges`), удаление
  конфликтующих по NK строк old в пользу new (identifiers/aliases/names/
  attribute_state/tags/review), репойнт всех зависимых таблиц, копия
  недостающих provenance (DO NOTHING — голоса new не перезаписываются),
  тумстоун old (SCD2 `valid_to`). Залоченные — только с `force`.
  `ResolveTerminalID` — bounded-обход карты redirect (32 хопа).
- **DeleteTerminal:** операторский тумстоун SCD2 (`valid_to=CURRENT_DATE`,
  `is_locked=false`), не физическое удаление; залоченный — с `force`.
### 5.5 Изоляция ручных операций от is_locked; надёжность сбора (2026-09-11)

- **Семантика `is_locked` (§0):** лок — барьер только для автоматики.
  Ручные merge/delete в UI выполняются безусловно (оператор — автор
  лока); force-флаги удалены из API. Ошибки называют точную причину:
  `не существует / уже удалён (тумстоун) / уже слит в N` (helper
  `terminalState`), а не «не найден или не залочен».
- **Тумстоуны исключены из выдачи:** списки терминалов (filtered/liveness,
  pool+tx) и `LoadNetwork` (стопы удалённого терминала не попадают в сеть
  планировщика) фильтруют `valid_to IS NULL`. До фикса удалённые
  показывались в поиске и кликались повторно → «не найден или уже удалён».
- **`trips_served` в поисковом списке** (filtered, pool+tx): счётчик живых
  рейсов и dead-бейдж прямо в поиске — быстрая диагностика пустых
  терминалов без открытия карточки (совпадает с liveness-режимом).
- **Tx-обёртка `ListTerminalIDByCode`:** дедуп скелета по коду (§5.4)
  упал на промоут-транзакции — «транзакция не умеет промоушен скелета»
  (jobs 12–15 dead наSkeleton-прогонах НСО/Кузбасса). Форвард добавлен.
- **Churn от чистого роста подавляется безусловно:** disappearance=0
  (исчезновений нет) больше не dead-letter независимо от force — первый
  полный прогон после частичного наследия легитимен (job 15: churn 0.336,
  disappearance 0 → done). Исчезновения остаются hard-алертом (§5.3).
- **Атомарный конвейер сбора рейсов:** станция → schedule → её нитки →
  flatten — обработка станции сразу после ответа, а не вторым проходом.
  Обрыв на любой точке не сжигает квоты: собранное оседает в кэш
  постранично и переиспользуется.
- **Источник станций `stations: yandex|overpass`** (job payload + UI
  «Сбор»): overpass-режим берёт терминальные станции bbox'а
  (`BuildTerminalStationsQuery`: bus_station/railway station/aerodrome,
  конфиг `sync.bbox`), станции без yandex_code резолвятся координатой
  через Rasp `nearest_stations` (новый `Rasp.NearestStation`,
  cache-first, та же квота). Станции из дампа — по-прежнему по коду.
- **Надёжность jobs:** `RecoverStuckJobs` — running-задачи после
  рестарта процесса возвращаются в retry (однопроцессный воркер,
  orphaned running); `ResetJob` `POST /api/v1/jobs/{id}/reset` +
  кнопка ↻ в UI — операторский перезапуск dead/retry/running с
  обнулением attempts.
- **API:** `POST /api/v1/admin/terminals/merge` `{old_id,new_id,reason,force}`,
  `DELETE /api/v1/admin/terminals/{id}[?force=1]`; обе операции в
  `audit_log`. Списки терминалов (filtered/liveness, pool+tx) отдают
  `valid_from`/`valid_to`.
- **UI (Терминалы):** колонка «добавлен» (+`→valid_to` для тумстоунов),
  кнопки в строке «⇄» (быстрый merge: old=строка, new=по промпту) и «✕»
  (delete с confirm), панель merge (old/new/reason).

### 5.6 Точечная загрузка расписания терминала; сброс канона; квоты провайдеров (2026-09-11)

- **Точечный сбор рейсов** (job payload `terminal_id`+`transport`,
  UI-карточка терминала): один терминал из канона вместо всего региона —
  `terminalStation` берёт `yandex_code` из `terminal_identifiers`,
  регион фолбэком из staging (`TerminalStagingRegion`), транспорт
  фильтрует нитки (`TransportCompatibleRasp`: suburban→rail,
  plane→flight). При нескольких `transport_types` на терминале UI даёт
  селектор. `region` в запросе не обязателен при `terminal_id`.
- **`TerminalScope` churn-гейта:** точечный прогон сравнивает churn
  только по затронутым маршрутам (`PrevCanon` обрезается до
  `RouteReg` входных рейсов), иначе «исчезновение» всего остального
  канона давало churn-alert при каждом точечном прогоне. Coverage-gate
  при пустом регионе (не enriched терминал) не применяется.
- **Сброс канона:** `POST /api/v1/admin/canon/reset`
  `{"confirm":"RESET","reason"}` (+ кнопка в UI «Сбор» с двойным
  подтверждением) — операторская полная очистка терминалов/рейсов/
  staging/истории синков/ревью в одной транзакции; каркас (users, keys,
  jobs, places, квоты) и дисковый кэш Яндекса не трогаются. В
  `audit_log` с counts по таблицам.
- **Квоты провайдеров:** строка `overpass` добавлена в справочник
  `providers` (FK `api_quotas.provider` молча падал, а
  `TryConsumeQuota` превращал ошибку БД в «quota exhausted» при
  used=0/1000). Ошибки квоты больше не маскируются: pool/tx возвращают
  её наружу (только `ErrNoRows` = реальный лимит), хендлер
  external-call и jobs-worker отличают 500 от 429.
- **Таймаут httpx 10s → 90s:** Overpass QL заявляет 30–60s на запрос;
  прежний хардкод рвал ответ раньше, чем перегруженный endpoint
  отвечал («context deadline exceeded» на живом зеркале).

### 5.7 Fuzzy-времена: fallback для стыковочных рейсов без расписания (2026-09-11)

- **Механика (§5.4):** untimed-стопы сохраняют позицию в
  последовательности (registry-parser `untimedFlatStop`, `IsFuzzy`),
  матчатся на терминалы как обычные; после матча
  `interpolateFuzzyTimes` линейно интерполирует время по ближайшим
  timed-соседям (цепочки подряд — делят интервал по перегонам), стоп
  помечается `is_fuzzy`. Крайний fuzzy-стоп — ошибка: время цеплять не
  к чему, рейс в staged. Времена абсолютные от полуночи (не от первого
  отправления — см. §5.8 fix), переход через полночь +1440.
- **Валидаторы остаются честными:** монотонность и скорости на
  fuzzy-перегонах считаются по интерполированным временам —
  «медленный» интерполированный перегон легитимно уходит в staged.
- **Персист и сеть:** `stop_times.is_fuzzy` (миграция + живая БД),
  `StopTimeRow/MatchedStopTime/StopTime/Connection.Fuzzy`,
  `LoadNetwork` читает флаг и метит перегоны `Fuzzy`.
- **Планировщик и ответ MCP:** fuzzy-лег получает `TimeHint`
  «время ориентировочное — уточняйте у перевозчика»;
  `enrichFuzzyLegs` (mcp) дополняет его контактами перевозчика из
  канона (`carriers.phone/info_url/address` — новые колонки, модель
  `Carrier` расширена, `Route.CarrierID` пробрасывается в сеть).
- **Счётчик `fuzzy_promoted`** в attach-отчёте — KPI доли
  интерполированных рейсов. Тест `TestAttachFuzzyFallback`:
  промоут 3-стопового рейса с fuzzy-серединой, интерполяция
  10:00→13:00 → 11:30, флаги и монотонность.

### 5.8 Поиск маршрутов в админке + инвалидация кэша сети (2026-09-11)

- **Инвалидация кэша сети (баг):** `mcp.App` кэшировал сеть на день, но
  канон менялся (сборы, правки терминалов) — кэш продолжал отдавать
  устаревшие данные (наблюдали: 7 стопов/10 рейсов при 8/11 в БД;
  «ближайшая остановка к Томску — Юрга, 83 км»). Теперь пакет-уровневый
  `canonVersion` (`mcp.BumpCanonVersion()`): collect-джобы (skeleton,
  trips), merge/reset/update/approve/delete терминалов бампают версию,
  `networkForDay` сравнивает закэшированную — при смене перечитывает
  `LoadNetwork`.
- **Поиск маршрутов в UI (над вкладками):** карточка «Поиск маршрута»
  (from/to текст или lat,lon; дата+время; allow_gap; свёрнутый блок
  «Расширенные параметры»: max_walk_minutes, max_transfers,
  preference, transit_modes) → JSON-RPC `tools/call find_route` на
  `/mcp` с тем же токеном. Результат — компактная таблица легов:
  время, режим (бейдж), терминалы с координатами, длительность,
  стоимость, код маршрута/рейса; fuzzy-леги — бейдж «⚠ время
  уточнить» с `time_hint` (перевозчик + контакты, §5.4/§5.7).
  Ошибки планировщика — красный блок с текстом ошибки.
- **Поиск рейсов по стопам (баг):** `ListRoutesAdmin` искал только по
  названию/коду маршрута — «юрга» не находила транзитные рейсы.
  Теперь ищет и по именам стопов живых рейсов маршрута (stop_names →
  terminal_names) и по id стопа.

## 6. Чек-лист устаревания (актуальные паттерны)

Прогонять `make check-deprecated` после закрытия каждого пункта
`14-plan.md`. Текущие сигналы, что что-то пора удалить/перенести:

- `grep -rn "Deprecated\|TODO.*phase" --include="*.go"` — явно помеченный
  устаревший код.
- `grep -rln "seed_pilot\|places_sqlite\|import_intercity" --include="*.go"`
  — файлы, привязанные к легаси-пути (JSON/sqlite), которые должны исчезнуть
  по завершении перехода из §2.
- `internal/store/sqlite/*` целиком — под удаление вместе с миграцией 002
  (см. §1), когда `PostgresStore` полностью покрывает функциональность.
- `store/places_test.go` — если тест специфичен для sqlite, переименовать в
  `store/sqlite_places_test.go` и держать рядом с `sqlite`-реализацией до
  её удаления, не путать с общими unit-тестами `Store`-интерфейса.

Актуальный список grep-паттернов поддерживается в `Makefile`
(`check-deprecated`), этот раздел — только пояснение, зачем каждый паттерн
нужен именно сейчас.
