# 14. План работ (актуальный)

> Ревизия: консолидированный редизайн импорта/синхронизации **терминалы-первыми**
> (терминал — первичная сущность, Минтранс — только источник рейсов).
> Заменяет предыдущую редакцию плана (удалена на диске вместе с кодом legacy-импорта).
> Миссия — `docs/13-mission.md`; термины и исходники верификации — `docs/02-glossary.md`;
> снимок состояния — `docs/15-dev-status.md`.

## 0. Приоритеты (не размениваются)

1. **Первичный:** корректные **терминалы и их атрибуты** — топоним (нас. пункт,
   заглавная буква), тип транспорта (возможно несколько — совмещённые терминалы),
   адрес, координаты, варианты названий для подсистем, пользовательское название,
   коды для разных систем, иные топографические/юридические данные. У каждого поля —
   источник и дата обновления (`attribute_state`).
2. **Вторичный:** **связи между терминалами** и всё с ними связанное — длительность,
   расстояние, расписание, цены с источниками и временем обновления, перевозчики,
   линии между терминалами.
3. Дамба против «мусора»: никакой объект не попадает в канон без проверки
   (`ScorePair`), неудовлетворительное — в `review_queue` с кандидатами, а не в БД.
   **Физически невозможные данные** (времена не монотонны, скорость абсурдна,
   «зигзаг» на сотни км) — не «в review», а **обязательный reject `dead` с loud
   error**: их источник — ошибка обработчика/источника, а не решение оператора.

Следствия, принятые по итогам ревью плана (не переопределять точечно):

- **Минтранс не создаёт терминалы.** Реестр — поставщик рейсов; остановки рейса
  сопоставляются с уже существующим скелетом терминалов. Несопоставленное → staging/
  `review_queue`, а не новая точка с `(0,0)`.
- **Маршрут/трип публикуется в канон только полностью сопоставленным.** Частичный
  матчинг держится в `staging_trips` (не виден планировщику), автопромоушн — по
  событию появления недостающего терминала. Никаких «дыр» в `seq` и склеенных
  перегонов через пропущенную остановку.
- **Единый скоринг.** `ScorePair` — единственный механизм оценки достоверности:
  скелет↔OSM, стоп Минтранса↔терминал, merge, дедуп. Ось «задача» и парное доверие
  источников — в самом скоринге (§3.8), не ad hoc AND-правила.
- **Одно значение на поле — один победитель.** Параллельные истории уносятся в
  `provenance_history`; `attribute_state` — только текущие конкуренты, каждый со
  своим значением (см. §3.1, §3.10).
- **Ручное не перезаписывается**: `is_locked` блокирует запись целиком,
  `attribute_state.actor_id` — точечно поле (`conflicts_with_confirmed`).
- **Решения оператора — мигрируемые данные.** Ручные правки, merges, резолюции
  `review_queue` экспортируются/импортируются как датасет overrides; пересбор схемы
  (dev `DROP SCHEMA + миграция`) их не теряет (§2 Фаза 1, §7).
- **Политика времени фиксируется до Фазы 4** (§3.13): хранение UTC, один
  winner-источник времён на весь трип, запрет фабрикации времён из частот.

## 1. Архитектура (терминалы-первыми, «от обратного»)

### 1.1 Диагноз (что не так со старым путём)

- Мастером терминалов был реестр Минтранса: имена вида `ОП «ЛПК»` без топонима,
  координат часто нет, названия не совпадают с картами (`missing_stops.json`,
  ставший сейчас review-сигналом). Геокодирование по имени = угадывание точки по
  нетопонимичной строке.
- `terminal_names` — максимум 2 строки (PK по языку): вариантам имён негде жить.
  Адреса нет; `transport_type` не привязан к терминалу.
- `MemoryCache` не использовался в импорте; живой кэш геокодинга — `map` без TTL
  + скриптовые `data/reestr/{yandex,nominatim}_geo.json` (без имени/даты).
- Quota БД не вызывалась на пути геокодинга; `VerifyTerminal` импортером не
  использовался (проверялись только `provenance`+`review_queue`).

### 1.2 Скелет терминалов

- **Каноническая идентичность — OSM** (`data/osm/stations.json`, онлайн-добор
  Overpass). **Overpass — второй сборщик маршрутов в паре с Яндекс Расписанием:**
  Яндекс даёт станции + времена отправлений (*когда*), Overpass — `route_master`/
  `route` relations (`ref`, `operator`, `network`, упорядоченные члены-остановки,
  геометрия: *что и в каком порядке*). Времена остаются монополией Яндекса (в OSM
  расписаний практически нет). Identity-доказательство (этот ли маршрут: `ref`+
  `operator` из Overpass) и geometry/время (Яндекс) живут раздельно, не схлопываются
  в один флаг. Детали — §10.
- **Офлайн-обогащение — существующий дамп Yandex**
  `data/yandex/cache/global_stations_list.json` (159 167 станций, все виды
  транспорта: `transport_type`, `station_type`, координаты,
  `codes{yandex_code, esr_code}`, иерархия country→region→settlement).
- **Иерархия OSM разрешается на этапе экстракции**: `stop_area` → один терминал;
  платформы/выходы в него не порождают отдельных терминалов (иначе — волна ложных
  `possible_merge` в начале работы).
- Правило существования: терминал найден **только в Yandex** (нет OSM-пары) →
  `review_queue{skeleton_unverified}`, в канон не попадает. Совпадение источников —
  через `ScorePair`. Только OSM → канон с `enrichment_status='identity_only'`.
- Интерфейс `SkeletonSource` с реализациями `osm` (мастер) и `yandex` (офлайн).
  Юридический статус дампа Yandex как мастер-источника — go/no-go, поэтому
  канонический набор всегда пересобираем без этого файла.

### 1.3 Конвейер `internal/sync` (новый, стадии)

1. `terminals skeleton` — OSM + Yandex-обогащение (только локальные файлы, **0 API**).
2. `terminals verify` — единственная стадия с внешними запросами: спорные точки
   (нет пары, расхождение > порога) → `geocode_cache` → API, с квотой БД и
   глобальным windowed-токеном в `api_quotas` (verify-стадия остаётся синхронной;
   отдельный writer-job — опционально, только если понадобится очередь, §3.7).
3. `trips attach` — реестр Минтранса → рейсы; стопы матчатся на скелет (см. §4),
   полностью сопоставленные рейсы → канон. **Не стартует ниже coverage-gate
   скелета по регионам трасс** (§5.4).
4. `trips validate` — валидаторы правдоподобия (монотонность времён, скорости,
   пространственная когерентность, согласованность distance/длительность), reject →
   `dead` с loud error (§5.1); неудовлетворительное по матчингу → staging/review.
5. `freshness sweep` — `attribute_state`, финализация/GC, tombstone исчезнувших
   routes/трипов (diff источник vs канон, §5.3), аудит `possible_merge`, KPI.

Старый importer (`internal/adapters/mintrans/importer.go`), `GeoResolver`,
`internal/import/pipeline.go` — `Deprecated`, удаление по §6 после паритета.

## 2. Фазы и вехи

Верификация каждой фазы: `gofmt` + `go vet ./...` + `make test` (обязательно),
операционные фазы — на Postgres (правило §4.1), сверка `v_stale_attributes`/KPI.

