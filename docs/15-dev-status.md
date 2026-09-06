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
  верификация → канон, коннекторы в `internal/adapters/{mintrans,yandex,
  motis,gtfs}`, компилятор `internal/gtfs/compiler` (per-region
  `gtfs_{code}_{mode}.zip`), очередь `internal/jobs` + `outbox`.

Пока обе архитектуры сосуществуют: `internal/providers` обслуживает текущий
`find_route`, канон в Postgres наполняется параллельно по фазам плана.
Точка переключения планировщика с `Provider`(JSON) на `Store`(Postgres) —
отдельная задача, не начатая на момент этого снимка; при переключении
обновить этот раздел и убрать `internal/providers/intercity` в `deprecated`
по чек-листу ниже.

## 3. Дерево пакетов (текущее, полное)

В дополнение к стабильному ядру из `AGENTS.md`:

```
internal/providers/     Provider-интерфейс, Registry, synth (мок), intercity (JSON, legacy)
internal/geo/           гаверсин, ближайшие остановки, время пешего доступа (`GeoResolver.maxCalls` — Deprecated, замена: квота БД + TTL)
internal/sync/          Фазы 0–1: `planid` (plan_id + ChunkFresh), `replay` (каноническая проекция + diff), `overrides` (экспорт/импорт решений оператора), `elect` (per-field конкурс + legacy-матчинг)
internal/skeleton/      Фаза 3: источники отдают `model.AdaptedRecord` (`Kind=AdaptedTerminal`, идентификаторы `osm_id`/`yandex_code`/`esr_code`, `Extra`: settlement/region/transport_type) — своего типа терминала нет; `OSMSource` + `YandexDumpSource` (только локальные файлы, 0 API), `CollapseStopArea` (иерархия OSM на экстракции), `Join` (скоринг OSM↔Yandex: код > гео+имя; `Enrichment`/`Score` — в результате join'а, не в записи; Yandex-only → unverified, не канон), `InterpolatePosition` (seq/долевой кандидат, origin seed), `CoverageByRegion`+`GatePass` (coverage-gate)
internal/geocoder/      fallback + `cached.go` (lookup `geocode_cache`, TTL 90д/7д, stale-while-revalidate, квота БД) + `seed.go` (загрузка `{yandex,nominatim}_geo.json` с `origin='seed'`)
internal/planner/       CSA-поиск + сборка Journey (работает поверх Provider, не Store)
internal/store/         Store/PlaceStore и т.п. интерфейсы; postgres/, sqlite/ (legacy, к удалению), memory/ (тесты)
internal/model/adapted.go   AdaptedRecord — единый промежуточный формат коннекторов
internal/verification/  VerifyTerminal и алгоритм confidence (A/B/C/D, пороги по density_class)
internal/import/        оркестрация конвейера: normalize → enrich → dedup → verify → canonical
internal/adapters/mintrans/  коннектор реестра Минтранса (XLSX → AdaptedRecord)
internal/adapters/gtfs/      коннектор внешних GTFS (Москва/СПб)
internal/adapters/yandex/    коннектор Яндекс.Расписание (квоты, ротация)
internal/adapters/motis/     коннектор MOTIS (geocode/reverse-geocode/areas)
internal/gtfs/compiler/ Postgres → gtfs_{region}_{mode}.zip, per-region
internal/jobs/           очередь (jobs+outbox), воркер, backoff, ротация провайдеров
tools/osm-extract/       отдельный Go-модуль: PBF (OSM) → JSON гео-меток
data/                    сырьё и датасеты сбора (НЕ коммитятся)
scripts/api-demo.sh          ручное демо/обследование API (curl+jq)
scripts/mcp-route.sh         шаблоны запросов MCP
scripts/yandex-collect.sh    точечный сбор фикстур Яндекса
scripts/extract-minstran.py  XLSX-реестр Минтранса → JSON датасет (legacy-путь, до полного переноса в internal/adapters/mintrans)
```

`internal/store/sqlite` — помечен legacy, план удаления привязан к миграции
002 (см. §1) и переносу оставшихся ссылок на legacy-таблицы.

