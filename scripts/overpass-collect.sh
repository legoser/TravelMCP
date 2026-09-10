#!/usr/bin/env bash
# Ручной скраппинг Overpass (O-6, план §10): сырьё для коннектора
# internal/adapters/overpass. Живой API вызывается ТОЛЬКО этим скриптом
# (вежливый пейсер ≥1.2с — как в Go-пути cache+quota); Go работает по
# фикстурам/офлайн-дампам. Ключей нет — публичный API.
#
# Вход: список точек/маршрутов ниже или $1 = файл unmatched-списка attach
# (по одной точке/строке: "lat lon" либо "ref" маршрутного relation).
# Сырьё: data/overpass/raw/ (не коммитится); сводный офлайн-дамп:
# data/overpass/stations.json ( формат адаптера — elements[]).
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RAW="$DIR/data/overpass/raw"
OUT="$DIR/data/overpass/stations.json"
ENDPOINTS=(
  "https://overpass-api.de/api/interpreter"
  "https://overpass.openstreetmap.fr/api/interpreter"
)
PACE=1.2

command -v jq >/dev/null 2>&1 || { echo "требуется jq"; exit 1; }
command -v curl >/dev/null 2>&1 || { echo "требуется curl"; exit 1; }
mkdir -p "$RAW"

post_ql() { # $1 out-slug, $2 QL
  local slug="$1" ql="$2" ep
  local out="$RAW/$slug.json"
  if [ -s "$out" ]; then
    echo "  $slug.json уже есть ($(stat -c%s "$out") байт) — пропуск"
    return 0
  fi
  for ep in "${ENDPOINTS[@]}"; do
    if curl -sf --max-time 120 "$ep" --data-urlencode "data=$ql" > "$out.tmp"; then
      mv "$out.tmp" "$out"
      echo "  $slug.json ($(stat -c%s "$out") байт, ${ep##*//})"
      sleep "$PACE"
      return 0
    fi
    echo "    fallback: $ep не ответил"
  done
  rm -f "$out.tmp"
  echo "  ОШИБКА: все инстансы недоступны для $slug" >&2
  return 1
}

# Точки добора по умолчанию: хабы пилота СФО (lat lon slug) — по образцу
# O-4 StationsAround радиус 500м. Координаты — реальные автовокзалы OSM
# (Кемерово: 55.341592, 86.061353). $1 = файл точек переопределяет список.
collect_points() {
  local list="${1:-}"
  if [ -n "$list" ] && [ -f "$list" ]; then
    cat "$list"
    return
  fi
  cat <<'EOF'
55.341592 86.061353 kemerovo_avtovokzal
56.0330631 92.90629307 krasnoyarsk_mkav
53.3523062 83.7591236 barnaul_avtovokzal
56.4613482 84.9914307 tomsk_avtovokzal
56.0301547 87.6205466 kemerovskaya_oblast_avtovokzal
EOF
}

echo ">> добор станций вокруг точек (StationsAround, 500м)"
collect_points "${1:-}" | while read -r lat lon slug; do
  [ -n "${lat:-}" ] || continue
  ql="[out:json][timeout:60];node(around:500,${lat},${lon})[\"public_transport\"~\"platform|station|stop_position\"];node(around:500,${lat},${lon})[\"highway\"=\"bus_stop\"];node(around:500,${lat},${lon})[\"railway\"~\"station|halt\"];out center;"
  post_ql "around_${slug}" "$ql"
done

echo ">> маршрутные relation'ы (O-5 FetchRouteRelations, bbox СФО)"
for ref in 42 101 123; do
  ql="[out:json][timeout:60];relation[\"type\"=\"route\"][\"route\"~\"bus|trolleybus\"][\"ref\"=\"${ref}\"](53.0,82.0,57.0,90.0);out body;>;out skel qt;"
  post_ql "route_${ref}" "$ql"
done

echo ">> сводный дамп $OUT"
jq -s --argjson date "\"$(date -u +%FT%TZ)\"" '
  {generated_at: $date, elements: (map(.elements // []) | add // [])}
' "$RAW"/around_*.json "$RAW"/route_*.json > "$OUT" 2>/dev/null || {
  echo "  нет собранного сырья для сводного дампа — сначала сбор"
  exit 0
}
echo "  $OUT ($(stat -c%s "$OUT") байт, $(jq '.elements|length' "$OUT") elements)"
echo ">> готово: сырьё в $RAW (не коммитится), дамп в $OUT"
