#!/usr/bin/env bash
# Демо/обследование API и MCP-инструментов travelmcp (ручной режим).
# MCP вызывается stateless (без initialize/сессии); при заданном ADMIN_TOKEN
# в каждый запрос добавляется заголовок API-ключа.
# Для регрессионных тестов используйте `make test`.
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="$DIR/bin/mcp-server"
PORT="${PORT:-8099}"
BASE="http://127.0.0.1:$PORT"
OUT=/tmp/travelmcp-rpc.json

command -v jq >/dev/null 2>&1 || { echo "требуется jq"; exit 1; }

if [ ! -x "$BIN" ]; then
  echo ">> building mcp-server"
  (cd "$DIR" && go build -o "$BIN" ./cmd/mcp-server)
fi

TOKEN=""
if [ -f "$DIR/.env" ]; then
  set -a; . "$DIR/.env"; set +a
fi
AUTH=()
if [ -n "${ADMIN_TOKEN:-}" ]; then
  TOKEN="$ADMIN_TOKEN"
  AUTH=(-H "X-API-Key: $TOKEN")
  echo ">> using API-ключ из ADMIN_TOKEN"
fi

echo ">> starting server on :$PORT"
HTTP_ADDR="127.0.0.1:$PORT" PROVIDERS_ENABLED=synth ADMIN_TOKEN="${ADMIN_TOKEN:-}" \
  "$BIN" -config "$DIR/configs/config.example.yaml" >/tmp/travelmcp-demo.log 2>&1 &
SRV=$!
trap 'kill "$SRV" 2>/dev/null || true; wait "$SRV" 2>/dev/null || true' EXIT

for _ in $(seq 1 50); do
  curl -sf "$BASE/healthz" >/dev/null 2>&1 && break
  sleep 0.2
done

hr() { echo; echo "────────────────────────────────────────"; }
say() { echo; echo "== $*"; }

say "HEALTH"
hr
echo "healthz: $(curl -s "$BASE/healthz" | jq -c .)"
echo "readyz:  $(curl -s "$BASE/readyz" | jq -c '{status}')"

say "PROVIDERS"
hr
curl -s "${AUTH[@]}" "$BASE/api/v1/providers" | jq -r 'to_entries[] | "\(.key): up=\(.value.up) records=\(.value.records)"'

ID=0

rpc() {
  local method="$1" payload="$2"
  ID=$((ID + 1))
  curl -s -o "$OUT" -X POST "$BASE/mcp" \
    -H 'Content-Type: application/json' -H 'Accept: application/json' \
    "${AUTH[@]}" \
    -d "{\"jsonrpc\":\"2.0\",\"id\":$ID,\"method\":\"$method\",\"params\":$payload}"
}

say "MCP tools/list"
hr
rpc tools/list '{}'
jq -r '.result.tools[].name' "$OUT"

say "MCP find_route: Пермь → Екатеринбург (наземка, 06:00)"
hr
rpc tools/call '{"name":"find_route","arguments":{"from_lat":58.0135,"from_lon":56.2495,"to_lat":56.84,"to_lon":60.607,"departure":"2026-08-30T06:00:00Z"}}'
jq -r '.result.content[0].text | fromjson |
  "прибытие: \(.arrival)  пересадок: \(.transfers)",
  (.legs[] | "  \(.mode): \(.from.name // .from.stop_id) → \(.to.name // .to.stop_id)  \(.departure | .[11:16])–\(.arrival | .[11:16])")' "$OUT"

say "MCP find_route: по населённым пунктам Юрга → Барнаул"
hr
rpc tools/call '{"name":"find_route","arguments":{"from_place":"Юрга","to_place":"Барнаул","departure":"2026-08-30T06:00:00Z"}}'
jq -r '
  if (.result.isError == true) or (.error != null) then
    "isError: true  сообщение: " + (.result.content[0].text // .error.message)
  else
    (.result.content[0].text | fromjson |
      "прибытие: \(.arrival)  пересадок: \(.transfers)",
      (.legs[] | "  \(.mode): \(.from.name // .from.stop_id) → \(.to.name // .to.stop_id)  \(.departure | .[11:16])–\(.arrival | .[11:16])"))
  end' "$OUT"

say "MCP find_route: по населённым пунктам Пермь → Екатеринбург"
hr
rpc tools/call '{"name":"find_route","arguments":{"from_place":"Пермь","to_place":"Екатеринбург","departure":"2026-08-30T06:00:00Z"}}'
jq -r '.result.content[0].text | fromjson |
  "прибытие: \(.arrival)  пересадок: \(.transfers)",
  (.legs[] | "  \(.mode): \(.from.name // .from.stop_id) → \(.to.name // .to.stop_id)  \(.departure | .[11:16])–\(.arrival | .[11:16])")' "$OUT"

say "MCP find_route: аэропорты (перелёт, 07:00)"
hr
rpc tools/call '{"name":"find_route","arguments":{"from_lat":57.9148,"from_lon":56.0217,"to_lat":56.7431,"to_lon":60.8028,"departure":"2026-08-30T07:00:00Z"}}'
jq -r '.result.content[0].text | fromjson |
  "прибытие: \(.arrival)  пересадок: \(.transfers)",
  (.legs[] | "  \(.mode): \(.from.name // .from.stop_id) → \(.to.name // .to.stop_id)  \(.departure | .[11:16])–\(.arrival | .[11:16])")' "$OUT"

say "MCP find_route: белое пятно (ошибка)"
hr
rpc tools/call '{"name":"find_route","arguments":{"from_lat":58.003,"from_lon":56.285,"to_lat":56.84,"to_lon":60.607,"departure":"2026-08-30T06:00:00Z"}}'
jq -r '.result.isError, .result.content[0].text' "$OUT"

say "MCP list_providers"
hr
rpc tools/call '{"name":"list_providers","arguments":{}}'
jq -r '.result.content[0].text | fromjson | to_entries[] | "\(.key): up=\(.value.up) records=\(.value.records)"' "$OUT"

echo
echo ">> done"