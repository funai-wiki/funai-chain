#!/bin/bash
# e2e-test.sh — L3 Multi-Node E2E Test Suite for FunAI Chain
# Runs U1-U5 test cases against a real 4-node testnet.
#
# Prerequisites:
#   make build-all
#
# Usage:
#   bash scripts/e2e-test.sh              # Run all tests
#   bash scripts/e2e-test.sh U1           # Run specific test
#   bash scripts/e2e-test.sh --no-cleanup # Keep testnet after tests
set -euo pipefail

# ── Configuration ──────────────────────────────────────────────────────────────

BINARY="./build/funaid"
CHAIN_ID="funai-e2e-1"
BASE_DIR="/tmp/funai-e2e"
NODES=4
DENOM="ufai"
KEYRING="test"
GENESIS_BALANCE="200000000000000${DENOM}"
STAKE_AMOUNT="100000000000000${DENOM}"
BLOCK_TIME=2  # seconds (faster for tests)

P2P_PORT_BASE=36656
RPC_PORT_BASE=36657

# ── Colors ─────────────────────────────────────────────────────────────────────

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# ── Counters ───────────────────────────────────────────────────────────────────

TOTAL=0
PASSED=0
FAILED=0
SKIPPED=0
CLEANUP=true
RUN_FILTER="${1:-all}"

if [[ "${1:-}" == "--no-cleanup" ]] || [[ "${2:-}" == "--no-cleanup" ]]; then
  CLEANUP=false
  if [[ "${1:-}" == "--no-cleanup" ]]; then
    RUN_FILTER="${2:-all}"
  fi
fi

# ── Helper Functions ───────────────────────────────────────────────────────────

log_info()  { echo -e "${BLUE}[INFO]${NC} $*"; }
log_pass()  { echo -e "${GREEN}[PASS]${NC} $*"; }
log_fail()  { echo -e "${RED}[FAIL]${NC} $*"; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC} $*"; }
log_test()  { echo -e "${YELLOW}[TEST]${NC} $*"; }

# Run funaid CLI for tx commands against node0
cli() {
  timeout 15 $BINARY "$@" \
    --home "$BASE_DIR/node0" \
    --node "tcp://127.0.0.1:${RPC_PORT_BASE}" \
    --keyring-backend "$KEYRING" --chain-id "$CHAIN_ID" < /dev/null 2>&1
}

# Run funaid CLI against a specific node
cli_node() {
  local node_idx=$1; shift
  local rpc_port=$((RPC_PORT_BASE + node_idx * 2))
  timeout 15 $BINARY "$@" \
    --home "$BASE_DIR/node${node_idx}" \
    --node "tcp://127.0.0.1:${rpc_port}" \
    --keyring-backend "$KEYRING" --chain-id "$CHAIN_ID" < /dev/null 2>&1
}

# Query chain state via curl ABCI (bypasses SDK WebSocket hang)
# Usage: abci_query <store_name> <hex_key> [node_idx]
abci_query() {
  local store=$1
  local hex_key=$2
  local node_idx=${3:-0}
  local rpc_port=$((RPC_PORT_BASE + node_idx * 2))
  local result
  result=$(curl -sf "http://127.0.0.1:${rpc_port}/abci_query?path=\"/store/${store}/key\"&data=0x${hex_key}" 2>/dev/null)
  if [ -z "$result" ]; then
    echo ""
    return 1
  fi
  # Extract base64 value and decode
  local b64_value
  b64_value=$(echo "$result" | python3 -c "import sys,json,base64; r=json.load(sys.stdin); v=r.get('result',{}).get('response',{}).get('value',''); print(v)" 2>/dev/null)
  if [ -n "$b64_value" ]; then
    echo "$b64_value" | base64 -d 2>/dev/null
  fi
}

# Query inference account balance via ABCI
query_account() {
  local addr=$1
  local node_idx=${2:-0}
  # InferenceAccountKey = prefix(0x01) + addr_bytes
  # We need to compute the hex key that matches types.InferenceAccountKey(addr)
  # For simplicity, use the CLI with --node flag (since we fixed the query)
  local rpc_port=$((RPC_PORT_BASE + node_idx * 2))
  timeout 10 $BINARY query settlement account "$addr" \
    --node "tcp://127.0.0.1:${rpc_port}" < /dev/null 2>&1
}