| Фаза | Содержание | Выход/готовность |
|---|---|---|
| **0. Миграция данных** | Политика per-field: `geom` получает повышенный вес skeleton-источников в `ScorePair`; остальные поля — обычный конкурс `confidence → observed_at`; `actor_id` в `attribute_state` блокирует поле точечно. Существующие записи матчатся на скелет; без пары → `review_queue{legacy_unmatched}` (не остаются молча). Не самостоятельна: исполняется внутри Фазы 3 (нужны DDL и скелет, см. DAG ниже). | Старые координаты скелет/OSM перекрывают, ручные правки целы |
| **1. DDL и инфраструктура** | Всё аддитивно в `migrations/001_initial.sql` (**без нового слоя**, правило §7): `terminals.address/address_parts/transport_types/object_type/enrichment_status`, `terminal_aliases` (PK с `lang`), `terminal_merges`, `routes UNIQUE(source, external_route_code)` + `valid_to`, `trips(route_id, external_trip_code) UNIQUE` + `direction_id` + `duration_s/distance_m/method` + `valid_to` (tombstone), `staging_trips` NK `(source, external_route_code, external_trip_code)` + `unmatched_stops`, `trip_sources`, `attribute_state`(+`value`,`origin`,`sync_run_id`), `geocode_cache`(+`origin`), `review_queue`(+`state`,`fingerprint`, CHECK по enum-канону §3.10), индекс `provenance_history(sync_run_id)`, `sync_runs/sync_chunks`, `sync_runs.summary` под per-stage счётчики; publisher `outbox`. **Инфраструктура:** replay-diff harness (§5.0), экспорт/импорт overrides, doc-lint скрипт (§-ссылки, enum'ы, шкала, env). | Схема и harness консистентны, тесты зелёные |
| **2. Кэш геокодера** | `cachedGeocoder` (lookup `geocode_cache`, TTL-политика §3.11: 90д подтверждённые / 7д спорные — класс TTL проставляет верификатор после confirmed-матча, `stale-while-revalidate`), seed `origin='seed'` из `{yandex,nominatim}_geo.json`; квота БД + глобальный windowed-токен в `api_quotas` (Nominatim 1 rps); deprecated `GEOCODE_MAX_CALLS`. | «2 фабричных голоса» из одного кривого запроса не финализируют гео; банов нет |
| **3. Skeleton + миграция старых терминалов** | `SkeletonSource{osm,yandex}`; иерархия OSM на экстракции (§1.2); join OSM через staging-таблицы и `ST_DWithin/GiST`; прогон Фазы 0; генерация кандидатов позиций интерполяцией (§5.4). | Скелет в каноне, старые записи сматчены/в review, coverage измерен по регионам |
| **4. Trips** | Минтранс → только рейсы; **запуск только после coverage-gate** по регионам трасс — метод измерения: доля стопов реестра с кандидатом выше мягкого порога `ScorePair` (§5.4); рекалибровка порогов `ScorePair` на новых регионах до массового attach; **предусловие NK:** bootstrap-сравнение двух срезов реестра на стабильность `external_route_code` до доверия ключу, при нестабильности — синтезированный ключ (регион + номер + конечные + carrier, пометка `synthetic`) + churn-мониторинг (§3.4); матчинг стопов на скелет (`ScorePair`, ось `stop_terminal`); единица работы — **трип/маршрут**, барьер межрегиональности с таймаутом (§4.2); **промоушен-транзакция = трип** (job=route — батчинг); политика времён §3.13 (winner-источник, запрет фабрикации); staging + автопромоушн по `outbox`; cached yandex-расписания как доп. источник; tombstone/GC исчезнувших рейсов; row-reconciliation per stage; churn-alert (>N% изменений за прогон → стоп). | Полные рейсы в каноне, `% полных рейсов` в дашборде, фантомов нет |
| **5. Freshness** | `finalize`/GC `attribute_state` (retention 90д, victories в `provenance_history`), `v_stale_attributes`, аудит `possible_merge`, ночной recompute `transport_types`, золотой набор `testdata/golden_terminals.json` + `golden_trips.json` → precision/recall в CI, возраст расписаний и TTL-ресинк (§3.4); demand-driven ресинк расписаний по приоритету (трипы реальных запросов планировщика + истекающие TTL, остальное лениво). | Устаревание видно, объём `attribute_state` ограничен, рекалибровка измерима |
| **6. Депрекация/масштаб** | Удаление legacy-пути по §6; при прод-деплое — заморозка `001_initial.sql`, далее аддитивные `002_*.sql`. РФ целиком — после паритета на полном `regions.json`. **Производственный GTFS:** проверка полноты provenance (`source/channel`, §3.3) как hard-gate CI (скан provenance zip) + GTFS Validator (MobilityData). `plan_id`: переход с `sha(binary)` на ручной logic version (бамп при семантических изменениях), иначе каждый деплой = full-resync чанков. | `make check-deprecated` чисто |

**Зависимости фаз (DAG).** Фаза 1 — корень для всех остальных (DDL-сущности
используются Фазами 2–5). Фаза 2 ⊂ Фаза 3 (verify-стадия). Фаза 0 — не точка
входа, а прогон внутри Фазы 3 после построения скелета (эксплицитно, а не
«запускается первым»). **Фаза 3 → Фаза 4 — через coverage-gate** (§5.4), не по
календарю. Фаза 5 требуется после Фазы 4 (свежесть снапшотов 4). Фаза 6 — финал.

## 3. Структура БД (канонические объекты)

### 3.1 Терминал — модель и свежесть

Атрибут — не «колонка с одним значением», а колонка + записи `attribute_state`
(«что, откуда, когда, с какой уверенностью, какой вес»). Колонка в `terminals` =
текущий победитель конкурса источников; **значение конкурента живёт в самом
`attribute_state`**, а не только в history.

```
terminals (дополнения):
  address text, address_parts jsonb          — адрес (строка + улица/дом/индекс)
  transport_types text[]                     — агрегат (bus+rail у совмещённых),
                                               материализуется триггером от
                                               stops_canonical.stop_type/routes.mode,
                                               ночной recompute-джоб (сверка, как
                                               rebuild_closure) против bulk-путей
                                               с DISABLE TRIGGER
  object_type text                           — station|stop|platform|airport (Yandex station_type)
  enrichment_status text                     — identity_only|enriched (KPI полноты)

terminal_aliases(terminal_id, alias, lang, source, observed_at)  PK(terminal_id, alias, lang)
  — варианты названий для подсистем (Yandex title, Минтранс, OSM, пользовательское).
  `lang` в PK: один alias в ru и en — разные строки (коллизия иначе).
  Канон ru/en остаётся в terminal_names.

attribute_state(entity_type, entity_id, field,
                value jsonb,                — ЗНАЧЕНИЕ конкурирующего источника
                source, confidence real, observed_at, actor_id, origin text
                CHECK origin IN('live','seed'),
                sync_run_id bigint)         — прогон-автор строки
  PK(entity_type, entity_id, field, source)
  — только текущие конкуренты поля; история → provenance_history (append-only).
  Значение текущего победителя = terminals.<field>; значение любого живого
  конкурента читается отсюда — промоут при победе не требует разбора history.
  origin='seed' — ни НИКОГДА не голос за finalize, только fallback-кандидат.
  actor_id — барьер ручной правки на уровне поля.
  sync_run_id — признак прогона: вклад конкретного прогона выделяется точно
  (не окном observed_at) — опора хирургического отката (§4.4).

terminal_merges(old_id bigint UNIQUE, new_id, reason, at, actor_id)
  — плоская карта redirect: merge пишет переписывание всех old редиректов (flatten),
    чтение через ResolveTerminalID (bounded). В одной транзакции репойнтятся ВСЕ
    зависимые таблицы: terminal_identifiers, terminal_aliases, terminal_names,
    attribute_state/provenance (копия с пометкой via_merge),
    stops_canonical.* → terminal_id, stop_times.trip_id/последовательности,
    routes/terminal_merges-цепочки; FK-cascade/repoint, без dangling.
    Реестр зависимых таблиц — в коде merge-операции + merge-тест в CI
    (golden merge-кейсы проверяют переезд ссылок, а не только identity).
```

### 3.2 places.level

Каноническая глубина иерархии мест 0..5 (денормализация `admin_level`) —
**единственная шкала задана словарём (`02-glossary.md:H`), здесь дословно**:
страна `0` → федеральный округ `1` → регион `2` → район/городской округ `3` →
город `4` → район города `5`. Топоним терминала — `place_id → places` с запросом
города `JOIN ... WHERE p.level=4` (как в словаре); fallback
`terminal_tags['settlement']` (display-форма). Коды регионов —
собственная нумерация Минтранса (мап в `internal/geo/resolve.go`, 81 код).
Шкала уже зашита в коде (`internal/model/places.go: LevelCity`, запросы
`p.level=4` в `internal/store/postgres`) — расхождений с ней не допускается.

### 3.3 Канон терминалов и конвейер адаптации

Любые новые данные в канон — только через конвейер
`сбор → нормализация → обогащение → дедуп → верификация → канон`.
Запись в `Store` из коннектора напрямую запрещена (инвариант AGENTS).
`ScorePair` — единый верификационный скоринг (см. §3.8).
Каждая запись `provenance` несёт `source` (происхождение данных:
`osm`, `mintrans`, `yandex`, `nominatim`) и `channel` (канал доставки:
`local_file`, `local_motis`, `transitous_prod/staging`). Лимиты каналов —
в конфиге и `15-dev-status.md`: Nominatim 1 rps, Transitous — лимиты
`serverConfig` (`maxOneToManySize` и др., см. `12-motis-api.md` §6).
CI-gate сканирует provenance собранного zip на полноту пары
`source/channel` и прогоняет GTFS Validator (MobilityData).

### 3.4 Расписания и связи

- Канон: `routes/trips/stop_times` (как есть). Идемпотентность ресинка:
  `trips UNIQUE(route_id, external_trip_code)`. **Natural key `routes`:**
  `UNIQUE(source, external_route_code)` — симметрично трипам, без направления
  и carrier в ключе (направление — `direction_id` на trip, GTFS-семантика).
  **Предусловие:** стабильность кода записи реестра не подтверждена — до доверия
  NK обязателен bootstrap: сравнение двух срезов реестра на стабильность кода;
  при нестабильности — синтезированный ключ (регион + номер + конечные +
  carrier, пометка `synthetic`) + churn-мониторинг (см. §2 Фаза 4).
- **Tombstone рейсов и routes:** `trips.valid_to`, `routes.valid_to`; diff
  «источник vs канон» каждый прогон (см. §5.3): исчезнувший из источника route →
  `valid_to` + каскад на его трипы, планировщику не видны; массовое исчезновение
  (>N%) — стоп прогона и ручная проверка.
- `staging_trips(id, route_raw jsonb, region, transport_type, source, state,
  matched_stop_times jsonb, unmatched_stops jsonb, retry_count, last_attempt_at)`
  NK `(source, external_route_code, external_trip_code)` + upsert по нему —
  ресинки не наливают дубли; рейсы, не добравшие полного матчинга
  (планировщику не видны).
- `trip_sources(trip_id, source, observed_at, price, price_currency, schedule_url,
  duration_s, distance_m, method)` PK(trip_id, source) — цены/длительность/
  расстояние только с источником и датой. Победитель времён — один источник на
  весь трип, политика §3.13. `method` — способ расчёта при отсутствии
  в источнике: `sum_stop_times` | `haversine_detour` (пометка approximation, не
  straight-line без оговорки). Возраст снапшота расписания экспонируется наружу
  («данные расписания на дату X» в MCP-ответе); TTL/ресинк-стратегия для
  yandex-кэша расписаний — как у `geocode_cache`, но с периодом по типу
  транспорта (рейсы меняются, координаты — нет).

### 3.5 Сетевые/графовые связи

Не меняется по сути; новые связи порождаются только между каноническими
верифицированными терминалами. Генерация пеших стыковок/перегонов — на этапе
компиляции из канона, не «на лету» от сырых остановок Минтранса.

### 3.6 provider vs carrier

Разведение сохранено: `provider` — субъект «кто сказал» (коннектор/источник),
`carrier` — юридический перевозчик рейса (сущность БД с ИНН/адресом, GS1/ОКПО).
`routes.carrier_id` — перевозчик; `providers.code` — источник. Мастер источника
терминала — `SkeletonSource`, не путать с provider-флагом.

### 3.7 Квоты и ротация провайдеров

Все обращения к квотируемым API — через атомарный счётчик БД
(`api_quotas`/`TryConsumeQuota`, уже реализован), не через локальное состояние
процесса. In-process счётчик `GeoResolver.maxCalls` — deprecated (замена: квота
БД + TTL `geocode_cache`). **Против бана жёстких rate-limiter'ов** (у Nominatim —
1 rps) — глобальный windowed-токен в той же `api_quotas` (атомарно, как
`TryConsumeQuota`); verify-стадия остаётся синхронной. Отдельный geocode
  writer-job — опционально, только если понадобится очередь (тогда singleflight
  становится тривиальным); вшивать оба сразу не требуется. При масштабе —
  локальный Nominatim/Photon. Приоритет геокодинга — nominatim → yandex
  (`fallback`): полный разбор в `02-glossary.md:G`.
