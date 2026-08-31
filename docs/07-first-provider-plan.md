# 07. План: первый провайдер междугородних автобусов

Статус: **утверждён, реализуется**. Этап 0 закрыт, Этап 1 — частично (см.
«Выполнено»). Это рабочий план добавления первого
крупного провайдера междугородних автобусов РФ. Прогресс по этапам — секциями
«Выполнено» в конце каждого этапа.

## Цель и охват

Добавить провайдера `intercity`, дающий реальные маршруты между городами по
регионам: **Новосибирская, Кемеровская, Томская области, Алтайский край**
(первые сквозные сценарии: НСК↔Барнаул, НСК↔Томск, НСК↔Кемерово).

## Решения (зафиксированы с владельцем)

| Вопрос | Решение |
|---|---|
| Источники | Реестр Минтранса РФ (каркас; **при наличии** «файла с точным временем» — источник времён), Яндекс Расписания + Геокодер (ключи есть, exact-времена, таймзоны), OSM (координаты, метчинг) |
| Если файла времён нет | Реестр сводится к метаданным (перевозчик, №) — приоритет Яндеkc + синтез времён из частот, помечается `estimate` |
| Расписания | exact (файл времён / Яндекс) → estimate (синтез из «отправлений в день»); источник времени отражается в данных |
| OSM | Слой гео-меток (автовокзалы/автостанции/остановки) + метчинг; **без** пешеходного графа на первом провайдере (access/egress — гаверсином) |
| Сбор OSM | Geofabrik `russia/siberian-fed-district` (покрывает все 4 региона) → `osmium tags-filter` → JSON-датасет |
| Хранение | In-memory + JSON-датасеты/снапшоты на диске (`data/`), TTL-рефреш; PostgreSQL/PostGIS — позже при росте. Канон. модель пересмотрена до кодинга (см. «Пересмотр канон. модели»): route/trip/service, canonical carrier, station/provider_stops, версия импортов |
| Координаты ОП | В реестре отсутствуют; геокодинг из XLSX-гео-текста маршрутов (`G`/`H` — названия нас. пунктов и дорог) + OSM-метчинг + Геокодер Яндекса; цель — покрыть >124/255 ОП, имеющих координаты сейчас |
| Время | Все `time.Time` в UTC; таймзона (IANA) — на каноническом `Stop`; локальные времена — в выводе |
| Маркировка | Provenance на запись: `Source` (`reg`/`yandex`/`osm`/`synth`) + `TimeBasis` (`exact`/`estimate`); метрики по источникам |
| Тесты | Живой Яндекс не нагружается: фикстуры из точечного сбора, адаптер — за тест-даблом |
| avtovokzaly.ru | Не источник на этом этапе; только ручной верификатор, кандидат на скрейпинг (Этап 6 roadmap) |

## Зависимости окружения

- Инструменты сбора (в основной Go-модуль **не** входят):
  - `tools/osm-extract` — отдельный Go-модуль (qedus/osmpbf): PBF → JSON.
    Замена осмини CLI: у osmium-tool нет бинарных ассетов в релизах, pyosmium
    недоступен для Python 3.14. Собирается один раз, результат — JSON.
  - `curl`, `jq` и `python3` для XLSX/анализа.
- Секреты: `YANDEX_RASP_KEY`, `YANDEX_GEOCODE_KEY` — только через env, в
  репозиторий не попадают.

## Этап 0 — разведка и решения (3 действия)

1. **Реестр**: актуальные выгрузки по 4 регионам (opendata CSV и/или XLSX
   «по состоянию на дату» из раздела «Документы» mintrans); найти «файл с
   точным временем отправления/прибытия»; зафиксировать схему и объёмы;
   быстро оценить avtovokzaly.ru (есть ли открытый JSON).
2. **Яндекс**: ключи и лимиты в конфиг; `scripts/yandex-collect.sh` — точечный
   сбор фикстур (3 пары городов) в `testdata`; проверить `/stations` (таймзона,
   координаты).
3. **OSM**: скачать Geofabrik СФО, конвертировать (tags-filter) в JSON-датасет
   автовокзалов/остановок; оценка объёма; мини-вырезка для тестов.

**Критерий выхода**: решено «реестр при наличии», собраны фикстуры Яндекса,
готов OSM-датасет, схема и объёмы всех источников зафиксированы, риски
уточнены.