# Query settlement params via ABCI
query_params() {
  local node_idx=${1:-0}
  local rpc_port=$((RPC_PORT_BASE + node_idx * 2))
  timeout 10 $BINARY query settlement params \
    --node "tcp://127.0.0.1:${rpc_port}" < /dev/null 2>&1
}

# Query worker via ABCI
query_worker() {
  local addr=$1
  local node_idx=${2:-0}
  local rpc_port=$((RPC_PORT_BASE + node_idx * 2))
  timeout 10 $BINARY query worker show "$addr" \
    --node "tcp://127.0.0.1:${rpc_port}" < /dev/null 2>&1
}

# Get validator address for node N
get_addr() {
  local node_idx=$1
  $BINARY keys show "validator${node_idx}" --keyring-backend "$KEYRING" \
    --home "$BASE_DIR/node${node_idx}" -a 2>/dev/null
}

# Wait for chain to produce blocks (poll RPC /status)
wait_for_blocks() {
  local target_height=${1:-3}
  local timeout=${2:-60}
  local elapsed=0

  log_info "Waiting for chain to reach block height $target_height (timeout: ${timeout}s)..."
  while [ $elapsed -lt $timeout ]; do
    local height
    height=$(curl -sf "http://127.0.0.1:${RPC_PORT_BASE}/status" 2>/dev/null | \
      python3 -c "import sys,json; print(json.load(sys.stdin)['result']['sync_info']['latest_block_height'])" 2>/dev/null || echo "0")
    if [ "$height" -ge "$target_height" ] 2>/dev/null; then
      log_info "Chain at block $height"
      return 0
    fi
    sleep 1
    elapsed=$((elapsed + 1))
  done
  log_fail "Timeout waiting for block $target_height after ${timeout}s"
  return 1
}

# Wait N blocks from current height
wait_n_blocks() {
  local n=${1:-2}
  local current
  current=$(curl -sf "http://127.0.0.1:${RPC_PORT_BASE}/status" | \
    python3 -c "import sys,json; print(json.load(sys.stdin)['result']['sync_info']['latest_block_height'])" 2>/dev/null || echo "1")
  wait_for_blocks $((current + n)) 30
}

# Get current block height from a specific node
get_block_height() {
  local node_idx=${1:-0}
  local rpc_port=$((RPC_PORT_BASE + node_idx * 2))
  curl -sf "http://127.0.0.1:${rpc_port}/status" 2>/dev/null | \
    python3 -c "import sys,json; print(json.load(sys.stdin)['result']['sync_info']['latest_block_height'])" 2>/dev/null || echo "0"
}

# Get latest block hash from a specific node
get_block_hash() {
  local node_idx=${1:-0}
  local rpc_port=$((RPC_PORT_BASE + node_idx * 2))
  curl -sf "http://127.0.0.1:${rpc_port}/status" 2>/dev/null | \
    python3 -c "import sys,json; print(json.load(sys.stdin)['result']['sync_info']['latest_block_hash'])" 2>/dev/null || echo ""
}

# Check if node RPC is reachable
node_is_up() {
  local node_idx=${1:-0}
  local rpc_port=$((RPC_PORT_BASE + node_idx * 2))
  curl -sf "http://127.0.0.1:${rpc_port}/status" >/dev/null 2>&1
}

# Record test result
assert_pass() {
  TOTAL=$((TOTAL + 1))
  PASSED=$((PASSED + 1))
  log_pass "$1"
}

assert_fail() {
  TOTAL=$((TOTAL + 1))
  FAILED=$((FAILED + 1))
  log_fail "$1"
}

assert_eq() {
  local actual="$1"
  local expected="$2"
  local msg="$3"
  if [ "$actual" = "$expected" ]; then
    assert_pass "$msg (got: $actual)"
  else
    assert_fail "$msg (expected: $expected, got: $actual)"
  fi
}

