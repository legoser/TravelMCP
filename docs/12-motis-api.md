# 12. MOTIS API — Local Instance Guide

Documentation for working with `MOTIS v2.11.2`, based on `openapi.yaml` (`motis-project/motis` `master/openapi.yaml`, `openapi: 3.1.0`, `version: v6`). MOTIS is used as a transit router (`GTFS siberian-fed-district` + `gtfs_bus.zip`) and as a geocoding source when `tiles` are enabled.

OpenAPI source: `https://raw.githubusercontent.com/motis-project/motis/master/openapi.yaml`

## 1. Servers and Environment Variables

In the examples, `IP:PORT` is provided through variables — do not hardcode the address (convention: `internal/config/config.go:14`, `HTTP_ADDR`).

```bash
MOTIS_HOST=${MOTIS_HOST:-192.168.57.14}
MOTIS_PORT=${MOTIS_PORT:-8077}
BASE="http://${MOTIS_HOST}:${MOTIS_PORT}"

# Alternative servers from openapi.yaml:
# https://api.transitous.org — Transitous production
# https://staging.api.transitous.org — staging
# http://localhost:8080 — default local build
```

Availability check (responds even without tiles/geocoding):

```bash
curl -i "${BASE}/api/v1/health"          # 200: {rt, gbfs}; 400: first import cycle not completed
curl -s "${BASE}/api/v1/map/initial" | jq # lat/lon/zoom + serverConfig
```

## 2. API Versions

| Prefix | Status | Notes |
|---|---|---|
| `/api/v6/plan` `/trip` `/stoptimes` `/one-to-all` `/map/stops` `/map/trips` | current | `level` is optional since `2.9.x` |
| `/api/v1/geocode` `/reverse-geocode` `/one-to-many` `/rentals` `/map/initial` `/map/levels` | current | geocoding/map |
| `/api/experimental/*` | unstable | `one-to-many-intermodal`, `map/routes` |
| `/api/v1..v5` | legacy | `METRO→SUBURBAN`, `polyline precision 7→6` |

The JS client `@motis-project/motis-client` automatically selects the latest version; with `curl`, specify `/v6` or `/v1` explicitly.

## 3. Cyrillic and URL Encoding — Required

All `text` parameters and `fromPlace`/`toPlace` values containing Cyrillic `stopId`s must be percent-encoded as UTF-8. Without encoding, MOTIS returns `400 {"error":"malformed URI or request"}` (reproduced with `curl .../api/address?text=Новосибирск`).

Incorrect:

```bash
curl "${BASE}/api/v1/geocode?text=Новосибирск"  # 400 malformed URI
```

Correct — `curl --data-urlencode` encodes automatically:

```bash
curl -G "${BASE}/api/v1/geocode" --data-urlencode "text=Новосибирск автовокзал" --data-urlencode "language=ru" | jq

curl -G "${BASE}/api/v1/geocode" --data-urlencode "text=Барнаул автовокзал" --data-urlencode "numResults=5" | jq
```

Alternatives:

```bash
# bash: python
TEXT=$(python3 -c "import urllib.parse; print(urllib.parse.quote('Кемерово автовокзал'))")

curl -s "${BASE}/api/v1/geocode?text=${TEXT}" | jq

# Go: net/url.QueryEscape (cmd/mcp-server/main.go:138, scripts/extract-minstran.py:449)
# TEXT := url.QueryEscape("Новосибирск")

# JS: encodeURIComponent("Новосибирск")
```

For `/api/v6/plan`, Cyrillic `stopId` values must also be encoded:

```bash
FROM=$(python3 -c "import urllib.parse; print(urllib.parse.quote('Кемерово автовокзал'))")

curl -G "${BASE}/api/v6/plan" --data-urlencode "fromPlace=${FROM}" --data-urlencode "toPlace=55.0084,82.9357" | jq
```

Recommendation: always use `-G --data-urlencode` instead of manually concatenating `?text=`.

## 4. Endpoints and Examples

All examples use `BASE` and `--data-urlencode`.

### 4.1 `GET /api/v1/geocode` — Autocomplete / Geocoding

```bash
curl -G "${BASE}/api/v1/geocode" \
  --data-urlencode "text=Новосибирск" \
  --data-urlencode "language=ru" \
  --data-urlencode "numResults=5" | jq

curl -G "${BASE}/api/v1/geocode" \
  --data-urlencode "text=Барнаул автовокзал" \
  --data-urlencode "type=STOP" \
  --data-urlencode "numResults=5" | jq

# Bias toward Kemerovo coordinates
curl -G "${BASE}/api/v1/geocode" \
  --data-urlencode "text=автовокзал" \
  --data-urlencode "place=55.355,86.088" \
  --data-urlencode "placeBias=2" | jq

# BBox filter
curl -G "${BASE}/api/v1/geocode" \
  --data-urlencode "text=автовокзал" \
  --data-urlencode "min=54.9,82.8" \
  --data-urlencode "max=55.2,83.2" | jq
```

Response: `200: Match[]` — `type/name/id/lat/lon/score/areas`. Requires `geocoding: true` and imported `tiles`; with `geocoding: false`, `config.yml` returns `404` (expected; use `Nominatim` via `internal/geocoder`).

### 4.2 `GET /api/v1/reverse-geocode`