- **Overpass идёт тем же путём `cache+quota`, нового пути нет** (§10):
  пространство ключей `geocode_cache` с префиксом `overpass:`, квота
  `api_quotas` на коде `osm` (данных OSM; отдельных кодов под Overpass в
  `providers` нет и не нужно — FK целы, миграций ноль), пейсер ≥1.2с между
  запросами, лимит точек на прогон `--overpass-max` (дефолт **200**).
  Инстансы: основной `https://overpass-api.de/api/interpreter`, зеркало —
  `https://overpass.openstreetmap.fr/api/interpreter` (kumi.systems исключён —
  проблемы с доступом; остальные зеркала — только строками конфига
  `overpass.url`). Живой сети в CI нет — только фикстуры `testdata/overpass/`
  и ручной скраппинг скриптом.

### 3.8 Верификация — единый `ScorePair`

```
ScorePair(a,b, Task, FeatureVec, class DensityClass) → Score
Task ∈ { skeleton_osm, stop_terminal, merge, dedup, legacy_match }
  — legacy_match: матчинг Фазы 0 (legacy-терминал ↔ скелет), отдельная ось
FeatureVec = { name(+norm), geom|null, transport_type|null, settlement|null,
               identifiers|null }   — совпадение кода в чужой системе
               (yandex_code/esr_code/...) — сильнейший сигнал

веса/пороги — ПЕРЕСТАВЛЯЕМЫЕ по обеим осям:
  feature_weight[task][class][f]  и  threshold[task][class]
  margin/ambiguity_margin[task][class]

score = Σ f ( feature_weight[task][class][f] · feature_match(f,a,b) · trust(pair) )

ренормализация при null-фичах (обязательна, иначе порог недостижим):
  w'_f = w_f / Σ_{f: present} w_f         — подтверждённые фичи усиливаются
  + hard-guard: verified требует (geometry-пара в пороге) OR (код-матч) OR
    (name+settlement вне urban-класса с выполненным margin top1−top2);
    матч с geom=null в urban по одному name+settlement — никогда verified

решение:
  score ≥ threshold[task][class]                       → verified
  threshold − margin ≤ score < threshold               → review_queue{low_confidence}
  top1 − top2 < ambiguity_margin[task][class]          → review_queue{duplicate_ambiguous}
```