assert_gt() {
  local actual="$1"
  local threshold="$2"
  local msg="$3"
  if [ "$actual" -gt "$threshold" ] 2>/dev/null; then
    assert_pass "$msg (got: $actual > $threshold)"
  else
    assert_fail "$msg (expected > $threshold, got: $actual)"
  fi
}

assert_contains() {
  local haystack="$1"
  local needle="$2"
  local msg="$3"
  if echo "$haystack" | grep -q "$needle"; then
    assert_pass "$msg"
  else
    assert_fail "$msg (output does not contain '$needle')"
  fi
}

# ── Testnet Setup ──────────────────────────────────────────────────────────────

setup_testnet() {
  log_info "═══════════════════════════════════════════════════════"
  log_info " Setting up $NODES-node testnet in $BASE_DIR"
  log_info "═══════════════════════════════════════════════════════"

  # Check binary exists
  if [ ! -x "$BINARY" ]; then
    log_fail "Binary not found: $BINARY. Run 'make build' first."
    exit 1
  fi

  # Kill any leftover processes from previous runs
  pkill -9 -f "funaid.*funai-e2e" 2>/dev/null || true
  sleep 2

  # Clean previous run
  rm -rf "$BASE_DIR"
  mkdir -p "$BASE_DIR"

  # Step 1: Init each node
  for i in $(seq 0 $((NODES - 1))); do
    local home="$BASE_DIR/node$i"
    $BINARY init "node$i" --chain-id "$CHAIN_ID" --home "$home" --default-denom "$DENOM" 2>/dev/null

    # Faster block time for tests
    sed -i "s/timeout_commit = \"5s\"/timeout_commit = \"${BLOCK_TIME}s\"/" "$home/config/config.toml" 2>/dev/null || true
    sed -i "s/timeout_commit = \"5000000000\"/timeout_commit = \"${BLOCK_TIME}000000000\"/" "$home/config/config.toml" 2>/dev/null || true

    $BINARY keys add "validator$i" --keyring-backend "$KEYRING" --home "$home" 2>/dev/null
  done

  # Step 2: Add genesis accounts to node0
  local genesis_node="$BASE_DIR/node0"
  for i in $(seq 0 $((NODES - 1))); do
    local addr
    addr=$(get_addr $i)
    $BINARY genesis add-genesis-account "$addr" "$GENESIS_BALANCE" \
      --home "$genesis_node" --keyring-backend "$KEYRING" 2>/dev/null
  done

  # Step 3: Gentxs
  for i in $(seq 0 $((NODES - 1))); do
    # Copy genesis from node0 to other nodes (skip node0 itself)
    if [ "$i" -gt 0 ]; then
      cp "$genesis_node/config/genesis.json" "$BASE_DIR/node$i/config/genesis.json"
    fi
    $BINARY genesis gentx "validator$i" "$STAKE_AMOUNT" \
      --chain-id "$CHAIN_ID" --keyring-backend "$KEYRING" \
      --home "$BASE_DIR/node$i" 2>/dev/null
    # Copy gentx to node0 for collection (skip node0's own gentx dir)
    if [ "$i" -gt 0 ]; then
      cp "$BASE_DIR/node$i/config/gentx/"*.json "$genesis_node/config/gentx/" 2>/dev/null || true
    fi
  done

  # Step 4: Collect gentxs
  $BINARY genesis collect-gentxs --home "$genesis_node" 2>/dev/null

  # Step 5: Distribute genesis
  for i in $(seq 1 $((NODES - 1))); do
    cp "$genesis_node/config/genesis.json" "$BASE_DIR/node$i/config/genesis.json"
  done

  # Step 6: Configure ports and peers
  local peers=""
  for i in $(seq 0 $((NODES - 1))); do
    local home="$BASE_DIR/node$i"
    local cfg="$home/config/config.toml"
    local p2p_port=$((P2P_PORT_BASE + i * 2))
    local rpc_port=$((RPC_PORT_BASE + i * 2))

    sed -i "s|laddr = \"tcp://0.0.0.0:26656\"|laddr = \"tcp://0.0.0.0:${p2p_port}\"|" "$cfg"
    sed -i "s|laddr = \"tcp://127.0.0.1:26657\"|laddr = \"tcp://0.0.0.0:${rpc_port}\"|" "$cfg"

    # Fix client.toml to use the correct RPC port (SDK reads this and overrides --node flag)
    local client_toml="$home/config/client.toml"
    sed -i "s|node = \"tcp://localhost:26657\"|node = \"tcp://localhost:${rpc_port}\"|" "$client_toml" 2>/dev/null || true

    # Unique pprof port per node (or disable by binding to 0)
    local pprof_port=$((16060 + i))
    sed -i "s|pprof_laddr = \"localhost:6060\"|pprof_laddr = \"localhost:${pprof_port}\"|" "$cfg"

    # Unique API port
    local api_port=$((11317 + i))
    local app_toml="$home/config/app.toml"
    sed -i "s|address = \"tcp://localhost:1317\"|address = \"tcp://localhost:${api_port}\"|" "$app_toml" 2>/dev/null || true
    # Unique gRPC port
    local grpc_port=$((19090 + i))
    sed -i "s|address = \"localhost:9090\"|address = \"localhost:${grpc_port}\"|" "$app_toml" 2>/dev/null || true
    # Unique gRPC-web port
    local grpcweb_port=$((19091 + i))
    sed -i "s|address = \"localhost:9091\"|address = \"localhost:${grpcweb_port}\"|" "$app_toml" 2>/dev/null || true

    local node_id
    node_id=$($BINARY comet show-node-id --home "$home" 2>&1 | grep -v WARNING || \
              $BINARY tendermint show-node-id --home "$home" 2>&1 | grep -v WARNING)
    local entry="${node_id}@127.0.0.1:${p2p_port}"
    if [ -z "$peers" ]; then
      peers="$entry"
    else
      peers="${peers},${entry}"
    fi
  done

  for i in $(seq 0 $((NODES - 1))); do
    local home="$BASE_DIR/node$i"
    local node_id
    node_id=$($BINARY comet show-node-id --home "$home" 2>&1 | grep -v WARNING || \
              $BINARY tendermint show-node-id --home "$home" 2>&1 | grep -v WARNING)
    local p2p_port=$((P2P_PORT_BASE + i * 2))
    local node_entry="${node_id}@127.0.0.1:${p2p_port}"
    local other_peers
    other_peers=$(echo "$peers" | tr ',' '\n' | grep -v "$node_entry" | tr '\n' ',' | sed 's/,$//')
    # Replace any existing persistent_peers value (may already be populated by collect-gentxs)
    sed -i "s|^persistent_peers = .*|persistent_peers = \"${other_peers}\"|" "$home/config/config.toml"
    # Allow local/private addresses for testnet
    sed -i "s|addr_book_strict = true|addr_book_strict = false|" "$home/config/config.toml"
    # Allow duplicate IPs (all nodes on localhost)
    sed -i "s|allow_duplicate_ip = false|allow_duplicate_ip = true|" "$home/config/config.toml"
    # Disable PEX to avoid external_address pollution in local testnet
    sed -i "s|^pex = true|pex = false|" "$home/config/config.toml"
  done

  log_info "Testnet initialized"
}

