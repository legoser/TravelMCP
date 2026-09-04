# 14. План

Актуальный план действий, структура и связи. Без ссылок на прошлое — только то что делается. Связан с `13-mission.md`, использует `12-motis-api.md` (`MOTIS v6`) и `02-glossary.md`.

## 1. Архитектура

```
MOTIS (OSM РФ) ─┐
Минтранс XLSX ──┼─> Коннекторы (Go-плагины) ─> Верификация ─> PostgreSQL+PostGIS (канон) ─> GTFS-компилятор ─> gtfs.zip (один на РФ)
Яндекс Rasp ────┤                              │                              │
Внешние GTFS ───┘                              └─> MOTIS import ──────────────┘
                                                  └─> travelmcp (MCP/HTTP) ─> /api/v6/plan
                                                  └─> Админка (импорт/правка/API-вызов)
```

Коннектор = сбор + адаптация + верификация + импорт. Импорт — внутри `travelmcp`, управляется из админки (запуск, мониторинг, ручная правка, вызов внешнего API с валидацией).

**Контракт адаптеров (Фаза 0, до кодирования Фазы 1):** все 4 источника (XLSX, Яндекс, MOTIS, внешний GTFS) приводят сырьё к единому промежуточному формату `AdaptedRecord` (Go-структура в `internal/providers/adapted.go`) перед этапом верификации. Поля: `kind=(place|terminal|stop|route|trip)`, `identifiers`, `names_ru/en`, `geom`, `validity`, `source`, `raw`. Верификация принимает только `AdaptedRecord`, а не сырой формат источника — иначе каждый коннектор изобретёт свой путь.

## 2. Действия по фазам

### Фаза 0 — Валидация MOTIS (2 дня)
* Проверить `GET /api/v1/geocode?text=Кемерово автовокзал&language=ru&language=en`, `GET /api/v1/reverse-geocode?place=55.355,86.088`, `GET /api/v6/map/stops?min=54.9,86.0&max=55.4,86.3` на Кузбассе/Томской (примеры `12-motis-api.md:4`). Критерий: `areas adminLevel 3/4/6/8`, `tz`, `importance 1` у автовокзалов.
* Зафиксировать словарь перевода нарицательных: `область→Oblast`, `край→Krai`, `республика→Republic`, `район→District`, `городской округ→Urban Okrug`, `федеральный округ→Federal District` (стандарт `ISO 3166-2`). Собственные имена — транслит `ГОСТ 7.79`.
* Зафиксировать контракт `AdaptedRecord` и шкалу `places.level` (см. §3.2) — блокер для Фазы 1.

### Фаза 1 — Места и терминалы (7 дней)
* Таблица `places` из `MOTIS areas` + `OSM boundary`, `place_closure` для иерархии, `place_names` `ru/en`. Шкала `level` — см. §3.2 таблица соответствия `level ↔ admin_level ↔ смысл`, зафиксирована в глоссарии.
* `terminals/stops`, `terminal_identifiers` (`mintrans/yandex/osm/gtfs`), `terminal_names ru/en`, `terminal_tags jsonb`. Сезонность — `validity daterange` (`летний вокзал`).
* Верификация: `1 lookup` `closure` для города терминала через `JOIN places` (см. §3.3 корректный SQL), перекрёст по формализованному порогу §3.8: `MOTIS/OSM`=один источник, независимые `B=Минтранс, C=Яндекс, D=Nominatim` — пороги `500м/lev<0.15`, но сильный одиночный матч (`<200м` + `lev==0` с `B` или `C`) даёт `confidence≥0.6` без второго источника, чтобы мелкие остановки не оседали в `review_queue` навсегда (калибровка на данных Кузбасса до старта Фазы 1, см. §3.8).
* Поддержка `place_closure` — триггер `trg_places_closure` на `INSERT/UPDATE(parent_id)/DELETE` в `places`, пересчитывающий поддерево, + ночная проверка `SELECT` расхождений `closure` vs `parent_id` (см. §3.2).

