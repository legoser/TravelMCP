# 12. MOTIS API — руководство для локального инстанса

Документация по работе с `MOTIS v2.11.2` на основе `openapi.yaml` (`motis-project/motis` `master/openapi.yaml`, `openapi: 3.1.0`, `version: v6`). Используется как транзитный роутер (GTFS `siberian-fed-district` + `gtfs_bus.zip`) и как источник геокодинга при включённых `tiles`.

Исходник OpenAPI: `https://raw.githubusercontent.com/motis-project/motis/master/openapi.yaml`

## 1. Серверы и переменные

В примерах `IP:PORT` вынесены в переменные — не хардкодьте адрес (конвенция `internal/config/config.go:14` `HTTP_ADDR`).

```bash
MOTIS_HOST=${MOTIS_HOST:-192.168.57.14}
MOTIS_PORT=${MOTIS_PORT:-8077}
BASE="http://${MOTIS_HOST}:${MOTIS_PORT}"
# Альтернативные серверы из openapi.yaml:
# https://api.transitous.org — Transitous prod
# https://staging.api.transitous.org — staging
# http://localhost:8080 — дефолт локальной сборки
```

Проверка доступности (без tiles/geocoding тоже отвечает):

```bash
curl -i "${BASE}/api/v1/health"          # 200: {rt, gbfs}, 400: не завершён первый цикл импорта
curl -s "${BASE}/api/v1/map/initial" | jq  # lat/lon/zoom + serverConfig
```

## 2. Версии API

| Префикс | Статус | Примечание |
|---|---|---|
| `/api/v6/plan` `/trip` `/stoptimes` `/one-to-all` `/map/stops` `/map/trips` | current | `level` опционален с `2.9.x` |
| `/api/v1/geocode` `/reverse-geocode` `/one-to-many` `/rentals` `/map/initial` `/map/levels` | current | геокодинг/карта |
| `/api/experimental/*` | unstable | `one-to-many-intermodal` `map/routes` |
| `/api/v1..v5` | legacy | `METRO→SUBURBAN`, `polyline precision 7→6` |

JS-клиент `@motis-project/motis-client` выбирает свежайшую версию автоматически, `curl` — указывайте `/v6`/`/v1` явно.

## 3. Кириллица и URL-encoding — обязательно

Все `text`, `fromPlace`/`toPlace` со `stopId` кириллицей должны быть `percent-encoded UTF-8`. Без кодирования MOTIS возвращает `400 {"error":"malformed URI or request"}` (воспроизведено `curl .../api/address?text=Новосибирск`).

Неправильно:

```bash
curl "${BASE}/api/v1/geocode?text=Новосибирск"  # 400 malformed URI
```

Правильно — `curl --data-urlencode` (кодирует автоматически):

```bash
curl -G "${BASE}/api/v1/geocode" --data-urlencode "text=Новосибирск автовокзал" --data-urlencode "language=ru" | jq
curl -G "${BASE}/api/v1/geocode" --data-urlencode "text=Барнаул автовокзал" --data-urlencode "numResults=5" | jq
```

Альтернативы:

```bash
# bash: python
TEXT=$(python3 -c "import urllib.parse; print(urllib.parse.quote('Кемерово автовокзал'))")
curl -s "${BASE}/api/v1/geocode?text=${TEXT}" | jq

# Go: net/url.QueryEscape  (cmd/mcp-server/main.go:138, scripts/extract-minstran.py:449)
# TEXT := url.QueryEscape("Новосибирск автовокзал")

# JS: encodeURIComponent("Новосибирск")
```

Для `/api/v6/plan` c кириллическим `stopId` также кодируйте:

```bash
FROM=$(python3 -c "import urllib.parse; print(urllib.parse.quote('Кемерово автовокзал'))")
curl -G "${BASE}/api/v6/plan" --data-urlencode "fromPlace=${FROM}" --data-urlencode "toPlace=55.0084,82.9357" | jq
```

Рекомендация: всегда используйте `-G --data-urlencode`, а не ручную конкатенацию `?text=`.

## 4. Эндпоинты и примеры

Все примеры используют `BASE` и `--data-urlencode`.

### 4.1 `GET /api/v1/geocode` — автокомплит/геокодинг

```bash
curl -G "${BASE}/api/v1/geocode" \
  --data-urlencode "text=Новосибирск" \
  --data-urlencode "language=ru" \
  --data-urlencode "numResults=5" | jq

curl -G "${BASE}/api/v1/geocode" \
  --data-urlencode "text=Барнаул автовокзал" \
  --data-urlencode "type=STOP" \
  --data-urlencode "numResults=5" | jq

# bias к координате Кемерово
curl -G "${BASE}/api/v1/geocode" \
  --data-urlencode "text=автовокзал" \
  --data-urlencode "place=55.355,86.088" \
  --data-urlencode "placeBias=2" | jq

# bbox фильтр
curl -G "${BASE}/api/v1/geocode" \
  --data-urlencode "text=автовокзал" \
  --data-urlencode "min=54.9,82.8" \
  --data-urlencode "max=55.2,83.2" | jq
```

Ответ `200: Match[]` — `type/name/id/lat/lon/score/areas`. Требует `geocoding: true` и импортированных `tiles`; при `geocoding: false` `config.yml` → `404` (норма, используйте `Nominatim` `internal/geocoder`).

### 4.2 `GET /api/v1/reverse-geocode`