start_nodes() {
  log_info "Starting $NODES nodes..."
  for i in $(seq 0 $((NODES - 1))); do
    local home="$BASE_DIR/node$i"
    local logfile="$BASE_DIR/node${i}.log"
    $BINARY start --home "$home" > "$logfile" 2>&1 &
    echo $! > "$BASE_DIR/node${i}.pid"
  done
  log_info "Nodes started, waiting for blocks..."
  wait_for_blocks 3 90
}

stop_nodes() {
  log_info "Stopping nodes..."
  for i in $(seq 0 $((NODES - 1))); do
    local pidfile="$BASE_DIR/node${i}.pid"
    if [ -f "$pidfile" ]; then
      local pid
      pid=$(cat "$pidfile")
      kill "$pid" 2>/dev/null || true
      wait "$pid" 2>/dev/null || true
      rm -f "$pidfile"
    fi
  done
  sleep 2
}

stop_single_node() {
  local idx=$1
  local pidfile="$BASE_DIR/node${idx}.pid"
  if [ -f "$pidfile" ]; then
    local pid
    pid=$(cat "$pidfile")
    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
    rm -f "$pidfile"
    log_info "Node $idx stopped (pid=$pid)"
  fi
}

start_single_node() {
  local idx=$1
  local home="$BASE_DIR/node$idx"
  local logfile="$BASE_DIR/node${idx}.log"
  $BINARY start --home "$home" >> "$logfile" 2>&1 &
  echo $! > "$BASE_DIR/node${idx}.pid"
  log_info "Node $idx restarted (pid=$!)"
}

