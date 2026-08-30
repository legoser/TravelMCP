#!/bin/sh
# Шаблоны запросов к MCP travelmcp: find_route (населённый пункт / координаты),
# tools/list, list_providers. POSIX sh: работает в sh, bash, zsh, dash.
#
# Зависимости: curl, jq.
#
# Конфигурация через env (переопределяемо):
#   BASE    URL сервера, по умолчанию http://127.0.0.1:8080
#   TOKEN   API-ключ (X-API-Key), по умолчанию dev-secret (config.example.yaml)
#
# Использование:
#   ./scripts/mcp-route.sh place   <from> <to> [departure] [max_walk] [max_transfers]
#   ./scripts/mcp-route.sh coords  <from_lat> <from_lon> <to_lat> <to_lon> \
#                                   [departure] [max_walk] [max_transfers]
#   ./scripts/mcp-route.sh tools
#   ./scripts/mcp-route.sh providers
#
# Примеры:
#   ./scripts/mcp-route.sh place Юрга Барнаул
#   ./scripts/mcp-route.sh place Новосибирск Томск 2026-08-30T06:00:00Z 30
#   ./scripts/mcp-route.sh coords 55.041057 83.027382 53.352306 83.759124
set -eu

BASE="${BASE:-http://127.0.0.1:8080}"
TOKEN="${TOKEN:-dev-secret}"
MCP="$BASE/mcp"
NOW="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

command -v curl >/dev/null 2>&1 || { echo "требуется curl" >&2; exit 1; }
command -v jq >/dev/null 2>&1 || { echo "требуется jq" >&2; exit 1; }

json_escape() {
  printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'
}

rpc() {
  method="$1"
  params="$2"
  curl -s -H "X-API-Key: $TOKEN" -X POST "$MCP" \
    -H 'Content-Type: application/json' -H 'Accept: application/json' \
    -d "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"$method\",\"params\":$params}"
}

fmt_route() {
  jq -r '
    if .error then "MCP error: \(.error.message)"
    elif .result.isError == true then "isError: \(.result.content[0].text)"
    else
      (.result.content[0].text | fromjson |
        "прибытие: \(.arrival)  пересадок: \(.transfers)",
        (.legs[] | "  \(.mode): \(.departure) \(.from.name // .from.stop_id) → \(.to.name // .to.stop_id) \(.arrival)"))
    end'
}

case "${1:-}" in
  place)
    test "${2:-}" && test "${3:-}" || {
      echo "usage: $0 place <from> <to> [departure] [max_walk] [max_transfers]" >&2
      exit 1
    }
    FROM="$(json_escape "$2")"
    TO="$(json_escape "$3")"
    DEP="${4:-$NOW}"
    WALK="${5:-30}"
    TRF="${6:--1}"
    args="{\"from_place\":\"$FROM\",\"to_place\":\"$TO\",\"departure\":\"$DEP\",\"max_walk_minutes\":$WALK,\"max_transfers\":$TRF}"
    rpc tools/call "{\"name\":\"find_route\",\"arguments\":$args}" | fmt_route
    ;;
  coords)
    test "${2:-}" && test "${3:-}" && test "${4:-}" && test "${5:-}" || {
      echo "usage: $0 coords <from_lat> <from_lon> <to_lat> <to_lon> [departure] [max_walk] [max_transfers]" >&2
      exit 1
    }
    DEP="${6:-$NOW}"
    WALK="${7:-30}"
    TRF="${8:--1}"
    args="{\"from_lat\":$2,\"from_lon\":$3,\"to_lat\":$4,\"to_lon\":$5,\"departure\":\"$DEP\",\"max_walk_minutes\":$WALK,\"max_transfers\":$TRF}"
    rpc tools/call "{\"name\":\"find_route\",\"arguments\":$args}" | fmt_route
    ;;
  tools)
    rpc tools/list '{}' | jq -r '.result.tools[].name'
    ;;
  providers)
    curl -s -H "X-API-Key: $TOKEN" "$BASE/api/v1/providers" |
      jq -r 'to_entries[] | "\(.key): up=\(.value.up) records=\(.value.records)"'
    ;;
  *)
    cat <<'EOF'
scripts/mcp-route.sh — шаблоны запросов к MCP travelmcp

  place  <from> <to> [departure] [max_walk] [max_transfers]   маршрут по населённым пунктам
  coords <from_lat> <from_lon> <to_lat> <to_lon> [departure] [max_walk] [max_transfers]
                                                              маршрут по координатам
  tools                                                       список инструментов
  providers                                                   состояние источников

env: BASE (по умолчанию http://127.0.0.1:8080), TOKEN (по умолчанию dev-secret)
примеры:
  ./scripts/mcp-route.sh place Юрга Барнаул
  ./scripts/mcp-route.sh place Новосибирск Томск 2026-08-30T06:00:00Z 30
  ./scripts/mcp-route.sh coords 55.041057 83.027382 53.352306 83.759124
EOF
    exit 1
    ;;
esac