### Фаза 2 — Маршруты и расписания (7 дней)
* `routes`, `trips`, `services/service_days/service_exceptions`, `stop_times` (с `pickup_type/drop_off_type`), `transfers` (пешие `0.4км` + `GTFS transfers.txt`).
* `frequencies` — опционально для headway-маршрутов (см. §3.4). Таблица `frequency_types` упразднена как отдельный справочник: частоты хранятся как `frequencies(trip_id, start_time, end_time, headway_secs)` по образцу `GTFS frequencies.txt`; для MVP поддерживаются только точные времена из Минтранса XLSX (`frequencies` остаётся пустой, зарезервирована).
* Коннектор Минтранса: `XLSX` 5 листов → exact времена `+1440` на след. сутки, `serviceActive` на день.
* Коннектор Яндекс: сборщик `GTFS` для городов/регионов, `language=ru,en` проверка.

### Фаза 3 — Цены и зоны (5 дней)
* `fare_attributes`, `fare_rules`, `zones` — из Минтранса (тарифы) и Яндекс. Тоглы видов — `transitModes=BUS,COACH,RAIL` (`openapi:Mode`).
* Ограничение MVP: цена составного маршрута считается как сумма `fare_rules` по leg'ам (линейно). Комбинированные/пересадочные тарифы (`GTFS-Fares v2` `fare_leg_rules/fare_products`) не моделируются — зафиксировано как осознанный компромисс; миграция в бэклоге после Фазы 5, схема совместима (добавление таблиц, не ALTER существующих).

### Фаза 4 — GTFS-продукт и импорт (5 дней)
* Компилятор `Postgres → gtfs.zip` (один файл, **мульти-агентный**): `agency.txt` строится из `carriers` (реальные перевозчики, `agency_id = carriers.id`), а не из `providers` (источников данных) — разведено `provider ≠ carrier` (см. §3.6). `stop_id/route_id/trip_id` — канонические `BIGSERIAL` PK как `text` без префикса `provider:` (глобально уникальны после реконсиляции; внешние `agency_id/route_id` хранятся в `provenance`/`terminal_identifiers` для трассировки). Единственный `ru_intercity` как единственный agency — отклонён.
* Файлы: `agency.txt`, `stops.txt`, `routes.txt`, `trips.txt`, `stop_times.txt`, `calendar.txt`, `calendar_dates.txt`, `transfers.txt`, `fare_attributes.txt`, `fare_rules.txt`. `shapes.txt` — осознанно отсутствует в MVP (маршруты без полилиний; влияет на отображение, не на роутинг; бэклог Фазы 6). Валидация `gtfs-validator`.
* `MOTIS import -c /data/config.yml` с `gtfs.zip`, проверка `GET /api/v6/plan?fromPlace=55.0084,82.9357&toPlace=53.3481,83.7754` (`--data-urlencode` для кириллицы).

### Фаза 5 — Админка и квоты (5 дней)
* Блок `/admin`: список импортов, запуск, логи, ручная правка `terminals`, кнопка `Вызвать внешний API` → валидация → `Сохранить`.
* Таблица `api_quotas(provider, day, used, limit, reset_at)`, `rate limiter` на коннектор (token bucket). Источник истины — строка на день `PK(provider, day)`; `reset_at` — производная колонка (время сброса 00:00 MSK), обновляется cron'ом, который вставляет строку следующего дня. Инкремент атомарный (см. §3.7).
* Аутентификация админки: JWT/сессия, роль `operator`; каждое действие (`Вызвать внешний API`, `Сохранить правку`) пишет `provenance.actor_id FK→users` + `audit_log`; `provenance.source` ≠ identity оператора.
* **Скоп пилота:** Фазы 1–5 = Кузбасс + Томская область (критерии §5). Масштаб на всю РФ — отдельная Фаза 6 (не входит в 29 дней плана).

## 3. Структура БД `PostgreSQL+PostGIS`

### 3.1 Справочники