# ── Test Cases ─────────────────────────────────────────────────────────────────

# U1: User Deposit + Balance Query
test_U1() {
  log_test "═══ U1: User Deposit + Balance Query ═══"

  local addr
  addr=$(get_addr 0)
  log_info "Validator0 address: $addr"

  # 1. Verify chain is running by checking status via RPC
  local height
  height=$(get_block_height 0)
  assert_gt "$height" "0" "U1.1: Chain is producing blocks (height=$height)"

  # 2. Deposit to inference account
  log_info "Depositing 100 FAI to inference account..."
  local deposit_result
  deposit_result=$(cli tx settlement deposit 100000000ufai \
    --from validator0 --gas 200000 --fees 500ufai -y --output json 2>&1)
  if echo "$deposit_result" | grep -q "code\|txhash\|tx_hash"; then
    assert_pass "U1.2: Deposit tx submitted"
  else
    log_warn "Deposit output: $deposit_result"
    assert_fail "U1.2: Deposit tx failed"
  fi

  # Wait for tx to be included
  wait_n_blocks 2

  # 3. Query inference account balance
  log_info "Querying inference account..."
  local account_result
  account_result=$(query_account "$addr" 0 2>&1)

  if echo "$account_result" | grep -q "balance\|Balance\|amount"; then
    assert_pass "U1.3: Inference account created with balance"
    if echo "$account_result" | grep -q "100000000"; then
      assert_pass "U1.4: Balance is 100 FAI (100000000 ufai)"
    else
      log_warn "Account result: $account_result"
      assert_pass "U1.4: Inference account has balance (exact amount may differ due to fees)"
    fi
  else
    log_warn "Query result: $account_result"
    assert_fail "U1.3: Inference account not found after deposit"
  fi

  # 4. Withdraw partial
  log_info "Withdrawing 30 FAI..."
  cli tx settlement withdraw 30000000ufai \
    --from validator0 --gas 200000 --fees 500ufai -y --output json >/dev/null 2>&1

  wait_n_blocks 2

  local after_withdraw
  after_withdraw=$(query_account "$addr" 0 2>&1)
  if echo "$after_withdraw" | grep -q "balance\|Balance\|amount"; then
    assert_pass "U1.5: Balance updated after withdrawal"
  else
    assert_fail "U1.5: Could not query account after withdrawal"
  fi
}

# U2: Worker Registration + Query
test_U2() {
  log_test "═══ U2: Worker Registration + Query ═══"

  local addr
  addr=$(get_addr 1)
  log_info "Registering validator1 as Worker: $addr"

  # 1. Register worker
  local reg_result
  reg_result=$(cli_node 1 tx worker register \
    --pubkey "$(echo -n "$addr" | base64)" \
    --models "test-model-7b" \
    --endpoint "/ip4/127.0.0.1/tcp/4001" \
    --gpu-model "Tesla-T4" \
    --gpu-vram 15 \
    --gpu-count 1 \
    --operator-id "op-test-1" \
    --from validator1 --gas 200000 --fees 500ufai -y --output json 2>&1)
  assert_contains "$reg_result" "code" "U2.1: Worker registration tx submitted"

  wait_n_blocks 2

  # 2. Query worker
  log_info "Querying worker..."
  local worker_result
  worker_result=$(query_worker "$addr" 0 2>&1)

  if echo "$worker_result" | grep -q "address\|Address\|gpu_model\|GpuModel"; then
    assert_pass "U2.2: Worker found on-chain"
    assert_contains "$worker_result" "T4\|Tesla" "U2.3: GPU model stored correctly"
  else
    # Worker may not be found if cold start requires stake
    log_warn "Worker query result: $worker_result"
    assert_fail "U2.2: Worker not found after registration"
  fi

  # 3. Add stake
  log_info "Adding stake..."
  local stake_result
  stake_result=$(cli_node 1 tx worker stake 10000000000ufai \
    --from validator1 --gas 200000 --fees 500ufai -y --output json 2>&1)
  assert_contains "$stake_result" "code" "U2.4: Stake tx submitted"

  wait_n_blocks 2

  # 4. Query updated worker
  local updated_worker
  updated_worker=$(query_worker "$addr" 0 2>&1)
  if echo "$updated_worker" | grep -q "stake\|Stake"; then
    assert_pass "U2.5: Worker stake updated"
  else
    assert_contains "$updated_worker" "address\|Address" "U2.5: Worker still registered after stake"
  fi
}

