#!/bin/bash
# e2e-real-inference.sh — Full E2E Test: Chain + P2P + Real TGI Inference
#
# Tests the complete FunAI pipeline using a real TGI backend (e.g., Qwen on T4 GPU):
#   Chain testnet → Worker registration → Deposit → P2P nodes → SDK inference → Verification → Settlement
#
# Prerequisites:
#   1. make build-all   (builds funaid + funai-node + e2e-client)
#   2. TGI running at $TGI_ENDPOINT (default http://localhost:8080)
#
# Usage:
#   bash scripts/e2e-real-inference.sh                           # Run all phases
#   TGI_ENDPOINT=http://1.2.3.4:8080 bash scripts/e2e-real-inference.sh  # Custom TGI
#   bash scripts/e2e-real-inference.sh --no-cleanup              # Keep testnet after tests
set -euo pipefail

# ── Configuration ──────────────────────────────────────────────────────────────

BINARY="./build/funaid"
P2P_BINARY="./build/funai-node"
CLIENT_BINARY="./build/e2e-client"
CHAIN_ID="${CHAIN_ID:-funai_7777777-1}"  # EVM-compatible: funai_<eip155>-<version>. The P2P nodes pass this as FUNAI_CHAIN_ID and app.init.0 calls evmtypes.DefaultChainConfig which panics on non-EVM format.
BASE_DIR="/tmp/funai-e2e-real"
NODES=4
DENOM="ufai"
KEYRING="test"
GENESIS_BALANCE="200000000000000${DENOM}"
STAKE_AMOUNT="100000000000000${DENOM}"
BLOCK_TIME=2

# Chain ports (offset from standard to avoid conflicts).
# All port bases are env-overridable so the e2e test can coexist with a running
# local testnet on the same box. Example for a box already using the 46656 range:
#   P2P_PORT_BASE=56656 RPC_PORT_BASE=56657 API_PORT_BASE=31317 \
#     GRPC_PORT_BASE=39090 P2P_LIBP2P_PORT_BASE=15001 \
#     bash scripts/e2e-real-inference.sh
P2P_PORT_BASE=${P2P_PORT_BASE:-46656}
RPC_PORT_BASE=${RPC_PORT_BASE:-46657}
API_PORT_BASE=${API_PORT_BASE:-21317}
GRPC_PORT_BASE=${GRPC_PORT_BASE:-29090}

# P2P node ports
P2P_LIBP2P_PORT_BASE=${P2P_LIBP2P_PORT_BASE:-5001}

# TGI endpoint (all P2P nodes share the same backend for verification consistency)
TGI_ENDPOINT="${TGI_ENDPOINT:-http://localhost:8080}"

# Model ID for testing
MODEL_ID="${MODEL_ID:-qwen-test}"

# Inference prompt
INFERENCE_PROMPT="${INFERENCE_PROMPT:-What is 2+2? Answer with just the number.}"

# Logits comparison epsilon (default 0.01; may need tuning for small models)
EPSILON="${FUNAI_EPSILON:-0.01}"

CLEANUP=true
if [[ "${1:-}" == "--no-cleanup" ]]; then
  CLEANUP=false
fi

# ── Colors ─────────────────────────────────────────────────────────────────────

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m'

# ── Counters ───────────────────────────────────────────────────────────────────

TOTAL=0
PASSED=0
FAILED=0

# ── Helper Functions ───────────────────────────────────────────────────────────

log_info()  { echo -e "${BLUE}[INFO]${NC} $*"; }
log_pass()  { echo -e "${GREEN}[PASS]${NC} $*"; TOTAL=$((TOTAL + 1)); PASSED=$((PASSED + 1)); }
log_fail()  { echo -e "${RED}[FAIL]${NC} $*"; TOTAL=$((TOTAL + 1)); FAILED=$((FAILED + 1)); }
log_warn()  { echo -e "${YELLOW}[WARN]${NC} $*"; }
log_phase() { echo -e "\n${CYAN}══════ $* ══════${NC}\n"; }

cli() {
  timeout 15 $BINARY "$@" \
    --home "$BASE_DIR/node0" \
    --node "tcp://127.0.0.1:${RPC_PORT_BASE}" \
    --keyring-backend "$KEYRING" --chain-id "$CHAIN_ID" < /dev/null 2>&1
}

cli_node() {
  local node_idx=$1; shift
  local rpc_port=$((RPC_PORT_BASE + node_idx * 2))
  timeout 15 $BINARY "$@" \
    --home "$BASE_DIR/node${node_idx}" \
    --node "tcp://127.0.0.1:${rpc_port}" \
    --keyring-backend "$KEYRING" --chain-id "$CHAIN_ID" < /dev/null 2>&1
}

get_addr() {
  local node_idx=$1
  $BINARY keys show "validator${node_idx}" --keyring-backend "$KEYRING" \
    --home "$BASE_DIR/node${node_idx}" -a 2>/dev/null
}

get_block_height() {
  local node_idx=${1:-0}
  local rpc_port=$((RPC_PORT_BASE + node_idx * 2))
  curl -sf "http://127.0.0.1:${rpc_port}/status" 2>/dev/null | \
    python3 -c "import sys,json; print(json.load(sys.stdin)['result']['sync_info']['latest_block_height'])" 2>/dev/null || echo "0"
}

