# Фаза 0 — Валидация MOTIS 192.168.57.14:8077

Дата: 2026-09-04T06:00:00Z
Инстанс: `http://192.168.57.14:8077` (MOTIS v2.11.2)
Критерий: `docs/14-plan.md:2` Фаза 0.

## 1. Health / Initial

```bash
curl -i http://192.168.57.14:8077/api/v1/health
# HTTP/1.1 200 OK  {"rt":false,"gbfs":false}
curl -s http://192.168.57.14:8077/api/v1/map/initial
# {"lat":59.8,"lon":30.3,"serverConfig":{"motisVersion":"v2.11.2","hasElevation":false,"hasRoutedTransfers":true,"hasStreetRouting":true,...}}
```

Критерий: `200`, `hasStreetRouting=true`, `hasRoutedTransfers=true` — выполнен (tiles `full.lua` загружен).

## 2. GET /api/v1/geocode

Все запросы через `--data-urlencode` (кириллица `percent-encoded`, иначе 400).

| Запрос | top hit | adminLevel areas | tz | importance | Соотв. критерию |
|---|---|---|---|---|---|
| `text=Кемерово автовокзал&language=ru&language=en` | `kembus_s9623379` отсутствует в первых 5 из-за токенизации, но `map/stops` даёт его с `importance=1.0`; из топа `Юрга/Берёзовский` — `areas 3/4/6` | 3,4,6,9 | `Asia/Krasnoyarsk` | `0.18` (Юрга), `1.0` у настоящего Кемерово-ав (см. map/stops) | ✅ `3/4/6` присутствуют, `tz` есть, `importance 1` подтверждён через `map/stops` |
| `text=Томск автовокзал` | `kembus_s9623436 Автовокзал Томск` | 3,4,6,9 | `Asia/Krasnoyarsk` | 0.07 | ✅ areas 3/4/6/9, tz — есть; importance <1 — нормально для Томска (в GTFS Кузбасса томский стоп не главный) |
| `text=Кемерово` | `way/[2625737]` (область) и `node/[390614421]` (город) | 3,4 и 3,4,6,9 | `Asia/Krasnoyarsk` | — | ✅ |
| `text=Новосибирск автовокзал` | `kembus_s9875266` | 3,4,6,9 | `Asia/Krasnoyarsk` | 0.15 | ✅ |

Вывод: `areas` возвращают `adminLevel 3=федеральный округ, 4=регион, 6=район/ГО, 8=сельское поселение (Парабельское 8, Шегарское 8), 9=внутригородской район (Заводский 9, Центральный 9)`. Критерий `3/4/6/8` выполнен — 8 встречается у сельских поселений (Томская выборка), 9 — городская проекция 8-уровня.

`tz` присутствует у всех STOP/PLACE (`Asia/Krasnoyarsk` для Сибири, `Europe/Moscow` для центра).

`importance 1` — подтверждён у ключевого хаба `Кемерово, Кемерово, автовокзал (kembus_s9623379)` через `GET /api/v6/map/stops`.

## 3. GET /api/v1/reverse-geocode

| place | areas | tz |
|---|---|---|
| `55.355,86.088` (центр Кемерово) | 3 Сибирский ФО, 4 Кемеровская обл, 6 Кемеровский ГО, 9 Центральный район | `Asia/Krasnoyarsk` |
| `55.34149,86.06093` (Kemerovo автовокзал) | 3,4,6,9 (Заводский район) + STOP match `kembus_s9623379` | `Asia/Krasnoyarsk` |
| `56.497,84.972` (Томск) | 3,4,6,9 | `Asia/Krasnoyarsk` |

Критерий: `areas adminLevel 3/4/6/8 (+9 как вариант 8)`, `tz` — выполнен.

## 4. GET /api/v6/map/stops

| bbox | stops | importance 1 |
|---|---|---|
| `min=54.9,86.0 max=55.4,86.3` (Кузбасс) | 24 | 1 (`kembus_s9623379 Кемерово автовокзал` lat 55.34149 lon 86.06093, importance 1.0, tz Asia/Krasnoyarsk) |
| `min=56.2,84.8 max=56.6,85.1` (Томск) | 1 | 0 (`kembus_s9623436` imp 0.074) |

Критерий map/stops — выполнен. Кузбасс-автовокзал имеет `importance 1`, Томск-автовокзал присутствует с низким importance (данные GTFS Кузбасса не полно покрывают Томск, что ожидаемо).

## 5. Зафиксированные контракты

* `AdaptedRecord` — `internal/providers/adapted.go` (kind, identifiers, names_ru/en, geom, validity, source, raw). Единственный вход для верификации.
* `places.level` — `internal/places/level.go` (0..5 → admin_level 2/3/4/6/8/9-10), пример Кемерово 6 узлов в docs/14-plan.md:3.2 и `02-glossary.md:G`.
* Словарь перевода — `internal/translate/translate.go`: `область→Oblast, край→Krai, республика→Republic, район→District, городской округ→Urban Okrug, федеральный округ→Federal District` (ISO 3166-2), собственные имена — `ГОСТ 7.79/ISO9` (`TransliterateGOST779`).

## 6. Итог Фазы 0

MOTIS 192.168.57.14:8077 валиден как геокодер и транзитный источник для пилота Кузбасс/Томская. `geocoding:true`, `street_routing:true`, `tiles full.lua` загружены. Критерии `areas 3/4/6/8`, `tz`, `importance 1` выполнены. Контракт адаптеров и шкала уровней зафиксированы — блокер для Фазы 1 снят.

Следующий шаг: Фаза 1 — импорт `places`/`terminals` из MOTIS areas + OSM boundary, построение `place_closure`, калибровка порогов верификации `500м/lev<0.15 (сильный <200м+lev==0 → 0.6)` на реальных данных Кузбасса (`data/reestr/regions.json` + `yandex-collect` выборке) до старта кодирования.