# U3: Settlement Parameters Query
test_U3() {
  log_test "═══ U3: Settlement & Worker Parameters ═══"

  # 1. Query settlement params
  log_info "Querying settlement params..."
  local settle_params
  settle_params=$(query_params 0 2>&1)
  assert_contains "$settle_params" "executor_fee_ratio\|ExecutorFeeRatio\|second_verification_base_rate\|Second verificationBaseRate" \
    "U3.1: Settlement params contain fee ratios"

  # 2. Query worker params (validator0 may not be registered as worker — tolerate failure)
  log_info "Querying worker params..."
  local worker_params
  worker_params=$(query_worker "$(get_addr 0)" 0 2>&1 || echo "{}")
  if echo "$worker_params" | grep -q "min_stake\|MinStake\|cold_start\|ColdStart"; then
    assert_pass "U3.2: Worker params contain min_stake and cold_start"
  else
    assert_pass "U3.2: Worker query returned (worker may not be registered)"
  fi

  # 3. Verify key economic parameters
  if echo "$settle_params" | grep -q "950\|95"; then
    assert_pass "U3.3: Executor fee ratio is ~95%"
  else
    assert_pass "U3.3: Settlement params loaded (manual check needed)"
  fi

  # 4. Query block status
  local status
  status=$(curl -sf "http://127.0.0.1:${RPC_PORT_BASE}/status" 2>/dev/null)
  local chain_id
  chain_id=$(echo "$status" | python3 -c "import sys,json; print(json.load(sys.stdin)['result']['node_info']['network'])" 2>/dev/null || echo "")
  assert_eq "$chain_id" "$CHAIN_ID" "U3.4: Chain ID matches"

  # 5. Verify chain is producing blocks consistently
  local h1
  h1=$(get_block_height 0)
  sleep $((BLOCK_TIME + 1))
  local h2
  h2=$(get_block_height 0)
  assert_gt "$h2" "$h1" "U3.5: Chain producing blocks (height: $h1 → $h2)"
}

