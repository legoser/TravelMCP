#!/usr/bin/env bash
set -euo pipefail

MOTIS_HOST=${MOTIS_HOST:-192.168.57.14}
MOTIS_PORT=${MOTIS_PORT:-8077}
BASE="http://${MOTIS_HOST}:${MOTIS_PORT}"

usage() {
  cat <<EOF
Usage: $0 <command> [args]

Env: MOTIS_HOST (default 192.168.57.14) MOTIS_PORT (default 8077)

Commands:
  health                          — /api/v1/health + /api/v1/map/initial
  geocode <text>                  — /api/v1/geocode (кириллица кодируется --data-urlencode)
  reverse <lat,lon>               — /api/v1/reverse-geocode?place=lat,lon
  plan <from> <to> [time]         — /api/v6/plan fromPlace/toPlace (координаты или stopId, кириллица кодируется)
  stoptimes <stopId>              — /api/v6/stoptimes
  stops <minLat,lon> <maxLat,lon> — /api/v6/map/stops

Примеры:
  $0 geocode "Барнаул автовокзал"
  $0 geocode "Новосибирск"
  $0 reverse 53.3523,83.7591
  $0 plan 55.0084,82.9357 53.3481,83.7754 2026-09-04T06:00:00Z
  $0 plan "Кемерово автовокзал" 55.355,86.088
  MOTIS_HOST=192.168.57.14 MOTIS_PORT=8077 $0 geocode "Кемерово автовокзал"
EOF
}

need_jq() {
  if ! command -v jq >/dev/null 2>&1; then
    echo "jq не найден, вывод без форматирования" >&2
    JQ=cat
  else
    JQ=jq
  fi
}

case "${1:-}" in
  health)
    need_jq
    echo "BASE=${BASE}" >&2
    echo "== /api/v1/health ==" >&2
    curl -s "${BASE}/api/v1/health" | ${JQ} || true
    echo "== /api/v1/map/initial ==" >&2
    curl -s "${BASE}/api/v1/map/initial" | ${JQ} || true
    ;;
  geocode)
    need_jq
    TEXT=${2:-Новосибирск}
    echo "BASE=${BASE} text=${TEXT}" >&2
    curl -G -s "${BASE}/api/v1/geocode" \
      --data-urlencode "text=${TEXT}" \
      --data-urlencode "language=ru" \
      --data-urlencode "numResults=5" | ${JQ}
    ;;
  reverse)
    need_jq
    PLACE=${2:-53.3523,83.7591}
    echo "BASE=${BASE} place=${PLACE}" >&2
    curl -G -s "${BASE}/api/v1/reverse-geocode" \
      --data-urlencode "place=${PLACE}" | ${JQ}
    ;;
  plan)
    need_jq
    FROM=${2:-55.0084,82.9357}
    TO=${3:-53.3481,83.7754}
    TIME=${4:-2026-09-04T06:00:00Z}
    echo "BASE=${BASE} from=${FROM} to=${TO} time=${TIME}" >&2
    curl -G -s "${BASE}/api/v6/plan" \
      --data-urlencode "fromPlace=${FROM}" \
      --data-urlencode "toPlace=${TO}" \
      --data-urlencode "time=${TIME}" \
      --data-urlencode "numItineraries=3" | ${JQ}
    ;;
  stoptimes)
    need_jq
    STOP=${2:?stopId required}
    curl -G -s "${BASE}/api/v6/stoptimes" \
      --data-urlencode "stopId=${STOP}" \
      --data-urlencode "n=10" | ${JQ}
    ;;
  stops)
    need_jq
    MIN=${2:-54.9,82.8}
    MAX=${3:-55.2,83.2}
    curl -G -s "${BASE}/api/v6/map/stops" \
      --data-urlencode "min=${MIN}" \
      --data-urlencode "max=${MAX}" | ${JQ} | head -n 100
    ;;
  ""|-h|--help|help)
    usage
    ;;
  *)
    echo "unknown command: $1" >&2
    usage
    exit 1
    ;;
esac