- **Парное доверие `trust[pair]`** — полная конфиг-матрица (не проза): пары из
  одного «голоса» (Минтранс↔Минтранс, Yandex↔Yandex — дубли внутри дампа, два
  сервиса на одной базе) понижены (`0.6`) — самосопоставление не масштабирует
  ошибку; пары независимых источников (`OSM↔Yandex`, `OSM↔Минтранс`,
  `Минтранс↔Yandex`) — `1.0`; пары с `seed`/`legacy` — отдельные строки матрицы
  (seed — низкий trust, legacy — по калибровке Фазы 0); `OSM/MOTIS` по-прежнему
  один источник A (не прибавляет уверенности парой OSM↔MOTIS). В оси
  `stop_terminal` матч по паре «оба из Минтранса» не достигает verified без
  дополнительной геометрии/соседей.
- Веса/пороги/`margin` — per `density_class` (rural/suburban/urban/metro) из
  конфига `density_thresholds` + `trust[pair]` (путь — в env-таблице
  `15-dev-status` как конфиг-файл, не env), не хардкод; калибровка на данных
  Кузбасса. Старые веса A/B/C (0.2/0.4/0.4) и simul+0.2 — **только приоры**
  для начальной калибровки; вытесняются trust-матрицей и калибровкой, в формулу
  отдельным слотом не входят; `geom`-weight для skeleton-источников структурно
  повышен (§0 Фаза 0).
- **Рекалибровка на новом регионе — gate Фазы 4**, с поправкой golden-набора
  по региону; перенос порогов пилота на РФ — не «позже в Фазе 6», а перед
  масштабным attach (см. §2 Фаза 4).
- Золотые наборы на входы обоих осей: `golden_terminals.json` (identity+merge)
  и `golden_trips.json` (стоп→терминал, включая sequence-кейсы) — precision/recall
  в CI для каждого `task` (см. §8).

### 3.9 Идентичность и история

SCD2 `valid_from/valid_to` на `places/terminals/routes` — как спроектировано;
`trips` — не SCD2, а tombstone `valid_to` (§3.4). `last_verified_at` — свежесть.
Стабильные ID (`never reuse`), `feed_info.feed_version` стабилен; `provenance` +
`provenance_history` (триггер-копия) — журнал «кто сказал», партиционирование по
`observed_at` откладывается до Фазы 6; **индекс на `provenance_history(sync_run_id)` —
уже с Фазы 1** (нужен самому откату). **`provenance_history` хранит `sync_run_id`
прогона**, `attribute_state` — тоже (§3.1): вклад конкретного прогона обратим
(хирургический откат, см. §4.4).

### 3.10 Журналы, очередь, ревью

- `import_logs`/`outbox`/`jobs` — как спроектировано: `outbox` получает publisher
  «terminal.created/updated» (Фаза 1) и подписчика автопромоушна staging (Фаза 4).
  Массовый skeleton → outbox-flood `terminal.created`: подписчик коалесируется
  (region-level sweep, debounce), не шторм перепроверок. Job-типы:
  `sync_stations`, `sync_refresh`, `sync_terminals_chunk`,
  `sync_trips_attach` (per-trip, §4.2); `sync_mintrans/sync_rail` — семантика
  «только рейсы».
- `review_queue` — единый «бакет неудовлетворительного». **Enum-канон причин**
  (единственный источник; CHECK Фазы 1 и вставки §3.8 сверяются с ним):
  `legacy_unmatched, skeleton_unverified, incomplete_trip, possible_merge,
  low_confidence, duplicate_ambiguous`. **Идемпотентность очереди:** natural key
  причины + upsert по нему, чтобы ресинк не заливал дубли (повторное подозрение —
  bump `observed_at`/`count`, не новая строка). **Sticky rejected:**
  `state open/resolved/rejected`; re-detection rejected-причины не переоткрывает
  (bump `count`), переоткрытие — только при смене fingerprint набора кандидатов;
  **fingerprint = внешние идентичности кандидатов (osm id / yandex_code /
  минтранс-NK) + score-bucket** — без внутренних ID, иначе dev-rebuild сломает
  sticky.
- **Гранулярность решений — entity, не chunk:** rejected/approved на уровне
  причины терминала/трипа; отклонение одной записи не отбрасывает чанк из 100
  терминалов (см. §4.4).
- `attribute_state` finalize: поле подтверждено ≥2 `origin='live'`/manual →
  соперники старее retention (90д, конфиг) удаляются; история не теряется
  (значения конкурентов до этого — в строках `attribute_state`, после — в
  `provenance_history`).

### 3.11 geocode_cache

```
geocode_cache(query_norm, provider, response jsonb, observed_at, origin)
  PK(query_norm, provider); origin IN('live','seed')
```
- Lookup-first в адаптерах (`cachedGeocoder`): свежая запись → hit без квоты;
  иначе квота → API → upsert `origin='live'`.
- **TTL-политика**: координаты статичных объектов долгоживущие — класс TTL
  **проставляет верификатор после confirmed-матча** (связь строки кэша с исходом
  верификации — полем/механизмом верификатора, не угадыванием по возрасту):
  подтверждённые — **90д**, спорные/неподтверждённые — **7д**. Сэкономит квоту
  Nominatim; конфиг `geocode.ttl_verified/ttl_disputed`.
- `stale-while-revalidate`: протухшая запись возвращается с флагом `stale`;
  singleflight ревалидации — через синхронный путь с глобальным токеном (§3.7),
  thundering herd закрыт без отдельного writer-job; пайплайн не блокируется
  при исчерпании квоты.
- Seed-миграция из `data/reestr/{yandex,nominatim}_geo.json` — только
  `origin='seed'` (не голос за finalize, см. §0/§3.1); переезд в `live` — после
  независимого подтверждения живым запросом.

### 3.12 Операционный слой синхронизации

```
sync_runs(id, plan_id, kind, input_sha, tag, state, created_at, finished_at,
          summary jsonb)                    -- summary: per-stage счётчики §5.2
sync_chunks(id, run_id, entity, chunk_key, state, plan_id_done, lease_at,
            attempts, last_error)
```
Детали чанков, `plan_id`, атомарность и логирование — §4.

### 3.13 Политика stop_times, частоты и время

- **Один winner-источник времён на весь трип** (Минтранс даёт структуру/частоты,
  yandex-кэш — времена; `trip_sources` — гранулярность «весь трип»). Приоритет:
  точные времена > интерполированные, затем свежесть, затем trust источника.
  Победитель записан в `trip_sources`; **per-stop микс времён из разных
  источников запрещён** (иначе — химеры для планировщика).
- **Смена winner-источника = атомарный replace** всего набора `stop_times` в
  промоут-транзакции; частичный микс запрещён; предыдущий набор — в
  `provenance_history`.
- **Частоты ≠ рейсы:** frequency-only маршруты не получают фабрикованных точных
  времён — держатся в staging до реальных времён (Yandex) либо моделируются как
  frequency (`frequencies.txt` в GTFS-компиляции).
