#!/usr/bin/env bash
# Точечный сбор фикстур Яндекс Расписаний для провайдера intercity.
# Живой API вызывается ТОЛЬКО этим скриптом; Go-адаптер работает по фикстурам.
# Ключи из env: YANDEX_RASP_KEY, YANDEX_GEOCODE_KEY.
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RAW="$DIR/data/yandex/raw"
FIX="$DIR/testdata/yandex"
DATE="${YANDEX_DATE:-$(date +%F)}"   # YYYY-MM-DD (по умолчанию сегодня)
API="https://api.rasp.yandex.net/v3.0"

command -v jq >/dev/null 2>&1 || { echo "требуется jq"; exit 1; }

# fallback на .env (gitignored), если ключи не заданы в окружении
dotenv_find() {
  local dotenv="${DOTENV:-}"
  if [ -z "$dotenv" ]; then
    local d="$DIR"
    while :; do
      [ -f "$d/.env" ] && { dotenv="$d/.env"; break; }
      d="$(dirname "$d")"
      [ "$d" = "/" ] && break
    done
  fi
  echo "$dotenv"
}
ENVF=$(dotenv_find)
if [ -n "$ENVF" ] && { [ -z "${YANDEX_RASP_KEY:-}" ] || [ -z "${YANDEX_GEOCODE_KEY:-}" ]; }; then
  while IFS='=' read -r k v || [ -n "$k" ]; do
    k="${k#"export "}"
    k="${k%%[[:space:]]*}"
    case "$k" in
      ''|'#'*) continue ;;
      [A-Za-z_]*[A-Za-z0-9_]*) ;;
      *) continue ;;
    esac
    if [ -z "${!k:-}" ]; then
      v="${v//[$'\r']/}"
      v="${v#\"}"; v="${v%\"}"; v="${v#\'}"; v="${v%\'}"
      export "$k=$v"
    fi
  done < "$ENVF"
fi

RASP="${YANDEX_RASP_KEY:-}"
GEOKEY="${YANDEX_GEOCODE_KEY:-}"
[ -n "$RASP" ]  || { echo "нет YANDEX_RASP_KEY (env или .env)"; exit 1; }
[ -n "$GEOKEY" ] || { echo "нет YANDEX_GEOCODE_KEY (env или .env)"; exit 1; }

mkdir -p "$RAW" "$FIX"

GEO="https://geocode-maps.yandex.ru/1.x/"

geocode() { # $1 запрос -> lat,lng
  local resp
  resp=$(curl -sfG "$GEO" -d 'format=json' -d "apikey=$GEOKEY" \
    --data-urlencode "geocode=$1" -d 'results=1')
  jq -r '.response.GeoObjectCollection.featureMember[0].GeoObject.Point.pos
          | split(" ") | "\(.[1]),\(.[0])"' <<<"$resp"
}

nearest() { # $1 coords "lat,lng" -> JSON станций
  curl -sfL -G "$API/nearest_stations" -d "apikey=$RASP" -d "lat=${1%,*}" -d "lng=${1#*,}" \
    -d 'distance=50' -d 'limit=50' -d 'lang=ru_RU'
}

api_call() { # $1 path, $2 slug, остальное -d args
  local path="$1" b="$2"; shift 2
  curl -sfL -G "$API/$path" -d "apikey=$RASP" -d 'lang=ru_RU' "$@" > "$RAW/$b.json"
  echo "  $b.json ($(stat -c%s "$RAW/$b.json") байт)"
}

declare -A STATIONS=(
  [nsk]="Новосибирск, Вокзальная магистраль 8"
  [barnaul]="Барнаул, проспект Строителей 137"
  [tomsk]="Томск, улица Енисейская 37"
  [kemerovo]="Кемерово, улица Дзержинского 4"
)

echo ">> геокодинг + /nearest_stations"
declare -A CODES
for k in "${!STATIONS[@]}"; do
  c=$(geocode "Россия, ${STATIONS[$k]}")
  echo "  $k: $c"
  nearest "$c" > "$RAW/$k.nearest.json"
  code=$(jq -r '.stations[]
           | select(.station_type=="bus_station")
           | select(.title|test("[Аа]втовокзал|[Аа]втостанция"))
           | .code' "$RAW/$k.nearest.json" | head -1)
  [ -n "$code" ] || code=$(jq -r '.stations[]|select(.station_type=="bus_station")|.code' "$RAW/$k.nearest.json" | head -1)
  [ -n "$code" ] || code=$(jq -r '.stations[]
           | select(.station_type=="station" or .station_type=="stop")
           | select(.title|test("[Аа]втовокзал|[Аа]втостанция"))
           | .code' "$RAW/$k.nearest.json" | head -1)
  [ -n "$code" ] || { echo "    ОШИБКА: нет автовокзалов в $RAW/$k.nearest.json"; exit 1; }
  echo "    -> станция $code"
  CODES[$k]="$code"
done

echo ">> /schedule для автовокзалов"
for k in "${!CODES[@]}"; do
  api_call schedule "$k" -d "station=${CODES[$k]}" -d "date=$DATE"
  jq -r '"    \(.schedule|length) рейсов; таймзона станции: \(.station.timezone // "?")"' \
    "$RAW/$k.json" || true
done

echo ">> /search для пар городов"
api_call search nsk_barnaul -d "from=${CODES[nsk]}" -d "to=${CODES[barnaul]}" -d "date=$DATE" -d 'transport_types=bus'
api_call search nsk_tomsk   -d "from=${CODES[nsk]}" -d "to=${CODES[tomsk]}"   -d "date=$DATE" -d 'transport_types=bus'
api_call search nsk_kemerovo -d "from=${CODES[nsk]}" -d "to=${CODES[kemerovo]}" -d "date=$DATE" -d 'transport_types=bus'

echo ">> срез в testdata/yandex"
trim_sel='if has("segments") then
            {date:(.date? // ""),pagination:(.pagination? // null),
             segments:(.segments[0:20]|map({title,departure,arrival,duration,
               thread:{number:.thread.number,uid:.thread.uid,ttype:.thread.ttype,transport_subtype:{title:.thread.transport_subtype.title}},
               stops:(.stops[0:8]|map({title,departure,arrival}))}))}
          elif has("schedule") then
            {date:(.date? // ""),station:{code:.station.code,title:.station.title,timezone:.station.timezone},
             schedule:(.schedule[0:60]|map({title,departure,
               thread:{number:.thread.number,uid:.thread.uid,ttype:.thread.ttype}}))}
          else . end'
for f in "$RAW"/*.json; do
  b=$(basename "$f")
  if [ "$(stat -c%s "$f")" -le 200000 ]; then
    cp "$f" "$FIX/$b"
  else
    jq -c "$trim_sel" "$f" > "$FIX/$b"
  fi
  echo "  $FIX/$b" "$(stat -c%s "$FIX/$b") байт"
done

echo ">> готово: сырьё в $RAW, фикстуры в $FIX"