```bash
curl -G "${BASE}/api/v1/reverse-geocode" \
  --data-urlencode "place=53.3523,83.7591" | jq

curl -G "${BASE}/api/v1/reverse-geocode" \
  --data-urlencode "place=55.0411,83.0274" \
  --data-urlencode "numResults=3" | jq
```

### 4.3 `GET /api/v6/plan` — маршрутизация (основной)

Координаты — `lat,lon[,level]`, либо `stopId` (из `geocode`/`map/stops`).

```bash
# Новосибирск → Барнаул, координаты
curl -G "${BASE}/api/v6/plan" \
  --data-urlencode "fromPlace=55.0084,82.9357" \
  --data-urlencode "toPlace=53.3481,83.7754" \
  --data-urlencode "time=2026-09-04T06:00:00Z" \
  --data-urlencode "maxTransfers=2" | jq

# С кириллицей + временем прибытия
curl -G "${BASE}/api/v6/plan" \
  --data-urlencode "fromPlace=Кемерово автовокзал" \
  --data-urlencode "toPlace=55.355,86.088" \
  --data-urlencode "time=2026-09-04T08:00:00Z" \
  --data-urlencode "arriveBy=true" \
  --data-urlencode "timetableView=true" | jq

# radius без OSM уличной сети (1.5 м/с, pre/post пешком)
curl -G "${BASE}/api/v6/plan" \
  --data-urlencode "fromPlace=55.0084,82.9357" \
  --data-urlencode "toPlace=53.3481,83.7754" \
  --data-urlencode "radius=1500" | jq
```

Параметры `plan` часто используемые: `via`, `viaMinimumStay`, `maxTransfers`, `maxTravelTime`, `minTransferTime`, `useRoutedTransfers`, `pedestrianProfile=FOOT|WHEELCHAIR`, `transitModes=TRANSIT|BUS|RAIL`, `directModes=WALK`, `maxPreTransitTime`, `maxPostTransitTime`, `timetableView`, `searchWindow=900`, `withFares`.

### 4.4 `GET /api/v6/trip`, `stoptimes`, `map/*`

```bash
# trip по ID из leg
curl -G "${BASE}/api/v6/trip" --data-urlencode "tripId=... " | jq

# отправления остановки
curl -G "${BASE}/api/v6/stoptimes" \
  --data-urlencode "stopId=..." \
  --data-urlencode "n=10" \
  --data-urlencode "radius=500" | jq

# центр + радиус (если stopId неизвестен)
curl -G "${BASE}/api/v6/stoptimes" \
  --data-urlencode "center=55.0084,82.9357" \
  --data-urlencode "radius=1000" | jq

# карта — стопы в bbox
curl -G "${BASE}/api/v6/map/stops" \
  --data-urlencode "min=54.9,82.8" \
  --data-urlencode "max=55.2,83.2" | jq

# уровни
curl -G "${BASE}/api/v1/map/levels" \
  --data-urlencode "min=54.9,82.8" \
  --data-urlencode "max=55.2,83.2" | jq
```

### 4.5 `GET /api/v1/one-to-many` и `experimental/one-to-many-intermodal`

```bash
curl -G "${BASE}/api/v1/one-to-many" \
  --data-urlencode "one=55.0084,82.9357" \
  --data-urlencode "many=53.3481,83.7754,55.355,86.088" \
  --data-urlencode "mode=WALK" \
  --data-urlencode "max=900" \
  --data-urlencode "maxMatchingDistance=250" \
  --data-urlencode "arriveBy=false" | jq
```

## 5. Конфигурация импорта (важно для `tiles`)

`tiles` требует профиль:

```bash
docker run --rm -v $(pwd)/input:/input:ro -v $(pwd)/data:/data \
  ghcr.io/motis-project/motis /motis import -c /data/config.yml --tiles-profile /tiles-profiles/full.lua

# альтернативно в config.yml:
# tiles:
#   profile: /motis/tiles-profiles/full.lua   # или /tiles-profiles/full.lua — проверьте ls в образе
```

Ошибка `tiles profile tiles-profiles/full.lua does not exist` → укажите абсолютный `/motis/tiles-profiles/full.lua` или отключите `geocoding: false` `reverse_geocoding: false` `street_routing: false` `osr_footpath: false` для транзитного-only импорта (геокодинг тогда через `Nominatim` `internal/adapters/nominatim`).

Бинарь в образе: `/motis` (файл в корне, не `motis/motis`).

## 6. Ошибки и лимиты

| Код | Тело | Причина |
|---|---|---|
| `400` | `{"error":"malformed URI or request"}` | не закодирована кириллица |
| `404` | `Not found` | `geocoding:false` в `config.yml` или неверный `/api/vX/*` префикс |
| `422` | `Error` | невалидные параметры `plan` |
| `500` | `Error` | внутренняя ошибка |

Лимиты сервера (`/api/v1/map/initial` `serverConfig`): `maxOneToManySize`, `maxOneToAllTravelTimeLimit`, `maxPrePostTransitTimeLimit`, `maxDirectTimeLimit`.

## 7. Скрипт демо

`scripts/motis-demo.sh` — переменные + `--data-urlencode` примеры (см. репозиторий). Запуск:

```bash
MOTIS_HOST=192.168.57.14 MOTIS_PORT=8077 ./scripts/motis-demo.sh plan 55.0084,82.9357 53.3481,83.7754
MOTIS_HOST=192.168.57.14 MOTIS_PORT=8077 ./scripts/motis-demo.sh geocode "Барнаул автовокзал"
```