## Этап 1 — модель и адаптеры (7 действий)

4. Модель: пересмотр канон. сущностей (см. «Пересмотр канон. модели»):
   `Source`/`TimeBasis`, таймзона (IANA) на `station`; разделение
   `route`/`trip`/`service` (календарь вынесен), `services`+`service_days`+
   `service_exceptions` (вместо `service_days TEXT`); `carrier`→canonical +
   `carrier_providers`; `station`+`provider_stops`(+`station_alias`/
   `station_sources`) вместо `stops`/`primary_provider`; версия импортов
   (`import_id`); формализованные `transfers` (не только пешие). Время в UTC.
5. CLI-парсер реестра → нормализованный JSON-датасет (маршруты/пункты/
   перевозчик; времена — если файл найден).
6. Геокодинг пунктов → координаты + таймзона (Геокодер Яндекса, кэш в датасет;
   резерв — OSM-метчинг).
7. Адаптер Яндекса: маппинг реестровых маршрутов на станции, `/schedule`+
   `/search` из фикстур, exact-времена.
8. OSM-слой: чтение JSON-датасета → канон `Stop` (Source=`osm`), метчинг с
   реестром/Яндekc (название + радиус города).
9. Унификация времён: exact → estimate (синтез из частот), source-метки.
10. `Provider intercity`: ленивый импорт из датасетов, snapshot-кэш, TTL-
    рефреш, бюджет квоты Яндекса.

## Этап 2 — интеграция с ядром (3 действия)

11. ID-политика (`reg:*`/`yandex:*`/`osm:*`) + смягчение жёсткого
    конфликт-фейла в `App.network()`.
12. Сквозные сценарии НСК↔Барнаул/Томск/Кемерово + стыковка с synth; таймзоны
    в выводе `find_route`.
13. Gap-детекция/self-leg для непокрытых фрагментов.

## Этап 3 — качество и документация (4 действия)

14. Юнит-тесты: парсеры, метчинг, таймзоны, синтез времён (фикстуры).
15. Сценарии в `test/common` + integration; smoke остаётся офлайн.
16. `make test`, `go vet`, `gofmt`.
17. Доки 04/05/06, `config.example.yaml`, `AGENTS.md`, `scripts/api-demo.sh`.

## Итоговая оценка

17 действий, ~8–10 дней (один разработчик). Риски: файл времён не найдётся
(→ Яндекс + синтез), квота геокодера, расхождение актуальности реестра
(XLSX vs opendata CSV), недоступность внешних доменов.

## Выполнено

### Этап 0 (разведка) — закрыт

**0.1 Реестр Минтранса.**
- Скачан актуальный «Реестр действующих межрегиональных маршрутов…»:
  `data/raw/minstran/reestr.xlsx` (файл 563368, 6,15 МБ, 5 листов). Качается
  с реферером `https://mintrans.gov.ru/documents/8/15657`; отдаётся как
  application/zip.
- «Файл с точным временем» **найден**: листы «Расписание прямое» (28082
   строки) и «Расписание обратное» (28180) всех межрегиональных маршрутов.
   Схема колонок: 0 рег.номер, 1 наименование, 3 ОП, 4 код региона ОП,
   5 рег.номер ОП; зимний блок cols 6–11 (дни отпр, время отпр, стоянка,
   дни приб, время приб, период), летний блок cols 12–17 (аналогично).
   Времена рейсов через `;`, `нет` = нет отправления, период — «круглогодично»
   или «с … по …». Координат в реестре нет (нужен геокодинг).
- Полный состав листов XLSX (проанализирован исходник, не `regions.json`):
  - **Маршруты** (5578): `A` рег.номер (PK), `B` порядковый №, `C` наименование,
    `D` перевозчик, `E` порядок посадки/высадки, `F` вид регулярных перевозок
    (нерегулируемые/регулируемые тарифы), `G`/`H` **гео-текст** маршрута
    (названия нас. пунктов и дорог Р-217/А-157 — источник геокодинга ОП),
    `I`/`J` протяжённость прямо/обратно, км (оценка длины/скорости рейса),
    `K` реквизиты решения, `L` дата решения, `M` дата начала перевозок
    (актуальность).
  - **Виды и классы ТС** (5578): `C`–`G` кол-во ТС по классам
    (ОМ/М/С/Б/ОБ), `H` сумма, `I` экологический класс (`Евро-3`) —
    обогащение маршрута вместимостью/экологией.
  - **Перевозчики** (6151): `C` наименование, `D` ИНН, `E` ОГРН/ОГРНИП,
    `F` место нахождения (адрес), `G` email — **больше**, чем в `regions.json`
    (там только имя+ИНН).
  - **Расписание прямое/обратное** (28084/28182): см. выше; это первоисточник
    exact-времён (не доверяем агрегату `regions.json`).