```sql
regions(code PK, name_ru, name_en, geom geometry, density_class text CHECK(density_class IN ('rural','suburban','urban','metro')) DEFAULT 'rural')
-- density_class — для per-class порогов верификации (§3.9), даже в пилоте один класс, схема готова к ре-калибровке мегаполиса
transport_modes(mode PK, max_speed real) -- BUS/COACH/RAIL/TRAM по Mode openapi
place_types(type PK) -- station/hub/platform
-- frequency_types удалён: частоты — в §3.4 frequencies (GTFS frequencies.txt), резерв на будущее
providers(code PK, name) -- источник данных: mintrans/yandex/osm/gtfs/motis/nominatim (для provenance/terminal_identifiers.system)
carriers(id PK, inn text, name_ru text, name_en text) -- реальный перевозчик (юрлицо), FK для routes/trips → agency.txt
-- CREATE UNIQUE INDEX uniq_carriers_inn ON carriers(inn) WHERE inn IS NOT NULL; -- INN — надёжный ключ дедупликации юрлиц РФ, частичный индекс (NULL — много, не конфликтует)
-- carrier_identifiers(carrier_id FK→carriers, system text, code_type text, code text, PRIMARY KEY(carrier_id,system,code_type), UNIQUE(system,code))
--   system: mintrans/yandex/gtfs, code: inn/ogrn/original_agency_id — для случаев когда INN не извлекается, дедуп по (system,code) как у terminal_identifiers
-- seed: INSERT INTO carriers(id, inn, name_ru) VALUES (0, NULL, 'Неизвестный перевозчик') ON CONFLICT DO NOTHING; -- placeholder для GTFS-совместимости, см. §3.4
```

### 3.2 Иерархия мест (детерминированная)

```sql
places(id PK, parent_id FK→places DEFERRABLE INITIALLY DEFERRED, admin_level int, level smallint, geom geography(Point,4326), bbox geometry, tz text,
       valid_from date NOT NULL DEFAULT CURRENT_DATE, valid_to date, is_current bool GENERATED ALWAYS AS (valid_to IS NULL) STORED)
-- SCD2: valid_to NULL = текущая версия; DEFERRABLE — для bulk COPY без топологической сортировки
place_closure(ancestor_id FK, descendant_id FK, depth smallint, PK(ancestor_id,descendant_id))
place_names(place_id FK, lang text CHECK(lang IN('ru','en')), name text, normalized text, PRIMARY KEY(place_id,lang))
-- GIN(normalized), GiST(geom)
-- Индексы closure:
-- PK(ancestor_id, descendant_id) покрывает "все потомки X"
-- CREATE INDEX idx_place_closure_descendant ON place_closure(descendant_id);
-- CREATE INDEX idx_place_closure_descendant_depth ON place_closure(descendant_id, depth);
```

**Каноническая шкала `places.level`** (единственная, зафиксирована в `02-glossary.md:G`):

| level | admin_level (OSM) | смысл | пример |
|---|---|---|---|
| 0 | 2 | страна | РФ |
| 1 | 3 | федеральный округ | Сибирский ФО |
| 2 | 4 | регион (область/край/республика) | Кемеровская область |
| 3 | 6 | район / городской округ | Кемеровский ГО |
| 4 | 8 | город / населённый пункт | Кемерово |
| 5 | 9–10 | внутригородской район / микрорайон | Центральный район |

`level` — наша денормализованная глубина для быстрых фильтров; `admin_level` — исходный OSM-тег.

Пример `Кемерово, автовокзал` (6 узлов):
```
places:
  1 РФ                 level0 admin_level2
  2 Сибирский ФО       level1 admin_level3 parent=1
  3 Кемеровская обл    level2 admin_level4 parent=2
  4 Кемеровский ГО     level3 admin_level6 parent=3
  5 Кемерово           level4 admin_level8 parent=4
  6 Центральный район  level5 admin_level9 parent=5
closure (ancestor, descendant, depth):
  (4,6,2) (5,6,1) (6,6,0)  -- плюс транзитивные (1,6,5) (2,6,4) (3,6,3)
place_names: (6,'ru','Кемерово'), (6,'en','Kemerovo')
```

