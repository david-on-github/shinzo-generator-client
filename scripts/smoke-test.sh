#!/usr/bin/env bash
# Smoke-test a running node's HTTP surface.
#   scripts/smoke-test.sh [base-url] [allowed-origin]
# Defaults: http://localhost  and  http://localhost:3000
set -u
BASE="${1:-http://localhost}"
ORIGIN="${2:-http://localhost:3000}"
pass=0; fail=0
check() { # name, condition-exit-code
  if [ "$2" -eq 0 ]; then echo "  ✅ $1"; pass=$((pass+1)); else echo "  ❌ $1"; fail=$((fail+1)); fi
}
hdr() { curl -s -o /dev/null -D - "$@" 2>/dev/null; }

echo "Node at $BASE"

code=$(curl -s -o /dev/null -w '%{http_code}' -H 'Accept: application/json' "$BASE/health")
check "GET /health returns 200 or 503 (got $code)" $([ "$code" = 200 ] || [ "$code" = 503 ]; echo $?)

code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/metrics")
check "GET /metrics returns 200 (got $code)" $([ "$code" = 200 ]; echo $?)

code=$(curl -s -o /dev/null -w '%{http_code}' -H 'Accept: application/json' "$BASE/registration")
check "GET /registration answers (got $code)" $([ "$code" != 000 ] && [ "$code" != 404 ]; echo $?)

code=$(curl -s -o /dev/null -w '%{http_code}' -H 'Accept: application/json' "$BASE/api/v0/registration")
check "GET /api/v0/registration alias not proxied to DefraDB (got $code)" $([ "$code" != 404 ] && [ "$code" != 502 ]; echo $?)

body=$(curl -s -X POST "$BASE/api/v0/graphql" -H 'Content-Type: application/json' -d '{"query":"{ __typename }"}')
check "POST /api/v0/graphql proxied to DefraDB ($(echo "$body" | head -c 60))" $(echo "$body" | grep -q '__typename\|data\|errors'; echo $?)

h=$(hdr -X OPTIONS -H "Origin: $ORIGIN" -H 'Access-Control-Request-Method: POST' "$BASE/api/v0/graphql")
check "OPTIONS preflight from $ORIGIN → 204 + Allow-Origin" $(echo "$h" | grep -q '^HTTP/.* 204' && echo "$h" | grep -qi "access-control-allow-origin: $ORIGIN"; echo $?)

h=$(hdr -H 'Origin: https://evil.example' "$BASE/health")
check "Disallowed origin gets no Allow-Origin header" $(! echo "$h" | grep -qi 'access-control-allow-origin'; echo $?)

h=$(hdr "$BASE/health")
check "Vary: Origin present" $(echo "$h" | grep -qi '^vary:.*origin'; echo $?)

peers=$(curl -s -H 'Accept: application/json' "$BASE/health" | python3 -c 'import json,sys; d=json.load(sys.stdin); print(len((d.get("p2p") or {}).get("peers") or []))' 2>/dev/null || echo 0)
check "P2P peers connected: $peers" $([ "${peers:-0}" -gt 0 ]; echo $?)

echo; echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