- `scripts/extract-minstran.py` (python3+lxml): XLSX → JSON по регионам.
  Результат `data/reestr/regions.json`: **351 маршрут** (все маршруты,
  затрагивающие 4 региона, включая конечные за пределами — напр.
  Горно-Алтайск), **255 остановок**, **702 блока** (прямое+обратное).
  Контрольный пример `54.22.078` НСК→Барнаул: stop `op:54:54098` … конечная
  `op:22:22165`, `winter.dep=["12:30","12:45",…]`.
- avtovokzaly.ru: открытого JSON-API нет (JS-бандл + Yandex Maps JS).
  Решение подтверждено: только ручной верификатор.

**0.2 Яндекс — подготовлено, фикстуры не собраны (нет ключей в env).**
- Скрипт `scripts/yandex-collect.sh` создан (см. раздел ниже). Запуск требует
  `YANDEX_RASP_KEY`/`YANDEX_GEOCODE_KEY` в окружении — ключи у владельца.

**0.3 OSM — датасет готов.**
- Скачана Geofabrik вырезка `russia/siberian-fed-district` (260829, 583 МБ) →
  `data/raw/osm/sfo.osm.pbf`.
- `tools/osm-extract` (Go, qedus/osmpbf, 2 прохода: way-геометрия через нужные
  node-координаты) → `data/osm/stations.json`: **24895 объектов** (nodes+ways),
  4,8 МБ. Теги: `amenity=bus_station`, `building=bus_station`,
  `public_transport=station` (без `railway`), `platform bus=yes`,
  `highway=bus_stop`. Только имя/оператор/ref + координаты.
- Целевые автовокзалы на месте: Новосибирский автовокзал-Главный
  (55.0411,83.0274), Автовокзал «Барнаул» (53.3523,83.7591), Томск
  (56.4612,84.9912), Кемерово (55.3416,86.0610).

**Выводы этапа 0.**
- Решение «реестр при наличии файла времён» подтверждено данными — `exact`
  времена доступны офлайн, без Яндекса.
- Данные не коммитятся: `data/` добавлена в `.gitignore`; сырьё и датасеты —
  рабочие артефакты сбора.
- Риск уточнён: OSM-метчинг остановок надёжен для автовокзалов, для рядовых
  ОП нужен Геокодер Яндекса (координаты в реестре отсутствуют).

### Пересмотр канон. модели (архитектурные проблемы мультимодального роутера)

Анализ выявил 6 проблем, которые проявятся именно в сервисе маршрутизации.
Канон. модель (`internal/model`) и набросок БД пересматриваются **до** кодинга
Этапа 1, чтобы не переделывать адаптеры/парсер later. PostgreSQL/PostGIS не
подключён (каркас), но схема фиксируется заранее. Координаты — `numeric(9,6)`
(или `geometry`); время — UTC; даты календаря — `INTEGER` (YYYYMMDD).

**1. Разделение route / trip / service (вместо смешения route→trip→frequency→stop_times).**
Календарь вынесен из trip в отдельную сущность `service`:
- `services(id, provider_id→providers.id, name, start_date INT, end_date INT)`
- `service_days(service_id, weekday INT, PK(service_id, weekday))`
- `service_exceptions(service_id, date INT, exception_type INT -- add/remove, PK(service_id, date))`
Даёт будни/выходные/сезоны/праздники/отмены рейсов — вместо `service_days TEXT`
и `period TEXT`, которые сейчас смешивают расписание и календарь.

**2. `trip` = конкретный рейс, без календаря.**
`trip(id, route_id→routes.id, service_id→services.id, provider_id, direction,
external_uid, headsign, frequency_flag DEFAULT 0, valid_from INT, valid_to INT,
UNIQUE(provider_id, external_uid))`. Календарь уехал в `service`; `external_uid`
уникален в рамках провайдера (Яндекс `thread.uid` — дедуп/кэш, не PK).