**Поддержание `place_closure` в согласованности с `parent_id`:**

* DDL-триггер `trg_places_closure_sync` `AFTER INSERT OR UPDATE OF parent_id OR DELETE ON places FOR EACH ROW` вызывает `fn_rebuild_closure_subtree(NEW.id)` — удаляет старые пути поддерева и вставляет новые `INSERT INTO place_closure SELECT super.ancestor_id, sub.descendant_id, super.depth+sub.depth+1 ...`. Функция `SECURITY DEFINER` (owner `places_writer`), `app_user` пишет только через неё.
* Прямые `UPDATE places SET parent_id=...` вне сервиса запрещены политикой (только через `internal/places/service.go` который вызывает `SELECT fn_rebuild_closure_subtree(...)`); `REVOKE UPDATE(parent_id) FROM app_user; GRANT EXECUTE ON FUNCTION fn_rebuild_closure_subtree TO app_user` — триггер как safety-net, не ломающий легитимную запись (сервис коннектится как `app_user`, но функция выполняется с правами `places_writer`).
* Bulk-загрузка (импорт `MOTIS areas` в Фазе 1 и вся РФ в Фазе 6, тысячи строк): `FOR EACH ROW` даёт `O(n²)` на больших поддеревьях — явно предусмотрен batch-режим: `ALTER TABLE places DISABLE TRIGGER trg_places_closure_sync; COPY ...; SELECT fn_rebuild_closure_all(); ALTER TABLE places ENABLE TRIGGER ...` в одной транзакции. Обычные правки в админке — через построчный триггер.
* Ночной `cron` `verify_closure_consistency()` сравнивает `WITH RECURSIVE` обход `parent_id` vs `closure` и алертит в `review_queue` при расхождении.

### 3.3 Терминалы и остановки

```sql
terminals(id PK, place_id FK→places, geom geography(Point,4326), tz text, validity daterange, osm_compatible_name text,
          valid_from date NOT NULL DEFAULT CURRENT_DATE, valid_to date, last_verified_at timestamptz)
-- GiST(geom), GiST(validity), GIN(osm_compatible_name gin_trgm_ops); last_verified_at — для свежести (§3.9)

stops(id PK, terminal_id FK→terminals, geom geography(Point,4326), stop_type text, validity daterange)

terminal_names(terminal_id FK, lang text, name text, is_primary bool, PK(terminal_id,lang))
stop_names(stop_id FK, lang text, name text, PK(stop_id,lang))

terminal_identifiers(terminal_id FK, system text, code_type text, code text, is_primary bool,
  PRIMARY KEY(terminal_id, system, code_type), UNIQUE(system, code))
-- system: mintrans/yandex/osm/gtfs, code_type: op_reg/station_code/osm_id/gtfs_stop_id, code: op:54:54098/s9875266/n217259914/kembus_s9623379

terminal_tags(terminal_id FK, tags jsonb) -- GIN(tags)
provenance(entity_type text CHECK(entity_type IN ('place','terminal','stop','route','trip')), entity_id bigint, source text, confidence real, observed_at timestamptz, raw jsonb, actor_id bigint REFERENCES users(id))
review_queue(entity_type text CHECK(entity_type IN ('place','terminal','stop','route','trip')), entity_id bigint, reason text, score real)
-- Полиморфная связь: PG не обеспечивает FK на (entity_type, entity_id) нативно.
-- Компенсация: CHECK(entity_type), триггер trg_provenance_no_orphan BEFORE INSERT/UPDATE проверяет существование в соответствующей таблице,
--            триггер trg_no_hard_delete вместо DELETE ставит deleted_at, ночной job чистит осиротевшие записи + тест TestProvenanceFK.
```

Быстрые запросы:
* `city терминала` — корректный запрос с JOIN (исправлен баг с `anc.level`):
  ```sql
  SELECT p.id, p.tz FROM place_closure pc
  JOIN places p ON p.id = pc.ancestor_id
  WHERE pc.descendant_id = :place_id AND p.level = 4; -- level4 = город
  ```