## 4. Env-переменные (текущий полный список)

| Переменная | Назначение |
|---|---|
| `HTTP_ADDR` | адрес HTTP-сервера |
| `DATABASE_DSN` | строка подключения к Postgres |
| `PROVIDERS_ENABLED` | список активных `Provider` (пусто по умолчанию; `synth` — тесты/демо; `intercity` — реальный реестр) |
| `INTERCITY_REESTR_PATH` | путь к `data/reestr/regions.json` для `intercity` |
| `ADMIN_TOKEN` | токен админки текущей фазы (до внедрения полноценного JWT/ролей по плану Фазы 5) |
| `SYNC_LOG_DIR` | каталог файлов операций `sync_<run>.log.jsonl` (план, Фаза 1; конфиг `sync.log_dir`) |
| `SYNC_COVERAGE_GATE` | порог N% coverage-gate скелета (план, Фаза 4; конфиг `sync.coverage_gate`) |
| `SYNC_SKELETON_CHUNK_SIZE` | терминалов на чанк промоушена скелета, потолок 100 (план §4.2; конфиг `sync.skeleton_chunk_size`) |
| `SYNC_OSM_PATH` / `SYNC_YANDEX_DUMP_PATH` / `SYNC_SKELETON_REGION` / `SYNC_BBOX` / `SYNC_REESTR_PATH` | входы пилота скелета: OSM-станции, дамп Яндекса, регион-фильтр, bbox, JSON-срез реестра для coverage (конфиг `sync.osm_path`, `sync.yandex_dump_path`, `sync.skeleton_region`, `sync.bbox`, `sync.reestr_path`) |
| `SYNC_LEGACY_THRESHOLD` / `SYNC_COVERAGE_SOFT_SCORE` | порог `legacy_match` Фазы 0 (0.6) и мягкий порог ScorePair для coverage (0.4); конфиг `sync.legacy_threshold` / `sync.coverage_soft_score` |
| `SYNC_TRIPS_CHURN_THRESHOLD` | churn-alert attach трипов: доля added+removed+changed за прогон, стоп при превышении, старт 0.2 (план, Фаза 4; конфиг `sync.trips_churn_threshold`) |
| `VERIFICATION_SCORE_MARGIN` / `VERIFICATION_SCORE_AMBIGUITY` | полоса ниже порога → `low_confidence` (0.1) и зазор top1−top2 → `duplicate_ambiguous` (0.05); конфиг `verification.score_margin` / `verification.score_ambiguity` (ScorePair, Фаза 4.1) |
| `GEOCODE_TTL_VERIFIED` / `GEOCODE_TTL_DISPUTED` | 90д / 7д для `geocode_cache` (план, Фаза 2; конфиг `geocode.ttl_*`) |
| *(конфиг-файл, не env)* | `density_thresholds` + `trust[pair]` ScorePair — путь в `configs/`, не env (план §3.8) |

Новые переменные (квоты Яндекса, пороги verification, JWT-секрет для
Фазы 5 и т.п.) — добавлять в эту таблицу по мере появления, не хранить
список только в коде/`configs/`.

## 5. Референс тестовых данных

### 5.0 Фазы 0–2 (выполнено)