**3. `routes.external_code` → нормальный `external_id`.**
`routes(id, provider_id, carrier_id→carriers.id, external_id NOT NULL,
short_name, long_name, mode NOT NULL, UNIQUE(provider_id, external_id))`.
`external_code TEXT` (реестр-рег / Яндекс `from+to+ttype`) опасен: один и тот же
`from+to+ttype` даёт разные перевозчики/наборы ОП. Идентичность маршрута —
только по реальному внешнему ID провайдера; если ID нет — `generated:<hash>`,
исходные компоненты храним отдельно (не в PK).

**4. `carriers` → canonical + provider-представление.**
- `carriers(id, name, inn, iata, icao)` — канонический перевозчик (РЖД — один).
- `carrier_providers(id, carrier_id, provider_id, external_code, external_name,
  address, UNIQUE(provider_id, external_code))` — представление в источнике
  (`Yandex carrier`, `RZD carrier`, `Mintrans carrier`). Дедуп/объединение
  источников по `carrier_id`.

**5. `stations` / `stops` — добавить уровень alias (направление верное).**
`station` = физическая сущность (canonical); `provider_stops` (бывш. `stops`) =
представление провайдера. «Москва Ярославский» / «Москва, Ярославский вокзал»
/ «Москва (Ярославский)» — aliases одного `station`. `stops` переименовать в
`provider_stops`, чтобы по имени было ясно, что это не физическая остановка.
`station_alias(station_id, provider_id, name)` для вариантов написания.

**6. Убрать `primary_provider` из `stations`.**
Физическая станция не принадлежит провайдеру (Казанский вокзал представлен
Yandex/RZD/Mintrans). `primary_provider` — атрибут качества матчинга, не
сущности. Заменить на `station_sources(station_id, provider_id, confidence REAL,
is_primary INT)` или оставить на уровне `station_codes`.

**Версия импортов (разделение данных импорта от актуального состояния).**
`imports(id, provider_id→providers.id, started_at INT, finished_at INT,
status TEXT, records INT, source TEXT, checksum TEXT, version TEXT)`.
У импортируемых сущностей `import_id INTEGER` — позволяет rollback, сравнение
расписаний, диагностику плохого импорта, A/B загрузчиков. Не терять историю
«почему вчера было так, а сегодня иначе».

**7. Ночные рейсы (переход через 00:00) — реализация и алгоритмы.**
Сейчас в `intercity.addRun` (intercity.go:300–315): накопление `prevEff` +
`arrOff`/`depOff` * 1440 мин при монотонном возрастании времени по остановкам.
`time.Time` в UTC — алгоритм работает для 23:40→07:20 на след. день. **Текущие
баги:**
- `dayBase = 00:00 UTC` жёстко, не учитывает таймзону первой остановки.
- Нет `service` календаря: `scheduleDays` возвращает строку («ежедневно», «1 через
  1», «пн-пт»), но она **ни на что не влияет** — `addRun` генерирует все runs
  без фильтра по календарю. Это баг: рейс «только по будням» строится и в
  субботу.
- `Trip.ServiceID` — строка, не FK.
- `Period` («круглогодично» / «с…по…») игнорируется — `pickPeriod` выбирает
  первый непустой блок.
**Решение (GTFS/CSA подход):**
1. `stations.timezone TEXT` (IANA, напр. `Asia/Novosibirsk`).
2. `services` + `service_days` + `service_exceptions` (п.1) — нормальный
   календарь. Парсить `Days` («пн-пт», «1 через 1», «суббота») в `weekday`
   биты; `Period` → `start_date`/`end_date`; исключения → `service_exceptions`.
3. В `addRun`: `dayBase = time.Date(..., firstStopTZ)`. При построении сети
   на день D: определить weekday D → lookup `service_days` → пропускать runs,
   где `service.days` не содержит weekday D. `Trip.ServiceID` → FK на
   `services.id`. При `find_route` с `Departure` → аналогичная фильтрация.
4. Ночные рейсы: `arr < dep` → `arr += 24h` (уже через `prevEff`);
   `dayBase` в зоне станции даёт корректное UTC время.

**8. `stop_times.stop_id` → `provider_stops.id` (не station).**
Маршрут использует конкретный provider stop. Маршрутизатор:
`provider_stop → canonical station → transfer graph`. Это правильная модель.