* `osm имя` — `osm_compatible_name` поддерживается триггером `trg_sync_osm_name` на `terminal_names` (а не GENERATED колонкой — PG GENERATED не видит чужие таблицы). Фильтр по `lang='ru'` обязателен — иначе `en`-строка перезапишёт поле английским именем; `DELETE` также обрабатывается (в `DELETE` доступен только `OLD`, `NEW` — NULL, обращение к `NEW` — ошибка plpgsql):
  ```sql
  CREATE OR REPLACE FUNCTION fn_sync_osm_name() RETURNS trigger AS $$
  BEGIN
    IF TG_OP = 'DELETE' THEN
      IF OLD.lang = 'ru' THEN
        UPDATE terminals SET osm_compatible_name = (
          SELECT lower(public.unaccent(name)) FROM terminal_names
          WHERE terminal_id = OLD.terminal_id AND lang='ru' LIMIT 1
        ) WHERE id = OLD.terminal_id;
      END IF;
      RETURN OLD;
    ELSE
      IF NEW.lang <> 'ru' THEN RETURN NEW; END IF;
      UPDATE terminals SET osm_compatible_name = lower(public.unaccent(NEW.name))
      WHERE id = NEW.terminal_id;
      RETURN NEW;
    END IF;
  END; $$ LANGUAGE plpgsql;
  CREATE TRIGGER trg_sync_osm_name AFTER INSERT OR UPDATE OF name OR DELETE ON terminal_names
    FOR EACH ROW EXECUTE FUNCTION fn_sync_osm_name();
  -- Требует CREATE EXTENSION unaccent; CREATE EXTENSION pg_trgm;
  ```
* `доступна сегодня` — `validity @> CURRENT_DATE AND service_active(place_id, CURRENT_DATE)` (`service_days`).

### 3.4 Расписания

```sql
routes(id PK, source_provider text FK→providers(code), carrier_id bigint NOT NULL FK→carriers(id), mode text FK→transport_modes, short_name text, long_name text,
       valid_from date NOT NULL DEFAULT CURRENT_DATE, valid_to date, last_verified_at timestamptz)
-- source_provider — откуда запись пришла (аудит), carrier_id — кто везёт (юрлицо, для agency.txt). Разведены: provider ≠ carrier.
-- carrier_id NOT NULL: маршрут без верифицированного перевозчика не попадает в канон как verified; для GTFS-совместимости (agency.txt многострочный → routes.agency_id обязателен) существует placeholder carriers(0,'Неизвестный перевозчик'), но компилятор gtfs.zip по умолчанию исключает routes с carrier_id=0 (флаг include_placeholder=false) — такие маршруты остаются в review_queue до ручной верификации перевозчика, иначе попали бы в прод с фиктивным agency_id.
services(id PK, source_provider text FK→providers(code), name text, start_date date, end_date date)
service_days(service_id FK, weekday int CHECK(weekday 0-6), PK(service_id,weekday))
service_exceptions(service_id FK, date date, type text CHECK(type IN('added','removed')), PK(service_id,date))

trips(id PK, route_id FK→routes, service_id FK→services, headsign_ru text, headsign_en text)
stop_times(trip_id FK, stop_id FK→stops, seq int, arrival int, departure int,
           pickup_type smallint DEFAULT 0 CHECK(pickup_type IN (0,1,2,3)),
           drop_off_type smallint DEFAULT 0 CHECK(drop_off_type IN (0,1,2,3)),
           PK(trip_id,seq))
-- pickup/drop_off: 0=регулярно, 1=нет посадки/высадки, 2=по требованию, 3=по связи с водителем (GTFS)
-- INDEX stop_times(stop_id, departure), trips(service_id), trips(route_id)

transfers(from_stop_id FK, to_stop_id FK, minutes int, type text, PK(from_stop_id,to_stop_id))

-- headway-частоты (GTFS frequencies.txt), пусто в MVP, зарезервировано:
frequencies(trip_id FK→trips, start_time int, end_time int, headway_secs int, exact_times int DEFAULT 0,
            PK(trip_id, start_time))
```