```bash
curl -G "${BASE}/api/v1/reverse-geocode" \
  --data-urlencode "place=53.3523,83.7591" | jq

curl -G "${BASE}/api/v1/reverse-geocode" \
  --data-urlencode "place=55.0411,83.0274" \
  --data-urlencode "numResults=3" | jq
```

### 4.3 `GET /api/v6/plan` — Routing (Primary)

Coordinates are `lat,lon[,level]`, or a `stopId` obtained from `geocode`/`map/stops`.

```bash
# Novosibirsk → Barnaul, coordinates
curl -G "${BASE}/api/v6/plan" \
  --data-urlencode "fromPlace=55.0084,82.9357" \
  --data-urlencode "toPlace=53.3481,83.7754" \
  --data-urlencode "time=2026-09-04T06:00:00Z" \
  --data-urlencode "maxTransfers=2" | jq

# With Cyrillic + arrival time constraint
curl -G "${BASE}/api/v6/plan" \
  --data-urlencode "fromPlace=Кемерово автовокзал" \
  --data-urlencode "toPlace=55.355,86.088" \
  --data-urlencode "time=2026-09-04T08:00:00Z" \
  --data-urlencode "arriveBy=true" \
  --data-urlencode "timetableView=true" | jq

# Radius without an OSM street network (1.5 m/s, pre/post walking)
curl -G "${BASE}/api/v6/plan" \
  --data-urlencode "fromPlace=55.0084,82.9357" \
  --data-urlencode "toPlace=53.3481,83.7754" \
  --data-urlencode "radius=1500" | jq
```

Commonly used `plan` parameters: `via`, `viaMinimumStay`, `maxTransfers`, `maxTravelTime`, `minTransferTime`, `useRoutedTransfers`, `pedestrianProfile=FOOT|WHEELCHAIR`, `transitModes=TRANSIT|BUS|RAIL`, `directModes=WALK`, `maxPreTransitTime`, `maxPostTransitTime`, `timetableView`, `searchWindow=900`, `withFares`.

### 4.4 `GET /api/v6/trip`, `stoptimes`, `map/*`

```bash
# trip by ID from a leg
curl -G "${BASE}/api/v6/trip" --data-urlencode "tripId=..." | jq

# Stop departures
curl -G "${BASE}/api/v6/stoptimes" \
  --data-urlencode "stopId=..." \
  --data-urlencode "n=10" \
  --data-urlencode "radius=500" | jq

# Center + radius (if stopId is unknown)
curl -G "${BASE}/api/v6/stoptimes" \
  --data-urlencode "center=55.0084,82.9357" \
  --data-urlencode "radius=1000" | jq

# Map — stops in BBox
curl -G "${BASE}/api/v6/map/stops" \
  --data-urlencode "min=54.9,82.8" \
  --data-urlencode "max=55.2,83.2" | jq

# Levels
curl -G "${BASE}/api/v1/map/levels" \
  --data-urlencode "min=54.9,82.8" \
  --data-urlencode "max=55.2,83.2" | jq
```

### 4.5 `GET /api/v1/one-to-many` and `experimental/one-to-many-intermodal`

```bash
curl -G "${BASE}/api/v1/one-to-many" \
  --data-urlencode "one=55.0084,82.9357" \
  --data-urlencode "many=53.3481,83.7754,55.355,86.088" \
  --data-urlencode "mode=WALK" \
  --data-urlencode "max=900" \
  --data-urlencode "maxMatchingDistance=250" \
  --data-urlencode "arriveBy=false" | jq
```

## 5. Import Configuration (Important for `tiles`)

`tiles` requires a profile:

```bash
docker run --rm -v $(pwd)/input:/input:ro -v $(pwd)/data:/data \
  ghcr.io/motis-project/motis /motis import -c /data/config.yml --tiles-profile /tiles-profiles/full.lua

# Alternatively in config.yml:
# tiles:
#   profile: /motis/tiles-profiles/full.lua   # or /tiles-profiles/full.lua — verify with ls in the image
```

Error `tiles profile tiles-profiles/full.lua does not exist` → specify the absolute path `/motis/tiles-profiles/full.lua` or disable `geocoding: false`, `reverse_geocoding: false`, `street_routing: false`, `osr_footpath: false` for a transit-only import. Geocoding can then be provided through `Nominatim` via `internal/adapters/nominatim`.

Binary in the image: `/motis` (file at the root, not `motis/motis`).

## 6. Errors and Limits

| Code | Body | Cause |
|---|---|---|
| `400` | `{"error":"malformed URI or request"}` | Cyrillic text is not encoded |
| `404` | `Not found` | `geocoding:false` in `config.yml` or incorrect `/api/vX/*` prefix |
| `422` | `Error` | Invalid `plan` parameters |
| `500` | `Error` | Internal error |

Server limits (`/api/v1/map/initial` → `serverConfig`): `maxOneToManySize`, `maxOneToAllTravelTimeLimit`, `maxPrePostTransitTimeLimit`, `maxDirectTimeLimit`.

## 7. Demo Script

`scripts/motis-demo.sh` — variables + `--data-urlencode` examples (see repository). Run:

```bash
MOTIS_HOST=192.168.57.14 MOTIS_PORT=8077 ./scripts/motis-demo.sh plan 55.0084,82.9357 53.3481,83.7754

MOTIS_HOST=192.168.57.14 MOTIS_PORT=8077 ./scripts/motis-demo.sh geocode "Барнаул автовокзал"
```