**9. `transfers` расширенный (не только пешие).**
`transfers(id, from_stop_id, to_stop_id, duration_sec, distance_m,
transfer_type, within_station, confidence, source)`.
`transfer_type`: `walking` | `same_platform` | `station_transfer` |
`airport_transfer` | `bus_to_bus` | `unknown`.
**Разделение**: физические переходы (station A → station B) храним; допустимые
пересадки (trip A → trip B) — **не храним**, вычисляются маршрутизатором:
`arrival(A) + min_transfer_time <= departure(B)`. Уменьшает граф.

**10. Три уровня БД (Source / Canonical Network / Transit Schedule).**
1. SOURCE: `providers`, `carrier_providers`, `imports`
2. CANONICAL NETWORK: `carriers`, `stations`, `station_alias`, `station_sources`,
   `transfers`
3. TRANSIT SCHEDULE: `routes`, `trips`, `services`, `service_days`,
   `service_exceptions`, `stop_times`, `frequencies`

**11. `fares` — MVP упрощён, задел на будущее.**
`fares(provider_id, from_zone, to_zone, amount, currency, basis)` достаточно
для MVP. Когда понадобятся составные билеты (bus+train) — добавить
`fare_products`, `fare_rules`.

**12. `quality_issues` привязан к import.**
`quality_issues(id, import_id, entity_type, entity_id, level, code, message,
created_at)`. `code` вместо текста: `NO_GEO`, `DUP_ROUTE`, `MISSING_TIME` —
статистика/дашборды.

**13. FK типы — единообразие INTEGER.**
`providers.id INTEGER PK` → везде `provider_id INTEGER` (не TEXT).
Коды провайдеров (`yandex`, `rzd`, `mintrans`) — в `providers.code/name`.
Исправить: `carriers.provider_id`, `stations.primary_provider` (убрав),
`provider_stops.provider_id`, `routes.provider_id`, `trips.provider_id`,
`fares.provider_id`, `quality_issues.provider_id`.

**14. Индексы под routing queries (критично для CSA/RAPTOR).**
`stop_times` хранит оба поля времени (GTFS-подход):
```sql
stop_times (
    trip_id       INTEGER NOT NULL,
    stop_id       INTEGER NOT NULL,
    seq           INTEGER NOT NULL,
    arrival_sec   INTEGER NOT NULL,  -- seconds from dayBase UTC
    departure_sec INTEGER NOT NULL,  -- seconds from dayBase UTC
    pickup_type   INTEGER,
    drop_off_type INTEGER,
    PRIMARY KEY (trip_id, seq)
);
```
Оба индекса обязательны (PK не покрывает оба access pattern):
```sql
-- RAPTOR/CSA: все отправления от стопа после T (ключевой индекс)
CREATE INDEX idx_stop_times_stop_departure
    ON stop_times(stop_id, departure_sec);

-- Сканирование trip в порядке seq (PK покрывает, но явный индекс страхует)
CREATE INDEX idx_stop_times_trip_seq
    ON stop_times(trip_id, seq);

-- Прочие:
CREATE INDEX idx_trips_route ON trips(route_id);
CREATE INDEX idx_trips_service ON trips(service_id);
CREATE INDEX idx_provider_stops_station ON provider_stops(station_id);
CREATE INDEX idx_station_geo ON stations(geo_cell);  -- H3/geohash/S2 cell
CREATE INDEX idx_transfers_from ON transfers(from_stop_id);
CREATE INDEX idx_transfers_to ON transfers(to_stop_id);
CREATE INDEX idx_routes_provider_mode ON routes(provider_id, mode);
```

**Connections — derived, RAM, не SQL.** CSA работает с `[]Connection` в RAM
(текущая архитектура: `provider.build()` → `stop_times` → `Connections[]` →
CSA). SQL VIEW с self-join `stop_times` на каждый запрос — дорого.
Materialized table `connections` в БД — избыточно: данные всё равно живут
в RAM `model.Network`. Правильная архитектура:
`stop_times → preprocessing (в provider) → []Connection (sorted) → CSA`.
RAPTOR требует наличия `stop_times` в БД с индексами выше —
для него `Connections` не нужны.