- **Таймзоны:** хранение — UTC; tz — у place/terminal. Компиляция GTFS — в tz
  терминала отправления; `stops.timezone` проставляется из канона (per-stop);
  ядро/MCP — UTC. Межзонный кейс (Кемерово UTC+7 → Омск UTC+6) — обязательный
  кейс `golden_trips.json`. Sanity-валидатор подозрительных времён (§5.1):
  «отправление 03:00 городского рейса — подозрительно».

## 4. Операционная модель обработки

### 4.1 Хранение данных во время прогонов

| Уровень | Где | Назначение |
|---|---|---|
| Raw inputs | файлы `data/{reestr,yandex/cache,osm}` (read-only, `sha256`) | исходники |
| Workspace | staging-таблицы, помечены `run_id` | частичные прогоны |
| Canon | `terminals/routes/trips/...` | виден планировщику/админке, меняется только через promotion |

- **Разработка данных — Postgres** (текущая dev-БД, scratch-схема при
  экспериментах). **`DATABASE_DSN=memory` — только отладка кода/unit-тесты**;
  логика терминалов/синхронизации гоняется на Postgres.

### 4.2 Деление пула: терминальные чанки и трипы

**Терминалы:** единица работы — **chunk ≤ 100 терминалов**; деление
детерминированное: сначала **регион**, затем **тип транспорта**, далее id-range
до целевого размера. Один chunk = один `job type=sync_terminals_chunk` с
`payload={chunk_id}` — существующий воркер (`FOR UPDATE SKIP LOCKED`, backoff,
dead). Прогресс в админке: «прогон R: 42/57 chunks готово».

**Trips (Фаза 4):** единица работы — **трип/маршрут**, НЕ терминальный чанк
(`job type=sync_trips_attach`, `payload={route|trip raw}`; батчинг разрешён,
семантика — per-trip). Причина: реестр Минтранса — **межрегиональные** маршруты;
трип Кемерово→Новосибирск зависит от скелета двух регионов, деление «сначала
регион» для него бессмысленно.
- **Барьер:** attach по маршруту стартует только когда skeleton complete по всем
  регионам трассы (по `sync_chunks.state` терминального слоя); порядок между
  attach-джобами не нужен — межрегиональность решается барьером, а не очередью.
  Барьер с таймаутом/эскалацией: dead-чанк скелета региона не должен голодать
  attach навсегда — starvation-alert, после таймаута attach идёт в обычном
  режиме (неполные → staging, это штатный путь).
- **Ре-триггер по событию:** если трип застрял в staging из-за отсутствия
  терминала, промоушн/создание терминала (через op-флоу §5.4) публикует
  `terminal.created` в outbox → подписчик перепроверяет `staging_trips` этого
  региона трассы → автопромоушн. Это уже есть в модели outbox, но семантика
  барьера зафиксирована здесь.

### 4.3 Возобновляемость после рестарта/правки кода

- `plan_id = sha256(binary/commit + эффективный конфиг + sha256(raw inputs))`.
  `plan_id` — про идемпотентность обработки, не про свежесть: совпадение
  `plan_id_done` пропускает чанк независимо от новых `live`-ответов
  `geocode_cache`; свежесть уже обработанного — через TTL/
  `stale-while-revalidate` (§3.11) и freshness sweep Фазы 5.
- Chunk хранит `plan_id_done`: совпал — пропуск (в т.ч. после рестарта);
  изменился код/конфиг/входные данные → `plan_id` сменился → устаревшие
  располовиненные результаты не смешиваются, chunk перепрогоняется.
- Крах посередине: `state=running` с истёкшим `lease_at` забирается любым
  воркером. Идемпотентность строк — natural keys + `attribute_state.observed_at`.
- **Тест сходимости в CI (достижимый):** (а) no-op upsert — `observed_at`
  бампается только при изменении value/confidence; (б) diff — по канонической
  проекции (natural keys, без audit-полей `observed_at/last_verified_at/`
  `sync_run_id`); (в) герметичный replay — снапшот `geocode_cache` + фикстуры
  как вход, 0 сети (§5.0). Два прогона подряд → zero diff проекции;
  kill-mid-chunk → рестарт → канон цел и конвергентен.

### 4.4 Атомарность, откат, неудовлетворительное

- **Promotion: job ≠ транзакция.** Терминалы: одна chunk = одна транзакция.
  Трипы: job=route — батчинг, **промоушен-транзакция = трип**; смерть job не
  откатывает уже промоутнутые трипы (идемпотентный re-run их пропускает),
  итоги — per-trip в summary. Канон трогается только в промоут-транзакции.
  Фейл → `state=dead` + staging чистится, канон цел. Частичный успех прогона
  **виден**: `sync_run.summary` + `sync_chunks` по каждому чанку.
- Откат прогона: `DELETE FROM staging_* WHERE run_id=:R` + `sync_runs.state`;
  канон не трогается. **Хирургический откат вклада прогона** — по `sync_run_id`
  в `provenance_history` + `attribute_state` (§3.1, §3.9): удалить вклад →
  re-election победителей затронутых (entity, field) из оставшегося
  `attribute_state` → обновить колонки. Edge-case: поле осталось без кандидатов →
  значение `NULL` + сущность в `review_queue{low_confidence}` (enum не расширяем),
  actor-поля не трогаются.
- **Решения по неудовлетворительному — на уровне entity** (терминал/трип), а не
  chunk: `review_queue` upsert по natural key (§3.10); оператор rejected →
  причина исключается из автопромоушена, остальные 99 терминалов чанка
  продолжают жить (частичный успех не теряется). Принудительный пересбор
  исключённого — новым `plan_id`.

### 4.5 Логирование операций

- **Файл операций** на каждую обработку: `data/logs/sync_<run_id>.log.jsonl`
  (jsonl: `ts, stage, chunk_id, entity, action, result, error`), путь из
  `SYNC_LOG_DIR`/`sync.log_dir`.
- Назначение: анализ по «блокам» (chunks) и неудовлетворительным результатам
  после факта; failed-строки остаются (паттерн GTFS-tmp: успех — чистим,
  fail — оставляем). Сводка прогона дополнительно в `sync_runs.summary`
  **с проверкой баланса строк**: `in == out_matched + out_unmatched + out_rejected`
  на каждой стадии (см. §5.2).

## 5. Валидация данных и полнота матчинга

### 5.0 Replay-diff harness (инфраструктура первого класса)

Сырьё иммутабельно и хешировано, `plan_id` детерминирован → можно прогонять
старый и новый код на одинаковых входах и диффать канон. **Любое изменение
обработчика становится CI-проверкой «diff канона = ожидаемый»**, а не инцидентом
в проде. Реализуется в Фазе 1, используется на всех последующих (вход фиксаций —
`testdata/golden_*.json` + срез `sync_runs`). Герметичность: снапшот
`geocode_cache` — часть входа (verify-стадия в replay не ходит в сеть); diff —
по канонической проекции (§4.3). Это главный инструмент против
«неверно построенных обработчиков».

### 5.1 Валидаторы правдоподобия (two-tier)

Слой validators между attach и promotion. **Hard-правила** — физически
невозможное → `state=dead` + loud error (не в `review_queue` — это не предмет
решения оператора):

- неубывание времён в `stop_times` (`arr ≤ dep`, времена не убывают по `seq`);
- отрицательная длительность;
- скорость выше физического предела по mode (автобус × 200 км/h — мусор).

**Guard интерполированной геометрии:** hard-проверка скорости применяется
только если `geom` обоих концов финализирован (правило §3.10: ≥2
`origin='live'`/manual, не `seed`/интерполяция). Иначе тот же сигнал уходит
в soft — `review_queue{low_confidence}` с указанием leg и источника
координаты, а не в `dead`.

