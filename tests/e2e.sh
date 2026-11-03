#!/usr/bin/env bash
# End-to-end smoke test: ingest, query, alerts, AI diagnosis, metrics.
# Usage: ./tests/e2e.sh [BASE_URL]
set -euo pipefail

BASE="${1:-http://localhost:8081}"
PASS=0
FAIL=0

ok()  { echo "  PASS $1"; PASS=$((PASS + 1)); }
bad() { echo "  FAIL $1"; FAIL=$((FAIL + 1)); }

check_contains() {
  local desc="$1" haystack="$2" needle="$3"
  if echo "$haystack" | grep -q "$needle"; then ok "$desc"; else bad "$desc (expected: $needle)"; fi
}

echo "== health =="
health=$(curl -fsS "$BASE/healthz")
check_contains "/healthz returns ok" "$health" '"status":"ok"'

echo "== ingest and query =="
for i in $(seq 1 20); do
  v=$((100 + i % 3))
  curl -fsS -X POST "$BASE/api/v1/points" \
    -H 'Content-Type: application/json' \
    -d "{\"metric\":\"e2e_latency_ms\",\"value\":$v}" > /dev/null
done
curl -fsS -X POST "$BASE/api/v1/points" \
  -H 'Content-Type: application/json' \
  -d '{"metric":"e2e_latency_ms","value":520}' > /dev/null

query=$(curl -fsS "$BASE/api/v1/query?metric=e2e_latency_ms")
count=$(echo "$query" | python3 -c 'import json,sys;print(json.load(sys.stdin)["count"])')
if [ "$count" -ge 21 ]; then ok "query returns $count points"; else bad "query returned $count points, expected >= 21"; fi

echo "== alerts =="
alerts=$(curl -fsS "$BASE/api/v1/alerts?limit=50")
check_contains "zscore alert fired" "$alerts" 'zscore-anomaly'

echo "== AI diagnosis =="
diag=$(curl -fsS -X POST "$BASE/api/v1/diagnose" \
  -H 'Content-Type: application/json' -d '{"metric":"e2e_latency_ms"}')
check_contains "diagnosis returns analysis" "$diag" 'analysis'

echo "== metrics =="
metrics=$(curl -fsS "$BASE/metrics")
check_contains "router metrics exposed" "$metrics" 'router_requests_total'

echo
echo "passed=$PASS failed=$FAIL"
if [ "$FAIL" -gt 0 ]; then exit 1; fi