**15. Геометрия станций — stations/stops, не stop_times.**
Координаты принадлежат `stations`/`provider_stops`, PostGIS spatial index
на них. `stop_times` индексируется по `(stop_id, departure_sec)`.
`stations.latitude/longitude` с `CHECK`:
```sql
latitude REAL CHECK(latitude BETWEEN -90 AND 90),
longitude REAL CHECK(longitude BETWEEN -180 AND 180),
geo_cell TEXT  -- H3/geohash/S2 cell для быстрого радиус-поиска
```
Routing workflow: `точка → spatial index stations/stops → nearest stops →
stop_times temporal index → RAPTOR/CSA`.

**16. `mode` нормализован.**
`CHECK(mode IN ('bus','rail','suburban','metro','tram','trolleybus',
'flight','ferry','walk','transfer'))`. Справочник не обязателен.

**17. Journeys (цепочки поездок) — НЕ в БД.**
Результат поиска = `Journey{Legs[]}` возвращается API, не сохраняется.
Static DB → Routing engine → Journey → API. Планировщик развивается отдельно.

**18. Итоговая ER-связность (цель Этапа 1):**
```
PROVIDERS
  providers
  imports
CANONICAL ENTITIES
  carriers
  carrier_providers
  stations
  station_codes
PROVIDER ENTITIES
  provider_stops
  routes
  trips
SCHEDULE
  services
  service_days
  service_exceptions
  stop_times
  frequencies
NETWORK
  transfers
PRICING
  fares
```
Связи: `provider → imports`; `provider → carrier_providers → carrier`;
`provider → provider_stops → station`; `provider → routes → carrier`;
`routes → trips → service`; `trips → stop_times → provider_stop`;
`station → transfers → station/provider_stop`; `station → station_codes → provider`.

### Мини-итог этапа 0.2 (после сбора фикстур)

- Станции (автовокзалы) по `/nearest_stations`:
  nsk `s9875266` «Новосибирский автовокзал - Главный», barnaul `s9623278`
  «Барнаул, автовокзал», tomsk `s9623436` «Томск, автовокзал»,
  kemerovo `s9623379» «Кемерово, автовокзал».
- Фикстуры в `testdata/yandex/`: 4×`*.nearest.json`; расписания отправлений
  `nsk.json` (70 рейсов, лимит 100), `barnaul.json` (100), `kemerovo.json`
  (100), `tomsk.json` (0 отправлений на дату сбора 2026-08-30); поиск
  `nsk_barnaul.json` (4 рейса), `nsk_tomsk.json` (1), `nsk_kemerovo.json`.
- Времена в фикстурах — ISO с offset `+07:00` (все 4 города в UTC+7).
  В `/schedule` отдельного поля `timezone` у станции нет — таймзона
  извлекается из offset времени отправления.

**Подводные камни API (по документации rasp.yandex.net v3.0):**
- Базовый URL `https://api.rasp.yandex.net/v3.0`; пути без завершающего
  слеша отдают 302 → запросы с `curl -L` (без `-L` ответы пустые).
- `date` — строго `YYYY-MM-DD`, литерал `today` не принимается.
- `system` — система кодирования станции; значения `standard` не существует
  (`v3.0_36`), коды вида `s…` уже код Яндекса, параметр не нужен.
- `station_type` принимает: `bus_station`, `station`, `stop`, `bus_stop`,
  `airport` (не `bus`).
- Геокодер — отдельный хост `geocode-maps.yandex.ru/1.x/` с `apikey`;
  координаты в `.response…Point.pos` в формате «lng lat».

**Вывод для Этапа 1:** источник exact-времён — расписания автовокзалов
(`/schedule`) + `search`-сегменты для пар; у Томска автовокзал отдаёт
0 отправлений (прибытия есть) — fallback на поиск по паре; таймзона из
ISO-offset; квоты: типичные ответы 20–60 КБ, рейса в поиске мало.

### Этап 1 — реализация провайдера (выполнено частично)

**1.5 Парсер реестра → JSON-датасет (готов, расширить).**
- `scripts/extract-minstran.py` читает XLSX: маршруты (351), остановки (255),
  блоки расписания (702, прямое+обратное), exact-времена из обоих периодов
  (зима/лето), дни/период действия. Выход `data/reestr/regions.json`
  (не коммитится: `data/` в `.gitignore`).