**Soft-правила** — подозрительное → `review_queue` с причиной (не dead, иначе
ложный reject убьёт валидные рейсы): детур-рейшн/«зигзаг» (у межгорода легитимен
заезд в посёлок), несогласованность distance/duration, аномальные времена
(«03:00 городского рейса»). Кольцевые рейсы разрешены (повторный визит того же
терминала); валидируются только реально присутствующие времена.

Нарушение hard → `dead` с сохранением диагностики (затем — в `sync_runs.summary`).
Тихая порча → сборка стоп-сигнала: неконсистентный источник падает на
availability-метриках здоровья коннектора.

### 5.2 Контракты сырых входов и баланс строк

- **Raw-schema contracts** на структуру сырых входов (набор и типы колонок,
  ожидаемые диапазоны): `sha256(raw)` защищает от подмены файла, но не от того,
  что Минтранс поменял колонки XLSX; обработчик обязан fail-loud при дрейфе
  формата, а не молча прочитать не те поля (сценарий «200 OK с неверными
  данными»). Contract = фикстура + явная таблица «колонка/тип/диапазон» в тесте
  адаптера (формат контракта зафиксирован здесь, не термин).
- **Row-reconciliation per stage** в `sync_runs.summary`: сколько вошло = matched +
  unmatched + отфильтровано (+dead), без «молчаливых потерь»; при несходимости —
  fail-loud.

### 5.3 Tombstone и GC (стратегия удаления)

- Каждый прогон: diff «источник vs канон» по natural key **routes, затем trips** →
  исчезнувший route → `valid_to` + каскад на его трипы; исчезнувший трип живого
  route → `valid_to`. SCD2-версионирование на trips не вводится (не нужен
  ретроспективный рейс — достаточно soft-delete). Планировщик затаймстоуненное
  не видит.
- **Воскрешение — source-driven и идемпотентно:** появление при `valid_to NOT NULL`
  → сброс `valid_to`, тот же ID (never-reuse цел), запись re-observation.
  Воскрешение route сбрасывает его `valid_to`; восстановлению подлежат **только
  трипы, присутствующие в текущем источнике** (не все исторические) — иначе
  resurrect-фантомы.
- **Инвариант живости:** трип в каноне ⇔ все его терминалы живы. Терминал получает
  `valid_to` (SCD2) → авто-tombstone зависимых канонических трипов + review.
- **Churn-alert**: если за прогон исчезло/изменилось >N% трипов — стоп прогона и
  ручная проверка (вероятно, реестр перенумеровал коды или сломал выгрузку), не
  тихая перезапись всего диапазона.
- Протухшие `staging_trips` (старше порога) — dead-letter + alert (expiry job).

### 5.4 Coverage-gate скелета и операторский флоу новых терминалов

- **Coverage-gate между Фазами 3 и 4:** измеряется % стопов Минтранса,
  матчащихся на скелет, по регионам. Метод измерения: доля стопов реестра
  с кандидатом выше мягкого порога `ScorePair` (калибруемо, не «на глаз»).
  При <N% в регионе трассы — attach по этому региону не стартует; расширяется
  скелет (добор OSM/дамп), а не «подгружаются» рейсы поверх пустоты. Значение N —
  конфиг `sync.coverage_gate`, стартовый N по результатам Фазы 3 на Кузбассе.
- **Операторский путь для несопоставленных стопов:**
  `review_queue{incomplete_trip}` → кандидат позиции (интерполяция, ниже) →
  оператор подтверждает/создаёт терминал (`actor_id` уже фиксируется в
  `attribute_state` — дамба не нарушена) → `outbox` → автопромоушн трипа.
- **Интерполяция позиции вдоль маршрута** (стандартный приём для recall):
  unmatched стоп между двумя сматченными соседями по `seq` получает оценку
  координаты из геометрии линии (доля пути по `duration_s`/`distance_m` между
  соседями). Если нет ни времён, ни дистанций (реально для реестра) — fallback
  seq-пропорционально с честным низким confidence. Оценка помечается в кандидате
  (`confidence` от длины перегона), не финальна до подтверждения оператором.
  Терминал, созданный из такой оценки, несёт низкую достоверность `geom`
  (`origin='seed'` + низкий `confidence` в `attribute_state`) до независимого
  подтверждения; см. guard интерполированной геометрии в §5.1.

## 6. Реестр депрекации (по мере реализации)

| Элемент | Статус | Триггер удаления |
|---|---|---|
| `internal/adapters/mintrans/importer.go` (`ImportIntercity*`) | Вырезан из сервера (старт + `sync_mintrans` + `POST /api/v1/import/mintrans`→409); файл оставлен как референс до паритета на новом конвейере | паритет `skeleton-sync`+`trips-sync` на `regions.json` |
| `internal/import/pipeline.go` | Deprecated | перенос стадий в `internal/sync` |
| `internal/geo/resolve.go` (`GeoResolver`, геокодинг-по-имени) | Deprecated | verify-стадия + `geocode_cache` |
| `internal/verification/verify.go` `VerifyTerminal` | Удалён; `haversineMeters` переехал в `scorepair.go` | — |
| `scripts/extract-minstran.py`: генер. `*_geo.json` | прекращается | после seed |
| `data/reestr/missing_stops.json` | датасет → review-сигнал | после миграции Фазы 0 |
| `data/reestr/{yandex,nominatim}_geo.json` | seed → архив | после seed |
| `GEOCODE_MAX_CALLS` / `GeoResolver.maxCalls` | Deprecated → квота БД + TTL | после Фазы 2 |
| `internal/geo/places.json` | без изменений, но **ревизуется** при переключении планировщика на Store(Postgres) | отдельный пункт Фазы 6 |
| `internal/providers/intercity` (JSON-legacy) | Планировщик переключён на Store (`networkForDay` → `LoadNetwork` из канонической БД; intercity остаётся fallback-провайдером реестра) | Фаза 6 — вырезание после паритета покрытия (store-сеть ≥ legacy-сеть на всём срезе реестра) |

Правило: после каждого пункта — `make check-deprecated`; срок жизни Deprecated
ограничен фазой паритета.

## 7. Стратегия миграций (правило этой фазы)

- До первого прод-деплоя с реальными данными: **одна консолидированная миграция
  `migrations/001_initial.sql`**, всю DDL новых сущностей добавлять в неё,
  **без создания нового слоя миграций** (`002_*.sql`). Dev/staging — пересбор
  `DROP SCHEMA + миграция`.
- **Решения оператора (overrides) при пересборе не теряются:** ручные правки,
  merges, резолюции `review_queue` экспортируются в файл датасета (Фаза 1) и
  применяются после миграции — это данные, а не побочный эффект интерактива.
  **Датасет ключован по внешней идентичности** (OSM id, `yandex_code`, natural key
  стопа Минтранса) и применяется через тот же резолвер, что матчинг; internal ID
  (`BIGSERIAL`) — только внутри БД: после `DROP SCHEMA + миграция` ID меняются
  (порядок вставок), привязка к ним ляжет или применится не к тем сущностям.
- С момента прод-деплоя: `001_initial.sql` замораживается, дальнейшие изменения —
  аддитивные `002_*.sql` (`ALTER` без разрушения данных, инвариант AGENTS).
- Ответственный за переход — обновить §4-§7, `15-dev-status.md` и AGENTS.

## 8. Наблюдаемость и метрики

