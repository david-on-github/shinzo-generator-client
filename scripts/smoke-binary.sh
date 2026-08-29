#!/usr/bin/env bash
# Functional smoke test for the generator binary against a local anvil chain.
#  Phase 1: index blocks CONTAINING TRANSACTIONS (empty blocks would skip the
#           tx/receipt paths); require >=5 committed blocks, a multi-doc block,
#           and a healthy /health.
#  Phase 2: crash recovery — kill -9, restart on the same data dir with the same
#           passphrase, require health + further commits.
# Usage: [ANVIL_PORT=8545] scripts/smoke-binary.sh <path-to-binary>   (run from repo root; needs anvil + cast + curl)
set -euo pipefail

BINARY="${1:?usage: smoke-binary.sh <path-to-binary>}"
REPO_ROOT="$(pwd)"
WORKDIR="$(mktemp -d)"
LOG="$WORKDIR/generator.log"

# anvil's default funded account #0 -> #1
KEY0=0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80
ADDR1=0x70997970C51812dc3A010C7d01b50e0d17dc79C8
ANVIL_PORT="${ANVIL_PORT:-8545}"
RPC="http://127.0.0.1:$ANVIL_PORT"

for tool in anvil cast curl; do
  command -v "$tool" >/dev/null 2>&1 || { echo "FAIL(preflight): '$tool' not on PATH"; exit 1; }
done
[ -x "$REPO_ROOT/$BINARY" ] || { echo "FAIL(preflight): binary not found/executable: $BINARY"; exit 1; }

cleanup() { kill "${GEN_PID:-}" "${TX_PID:-}" "${ANVIL_PID:-}" 2>/dev/null || true; }
trap cleanup EXIT

anvil --block-time 1 --port "$ANVIL_PORT" >"$WORKDIR/anvil.log" 2>&1 &
ANVIL_PID=$!
for _ in $(seq 1 30); do
  curl -sf -X POST -H 'Content-Type: application/json' \
    --data '{"jsonrpc":"2.0","id":1,"method":"eth_blockNumber","params":[]}' "$RPC" >/dev/null 2>&1 && break
  sleep 1
done
cast send "$ADDR1" --value 1 --private-key "$KEY0" --rpc-url "$RPC" >/dev/null 2>&1 \
  || { echo "FAIL(preflight): cast send against anvil failed"; tail -20 "$WORKDIR/anvil.log"; exit 1; }

# Keep blocks non-empty.
( while true; do cast send "$ADDR1" --value 1 --private-key "$KEY0" --rpc-url "$RPC" >/dev/null 2>&1 || true; sleep 0.2; done ) &
TX_PID=$!

# Data is CWD-relative (./data) — run inside the temp dir with the repo's config.
cp -r "$REPO_ROOT/config" "$WORKDIR/config"
cd "$WORKDIR"

start_generator() {
  GETH_RPC_URL="$RPC" GETH_WS_URL="ws://127.0.0.1:$ANVIL_PORT" GETH_API_KEY= GETH_API_KEY_TYPE= \
  SCHEMA_AUTH_MODE=none SHINZO_KEY_PASSPHRASE=smoke-test-secret SHINZO_DATA_DIR="$WORKDIR/data" LOG_LEVEL=info \
  "$REPO_ROOT/$BINARY" >>"$LOG" 2>&1 &
  GEN_PID=$!
}

max_docs() { sed -n 's/.*signed (\([0-9]*\) .*/\1/p' "$LOG" 2>/dev/null | sort -n | tail -1; }

await_progress() { # $1=min commits, $2=phase label, $3=timeout seconds
  local deadline=$((SECONDS + $3)) commits health
  while [ "$SECONDS" -lt "$deadline" ]; do
    kill -0 "$GEN_PID" 2>/dev/null || { echo "FAIL($2): generator exited early"; tail -30 "$LOG"; exit 1; }
    commits=$(grep -c "Committed block" "$LOG" 2>/dev/null || true)
    health=$(curl -sf -m 3 -H 'Accept: application/json' http://127.0.0.1:8080/health 2>/dev/null || true)
    if [ "${commits:-0}" -ge "$1" ] && printf '%s' "$health" | grep -q '"status":"healthy"'; then
      echo "OK($2): $commits blocks committed, /health healthy"; return 0
    fi
    sleep 2
  done
  echo "FAIL($2): timeout (commits=${commits:-0})"; tail -40 "$LOG"; exit 1
}

# ---- Phase 1: fresh start, tx-bearing blocks
start_generator
await_progress 5 "phase1" 120
DOCS=$(max_docs)
[ "${DOCS:-0}" -ge 2 ] || { echo "FAIL(phase1): no multi-item block seen (max=${DOCS:-0})"; tail -30 "$LOG"; exit 1; }
echo "OK(phase1): max docs in a block = $DOCS"

# ---- Phase 2: crash recovery on the same data dir
PHASE1_COMMITS=$(grep -c "Committed block" "$LOG")
echo "killing generator with SIGKILL (pid $GEN_PID)..."
kill -9 "$GEN_PID"; sleep 2
start_generator
await_progress $((PHASE1_COMMITS + 3)) "phase2-recovery" 120
grep -qi "failed to load existing" "$LOG" && { echo "FAIL(phase2): identity failed to reload"; exit 1; }
echo "PASS: indexing, tx docs, health, and crash recovery all verified"