- **Фаза 1:** `migrations/001_initial.sql` — единая миграция: `terminals.address/address_parts/transport_types/object_type/enrichment_status`, `terminal_aliases`, `terminal_merges`, `routes.external_route_code` + `UNIQUE(source_provider, external_route_code)`, `trips.external_trip_code/direction_id/duration_s/distance_m/method/valid_to` + `UNIQUE(route_id, external_trip_code)`, `staging_trips`, `trip_sources`, `attribute_state` (+`value`,`origin`,`sync_run_id`), `geocode_cache` (+`origin`), `review_queue` (+`state`,`fingerprint`,`count`,`observed_at`, enum-канон §3.10), `provenance_history.sync_run_id` + индекс, `sync_runs` (+`summary`)/`sync_chunks`, типы джоб `sync_*` в `jobs`, `v_stale_attributes`. Без `ALTER` — всё в исходных `CREATE TABLE`. Инфраструктура: `internal/sync` (plan_id, replay-diff, overrides), `scripts/doc-lint.sh`, `testdata/golden_*.json`.
- **Фаза 2:** `CachedGeocoder` (hit без квоты, stale-while-revalidate, singleflight на ключ), TTL из конфига `geocode.ttl_verified/ttl_disputed` (90д/7д, env `GEOCODE_TTL_*`), seed `origin='seed'` из `data/reestr/{yandex,nominatim}_geo.json` (не голос за finalize), квота БД через инжектируемый `QuotaFunc`, `GEOCODE_MAX_CALLS`/`GeoResolver.maxCalls` — Deprecated.
- **Фаза 0:** per-field конкурс `ElectField` (`geom` — повышенный вес скелета OSM/MOTIS, остальное `confidence → observed_at`, `actor_id` — sticky-барьер поля, `seed` — только fallback), без пары — `MatchLegacy` → `review_queue{legacy_unmatched}`. Исполняется внутри Фазы 3 после построения скелета.
- **Фаза 3 (скелет, без БД-зависимостей):** `internal/skeleton` — `OSMSource` (парсит `stations.json` экстрактора, безымянные отбрасываются), `YandexDumpSource` (офлайн-разбор `global_stations_list.json`: 159 167 станций, Кузбасс — 1684; координаты бывают пустыми строками → указатель `nil`), `CollapseStopArea` (одно имя + геоклетка → один терминал, агрегат `transport_types`, слияние идентификаторов), `Join` (verified только при гео-паре в пороге или код-матче; geom-null по одному имени — никогда verified; несопоставленный Yandex → `Unverified`/review, не канон; вариант названия Яндекса сохраняется в `Extra[yandex_title]` для алиасов), `InterpolatePosition` (кандидат между сматченными соседями по `seq`, долевой при наличии весов, `origin='seed'`, confidence < 0.6), `CoverageByRegion`/`GatePass` (порог — `sync.coverage_gate`/`SYNC_COVERAGE_GATE`, старт 0 до калибровки на Кузбассе).
- **Фаза 3 (скелет, Postgres-часть):** `internal/sync/skeleton_promote.go` — узкий `SkeletonStore` (терминал + алиас + `attribute_state` + `provenance`/`review_queue` + `sync_runs`, без расширения широкого `Store`), `ChunkSkeleton` (детерминировано регион→транспорт→код, чанк ≤ 100, потолок в `sync.skeleton_chunk_size`), `PromoteSkeletonChunk` (один чанк = одна транзакция через `WithTx`; row-reconciliation `in == written + review`, fail-loud; без координат → `review_queue{low_confidence}`, Yandex-only → `review_queue{skeleton_unverified}` с синтетическим отрицательным `entity_id` от fnv-внешней идентичности и `fingerprint=source:code`), `Begin/FinishSkeletonRun` (вид `skeleton`, summary-JSON). DDL в `001_initial.sql`: `staging_terminals(run_id, source, source_code, ...)` + GiST по `geom` (кандидаты — `ST_DWithin`, точный скоринг — в Go: `NearbyStagedCandidates` в `internal/store/postgres/skeleton.go`); `review_queue.fingerprint` пишется `SaveReviewQueue` (пустой не затирает); `attribute_state`-апсерт бампает `observed_at` только при смене value/confidence (no-op-конвергенция §4.3). Реализации: postgres (pool + tx) и memory.
- **Фаза 0 + пилот Кузбасс (прогон на живой БД `travelmcp_pilot`, 2026-09-06):** раннер `cmd/skeleton-sync` (конфиг `sync.*`, `plan_id` из версии + конфига + sha входов, staging → чанки `sync_chunks` с пропуском свежих по `plan_id_done`, resume флагом `-run`, ops-лог `sync_<run>.log.jsonl`, `-dry-run` без записи). OSM-экстракт СФО — 27 257 станций (в т.ч. 2360 ж/д: фильтр экстрактора расширен на `railway=station/halt`, иначе rail-терминалы не матчились бы вовсе); bbox пилота `53.5,84.0,57.0,88.5` (Новокузнецк внутри). Join OSM(2081 в bbox)×Yandex(1684, `Кемеровская область - Кузбасс`): канон 1982 после дистанционного схлопывания (было 2081 до фикса границы геоклетки), 1346 Yandex-only → `review_queue{skeleton_unverified}`; summary `in == written + review` сошёлся. Фаза 0 на 3 seed-legacy: 1 matched (ручной geom sticky — цел, значения конкурентов совпали), 2 → `review_queue{legacy_unmatched}` с fingerprint. Resume `-run` — все чанки пропущены как свежие, канон стабилен. Coverage по срезу реестра-42 (148 стоп, 8 регионов, мягкий порог 0.4): 42 — 37/46 = 0.80; 22 — 0.73; 54 — 0.95; 04 — 0.48; gate при 0 — pass. Покрытие 04 низкое: маршрут 04.42.010 уходит в Горный Алтай вне bbox/OSM-экстракта СФО — расширять скелет, а не тянуть рейсы (§5.4).
- **Политика сторон дороги (решение):** флага направления на терминале нет и не будет — направление это свойство рейса (`direction_id`), а не места (GTFS-семантика). Остановки с разных сторон дороги — разные строки `stops_canonical` с одним `terminal_id` (parent_station-семантика); заселяются на attach Фаза 4. Дубли вида «Собор» 8/9 (61м, разные геоклетки) устранены заменой ключа `имя+геоклетка` на дистанционный кластер: одно нормализованное имя + single-linkage ≤ 300м (`collapseMaxM`, калибруется с ScorePair). Без координат — отдельная группа на имя (как раньше).
- **Мульти-тип `{bus,rail}` (аудит пилота):** 18 шт, порядок нормализован сортировкой. Разбор: узлы типа «Вокзал Томск I», «Белово» — настоящие интермодальные хабы (ж/д + автобусные остановки «Станция X» в радиусе 300м — корректный агрегат); кейс 2078 «Городская» (ДЖД) был багом классификации — `building=train_station` без `railway=*` считался bus, исправлено (`osmTransportType`), теперь `{rail}`. Потеря вторых кодов при схлопывании (PK без `code`) исправлена: PK `(terminal_id, system, code_type, code)` + `ON CONFLICT DO NOTHING`, «Собор» хранит оба `osm_id`. Остаток 18 — кандидаты авто-аудита `possible_merge` Фазы 5 (план §2).
- **Приоритет межгорода (решение):** городской/межгород отдельной метки пока нет; частичное покрытие — `object_type` (`station|stop|platform|airport`). Межгород Tagging — на attach Фаза 4 (`terminal_tags['intercity']` для терминалов, сматченных на стопы реестра); скелет строится полностью, фильтр — на чтении/attach, чтобы городские данные не потерять.
- **`configs/cities.yaml` удалён** (Go-конфиг `Cities`, env `CITIES_PATH/DATA_PATH`, мост в `main.go`, fallback в `extract-minstran.py` — всё вычищено; газетир Go — только встроенный `internal/geo/places.json` до переезда данных в БД `places`).
- **Справочники:** `regions` пуст (заполнение — геометрии границ + `density_class` перед рекалибровкой Фазы 4; кандидаты источников — OSM boundary relations, MOTIS areas); `places` строится из иерархии Yandex-дампа (страна→регион→поселение) на фазе дообогащения; `tz` — статическая карта регион→tz (регион целиком в одном tz), без геокодинга каждой точки.
- Следующий шаг — Фаза 4.2 (staging + промоушен-транзакция=trip: two-tier валидаторы, tombstone/GC, row-reconciliation).
- **Фаза 4.1 (ScorePair ось `stop_terminal`, выполнено):** `internal/verification/scorepair.go` — единый скоринг по §3.8: `PairItem` (имя/гео/транспорт/settlement/коды/источник), веса-приоры `0.45/0.3/0.15/0.1` с ренормализацией при null-фичах, код-матч — сильнейший сигнал, `trust[pair]` (один голос 0.6, OSM/MOTIS — один голос, seed 0.4, legacy 0.7, manual 1.0), hard-guard (гео-пара в пороге ИЛИ код ИЛИ имя+settlement вне urban с margin и trust 1.0; urban geom-null — никогда verified; оба-из-Минтранса без гео — никогда verified), решения verified/low_confidence/duplicate_ambiguous/rejected через `MatchStopToTerminal` (top1−top2 против ambiguity). Пороги/margin — `verification.*` + per-class override `density_thresholds`, калибровка весов/trust на Кузбассе — перед массовым attach (gate Фазы 4). Golden: `testdata/golden_trips.json` — 8 `matcher=scorepair` кейсов, CI-тест `TestGoldenTripsScorepair` требует accuracy/precision/recall 1.0; старые 3 кейса (sequence/validate) — пропуск до 4.2.
- **Фаза 4.0 (bootstrap NK + контракт сырого входа, выполнено):** `internal/adapters/mintrans/contract.go` — `ValidateDatasetContract` (fail-loud на дрейф формата: обязательные поля, `source='minstran_reestr'`, `snapshot` YYYY-MM-DD, `reg` NN.NN.NNN + уникальность, стопы `region` NN + диапазон координат, расписания `forward|backward` + ссылки на routes/stops/services + форматы `HH:MM`/dwell, `service_days` 0..6); `bootstrap.go` — `CompareDatasets` (сигнатура маршрута = carrier + стопы forward/backward, `churn=(added+removed+changed)/max`, `disappearance=removed/old`, `TrustNK` при обоих ≤ порога, иначе `Alert` + рекомендация synthetic-ключа `synthetic:region:number:from:to:carrier`), sha обоих срезов в отчёте. Порог — `sync.trips_churn_threshold` / `SYNC_TRIPS_CHURN_THRESHOLD`, старт 0.2. Тесты — `contract_bootstrap_test.go` на `testdata/test.json` (13 кейсов дрейфа, stable/churn/carrier/synthetic).
- **Фикс миграции:** `users`/`api_keys` перенесены в `001_initial.sql` выше `provenance` — иначе на чистой БД цепочка `provenance`/`provenance_history` не создавалась (FK на несуществующих `users`, тихий ERROR без `ON_ERROR_STOP`). Пилотная БД `travelmcp_pilot` (docker, `localhost:5442`) пересоздана и мигрируется с 0 ошибок; `travelmcp` (основная dev-БД) осталась со старой схемой без `provenance` — перед следующим использованием пересоздать.
- **Дерево (новое):** `cmd/skeleton-sync/` — раннер пилота (конфиг → план → staging → чанки → Фаза 0 → coverage); `internal/sync/legacy_match.go` (Фаза 0: `Join` как `legacy_match`, конкурс `ElectField`, review), `internal/sync/coverage.go` (чистый `MeasureCoverage` + `GatePass`), `internal/store/postgres/skeleton.go` (staging/chunks/legacy-чтения), `tools/osm-extract` — фильтр расширен на ж/д станции.
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

### 5.2 intercity (реестр Минтранса, JSON-путь)

- Источник exact-времён: `scripts/extract-minstran.py` (XLSX → JSON) с опц.
  `--osm data/osm/stations.json` для геокодинга (140/255 остановок с
  координатами на момент снимка). Рабочий датасет `data/reestr/regions.json`
  (не коммитится); фикстура для тестов `testdata/test.json`
  (коммитится).
- Проверенный сквозной сценарий (поиск 06:00 UTC, лимит пешего подхода
  30 мин): НСК-автовокзал → Барнаул/Томск/Кемерово находятся и строятся
  через CSA.
- Пешие стыковки между близкими терминалами (< 0,4 км) строятся
  автоматически (`addTransferLinks`) — соседний вокзал/автостанция часто
  оформлены как разные стопы реестра.
- Сеть строится лениво на каждый запрос (см. §2 — целевой канон в Postgres
  снимет это ограничение через материализованный компилятор GTFS).

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