- Структурные логи (`slog`) с корреляцией по `run_id/chunk_id/entity`, файл
  операций (§4.5). Все долгие операции принимают `context.Context` и уважают отмену.
- KPI в дашборде:
  - `% обогащённых терминалов` (`enrichment_status`), `identity_only` — очередь
    на дообогащение;
  - `% полных рейсов` по региону (`canonical_trips / total_parsed`);
  - **match-rate по регионам** (стопы реестра → скелет) — вход coverage-gate и
    сигнал расширять скелет;
  - **churn-alert**: % исчезнувших/изменившихся трипов за прогон (стоп при >N%);
  - `v_stale_attributes` — устаревание полей и возраст расписаний (по
    `observed_at`/`last_verified_at`), со «сложением» причин для ресинка;
  - застрявшие `staging_trips` старше порога — alert/expiry job (dead-letter);
  - ночной recompute-джоб `transport_types`, сверка агрегата (как closure);
  - **FP-оценка матчинга** по выборочному аудиту (acceptance sampling: N-сэмпл
    из verified по прогону → ручная проверка → FP-рейт в KPI).
- Золотые наборы (в CI, precision/recall по каждому `task` ScorePair):
  - `testdata/golden_terminals.json` — identity-кейсы («должен/не должен быть
    терминалом») + merge-кейсы («должен/не должен слиться», включая переезд
    ссылок);
  - `testdata/golden_trips.json` — кейсы стоп→терминал («должен/не должен
    сматчиться», включая sequence-монотонность и интерполяцию, в т.ч.
    интерполированный конец + завышенная скорость → `review`, не `dead`). Наборы по регионам
    не пересекаются с калибровочной выборкой Кузбасса.

## 9. Критерии готовности плана

- F0–F3: канон терминалов строится из OSM+Yandex без внешних запросов в базовом
  пути; старые записи сматчены/в review; `missing_stops`-сигнал сходится в review
  с кандидатами, а не в `(0,0)`; покрытие скелета измерено по регионам.
- F4: полные рейсы Минтранса в каноне (вход — coverage-gate с измеренным методом),
  частичные — в staging с видимой метрикой; пороги рекалиброваны на регионах
  трасс; `trips` ресинк идемпотентен (natural key), цены/длительности с источником
  и `method`; фантомов нет (two-tier validators + tombstone); zero-diff harness
  зелёный.
- F5: устаревание видно, точность матчинга измерена золотым набором по обоим
  `task`'ам (terminals + trips), FP-рейт по аудиту в норме.
- F6: `make check-deprecated` чисто; депрекация по §6 выполнена; GTFS-продукт
  проходит provenance-фильтр и GTFS Validator.

## 10. Overpass + settlement-backfill + two-tier (мини-блоки, текущий фокус)

Решения, зафиксированные здесь (источник истины, не чат): Overpass — сборщик
маршрутов в паре с Яндекс Расписанием (§1.2); коннектор реализует
`geocoder.MultiGeocoder` и висит на том же `cache+quota`, что Nominatim/Yandex
(§3.7); `--overpass-max` по умолчанию **200**; зеркала — main + openstreetmap.fr,
kumi исключён. **Лицензионный waiver:** данные OSM/Overpass потребляются только
внутри сервиса (планировщик), распространение датасетов не предусматривается —
строка риска не ведётся, решение зафиксировано этим абзацем. Порядок: сначала
O-блоки (коннектор), затем S-блоки (settlement), затем C-блоки (two-tier);
каждый блок — независимо проверяемый дифф (`gofmt` + `go vet` + `make test`).

### O. Коннектор Overpass (по лекалу `adapters/yandex`, переиспользование)

- **O-1. Каркас пакета.** `internal/adapters/overpass/overpass.go`: структура
  `{cfg, client, baseURLs}`, `New(cfg, httpx)`, конфиг `overpass.url`, QL-билдер
  bbox-запроса. Готово: компилируется, юнит на текст QL. Без сети.
- **O-2. Регистрация в `geocoder`.** `init()` → `geocoder.Register("overpass")`;
  `GeocodeCandidates` (поиск остановок по имени в bbox), `Geocode` = top1
  кандидатов, `Reverse` — явная ошибка «не поддерживается» (reverse остаётся за
  Nominatim). Готово: юнит через `httptest`-сервер.
- **O-3. Парсер ответа.** Overpass JSON (`elements`: node/way/relation, теги,
  `members` с ролями) → `model.AdaptedRecord` (Kind=terminal, идентификаторы
  `{osm, osm_id}`, `Extra`: settlement/transport_type, `Source: "osm"`).
  Фикстуры `testdata/overpass/stations.json` + пустой ответ. Готово: юнит без сети.
- **O-4. `StationsAround` + путь `cache+quota`.** Точечный добор
  `node(around:R,lat,lon)`; ключи кэша `overpass:`, квота `api_quotas` на `osm`,
  пейсер ≥1.2с (переиспользование `cached.go`/пейсера, не новьё). Готово: юнит с
  фейк-кэшем/квотой, повторный вызов — hit без квоты.
- **O-5. `FetchRouteRelations`.** QL `relation["route"~"bus|trolleybus"]["ref"~...]`
  + рекурсия `>>` за членами; парсер строит скелет `AdaptedTrip`: упорядоченный
  список стопов с OSM-ID + `ref`/`operator` как identity-доказательство. Фикстура
  relation в `testdata/overpass/`. Готово: юнит, порядок членов сохранён.
- **O-6. Ручной скраппинг.** `scripts/overpass-collect.sh` по образцу
  `yandex-collect.sh`: вход — unmatched-список attach, сырьё —
  `data/overpass/raw/` (не коммитится), сводный офлайн-дамп
  `data/overpass/stations.json`. Готово: одна пачка собрана руками.
- **O-7. Gap-fill в `skeleton-sync`.** После join: непарные Yandex + unmatched
  стопы реестра → `StationsAround` по их координатам → кандидаты в пул
  верификации `ScorePair` (первый verified побеждает). Флаги `--overpass-max`
  (дефолт 200) и `--no-overpass`. Готово: dry-run показывает число добора.
- **O-8. Hub-цепочка.** Fallback привязки: локально → Yandex-дамп → Nominatim →
  Overpass (структурный поиск `highway=bus_station` + имя); разовая проверка
  Кемерово/Новокузнецк/Томск/Толмачёво с сохранением в объекте (OVERRIDES
  заменяются цепочкой); bbox на запад ~82.0 (конфиг `sync.bbox`). Готово: хабы
  привязаны, цепочка задокументирована.
- **O-9. Вторая пара глаз.** Кандидат из Overpass — в пул `duplicate_ambiguous`
  вместо третьего перебора тех же дампов. `Join` теперь детектор `duplicate_ambiguous`
  (`gap < Ambiguity=0.05` и `score >= threshold - margin` с 2+ кандидатами):
  сомнительный match уходит в `DuplicateAmbiguous` (а не в `Canon`), второй
  кандидат — в `Unverified`. Golden `TestJoinDuplicateAmbiguous`: два Яндекс-кандидата
  с nameSim 1.0 и 0.9 на одинаковом расстоянии → gap 0.045 < 0.05 → ambiguous.
- **O-10. Yandex-only с координатами — в канон.** Правило существования §1.2
  смягчается: Yandex-only с координатами принимается с `confidence=0.4` + provenance
  (`provenanceSources` через `IdentityOnly` → только yandex-system), вместо review.
  Без координат — всё ещё `review_queue{skeleton_unverified}`. Реализация:
  `PromoteSkeletonChunk` роутит `Unverified` с координатами → `promoteJoined` с
  `Score=0.4, Enrichment=IdentityOnly`; без координат — `reviewUnverified`.
  `TestPromoteSkeletonChunkYandexOnlyWithCoords`: 2 unverified (с/без координат) →
  1 written + 1 review.