### 3.5 Цены

```sql
fare_attributes(fare_id PK, price numeric, currency text, basis text)
fare_rules(fare_id FK, route_id FK, origin_zone text, destination_zone text)
zones(zone_id PK, name_ru text, name_en text, geom geometry)
-- Примечание: Fares v2 (fare_leg_rules/fare_products) — бэклог после Фазы 5, см. §2 Фаза 3.
```

### 3.6 GTFS-экспорт (один файл РФ, мульти-агентный)

```sql
-- view для компилятора
CREATE VIEW v_gtfs_stops AS SELECT s.id::text AS stop_id, tn.name AS stop_name, ST_Y(geom::geometry) AS lat, ST_X(geom::geometry) AS lon, p.tz AS zone_id FROM stops s JOIN terminal_names tn ON ...;
-- stop_id/route_id/trip_id — канонические PK (BIGSERIAL) как text, без префикса provider: глобально уникальны после реконсиляции, коллизий нет
CREATE VIEW v_gtfs_agency AS SELECT c.id::text AS agency_id, c.name_ru AS agency_name, 'https://travelmcp.local' AS agency_url, 'Europe/Moscow' AS agency_timezone FROM carriers c;
```

Файлы: `agency.txt` (N строк — по одной на `carriers.id`), `stops.txt` (из `v_gtfs_stops`), `routes.txt`, `trips.txt`, `stop_times.txt`, `calendar.txt`, `calendar_dates.txt`, `transfers.txt`, `fare_attributes.txt`, `fare_rules.txt`, `feed_info.txt` (`feed_version` timestamp+hash, `feed_start/end_date` — см. §3.9 стабильность ID). `shapes.txt` отсутствует в MVP (осознанно, см. §2).

Принцип: все данные, включая внешние GTFS Москвы/СПб, проходят через `AdaptedRecord → верификация → канонические tables` (`Одна истина` `13-mission.md:2`). Поэтому `stops/routes/trips` используют глобально уникальные канонические `id` без префикса `provider:` — префиксация не нужна и была бы смешением `provider` (источник) с `carrier` (перевозчик). Исходные внешние `agency_id/route_id` сохраняются в `terminal_identifiers`/`provenance.raw` для трассировки, но не в `gtfs.zip`. Если фид не проходит реконсиляцию (pass-through), это противоречит миссии — такой режим запрещён; явно зафиксировано: pass-through GTFS нет, только канонический импорт.
`routes.source_provider` и `provenance.source` хранят источник данных для аудита; `agency.txt` — только `carriers` (реальные перевозчики: два автопарка из Минтранса получат два разных `agency_id`, а не один `mintrans:`).

### 3.7 Квоты

```sql
api_quotas(provider text, day date, used int, limit int, reset_at timestamptz, PRIMARY KEY(provider,day))
api_calls(id PK, provider text, endpoint text, at timestamptz, cost int)
-- reset_at — денормализованное время следующего сброса (00:00 MSK следующего дня), заполняется cron'ом
-- rate_limiter: token bucket (limit/period из config), сброс — вставка строки нового дня, не UPDATE used=0
```

Источник истины — строка на день `PK(provider, day)`. `reset_at` и cron — лишь удобство/мониторинг, не второй механизм сброса. Канонический путь инкремента — один, атомарный `INSERT ... ON CONFLICT ... WHERE used < limit RETURNING` (исключает гонку без явных блокировок):

```sql
INSERT INTO api_quotas(provider, day, used, limit, reset_at)
VALUES ($1, CURRENT_DATE, 1, $2, (CURRENT_DATE + INTERVAL '1 day')::timestamptz AT TIME ZONE 'Europe/Moscow')
ON CONFLICT (provider, day) DO UPDATE SET used = api_quotas.used + 1
WHERE api_quotas.used < api_quotas.limit
RETURNING used;
-- Приложение проверяет RETURNING: если 0 строк — квота исчерпана (429), не делает внешний вызов.
-- Альтернативный UPDATE ... WHERE used < limit RETURNING семантически эквивалентен, но не используется параллельно — зафиксирован один путь (ON CONFLICT), чтобы не плодить две реализации с разной семантикой. SELECT ... FOR UPDATE не требуется.
```