- **Доработка парсера** (по анализу XLSX, см. 0.1): текущий `regions.json`
  теряет поля, полезные для обогащения объектов. Добавить в выходной JSON:
  - `routes`: `kind` (вид перевозок из `F`), `length_fwd_km`/`length_bwd_km`
    (`I`/`J`), `decision_no`/`decision_date`/`start_date` (`K`/`L`/`M`);
    гео-текст `streets_fwd`/`streets_bwd` (`G`/`H`) — для геокодинга ОП.
  - `routes_vehicles` (новый раздел): `route`, `class` (ОМ/М/С/Б/ОБ), `count`,
    `eco_class` (лист «Виды и классы ТС»).
  - `carriers` (canonical) + `carrier_providers` (новые разделы): canonical
    `name`, `inn`, `ogrn`, `address`, `email` (лист «Перевозчики»); по
    провайдеру — `external_code`, `external_name`; связь
    `routes.carrier_id → carriers.id`. Дедуп РЖД/RZD/Yandex/Mintrans по
    `carrier_id`.
  - `provider_stops` (бывш. `stops`): обогатить координатами из гео-текста
    маршрутов там, где ОП нет координат (сейчас 124/255); canonical `station`
    + `station_alias` для вариантов написания; `station_sources` вместо
    `primary_provider`.
  - `services` + `service_days` + `service_exceptions` (из листов расписания 4/5):
    каждая уникальная комбинация `days` («ежедневно», «1 через 1», «пн-пт»,
    «сб,вс») → `service` с `start_date`/`end_date` из `Period` («круглогодично»
    / «с…по…»); дни → `service_days` (биты будних/выходных); исключения →
    `service_exceptions`. Текущий `addRun` не фильтрует по календарю (баг —
    `scheduleDays` возвращает строку, не влияющую на выбор trips); исправить:
    привязка `day` в `addRun` → определение weekday → lookup `service_days`
    → пропуск неактивных runs.

**1.6 Геокодинг пунктов (готов, резервный путь).**
- Тот же скрипт с `--osm data/osm/stations.json`: метчинг автовокзалов/станций
  по OSM + газетир городов → координаты для **140 из 255** остановок.
  `regions.json` теперь содержит `lat/lon`; запуск воспроизводим
  (напр. автостанции без координат остаются без `lat/lon`).
- Мини-фикстура для тестов: `testdata/reestr/mini.json` (4 маршрута:
  НСК↔Барнаул/Томск/Кемерово, 11 остановок, коммитится).

**1.10 Provider `intercity` (готов, кэша нет).**
- `internal/providers/intercity.go`: читает JSON-датасет → канон `Network`
  (255 стопов / 351 маршрут / 810 рейсов / 4776 соединений). Времена рейсов —
  из реестра, благодаря чему найдены сквозные пути: НСК→Барнаул, НСК→Томск,
  НСК→Кемерово.
- Пешие стыковки между близкими терминалами (< 0,4 км) строятся автоматически
  (`addTransferLinks`): соседние вокзал/автостанция НСК соединяются, CSA
  строит «дверь-в-дверь» через обе точки.
- Включение: `PROVIDERS_ENABLED=intercity` + `reestr_path` (env
  `INTERCITY_REESTR_PATH`); `Registry` получил `NewRegistryWith`. Сеть строится
  на каждый вызов (лентяйно), кэш/TTL — позже.
- Юнит-тест `internal/providers/intercity_test.go` на `mini.json` (рейсы,
  времена, пересадка через полночь).

**Не сделано (стартовать с Этапа 1 или 2):** п.4 (Source/TimeBasis, таймзоны
в UTC+7), п.7 (адаптер Яндекса по фикстурам), п.8 (OSM-слой как источник),
п.9 (унификация exact→estimate с source-метками), п.11 (ID-политика и
смягчение конфликт-фейла при совместном включении с synth), пп.12–13.

## Скрипт сбора Яндекса (`scripts/yandex-collect.sh`)

- Читает `YANDEX_RASP_KEY`, `YANDEX_GEOCODE_KEY` из env.
- `/stations` для автовокзалов НСК/Барнаул/Томск/Кемерово → coords+timezone.
- `/search` для пар НСК↔Барнаул, НСК↔Томск, НСК↔Кемерово → рейсы на «сегодня».
- Сохраняет сырые ответы в `data/yandex/raw/`; эталонные срезы для фикстур —
  в `testdata/` (коммитятся).
- Живой API вне сбора не вызывается: Go-адаптер работает только по фикстурам.