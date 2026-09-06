#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
fail=0

check() {
  if ! eval "$1"; then
    echo "doc-lint FAIL: $2"
    fail=1
  fi
}

check "grep -q \"legacy_unmatched\" migrations/001_initial.sql && grep -q \"skeleton_unverified\" migrations/001_initial.sql && grep -q \"incomplete_trip\" migrations/001_initial.sql && grep -q \"possible_merge\" migrations/001_initial.sql && grep -q \"low_confidence\" migrations/001_initial.sql && grep -q \"duplicate_ambiguous\" migrations/001_initial.sql" \
  "enum-канон review_queue (§3.10) отсутствует в migrations/001_initial.sql"

check "grep -q \"geocode_cache\" migrations/001_initial.sql && grep -q \"attribute_state\" migrations/001_initial.sql && grep -q \"sync_runs\" migrations/001_initial.sql && grep -q \"sync_chunks\" migrations/001_initial.sql" \
  "таблицы фазы 1 (geocode_cache/attribute_state/sync_runs/sync_chunks) отсутствуют в миграции"

check "grep -q \"external_route_code text NOT NULL\" migrations/001_initial.sql && grep -q \"external_trip_code text NOT NULL\" migrations/001_initial.sql && grep -q \"source_provider text NOT NULL\" migrations/001_initial.sql" \
  "обе половины NK ресинка (source_provider + код) обязаны быть NOT NULL (иначе UNIQUE не ловит дубли)"

check "! grep -q \"external_code text\" migrations/001_initial.sql" \
  "дубль natural key на routes (external_code) обязан быть удалён"

check "grep -q \"quota_limit\" migrations/001_initial.sql && ! grep -qE \"used, limit[,)]\" migrations/001_initial.sql" \
  "колонка лимита квоты обязана называться quota_limit, не limit"

check "grep -q \"ttl_class\" migrations/001_initial.sql && grep -q \"esr_code\" migrations/001_initial.sql && grep -q \"is_synthetic_key\" migrations/001_initial.sql && grep -q \"'manual'\" migrations/001_initial.sql" \
  "ttl_class / esr_code / is_synthetic_key / providers.manual обязаны быть в миграции"

check "! grep -qE \"^ALTER TABLE|^UPDATE \" migrations/001_initial.sql" \
  "миграция 001 обязана содержать только создание, без ALTER/UPDATE"

check "! grep -n \"14-plan.md:\" docs/02-glossary.md | grep -vE \"14-plan.md:3\\.[0-9]+\" || true" \
  "проверка ссылок §3.x вручную (см. список ниже)"
echo "==> ссылки на 14-plan.md из глоссария:"
grep -n "14-plan.md:" docs/02-glossary.md || true

for lvl in 0 1 2 3 4 5; do
  check "grep -q \"| $lvl |\" docs/02-glossary.md" "шкала places.level: нет строки level $lvl в 02-glossary.md"
done

for v in HTTP_ADDR DATABASE_DSN PROVIDERS_ENABLED INTERCITY_REESTR_PATH ADMIN_TOKEN SYNC_LOG_DIR SYNC_COVERAGE_GATE SYNC_ATTACH_WAIT SYNC_TRIPS_CHURN_THRESHOLD SYNC_TRIPS_MAX_SPEED_KMH VERIFICATION_SCORE_MARGIN VERIFICATION_SCORE_AMBIGUITY GEOCODE_TTL_VERIFIED GEOCODE_TTL_DISPUTED; do
  check "grep -q \"$v\" docs/15-dev-status.md" "env $v не задокументирована в docs/15-dev-status.md"
done

if [ "$fail" -ne 0 ]; then
  echo "doc-lint: FAILED"
  exit 1
fi
echo "doc-lint: OK"
