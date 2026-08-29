#!/usr/bin/env bash
# Upgrade-path smoke test for the generator: an operator running the PREVIOUS
# release (its binary, its config file, its ./.defra layout, its env var
# names) starts the NEW binary on the same directory and keeps their identity.
#
#  1. anvil produces blocks with transactions.
#  2. The OLD generator runs the way it shipped: ./config/config.yaml + ./.defra,
#     DEFRADB_KEYRING_SECRET in the environment. It commits some blocks.
#  3. kill -9 it. Start the NEW binary in the same directory with the OLD config
#     file untouched and the OLD env var name.
#  4. Assert: same peer ID, healthy, commits continue, state stayed in ./.defra
#     (nothing was created under ~/.shinzo), no new passphrase generated.
#
# Usage: [ANVIL_PORT=8545] scripts/smoke-upgrade.sh <old-binary> <new-binary>
#   run from the NEW repo root; the old binary's repo must be at <old-binary>/../..
set -euo pipefail

OLD_BIN="${1:?usage: smoke-upgrade.sh <old-binary> <new-binary>}"; NEW_BIN="${2:?}"
REPO_ROOT="$(pwd)"
ANVIL_PORT="${ANVIL_PORT:-8545}"; RPC="http://127.0.0.1:$ANVIL_PORT"
WORKDIR="$(mktemp -d)"; OLD_LOG="$WORKDIR/old.log"; NEW_LOG="$WORKDIR/new.log"
HTTP=8084; DEFRA=9185; P2P=9175

KEY0=0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80
ADDR1=0x70997970C51812dc3A010C7d01b50e0d17dc79C8

abs() { case "$1" in /*) echo "$1";; *) echo "$REPO_ROOT/$1";; esac; }
OLD_BIN="$(abs "$OLD_BIN")"; NEW_BIN="$(abs "$NEW_BIN")"
OLD_REPO="$(dirname "$(dirname "$OLD_BIN")")"
for tool in anvil cast curl python3; do command -v "$tool" >/dev/null || { echo "FAIL(preflight): '$tool' not on PATH"; exit 1; }; done
[ -x "$OLD_BIN" ] && [ -x "$NEW_BIN" ] || { echo "FAIL(preflight): binaries not executable"; exit 1; }
[ -f "$OLD_REPO/config/config.yaml" ] || { echo "FAIL(preflight): old repo config not found at $OLD_REPO/config/config.yaml"; exit 1; }

cleanup() { kill "${GEN_PID:-}" "${TX_PID:-}" "${ANVIL_PID:-}" 2>/dev/null || true; }
trap cleanup EXIT

anvil --block-time 1 --port "$ANVIL_PORT" >"$WORKDIR/anvil.log" 2>&1 &
ANVIL_PID=$!
for _ in $(seq 1 30); do curl -sf -X POST -H 'Content-Type: application/json' --data '{"jsonrpc":"2.0","id":1,"method":"eth_blockNumber","params":[]}' "$RPC" >/dev/null 2>&1 && break; sleep 1; done
( while true; do cast send "$ADDR1" --value 1 --private-key "$KEY0" --rpc-url "$RPC" >/dev/null 2>&1 || true; sleep 0.2; done ) &
TX_PID=$!

# ---- the operator's old installation ----------------------------------------------------
OLD_DIR="$WORKDIR/old"; mkdir -p "$OLD_DIR/config"
python3 - "$OLD_REPO/config/config.yaml" "$OLD_DIR/config/config.yaml" "$DEFRA" "$P2P" "$HTTP" <<'PY'
import re, sys
src, dst, defra, p2p, http = sys.argv[1:]
s = open(src).read()
s = s.replace('url: "http://localhost:9181"', f'url: "http://localhost:{defra}"', 1)
s = re.sub(r'listen_addr: "/ip4/0\.0\.0\.0/tcp/9171"', f'listen_addr: "/ip4/0.0.0.0/tcp/{p2p}"', s, count=1)
s = re.sub(r'^(\s*health_server_port:) 8080', rf'\1 {http}', s, count=1, flags=re.M)
assert './.defra' in s, "old config no longer uses ./.defra — is the old binary really old?"
open(dst, 'w').write(s)
PY

peer_id() { curl -sf -m 3 -H 'Accept: application/json' "http://127.0.0.1:$HTTP/registration" 2>/dev/null | python3 -c 'import json,sys; print((json.load(sys.stdin).get("p2p") or {}).get("self",{}).get("id",""))' 2>/dev/null || true; }
await_commits() { # $1=log $2=min $3=label
  local deadline=$((SECONDS + 150)) n
  while [ "$SECONDS" -lt "$deadline" ]; do
    kill -0 "$GEN_PID" 2>/dev/null || { echo "FAIL($3): generator exited early"; tail -30 "$1"; exit 1; }
    n=$(grep -c "Committed block" "$1" 2>/dev/null || true)
    [ "${n:-0}" -ge "$2" ] && [ -n "$(peer_id)" ] && { echo "OK($3): commits=$n"; return 0; }
    sleep 2
  done
  echo "FAIL($3): timeout"; tail -30 "$1"; exit 1
}
COMMON_ENV=(GETH_RPC_URL="$RPC" GETH_WS_URL="ws://127.0.0.1:$ANVIL_PORT" GETH_API_KEY= GETH_API_KEY_TYPE= SCHEMA_AUTH_MODE=none LOG_LEVEL=info)

echo "starting OLD generator ($(git -C "$OLD_REPO" rev-parse --short HEAD 2>/dev/null || echo '?')) on ./.defra ..."
( cd "$OLD_DIR" && exec env HOME="$WORKDIR/home-old" "${COMMON_ENV[@]}" DEFRADB_KEYRING_SECRET=old-secret "$OLD_BIN" -config config/config.yaml >>"$OLD_LOG" 2>&1 ) &
GEN_PID=$!
await_commits "$OLD_LOG" 3 "old-generator"
OLD_PEER=$(peer_id)
[ -d "$OLD_DIR/.defra" ] || { echo "FAIL(old-generator): expected state in ./.defra"; ls -la "$OLD_DIR"; exit 1; }
echo "OK(old-generator): peer $OLD_PEER, state in ./.defra"
kill -9 "$GEN_PID"; sleep 2

# ---- upgrade: new binary, same directory, old config file, old env var name -------------------
echo "starting NEW generator in the same directory ..."
( cd "$OLD_DIR" && exec env HOME="$WORKDIR/home-new" "${COMMON_ENV[@]}" DEFRADB_KEYRING_SECRET=old-secret "$NEW_BIN" run >>"$NEW_LOG" 2>&1 ) &
GEN_PID=$!
await_commits "$NEW_LOG" 3 "new-generator"
NEW_PEER=$(peer_id)
[ "$OLD_PEER" = "$NEW_PEER" ] || { echo "FAIL(upgrade): peer id changed ($OLD_PEER -> $NEW_PEER)"; exit 1; }
grep -q 'config: ./config/config.yaml' "$NEW_LOG" || { echo "FAIL(upgrade): new binary did not pick up the old config file"; grep -m1 'starting' "$NEW_LOG"; exit 1; }
[ -e "$WORKDIR/home-new/.shinzo" ] && { echo "FAIL(upgrade): new binary created ~/.shinzo despite the old layout"; exit 1; }
grep -q 'Passphrase   : generated' "$NEW_LOG" && { echo "FAIL(upgrade): a new passphrase was generated — old secret ignored"; exit 1; }
echo "PASS(upgrade): same identity $NEW_PEER, old config + ./.defra + DEFRADB_KEYRING_SECRET all honoured, indexing continues"