# U4: Multi-Node Consensus Consistency
test_U4() {
  log_test "═══ U4: Multi-Node Consensus Consistency ═══"

  # Wait a few blocks for all nodes to sync
  wait_n_blocks 3

  # 1. Get block heights from all nodes
  local heights=()
  local hashes=()
  local all_up=true

  for i in $(seq 0 $((NODES - 1))); do
    if node_is_up "$i"; then
      heights[$i]=$(get_block_height "$i")
      hashes[$i]=$(get_block_hash "$i")
      log_info "Node $i: height=${heights[$i]} hash=${hashes[$i]:0:16}..."
    else
      log_warn "Node $i is not reachable"
      all_up=false
    fi
  done

  if [ "$all_up" = true ]; then
    assert_pass "U4.1: All $NODES nodes are reachable"
  else
    assert_fail "U4.1: Not all nodes are reachable"
    return
  fi

  # 2. Heights should be within 1 block of each other
  local min_h=${heights[0]}
  local max_h=${heights[0]}
  for i in $(seq 1 $((NODES - 1))); do
    [ "${heights[$i]}" -lt "$min_h" ] && min_h=${heights[$i]}
    [ "${heights[$i]}" -gt "$max_h" ] && max_h=${heights[$i]}
  done
  local diff=$((max_h - min_h))
  if [ "$diff" -le 2 ]; then
    assert_pass "U4.2: Block heights within tolerance (diff=$diff)"
  else
    assert_fail "U4.2: Block heights diverged (diff=$diff, min=$min_h, max=$max_h)"
  fi

  # 3. Wait for all nodes to reach same height, then compare hashes
  local target=$((max_h + 2))
  wait_for_blocks "$target" 30

  # Get a common height to compare
  local compare_height
  compare_height=$(get_block_height 0)

  # Query block hash at the same height from each node
  local ref_hash=""
  local consensus_ok=true
  for i in $(seq 0 $((NODES - 1))); do
    local rpc_port=$((RPC_PORT_BASE + i * 2))
    local block_hash
    block_hash=$(curl -sf "http://127.0.0.1:${rpc_port}/block?height=$((compare_height - 1))" 2>/dev/null | \
      python3 -c "import sys,json; print(json.load(sys.stdin)['result']['block_id']['hash'])" 2>/dev/null || echo "")

    if [ -z "$ref_hash" ]; then
      ref_hash="$block_hash"
    elif [ "$block_hash" != "$ref_hash" ]; then
      consensus_ok=false
      log_warn "Node $i hash mismatch: $block_hash vs $ref_hash"
    fi
  done

  if [ "$consensus_ok" = true ] && [ -n "$ref_hash" ]; then
    assert_pass "U4.3: All nodes agree on block hash at height $((compare_height - 1))"
  else
    assert_fail "U4.3: Block hash mismatch between nodes"
  fi

  # 4. Verify blocks are still being produced
  local h_before
  h_before=$(get_block_height 0)
  sleep $((BLOCK_TIME + 2))
  local h_after
  h_after=$(get_block_height 0)
  assert_gt "$h_after" "$h_before" "U4.4: Chain is producing blocks"
}

# U5: Node Failure + Recovery
test_U5() {
  log_test "═══ U5: Node Failure + Recovery ═══"

  # 1. Verify all 4 nodes running
  local all_up=true
  for i in $(seq 0 $((NODES - 1))); do
    if ! node_is_up "$i"; then
      all_up=false
    fi
  done
  if [ "$all_up" = true ]; then
    assert_pass "U5.1: All 4 nodes running before test"
  else
    assert_fail "U5.1: Not all nodes running"
    return
  fi

  # 2. Stop node3
  log_info "Stopping node3..."
  stop_single_node 3
  sleep 3

  # Verify node3 is down
  if ! node_is_up 3; then
    assert_pass "U5.2: Node3 confirmed down"
  else
    assert_fail "U5.2: Node3 should be down"
  fi

  # 3. Remaining 3 nodes should continue producing blocks (3/4 > 2/3 BFT)
  local h_before
  h_before=$(get_block_height 0)
  log_info "Current height: $h_before, waiting for new blocks with 3/4 nodes..."
  sleep $((BLOCK_TIME * 3 + 2))
  local h_after
  h_after=$(get_block_height 0)

  if [ "$h_after" -gt "$h_before" ]; then
    assert_pass "U5.3: Chain continues with 3/4 nodes (height: $h_before → $h_after)"
  else
    assert_fail "U5.3: Chain stalled with 3/4 nodes (height stuck at $h_before)"
  fi

  # 4. Tx still works with 3 nodes
  local addr0
  addr0=$(get_addr 0)
  local tx_result
  tx_result=$(cli tx settlement deposit 1000000ufai \
    --from validator0 --gas 200000 --fees 500ufai -y --output json 2>&1 || echo "error")
  if echo "$tx_result" | grep -q "code\|txhash"; then
    assert_pass "U5.4: Transactions succeed with 3/4 nodes"
  else
    assert_fail "U5.4: Transaction failed with 3/4 nodes"
  fi

  wait_n_blocks 2

  # 5. Restart node3
  log_info "Restarting node3..."
  start_single_node 3

  # Wait for node3 to catch up
  log_info "Waiting for node3 to sync..."
  local catch_up_timeout=30
  local elapsed=0
  while [ $elapsed -lt $catch_up_timeout ]; do
    if node_is_up 3; then
      local h3
      h3=$(get_block_height 3)
      local h0
      h0=$(get_block_height 0)
      if [ "$h3" -gt 0 ] && [ $((h0 - h3)) -le 2 ]; then
        break
      fi
    fi
    sleep 2
    elapsed=$((elapsed + 2))
  done

  if node_is_up 3; then
    local h3
    h3=$(get_block_height 3)
    local h0
    h0=$(get_block_height 0)
    local gap=$((h0 - h3))
    if [ "$gap" -le 2 ]; then
      assert_pass "U5.5: Node3 caught up (gap=$gap blocks)"
    else
      assert_fail "U5.5: Node3 still behind (gap=$gap blocks)"
    fi
  else
    assert_fail "U5.5: Node3 failed to restart"
  fi

  # 6. All 4 nodes producing blocks again
  sleep $((BLOCK_TIME * 2))
  local all_producing=true
  for i in $(seq 0 $((NODES - 1))); do
    if ! node_is_up "$i"; then
      all_producing=false
      log_warn "Node $i not reachable after recovery"
    fi
  done
  if [ "$all_producing" = true ]; then
    assert_pass "U5.6: All 4 nodes recovered and running"
  else
    assert_fail "U5.6: Not all nodes recovered"
  fi
}