### S. Settlement-backfill (Yandex-first: сначала бесплатно, потом квота)

- **S-1. Офлайн-матч (0 квоты).** Сопоставление 1982 терминалов с поселениями
  Yandex-дампа по имени → `terminal_tags['settlement']` только где пусто.
  Готово: метрика `tags->>'settlement' <> ''`: 205 → **>1500**.
- **S-2. Остаток — Nominatim.** `ReverseSettlement` по остатку (квота+кэш,
  пейсинг). Готово: dry-run attach, settlement-матчи кратно выросли от базы 7.
- **S-3. Rural-класс.** Переключатель rural-класса для минтранса только после
  S-1–S-2 (без backfill даёт +7 стопов — замерено, вхолостую не тратить).
  Готово: dry-run verified-rate до/после.
- **S-4. Метод gate.** Замер выполнен CI-тестом `TestGateMethodSoftVsVerified`
  (`CompareGateMethods` в `calibrate.go`; сценарии full/noisy-555м/empty).
  Результаты: инвариант `verified-rate ≤ soft-rate` подтверждён на всех
  сценариях (verified ⊂ soft — каждый verified-стоп проходит и soft-порог);
  на зашумлённом индексе (+555м) soft держит 1.0 (гео-фича в полосе порога),
  verified честно падает в 0.0 (hard-guard режет сдвинутую геометрию).
  **Решение:** coverage-gate меряет **soft-rate** (есть ли кандидат вообще —
  сигнал расширять скелет), attach-движок продолжает требовать **verified**
  (двухуровневая дамба). Расхождение осей на регионе — стопы «кандидат есть,
  но не верифицируется» — зона operator-флоу §5.4 (интерполяция/подтверждение),
  а не причина блокировать или форсировать attach. `sync.coverage_gate`
  (soft-порог) и `verification.*` (verified-порог) калибруются независимо.

### C. Two-tier attach (после O+S)

- **C-1. Флаг `is_provisional`.** `stop_times.is_provisional bool DEFAULT false`;
  6 точек выполнены: DDL (миграция на scratch-схеме — 0 ошибок), `StopTimeRow.IsProvisional`
  (+`MatchedStopTime.IsProvisional`, `model.StopTime.IsProvisional`), pool/tx
  `UpsertStopTime` (INSERT + ON CONFLICT UPDATE), memory (структура целиком),
  движок attach (`matchStops`: `IsProvisional = !terms[idx].GeomFinalized` — identity_only
  терминал → provisional stop_time; тест `TestMatchStopsProvisionalFlag`),
  `LoadNetwork` postgres/memory (SELECT `is_provisional` → модель) + CSA-ветка
  (`Network.ProvisionalStops` в `BuildIndexes`; `relax` не выходит пешком из
  provisional и не входит в provisional — посадка/высадка поездом да, пересадки нет;
  тест `TestPlanProvisionalStopNoTransfer`). `pickup_type/drop_off_type` не тронуты
  (чужая GTFS-семантика).
- **C-2. Валидаторы на склейке.** Монотонность/скорость проверяются на новой
  форме входа (порядок стопов из Overpass × времена из Яндекса) — явный тест,
  что hard-валидаторы корректны именно на ней (писались под другую структуру).
  `rejected`-промежуток → промоут рейса без этого стопа (решение). Готово: тест
  склейки зелёный.
- **C-3. Реран attach.** Полный цикл до первых promoted + конвергенция ресинка.
  Готово: promoted > 0, ресинк без dupes.

### D. Entity-resolution пробелы attach-пайплайна (по итогам внешнего ревью, принято)

Ревью сопоставило attach-пайплайн с эталонным record-linkage (normalize →
block → score → assign → threshold → persist). Архитектура подтверждена
(GTFS-схема, multi-feature score, три зоны confidence), вскрыты структурные
пробелы на масштабе РФ. Причины возникновения задокументированы, порядок
фиксации — по рычагу/дешевизне. Каждый блок — независимо проверяемый дифф
(`gofmt` + `go vet` + `make test`).

- **D-1. Blocking в `matchStops` (пробел №1 ревью) — выполнено.** `internal/sync/trips_match_index.go`
  `matchIndex`: гео-ячейки 0.5° + соседи ±1, пул стопа = гео-окно ∪ код-совпадения ∪
  бескординатные терминалы (код-матч проходит гео-окно — golden «тёзка за 79 км»
  держит, голый `Nearest` отвергнут при дизайне). Индекс hoist per-run (`AttachTrips`
  строит один раз, не per-trip). Тест `TestMatchIndexBlocking`: далёкий без кода —
  не кандидат, далёкий с кодом — кандидат, no-coords стоп — только no-coords пул.
- **D-2. Greedy exclusivity внутри рейса (пробел №2 ревью) — выполнено.**
  `used` по terminalID в `matchStops`: занятый терминал недоступен следующим
  позициям рейса; кольцевой возврат на терминал первого стопа на последней
  позиции — легитимен (§5.1), 2-позиционные дубликаты — нет. Тесты
  `TestMatchStopsExclusivityWithinTrip` (тёзка → unmatched, не дубль) +
  `TestMatchStopsCircularRouteAllowed` (кольцо через другой терминал цело).
- **D-3. Backbone-промоушен (пробел №3 ревью) — выполнено.** Трип с verified-концами
  и дырками в середине промоутится без непроверенных стопов (доля verified ≥ 2/3,
  минимум 2 стопа) — all-or-nothing снят; концы строгие (без них — staging целиком).
  Перегоны через пропуск валидируются на эффективных соседях (`checkSpeeds` на
  collapsed — склеенный перегон считается честно, overspeed ловится; закреплено
  `TestBackboneGapSpeedOnEffectiveNeighbors`). Счётчики `mid_gaps`/`gapped_promoted`
  в отчёте — вход для KPI «% полных рейсов» и операторского добора середины (§5.4).
- **D-4. Трассируемость матчинга (пробел №4 ревью) — выполнено.**
  `stop_times.match_score real` + `match_method text CHECK IN ('code','scorepair')`
  (аддитивно в `001_initial.sql`, миграция scratch-схемы — 0 ошибок);
  `MatchedStopTime.MatchScore/MatchMethod` из `matchStops` (code при код-матче),
  персист в `persistPromotedTrip` через `StopTimeRow.MatchScore(*float64)/MatchMethod`,
  round-trip `TestPersistMatchScoreMethodRoundTrip`. Вход для §8 FP-аудита
  (выборка маргинальных матчей) сохранён.
- **D-5. Единая формула скоринга (дубль ревью) — выполнено.** Общее тело
  `scorePairBody(a, b, cfg, nameSimFn)`: `PairScore` — прямой namesim,
  `JoinPager.pairScoreCached` — мемоизированная обёртка. `Join()` не удалён —
  живой потребитель `legacy_match.go` (вход 1×N, blocking там бессмыслен).
  Existing-тесты зелёные без правки ожиданий.
- **D-6 (backlog, не этой фазой).** Sequence-context как активный сигнал
  disambiguation (направление рейса — tie-break при ambiguity с
  подтверждёнными соседями); McRAPTOR вместо эвристики Парето-альтернатив
  (`paretoAlternatives` — сэмплинг, не полный фронт). Зафиксировано, не
  блокер текущей фазы.