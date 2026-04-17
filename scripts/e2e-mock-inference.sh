#!/bin/bash
# e2e-mock-inference.sh — Run the full P2P e2e pipeline against mock-tgi.py.
#
# Same as scripts/e2e-real-inference.sh but with a Python mock TGI server
# started/stopped automatically. No GPU required, fully deterministic, CI-safe.
#
# Ports are pre-offset to avoid collision with a running main testnet on
# 46656/46657/21317. Override via env if needed.
#
# Usage:
#   bash scripts/e2e-mock-inference.sh                # Run all phases
#   MOCK_PORT=18081 bash scripts/e2e-mock-inference.sh  # Custom mock TGI port
set -euo pipefail

MOCK_PORT="${MOCK_PORT:-18080}"

# Kill any stale mock-tgi instances on our port (from a prior aborted run).
# shellcheck disable=SC2009
ps -ef | grep -E "mock-tgi.py $MOCK_PORT" | grep -v grep | awk '{print $2}' | xargs -r kill -9 2>/dev/null || true

python3 scripts/mock-tgi.py "$MOCK_PORT" --tgi-version 3 >/tmp/mock-tgi.log 2>&1 &
MOCK_PID=$!
trap 'kill -9 $MOCK_PID 2>/dev/null || true' EXIT

# Wait for mock TGI to come up (up to 10s).
for _ in $(seq 1 10); do
  if curl -sf "http://localhost:$MOCK_PORT/health" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
if ! curl -sf "http://localhost:$MOCK_PORT/health" >/dev/null 2>&1; then
  echo "mock-tgi failed to start on port $MOCK_PORT"
  tail -20 /tmp/mock-tgi.log
  exit 1
fi

export TGI_ENDPOINT="http://localhost:$MOCK_PORT"
# Offset all chain + P2P ports so we don't clash with a running main testnet.
export P2P_PORT_BASE="${P2P_PORT_BASE:-56656}"
export RPC_PORT_BASE="${RPC_PORT_BASE:-56657}"
export API_PORT_BASE="${API_PORT_BASE:-31317}"
export GRPC_PORT_BASE="${GRPC_PORT_BASE:-39090}"
export P2P_LIBP2P_PORT_BASE="${P2P_LIBP2P_PORT_BASE:-15001}"

exec bash scripts/e2e-real-inference.sh "$@"