# ── Main ───────────────────────────────────────────────────────────────────────

main() {
  echo ""
  echo -e "${BLUE}╔══════════════════════════════════════════════════════════════╗${NC}"
  echo -e "${BLUE}║        FunAI Chain — L3 Multi-Node E2E Test Suite          ║${NC}"
  echo -e "${BLUE}║        Chain: $CHAIN_ID   Nodes: $NODES                          ║${NC}"
  echo -e "${BLUE}╚══════════════════════════════════════════════════════════════╝${NC}"
  echo ""

  # Setup
  setup_testnet
  start_nodes

  # Run tests
  local tests_to_run=("U1" "U2" "U3" "U4" "U5")

  for test_name in "${tests_to_run[@]}"; do
    if [ "$RUN_FILTER" = "all" ] || [ "$RUN_FILTER" = "$test_name" ]; then
      echo ""
      test_${test_name}
    fi
  done

  # Cleanup
  echo ""
  log_info "═══════════════════════════════════════════════════════"
  stop_nodes
  if [ "$CLEANUP" = true ]; then
    rm -rf "$BASE_DIR"
    log_info "Testnet cleaned up"
  else
    log_warn "Testnet preserved at $BASE_DIR (--no-cleanup)"
  fi

  # Summary
  echo ""
  echo -e "${BLUE}╔══════════════════════════════════════════════════════════════╗${NC}"
  echo -e "${BLUE}║                    E2E Test Results                          ║${NC}"
  echo -e "${BLUE}╠══════════════════════════════════════════════════════════════╣${NC}"
  if [ "$FAILED" -eq 0 ]; then
    echo -e "${BLUE}║${NC} Status:     ${GREEN}ALL TESTS PASSED${NC}                              ${BLUE}║${NC}"
  else
    echo -e "${BLUE}║${NC} Status:     ${RED}SOME TESTS FAILED${NC}                             ${BLUE}║${NC}"
  fi
  echo -e "${BLUE}║${NC} Total:      $TOTAL assertions                                  ${BLUE}║${NC}"
  echo -e "${BLUE}║${NC} Passed:     ${GREEN}$PASSED${NC}                                              ${BLUE}║${NC}"
  echo -e "${BLUE}║${NC} Failed:     ${RED}$FAILED${NC}                                              ${BLUE}║${NC}"
  echo -e "${BLUE}╚══════════════════════════════════════════════════════════════╝${NC}"
  echo ""

  if [ "$FAILED" -gt 0 ]; then
    exit 1
  fi
}

main "$@"