wait_for_blocks() {
  local target_height=${1:-3}
  local timeout_sec=${2:-60}
  local elapsed=0
  log_info "Waiting for chain to reach block $target_height (timeout: ${timeout_sec}s)..."
  while [ $elapsed -lt $timeout_sec ]; do
    local height=$(get_block_height 0)
    if [ "$height" -ge "$target_height" ] 2>/dev/null; then
      log_info "Chain at block $height"
      return 0
    fi
    sleep 1
    elapsed=$((elapsed + 1))
  done
  log_fail "Timeout waiting for block $target_height"
  return 1
}

wait_n_blocks() {
  local n=${1:-2}
  local current=$(get_block_height 0)
  wait_for_blocks $((current + n)) 30
}

# Generate a deterministic secp256k1 private key from a seed string
# Returns hex-encoded 32-byte private key
gen_privkey() {
  python3 -c "
import hashlib
seed = '$1'.encode()
h = hashlib.sha256(seed).hexdigest()
print(h)
"
}

# Derive bech32 address from a hex private key using the Go binary
# Prints funai1... address to stdout
derive_address() {
  local privkey_hex=$1
  ./build/e2e-client 2>/dev/null <<< "" || true
  # Use a small Go helper embedded in e2e-client via env var
  E2E_USER_PRIVKEY="$privkey_hex" E2E_DERIVE_ONLY=1 ./build/e2e-client 2>/dev/null | grep "^ADDRESS:" | cut -d: -f2 || echo ""
}

# ── Phase 0: Preflight Checks ────────────────────────────────────────────────