Загрузка лимитов из БД на старте (`cmd/mcp-server`). Тест `TestQuotaRace` с параллельными горутинами.

### 3.8 Верификация и перекрёстная проверка

`MOTIS` geocoder построен на `OSM` — это один источник geo-правды, не два. Независимыми считаются:
* `A = OSM/MOTIS` (граф + геокодер)
* `B = Минтранс XLSX` (реестр)
* `C = Яндекс Расписание`
* `D = Nominatim / OpenAddresses` (адресный геокодер, опционально)

Алгоритм верификации `terminals`/`places` (веса калибруются до Фазы 1 на данных Кузбасса/Томской — оценка доли терминалов с ≥1 vs ≥2 источниками и размера `review_queue`):
```
confidence = 0; strong = (distance < 200m and lev==0)
if distance(A.geom, B.geom) < 500m and lev<0.15 → confidence += 0.4 + (strong?0.2:0)
if distance(A.geom, C.geom) < 500m and lev<0.15 → confidence += 0.4 + (strong?0.2:0)
if distance(A.geom, D.geom) < 500m → confidence += 0.2
// Итого: слабый B или C → 0.4, сильный B/C → 0.6 (проходит порог одним источником), B+C → 0.8, B+C+D → 1.0
if confidence >= 0.6 → provenance.confidence=confidence, статус verified
else → INSERT INTO review_queue(entity_type, entity_id, reason='low confidence', score=confidence)
```
Пороги: `500м` (сильный `<200м`), `lev<0.15` (сильный `lev==0`). Параметры — в `config verification.threshold_*`, меняются без пересборки. До старта Фазы 1 — прогон на реальных `data/reestr/regions.json` + `yandex-collect` выборке Кузбасса для выбора между `threshold=0.6 сильный одиночный` vs `threshold=0.4` (если `review_queue` >30% — снизить порог или поднять вес `B/C` до `0.6`).

> **На вырост (Фаза 6, Москва/СПб):** пороги калиброваны на разреженной сети Кузбасса/Томской; в плотной городской сети (остановки 50–100 м) `500м` даст ложные склейки разных остановок. Перед масштабом на всю РФ — обязательная ре-калибровка `distance/lev` на городской выборке, не перенос параметров пилота «как есть».

### 3.9 Темпоральность, стабильность ID, партиционирование, свежесть (уровень — опыт крупных агрегаторов)

**Темпоральность SCD2 (дешево сейчас, дорого в Фазе 6):** `places/terminals/routes` получают `valid_from date NOT NULL DEFAULT CURRENT_DATE, valid_to date, is_current bool GENERATED ALWAYS AS (valid_to IS NULL) STORED` — даже если не используется в пилоте, миграция заполненной БД с `stop_times` → «устаревшая версия» невозможна без переписывания FK. История нужна для «как выглядело на дату X» (жалобы/отладка) и чтобы два наблюдения координаты с разных дат не считались конфликтом верификации (два момента времени, не конфликт). `provenance.observed_at` остаётся источником, но версия сущности — отдельно. `services` уже покрыт `start_date/end_date`, геометрия/имена — нет.

**Стабильность ID и `feed_info.txt`:** политика жизненного цикла — «никогда не переиспользовать PK, только `valid_to`=дата + `is_current=false`/`deprecated`». `gtfs.zip` включает `feed_info.txt` с `feed_version = YYYYMMDDThhmmssZ_{gitHash}` и `feed_start_date/feed_end_date = min(services.start_date)/max(end_date)`. Внешние потребители/`MOTIS`/`GTFS-RT` матчатся по стабильному `trip_id`; дифф между версиями — по `feed_version`. Без этого «прыгающие» BIGSERIAL после дедупа carriers сломают кэши и будущий `TripUpdates`.

