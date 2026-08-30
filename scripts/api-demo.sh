#!/usr/bin/env bash
# Демо/обследование API и MCP-инструментов travelmcp (ручной режим).
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

echo ">> starting server on :$PORT"
HTTP_ADDR="127.0.0.1:$PORT" PROVIDERS_ENABLED=synth "$BIN" -config "$DIR/configs/config.example.yaml" >/tmp/travelmcp-demo.log 2>&1 &
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
curl -s "$BASE/api/v1/providers" | jq -r 'to_entries[] | "\(.key): up=\(.value.up) records=\(.value.records)"'

SESSION=/tmp/travelmcp-session.txt
: > "$SESSION"
ID=0

rpc() {
  local method="$1" payload="$2" hdr sid newsid
  local auth=()
  sid=$(cat "$SESSION" 2>/dev/null || true)
  [ -n "$sid" ] && auth=(-H "Mcp-Session-Id: $sid")
  ID=$((ID + 1))
  hdr=$(curl -s -D - -o "$OUT" -X POST "$BASE/mcp" \
    -H 'Content-Type: application/json' -H 'Accept: application/json' \
    "${auth[@]}" \
    -d "{\"jsonrpc\":\"2.0\",\"id\":$ID,\"method\":\"$method\",\"params\":$payload}")
  newsid=$(printf '%s' "$hdr" | tr -d '\r' | sed -n 's/^Mcp-Session-Id: //p')
  if [ -n "$newsid" ]; then
    printf '%s' "$newsid" > "$SESSION"
  fi
}

say "MCP initialize / tools/list"
hr
rpc initialize '{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"api-demo","version":"0"}}'
jq -r '.result.serverInfo.name + " v" + .result.serverInfo.version, (.result.capabilities.tools | "tools: \(.listChanged)")' "$OUT"
rpc tools/list '{}'
jq -r '.result.tools[].name' "$OUT"

say "MCP find_route: Пермь → Екатеринбург (наземка, 06:00)"
hr
rpc tools/call '{"name":"find_route","arguments":{"from_lat":58.0135,"from_lon":56.2495,"to_lat":56.84,"to_lon":60.607,"departure":"2026-08-30T06:00:00Z"}}'
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