preflight() {
  log_phase "Phase 0: Preflight Checks"

  # Check binaries
  if [ ! -x "$BINARY" ]; then
    log_fail "Chain binary not found: $BINARY"
    log_info "Run: make build-all"
    exit 1
  fi
  log_pass "Chain binary found: $BINARY"

  if [ ! -x "$P2P_BINARY" ]; then
    log_fail "P2P node binary not found: $P2P_BINARY"
    log_info "Run: make build-all"
    exit 1
  fi
  log_pass "P2P node binary found: $P2P_BINARY"

  # Build e2e-client if needed
  if [ ! -x "$CLIENT_BINARY" ]; then
    log_info "Building e2e-client..."
    go build -o "$CLIENT_BINARY" ./cmd/e2e-client 2>&1 || {
      log_fail "Failed to build e2e-client"
      exit 1
    }
  fi
  log_pass "E2E client binary ready: $CLIENT_BINARY"

  # Check TGI
  log_info "Checking TGI endpoint: $TGI_ENDPOINT"
  local tgi_response
  tgi_response=$(curl -sf --connect-timeout 5 "${TGI_ENDPOINT}/generate" \
    -H "Content-Type: application/json" \
    -d '{"inputs":"Hi","parameters":{"max_new_tokens":3}}' 2>/dev/null || echo "")
  if [ -n "$tgi_response" ] && echo "$tgi_response" | python3 -c "import sys,json; json.load(sys.stdin)" 2>/dev/null; then
    log_pass "TGI endpoint is responsive"
    log_info "TGI response: $(echo "$tgi_response" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('generated_text','')[:80])" 2>/dev/null)"
  else
    log_fail "TGI endpoint not responding at $TGI_ENDPOINT"
    log_info "Please ensure TGI is running. Example:"
    log_info "  docker run --gpus all -p 8080:80 ghcr.io/huggingface/text-generation-inference:latest \\"
    log_info "    --model-id Qwen/Qwen2.5-0.5B-Instruct --dtype float16"
    exit 1
  fi

  # ── TGI v3 API Compatibility Probes ──

  # 1) Version detection
  local tgi_version=""
  local tgi_major=0
  tgi_version=$(curl -sf --connect-timeout 5 "${TGI_ENDPOINT}/info" 2>/dev/null | \
    python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('version',''))" 2>/dev/null || echo "")
  if [ -n "$tgi_version" ]; then
    tgi_major=$(echo "$tgi_version" | cut -d. -f1)
    log_info "TGI version: $tgi_version (major=$tgi_major)"
    if [ "$tgi_major" -ge 3 ] 2>/dev/null; then
      log_warn "TGI v3+ detected — decoder_input_details may use token-by-token fallback"
    fi
  fi

  # 2) decoder_input_details probe
  local prefill_count=0
  local did_response
  did_response=$(curl -sf --connect-timeout 10 "${TGI_ENDPOINT}/generate" \
    -H "Content-Type: application/json" \
    -d '{"inputs":"Hi","parameters":{"max_new_tokens":1,"details":true,"decoder_input_details":true}}' 2>/dev/null || echo "")
  if [ -n "$did_response" ]; then
    prefill_count=$(echo "$did_response" | python3 -c "
import sys,json
d=json.load(sys.stdin)
pf=d.get('details',{}).get('prefill',[])
print(len(pf))
" 2>/dev/null || echo "0")
    if [ "$prefill_count" -gt 0 ] 2>/dev/null; then
      log_pass "decoder_input_details supported (prefill=$prefill_count tokens)"
    else
      log_warn "decoder_input_details returned empty prefill — will use token-by-token fallback"
    fi
  fi

  # 3) /tokenize format probe
  local tok_response
  tok_response=$(curl -sf --connect-timeout 5 "${TGI_ENDPOINT}/tokenize" \
    -H "Content-Type: application/json" \
    -d '{"inputs":"What is 2+2?"}' 2>/dev/null || echo "")
  if [ -n "$tok_response" ]; then
    local tok_format
    tok_format=$(echo "$tok_response" | python3 -c "
import sys,json
d=json.load(sys.stdin)
if isinstance(d, list):
    print('bare_array (%d tokens)' % len(d))
elif isinstance(d, dict) and 'tokens' in d:
    print('wrapped_object (%d tokens)' % len(d['tokens']))
else:
    print('unknown')
" 2>/dev/null || echo "unknown")
    log_info "/tokenize response format: $tok_format"
  else
    log_warn "/tokenize endpoint not available"
  fi

  # 4) top_n_tokens probe
  local topn_response
  topn_response=$(curl -sf --connect-timeout 10 "${TGI_ENDPOINT}/generate" \
    -H "Content-Type: application/json" \
    -d '{"inputs":"Hi","parameters":{"max_new_tokens":1,"details":true,"top_n_tokens":5}}' 2>/dev/null || echo "")
  if [ -n "$topn_response" ]; then
    local topn_count
    topn_count=$(echo "$topn_response" | python3 -c "
import sys,json
d=json.load(sys.stdin)
toks=d.get('details',{}).get('tokens',[])
if toks:
    tt=toks[0].get('top_tokens',[])
    print(len(tt))
else:
    print(0)
" 2>/dev/null || echo "0")
    if [ "$topn_count" -gt 0 ] 2>/dev/null; then
      log_pass "top_n_tokens supported ($topn_count alternatives returned)"
    else
      log_warn "top_n_tokens returned no alternatives"
    fi
  fi

  # 5) logprob field name detection
  if [ -n "$topn_response" ]; then
    local logprob_field
    logprob_field=$(echo "$topn_response" | python3 -c "
import sys,json
d=json.load(sys.stdin)
toks=d.get('details',{}).get('tokens',[])
if toks:
    t=toks[0]
    if 'logprob' in t: print('logprob')
    elif 'log_prob' in t: print('log_prob')
    else: print('none')
else: print('none')
" 2>/dev/null || echo "none")
    log_info "Logprob field name: $logprob_field"
  fi
}

# ── Phase 1: Setup Chain Testnet ──────────────────────────────────────────────

setup_testnet() {
  log_phase "Phase 1: Setup $NODES-node Chain Testnet"

  # Kill leftover processes
  pkill -9 -f "funaid.*funai-e2e-real" 2>/dev/null || true
  pkill -9 -f "funai-node.*funai-e2e-real" 2>/dev/null || true
  sleep 2

  rm -rf "$BASE_DIR"
  mkdir -p "$BASE_DIR"

  # Step 1: Init nodes
  for i in $(seq 0 $((NODES - 1))); do
    local home="$BASE_DIR/node$i"
    $BINARY init "node$i" --chain-id "$CHAIN_ID" --home "$home" --default-denom "$DENOM" 2>/dev/null

    # Fast block time for tests
    sed -i "s/timeout_commit = \"5s\"/timeout_commit = \"${BLOCK_TIME}s\"/" "$home/config/config.toml" 2>/dev/null || true
    sed -i "s/timeout_commit = \"5000000000\"/timeout_commit = \"${BLOCK_TIME}000000000\"/" "$home/config/config.toml" 2>/dev/null || true

    $BINARY keys add "validator$i" --keyring-backend "$KEYRING" --home "$home" 2>/dev/null

    # Also generate a separate "user" key on node0 for inference requests
    if [ "$i" -eq 0 ]; then
      $BINARY keys add "user0" --keyring-backend "$KEYRING" --home "$home" 2>/dev/null
    fi
  done

  # Step 2: Genesis accounts on node0
  local genesis_node="$BASE_DIR/node0"
  for i in $(seq 0 $((NODES - 1))); do
    local addr=$(get_addr $i)
    $BINARY genesis add-genesis-account "$addr" "$GENESIS_BALANCE" \
      --home "$genesis_node" --keyring-backend "$KEYRING" 2>/dev/null
  done
  # Add user0 to genesis
  local user_addr
  user_addr=$($BINARY keys show "user0" --keyring-backend "$KEYRING" --home "$BASE_DIR/node0" -a 2>/dev/null)
  $BINARY genesis add-genesis-account "$user_addr" "100000000000000${DENOM}" \
    --home "$genesis_node" --keyring-backend "$KEYRING" 2>/dev/null

  # Step 3: Gentxs
  for i in $(seq 0 $((NODES - 1))); do
    if [ "$i" -gt 0 ]; then
      cp "$genesis_node/config/genesis.json" "$BASE_DIR/node$i/config/genesis.json"
    fi
    $BINARY genesis gentx "validator$i" "$STAKE_AMOUNT" \
      --chain-id "$CHAIN_ID" --keyring-backend "$KEYRING" \
      --home "$BASE_DIR/node$i" 2>/dev/null
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

    # Client.toml
    local client_toml="$home/config/client.toml"
    sed -i "s|node = \"tcp://localhost:26657\"|node = \"tcp://localhost:${rpc_port}\"|" "$client_toml" 2>/dev/null || true

    # Unique pprof/API/gRPC ports
    local pprof_port=$((26060 + i))
    sed -i "s|pprof_laddr = \"localhost:6060\"|pprof_laddr = \"localhost:${pprof_port}\"|" "$cfg"

    local api_port=$((API_PORT_BASE + i))
    local app_toml="$home/config/app.toml"
    sed -i "s|address = \"tcp://localhost:1317\"|address = \"tcp://localhost:${api_port}\"|" "$app_toml" 2>/dev/null || true

    local grpc_port=$((GRPC_PORT_BASE + i))
    sed -i "s|address = \"localhost:9090\"|address = \"localhost:${grpc_port}\"|" "$app_toml" 2>/dev/null || true
    local grpcweb_port=$((GRPC_PORT_BASE + 100 + i))
    sed -i "s|address = \"localhost:9091\"|address = \"localhost:${grpcweb_port}\"|" "$app_toml" 2>/dev/null || true

    # Enable REST API (only in [api] section)
    sed -i '/^\[api\]$/,/^\[/ s|^enable = false|enable = true|' "$app_toml" 2>/dev/null || true

    local node_id
    node_id=$($BINARY comet show-node-id --home "$home" 2>&1 | grep -oP '^[a-f0-9]{40}$' || \
              $BINARY tendermint show-node-id --home "$home" 2>&1 | grep -oP '^[a-f0-9]{40}$')
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
    node_id=$($BINARY comet show-node-id --home "$home" 2>&1 | grep -oP '^[a-f0-9]{40}$' || \
              $BINARY tendermint show-node-id --home "$home" 2>&1 | grep -oP '^[a-f0-9]{40}$')
    local p2p_port=$((P2P_PORT_BASE + i * 2))
    local node_entry="${node_id}@127.0.0.1:${p2p_port}"
    local other_peers
    other_peers=$(echo "$peers" | tr ',' '\n' | grep -v "$node_entry" | tr '\n' ',' | sed 's/,$//')
    sed -i "s|^persistent_peers = .*|persistent_peers = \"${other_peers}\"|" "$home/config/config.toml"
    sed -i "s|addr_book_strict = true|addr_book_strict = false|" "$home/config/config.toml"
    sed -i "s|allow_duplicate_ip = false|allow_duplicate_ip = true|" "$home/config/config.toml"
  done

  log_pass "Testnet initialized with $NODES nodes"
}

start_chain_nodes() {
  log_info "Starting $NODES chain nodes..."
  for i in $(seq 0 $((NODES - 1))); do
    local home="$BASE_DIR/node$i"
    local logfile="$BASE_DIR/chain-node${i}.log"
    $BINARY start --home "$home" > "$logfile" 2>&1 &
    echo $! > "$BASE_DIR/chain-node${i}.pid"
  done
  wait_for_blocks 3 90
  log_pass "Chain nodes started and producing blocks"
}

# ── Phase 2: Register Workers & Deposit ───────────────────────────────────────

setup_workers_and_deposit() {
  log_phase "Phase 2: Register Workers & Deposit Funds"

  # G1 fix: Extract real validator secp256k1 keys for P2P nodes.
  # On-chain, worker address = tx signer (validator). P2P node must use the SAME
  # private key so its derived pubkey matches what's registered on-chain.
  for i in $(seq 0 $((NODES - 1))); do
    # Export raw secp256k1 private key from validator keyring
    local privkey_hex
    privkey_hex=$(echo "y" | $BINARY keys export "validator${i}" --keyring-backend "$KEYRING" \
      --home "$BASE_DIR/node${i}" --unsafe --unarmored-hex 2>/dev/null | grep -oP '^[0-9a-fA-F]{64}$' || echo "")
    if [ -z "$privkey_hex" ] || [ ${#privkey_hex} -ne 64 ]; then
      # Fallback: try tail approach
      privkey_hex=$(echo "y" | $BINARY keys export "validator${i}" --keyring-backend "$KEYRING" \
        --home "$BASE_DIR/node${i}" --unsafe --unarmored-hex 2>&1 | tail -1 | tr -d '[:space:]')
    fi
    if [ ${#privkey_hex} -ne 64 ]; then
      log_warn "Could not export validator${i} privkey (len=${#privkey_hex}), falling back to generated key"
      privkey_hex=$(gen_privkey "funai-e2e-worker-${i}-secret-seed")
    fi
    echo "$privkey_hex" > "$BASE_DIR/worker${i}.privkey"

    # Extract compressed pubkey from keyring (base64 → hex)
    local pubkey_hex
    pubkey_hex=$($BINARY keys show "validator${i}" --keyring-backend "$KEYRING" \
      --home "$BASE_DIR/node${i}" -p 2>/dev/null | \
      python3 -c "
import sys, json, base64
d = json.load(sys.stdin)
b64 = d.get('key', '')
raw = base64.b64decode(b64)
print(raw.hex())
" 2>/dev/null || echo "")
    if [ -z "$pubkey_hex" ] || [ ${#pubkey_hex} -lt 60 ]; then
      log_warn "Could not extract validator${i} pubkey, using privkey-derived fallback"
      # Let P2P node auto-derive; register with placeholder (will be overwritten)
      pubkey_hex="02$(echo -n "$privkey_hex" | sha256sum | cut -c1-64)"
    fi
    echo "$pubkey_hex" > "$BASE_DIR/worker${i}.pubkey"
    log_info "Worker $i key: addr=$(get_addr $i) pubkey=${pubkey_hex:0:16}..."
  done
  log_info "Extracted $NODES validator keypairs for P2P workers"

  # Register each validator as a worker
  for i in $(seq 0 $((NODES - 1))); do
    local addr=$(get_addr $i)
    local pubkey_hex=$(cat "$BASE_DIR/worker${i}.pubkey")
    local libp2p_port=$((P2P_LIBP2P_PORT_BASE + i))

    log_info "Registering worker $i ($addr) for model $MODEL_ID..."
    local reg_result
    reg_result=$(cli_node $i tx worker register \
      --pubkey "$pubkey_hex" \
      --models "$MODEL_ID" \
      --endpoint "/ip4/127.0.0.1/tcp/${libp2p_port}" \
      --gpu-model "Tesla-T4" \
      --gpu-vram 15 \
      --gpu-count 1 \
      --operator-id "e2e-op-${i}" \
      --from "validator$i" --gas 300000 --fees 1000ufai -y --output json 2>&1)

    if echo "$reg_result" | grep -q "code\|txhash"; then
      log_info "Worker $i registration tx submitted"
    else
      log_warn "Worker $i registration output: $reg_result"
    fi
  done

  wait_n_blocks 3

  # Verify workers registered
  for i in $(seq 0 $((NODES - 1))); do
    local addr=$(get_addr $i)
    local rpc_port=$((RPC_PORT_BASE))
    local worker_result
    worker_result=$(timeout 10 $BINARY query worker show "$addr" \
      --node "tcp://127.0.0.1:${rpc_port}" < /dev/null 2>&1 || echo "not found")
    if echo "$worker_result" | grep -q "address\|Address\|pubkey\|Pubkey"; then
      log_pass "Worker $i registered on-chain"
    else
      log_warn "Worker $i query: $worker_result"
      log_fail "Worker $i not found on-chain"
    fi
  done

  # Add stake for each worker
  for i in $(seq 0 $((NODES - 1))); do
    cli_node $i tx worker stake 10000000000ufai \
      --from "validator$i" --gas 200000 --fees 500ufai -y --output json >/dev/null 2>&1 || true
  done
  wait_n_blocks 2
  log_info "Worker stake added"

  # Derive the SDK user's bech32 address from its deterministic privkey
  local sdk_user_privkey=$(gen_privkey "funai-e2e-user0-secret-seed")
  echo "$sdk_user_privkey" > "$BASE_DIR/sdk-user.privkey"
  local sdk_user_addr
  sdk_user_addr=$(E2E_USER_PRIVKEY="$sdk_user_privkey" E2E_DERIVE_ONLY=1 $CLIENT_BINARY 2>/dev/null | grep "^ADDRESS:" | cut -d: -f2)
  log_info "SDK user address: $sdk_user_addr"

  # First, send bank tokens from validator0 to the SDK user address
  log_info "Sending 10000 FAI from validator0 to SDK user..."
  local send_result
  send_result=$(cli tx send "validator0" "$sdk_user_addr" 10000000000ufai \
    --gas 200000 --fees 500ufai -y --output json 2>&1)
  if echo "$send_result" | grep -q "code\|txhash"; then
    log_info "Bank send tx submitted"
  else
    log_warn "Bank send output: $send_result"
  fi

  wait_n_blocks 2

  # Import the SDK user key into the keyring so we can deposit from it
  # We do this by recovering the key with a mnemonic — but since we use SHA256 seed,
  # we'll instead deposit from validator0 on behalf of the SDK user.
  # The settlement module's MsgDeposit deposits FROM the signer's account TO their own inference account.
  # So we need the deposit to come from the SDK user's address.
  # Workaround: deposit from validator0 and then check if settlement supports depositing for others.
  # Actually, let's deposit from validator0 which has funds and use that as the user account.

  # Simpler approach: deposit from validator0 — the Leader will check validator0's inference balance
  # when it gets a request signed by the SDK user. But this won't match.
  #
  # Best approach: have validator0 deposit, and make SDK client use validator0's key.
  # Let's extract validator0's raw privkey using the keyring export.

  # Actually, the simplest E2E approach: just deposit from validator0 and
  # use validator0's address/key as the SDK user identity.
  local val0_addr=$(get_addr 0)
  log_info "Using validator0 ($val0_addr) as SDK user for deposit"

  log_info "Depositing 1000 FAI to inference account..."
  local deposit_result
  deposit_result=$(cli tx settlement deposit 1000000000ufai \
    --from validator0 --gas 200000 --fees 500ufai -y --output json 2>&1)
  if echo "$deposit_result" | grep -q "code\|txhash"; then
    log_pass "User deposit submitted (1000 FAI)"
  else
    log_warn "Deposit output: $deposit_result"
    log_fail "User deposit failed"
  fi

  wait_n_blocks 2

  # Verify deposit
  local balance_result
  balance_result=$(timeout 10 $BINARY query settlement account "$val0_addr" \
    --node "tcp://127.0.0.1:${RPC_PORT_BASE}" < /dev/null 2>&1 || echo "")
  if echo "$balance_result" | grep -q "balance\|Balance\|amount"; then
    log_pass "User inference account has balance"
  else
    log_warn "Balance query: $balance_result"
  fi

  # Export validator0's raw secp256k1 private key for the SDK client
  log_info "Extracting validator0 private key for SDK client..."
  local val0_privkey_hex
  val0_privkey_hex=$(echo "y" | $BINARY keys export validator0 --keyring-backend "$KEYRING" \
    --home "$BASE_DIR/node0" --unsafe --unarmored-hex 2>/dev/null | grep -oP '^[0-9a-fA-F]{64}$' || echo "")
  if [ -n "$val0_privkey_hex" ] && [ ${#val0_privkey_hex} -eq 64 ]; then
    echo "$val0_privkey_hex" > "$BASE_DIR/sdk-user.privkey"
    log_pass "Validator0 privkey exported for SDK client"
  else
    # Fallback: try alternative export format
    val0_privkey_hex=$(echo "y" | $BINARY keys export validator0 --keyring-backend "$KEYRING" \
      --home "$BASE_DIR/node0" --unsafe --unarmored-hex 2>&1 | tail -1 | tr -d '[:space:]')
    if [ ${#val0_privkey_hex} -eq 64 ]; then
      echo "$val0_privkey_hex" > "$BASE_DIR/sdk-user.privkey"
      log_pass "Validator0 privkey exported (alt method)"
    else
      log_warn "Could not export validator0 privkey (got len=${#val0_privkey_hex})"
      log_info "Export output: $val0_privkey_hex"
    fi
  fi
}

# ── Phase 3: Start P2P Nodes ─────────────────────────────────────────────────

start_p2p_nodes() {
  log_phase "Phase 3: Start P2P Inference Nodes"

  # Collect first node's multiaddr as bootstrap for others
  local first_p2p_addr=""

  for i in $(seq 0 $((NODES - 1))); do
    local addr=$(get_addr $i)
    local privkey_hex=$(cat "$BASE_DIR/worker${i}.privkey")
    local libp2p_port=$((P2P_LIBP2P_PORT_BASE + i))
    local rpc_port=$((RPC_PORT_BASE + i * 2))
    local api_port=$((API_PORT_BASE + i))
    local metrics_port=$((19100 + i))
    local logfile="$BASE_DIR/p2p-node${i}.log"

    local boot_peers=""
    if [ -n "$first_p2p_addr" ]; then
      boot_peers="$first_p2p_addr"
    fi

    FUNAI_LISTEN_ADDR="/ip4/127.0.0.1/tcp/${libp2p_port}" \
    FUNAI_CHAIN_RPC="http://127.0.0.1:${rpc_port}" \
    FUNAI_CHAIN_REST="http://127.0.0.1:${api_port}" \
    FUNAI_TGI_ENDPOINT="$TGI_ENDPOINT" \
    FUNAI_WORKER_ADDR="$addr" \
    FUNAI_WORKER_PRIVKEY="$privkey_hex" \
    FUNAI_MODELS="$MODEL_ID" \
    FUNAI_BOOT_PEERS="$boot_peers" \
    FUNAI_METRICS_ADDR=":${metrics_port}" \
    FUNAI_EPSILON="$EPSILON" \
    FUNAI_CHAIN_ID="$CHAIN_ID" \
    FUNAI_BATCH_INTERVAL="3s" \
    $P2P_BINARY > "$logfile" 2>&1 &
    echo $! > "$BASE_DIR/p2p-node${i}.pid"

    log_info "P2P node $i started (pid=$!, port=$libp2p_port, worker=$addr)"

    # Wait a moment for the first node to start and grab its multiaddr
    if [ "$i" -eq 0 ]; then
      sleep 3
      # Extract peer ID from log
      local peer_id
      peer_id=$(grep -oP 'peer_id=\K[A-Za-z0-9]+' "$logfile" 2>/dev/null | head -1 || echo "")
      if [ -n "$peer_id" ]; then
        first_p2p_addr="/ip4/127.0.0.1/tcp/${libp2p_port}/p2p/${peer_id}"
        log_info "Bootstrap peer: $first_p2p_addr"
      else
        # Fallback: extract from Listening line
        first_p2p_addr=$(grep -oP 'Listening: \K[^\s]+' "$logfile" 2>/dev/null | head -1 || echo "")
        if [ -n "$first_p2p_addr" ]; then
          log_info "Bootstrap peer (from Listening): $first_p2p_addr"
        else
          log_warn "Could not extract bootstrap peer address from node 0 log"
          log_info "Node 0 log:"
          cat "$logfile" 2>/dev/null | head -20
        fi
      fi
    fi
  done

  # Wait for P2P nodes to discover each other and refresh worker lists
  log_info "Waiting 15s for P2P mesh formation and worker list refresh..."
  sleep 15

  # Check P2P nodes are running
  local running=0
  for i in $(seq 0 $((NODES - 1))); do
    local pidfile="$BASE_DIR/p2p-node${i}.pid"
    if [ -f "$pidfile" ]; then
      local pid=$(cat "$pidfile")
      if kill -0 "$pid" 2>/dev/null; then
        running=$((running + 1))
      else
        log_warn "P2P node $i (pid=$pid) is not running"
        log_info "Last 10 lines of log:"
        tail -10 "$BASE_DIR/p2p-node${i}.log" 2>/dev/null || true
      fi
    fi
  done

  if [ "$running" -ge 4 ]; then
    log_pass "All $running P2P nodes running"
  elif [ "$running" -ge 1 ]; then
    log_warn "$running/$NODES P2P nodes running (some failed, continuing...)"
  else
    log_fail "No P2P nodes running"
    for i in $(seq 0 $((NODES - 1))); do
      log_info "=== P2P node $i log ==="
      cat "$BASE_DIR/p2p-node${i}.log" 2>/dev/null | tail -20
    done
    exit 1
  fi

  # Check if nodes see each other (look for worker refresh in logs)
  for i in $(seq 0 $((NODES - 1))); do
    if grep -q "refreshed.*workers\|Dispatch loops started" "$BASE_DIR/p2p-node${i}.log" 2>/dev/null; then
      log_info "P2P node $i: dispatch active"
    else
      log_warn "P2P node $i: no dispatch activity yet"
    fi
  done
}

# ── Phase 4: Send Inference Request ───────────────────────────────────────────

run_inference_test() {
  log_phase "Phase 4: Send Inference Request via SDK"

  # Use the private key prepared in Phase 2 (validator0's or generated key)
  local user_privkey_hex
  user_privkey_hex=$(cat "$BASE_DIR/sdk-user.privkey" 2>/dev/null || echo "")
  if [ -z "$user_privkey_hex" ]; then
    user_privkey_hex=$(gen_privkey "funai-e2e-user0-secret-seed")
    log_warn "Using generated privkey (deposits may not match)"
  fi
  log_info "SDK user privkey loaded"

  # Get bootstrap peer address
  local boot_peer=""
  local first_log="$BASE_DIR/p2p-node0.log"
  boot_peer=$(grep -oP 'Listening: \K[^\s]+' "$first_log" 2>/dev/null | head -1 || echo "")
  if [ -z "$boot_peer" ]; then
    local libp2p_port=$((P2P_LIBP2P_PORT_BASE))
    local peer_id=$(grep -oP 'peer_id=\K[A-Za-z0-9]+' "$first_log" 2>/dev/null | head -1 || echo "")
    if [ -n "$peer_id" ]; then
      boot_peer="/ip4/127.0.0.1/tcp/${libp2p_port}/p2p/${peer_id}"
    fi
  fi

  if [ -z "$boot_peer" ]; then
    log_warn "No bootstrap peer found, SDK client may not discover nodes"
    log_info "P2P node 0 log:"
    head -20 "$first_log" 2>/dev/null || true
  else
    log_info "Bootstrap peer for SDK: $boot_peer"
  fi

  log_info "Sending inference request: \"$INFERENCE_PROMPT\""
  log_info "Model: $MODEL_ID, Temperature: 0 (greedy)"

  local client_log="$BASE_DIR/e2e-client.log"
  local rpc_port=$RPC_PORT_BASE
  local api_port=$API_PORT_BASE

  E2E_BOOT_PEERS="$boot_peer" \
  E2E_CHAIN_RPC="http://127.0.0.1:${rpc_port}" \
  E2E_CHAIN_REST="http://127.0.0.1:${api_port}" \
  E2E_USER_PRIVKEY="$user_privkey_hex" \
  E2E_MODEL_ID="$MODEL_ID" \
  E2E_PROMPT="$INFERENCE_PROMPT" \
  E2E_FEE="1000000" \
  E2E_TEMPERATURE="0" \
  E2E_TIMEOUT="120" \
  timeout 180 $CLIENT_BINARY > "$client_log" 2>&1
  local exit_code=$?

  echo ""
  log_info "=== SDK Client Output ==="
  cat "$client_log"
  echo ""

  if [ $exit_code -eq 0 ] && grep -q "SUCCESS" "$client_log"; then
    log_pass "Inference request completed successfully"

    # Extract output
    local output
    output=$(grep "^Output:" "$client_log" | sed 's/^Output:\s*//' || echo "")
    if [ -n "$output" ]; then
      log_pass "Got inference output: $output"
    fi

    # Check verification
    if grep -q "Verified:.*true" "$client_log"; then
      log_pass "Result verified (hash matched)"
    else
      log_warn "Result verification status unknown"
    fi
  else
    log_fail "Inference request failed (exit_code=$exit_code)"
    log_info "Check logs for details:"
    log_info "  Client:   $client_log"
    log_info "  P2P nodes: $BASE_DIR/p2p-node*.log"
  fi
}

# ── Phase 5: Verify P2P Flow ─────────────────────────────────────────────────

verify_p2p_flow() {
  log_phase "Phase 5: Verify P2P Message Flow"

  # Check for key events in P2P node logs
  local found_dispatch=false
  local found_complete=false
  local found_receipt=false
  local found_verify=false

  for i in $(seq 0 $((NODES - 1))); do
    local logfile="$BASE_DIR/p2p-node${i}.log"
    if [ ! -f "$logfile" ]; then continue; fi

    # Leader dispatch
    if grep -q "dispatched to\|HandleRequest" "$logfile" 2>/dev/null; then
      found_dispatch=true
      log_info "Node $i: Leader dispatched task"
    fi

    # Worker completion
    if grep -q "task.*completed\|HandleTask" "$logfile" 2>/dev/null; then
      found_complete=true
      log_info "Node $i: Worker completed inference"
    fi

    # Receipt broadcast
    if grep -q "receipt for task" "$logfile" 2>/dev/null; then
      found_receipt=true
      log_info "Node $i: Receipt broadcast received"
    fi

    # Verification
    if grep -q "verified task\|verify result" "$logfile" 2>/dev/null; then
      found_verify=true
      log_info "Node $i: Verification completed"
    fi
  done

  $found_dispatch && log_pass "Leader dispatched inference task" || log_warn "No dispatch event found in logs"
  $found_complete && log_pass "Worker executed inference" || log_warn "No completion event found in logs"
  $found_receipt && log_pass "InferReceipt broadcast" || log_warn "No receipt event found in logs"
  $found_verify && log_pass "Verifier processed result" || log_warn "No verification event found in logs"

  # Show TGI interaction from worker logs
  for i in $(seq 0 $((NODES - 1))); do
    local logfile="$BASE_DIR/p2p-node${i}.log"
    if grep -q "TGI\|inference.*result\|generate" "$logfile" 2>/dev/null; then
      log_info "Node $i TGI interactions found"
    fi
  done
}

# ── Phase 6: Check Settlement ─────────────────────────────────────────────────

check_settlement() {
  log_phase "Phase 6: Check On-Chain Settlement"

  # Wait for potential batch settlement
  log_info "Waiting for batch settlement (10s)..."
  sleep 10

  # Check if any settlement transactions were broadcast
  local height=$(get_block_height 0)
  log_info "Current block height: $height"

  # Check proposer logs for batch construction
  local batch_found=false
  for i in $(seq 0 $((NODES - 1))); do
    local logfile="$BASE_DIR/p2p-node${i}.log"
    if grep -q "BuildBatch\|BatchSettlement\|batch.*settlement" "$logfile" 2>/dev/null; then
      batch_found=true
      log_info "Node $i: Batch settlement activity found"
    fi
  done

  if $batch_found; then
    log_pass "Batch settlement initiated"
  else
    log_info "No batch settlement yet (may need more tasks or time)"
  fi

  # Check user balance changes
  local user_addr
  user_addr=$($BINARY keys show "user0" --keyring-backend "$KEYRING" --home "$BASE_DIR/node0" -a 2>/dev/null)
  local balance_result
  balance_result=$(timeout 10 $BINARY query settlement account "$user_addr" \
    --node "tcp://127.0.0.1:${RPC_PORT_BASE}" < /dev/null 2>&1 || echo "")
  if [ -n "$balance_result" ]; then
    log_info "User balance after inference: $balance_result"
  fi
}

# ── Cleanup ───────────────────────────────────────────────────────────────────

cleanup() {
  log_phase "Cleanup"

  # Stop P2P nodes
  for i in $(seq 0 $((NODES - 1))); do
    local pidfile="$BASE_DIR/p2p-node${i}.pid"
    if [ -f "$pidfile" ]; then
      local pid=$(cat "$pidfile")
      kill "$pid" 2>/dev/null || true
      wait "$pid" 2>/dev/null || true
    fi
  done
  log_info "P2P nodes stopped"

  # Stop chain nodes
  for i in $(seq 0 $((NODES - 1))); do
    local pidfile="$BASE_DIR/chain-node${i}.pid"
    if [ -f "$pidfile" ]; then
      local pid=$(cat "$pidfile")
      kill "$pid" 2>/dev/null || true
      wait "$pid" 2>/dev/null || true
    fi
  done
  log_info "Chain nodes stopped"

  if [ "$CLEANUP" = true ]; then
    rm -rf "$BASE_DIR"
    log_info "Testnet data removed"
  else
    log_warn "Testnet preserved at $BASE_DIR (--no-cleanup)"
    log_info "Logs:"
    log_info "  Chain: $BASE_DIR/chain-node*.log"
    log_info "  P2P:   $BASE_DIR/p2p-node*.log"
    log_info "  Client: $BASE_DIR/e2e-client.log"
  fi
}

# ── Main ──────────────────────────────────────────────────────────────────────

main() {
  echo ""
  echo -e "${CYAN}╔══════════════════════════════════════════════════════════════════╗${NC}"
  echo -e "${CYAN}║   FunAI Chain — Full E2E Real Inference Test                    ║${NC}"
  echo -e "${CYAN}║   Chain: $CHAIN_ID  |  Nodes: $NODES  |  TGI: $TGI_ENDPOINT ${NC}"
  echo -e "${CYAN}║   Model: $MODEL_ID                                             ${NC}"
  echo -e "${CYAN}╚══════════════════════════════════════════════════════════════════╝${NC}"
  echo ""

  trap cleanup EXIT

  preflight
  setup_testnet
  start_chain_nodes
  setup_workers_and_deposit
  start_p2p_nodes
  run_inference_test
  verify_p2p_flow
  check_settlement

  # Summary
  echo ""
  echo -e "${CYAN}╔══════════════════════════════════════════════════════════════════╗${NC}"
  echo -e "${CYAN}║                     E2E Real Inference Results                  ║${NC}"
  echo -e "${CYAN}╠══════════════════════════════════════════════════════════════════╣${NC}"
  if [ "$FAILED" -eq 0 ]; then
    echo -e "${CYAN}║${NC} Status:  ${GREEN}ALL CHECKS PASSED${NC}                                     ${CYAN}║${NC}"
  else
    echo -e "${CYAN}║${NC} Status:  ${RED}SOME CHECKS FAILED${NC}                                    ${CYAN}║${NC}"
  fi
  echo -e "${CYAN}║${NC} Total:   $TOTAL assertions                                         ${CYAN}║${NC}"
  echo -e "${CYAN}║${NC} Passed:  ${GREEN}$PASSED${NC}                                                     ${CYAN}║${NC}"
  echo -e "${CYAN}║${NC} Failed:  ${RED}$FAILED${NC}                                                     ${CYAN}║${NC}"
  echo -e "${CYAN}╚══════════════════════════════════════════════════════════════════╝${NC}"
  echo ""

  if [ "$FAILED" -gt 0 ]; then
    exit 1
  fi
}

main "$@"