**Ключи партиционирования (выбрать в Фазе 1, реализовать потом):** `stop_times` — кандидат `PARTITION BY RANGE (trip_id)` через `route→region`, либо `BY RANGE (service.start_date)`; `provenance/api_calls` — `PARTITION BY RANGE (observed_at/at)` (append-only лог, рост неограничен). DDL с самого начала пишется совместимо с `PARTITION BY` (FK без кросс-партиций, `PRIMARY KEY` включает ключ партиции), сам `ATTACH PARTITION` — в Фазе 6.

**Density-class пороги верификации:** единый `500м/0.6` не переживёт масштаб (Кузбасс 500м vs Москва 50–100м). В `config verification.*` закладывается не глобальный порог, а `per density_class` (`regions.density_class enum rural/suburban/urban/metro`, производный от площади/населения, см. §3.1 `regions`). Пилот использует один класс, но схема конфига уже `map[density_class]Threshold` — явная ре-калибровка Фазы 6 не требует переделки кода.

**Свежесть/календарь/backup/staging (эксплуатация):** `terminals/routes.last_verified_at timestamptz` + `review_queue` сортировка по `age = now()-last_verified_at` (не только `confidence`) — иначе расписание тихо устареет. `services.end_date` → `cron publish cadence`: ре-публикация за `N` дней до `MIN(end_date)` (иначе MOTIS отдаст пустые маршруты). «Одна истина» `Postgres` → обязателен `WAL-архив/PITR` + протестированный `restore` (ручные правки/verified-статусы невосстановимы). Готовность к `GTFS-RT`: стабильный `trip_id` + отдельные колонки `scheduled_arrival/planned` (не `ALTER` позже). Регресс компиляции: `golden dataset` + `counts diff` (`5600→3200` — баг или реальность?) перед промоушеном. Откат: `staging MOTIS` → `GET /api/v6/plan` на `golden` → `atomic promote`, не прямой импорт в прод (см. также `BACKUP.md`/`RUNBOOK.md` к Фазе 5).

## 4. Связи (ER)

```
places 1──∞ place_closure ──∞ places (ancestor/descendant)
places 1──∞ place_names
places 1──∞ terminals
terminals 1──∞ stops
terminals 1──∞ terminal_names / terminal_identifiers / terminal_tags
terminals 1──∞ provenance / review_queue (полиморфно, см. §3.3)
providers 1──∞ routes.source_provider / services.source_provider / provenance.source
carriers 1──∞ routes.carrier_id → agency.txt (provider ≠ carrier, см. §3.6)
carriers 1──∞ carrier_identifiers (дедуп по INN + system/code, см. §3.1)
routes 1──∞ trips
services 1──∞ service_days / service_exceptions
trips 1──∞ stop_times ──1 stops
trips 1──∞ frequencies (0..n, headway)
stops ∞──∞ transfers (self)
routes ∞──∞ fare_rules ──1 fare_attributes
api_quotas 1──∞ api_calls
users 1──∞ provenance.actor_id / audit_log
```

## 5. Критерии готовности

* `places` покрывает Кузбасс/Томскую с `ru/en` (проверка `geocode` `Kemovo sritis` → `Kemerovo Oblast`), `place_closure` консистентен (ночной чек зелёный), запрос города `level=4` возвращает корректно.
* `gtfs.zip` (мульти-агентный по `carriers`, без префикса `provider:` — см. §3.6) проходит валидатор, `MOTIS` `plan` возвращает `Кемерово→Томск` с тоглом `BUS/COACH`.
* Квоты `Яндекс` в БД, атомарный `used < limit`, перезапуск не теряет счётчик (строка на день), `reset_at` 00:00 MSK.
* Админка с аутентификацией, `audit_log` + `provenance.actor_id`, ручная правка синхронизирует `osm_compatible_name` триггером.
* Пилотный скоп: критерии выше — для Кузбасса/Томской; РФ целиком — Фаза 6 вне 29-дневного плана (см. §2).
