#!/bin/bash
# tgi-bootstrap-aliyun.sh — from a fresh Alibaba Cloud ECS GPU instance to a running
# TGI endpoint in one command.
#
# Target host:
#   - Alibaba Cloud ECS instance of class ecs.gn7i-* (A10), ecs.gn8is-* (L20),
#     ecs.gn7e-* / ecs.ebmgn7* (A100), or similar.
#   - Ubuntu 22.04 LTS. Other distros may need minor tweaks (apt → yum/dnf).
#   - Public IP if you plan to connect from outside the VPC.
#   - Security group rule (cloud-level, NOT handled by this script) that opens
#     TGI_PORT to your test runner / CI / office egress IPs only. Do NOT open
#     0.0.0.0/0 — TGI has no auth.
#   - ≥ 100 GB data disk (model weights + HF cache).
#
# What this script does:
#   1. Verifies GPU is present and driver is working.
#   2. Installs Docker if missing.
#   3. Installs and configures nvidia-container-toolkit so containers can see the GPU.
#   4. Pulls the pinned TGI image.
#   5. Launches TGI with a pinned model, restart=unless-stopped, HF cache bind-mounted.
#   6. Waits for /health, runs a smoke test against /generate, prints the endpoint URLs.
#
# What this script does NOT do:
#   - Install NVIDIA GPU drivers. Use an Alibaba Cloud GPU image that ships with the
#     driver already installed (e.g. Ubuntu 22.04 with NVIDIA driver 535+).
#   - Configure cloud security groups / firewall rules. Do that in the Alibaba console.
#   - Set up auth on TGI (it has none). Keep the security group tight.
#
# Usage (on the ECS instance, as root):
#   curl -O <repo-raw-url>/scripts/tgi-bootstrap-aliyun.sh
#   sudo bash tgi-bootstrap-aliyun.sh
#
# Overridable via env:
#   MODEL           Qwen/Qwen2.5-8B-Instruct          HF model id to serve
#   TGI_VERSION     3.3.6                             Pinned per FunAI_TPS_Logits_Test_Plan_KT.md
#   TGI_PORT        8080                              Host port (container internal port is 80)
#   DTYPE           float16
#   HF_CACHE_DIR    /data/hf_cache                    Model weights cached here; bind-mounted
#   HF_ENDPOINT     https://hf-mirror.com             China-friendly mirror; unset or blank to use hf.co directly
#   CONTAINER_NAME  tgi
#
# Example runs:
#   sudo bash tgi-bootstrap-aliyun.sh
#   sudo MODEL=Qwen/Qwen2.5-32B-Instruct-GPTQ-Int4 bash tgi-bootstrap-aliyun.sh
#   sudo TGI_VERSION=2.4.1 TGI_PORT=18080 bash tgi-bootstrap-aliyun.sh

set -euo pipefail

# ── Configuration ─────────────────────────────────────────────────────────────

MODEL="${MODEL:-Qwen/Qwen2.5-8B-Instruct}"
TGI_VERSION="${TGI_VERSION:-3.3.6}"
TGI_PORT="${TGI_PORT:-8080}"
DTYPE="${DTYPE:-float16}"
HF_CACHE_DIR="${HF_CACHE_DIR:-/data/hf_cache}"
HF_ENDPOINT="${HF_ENDPOINT:-https://hf-mirror.com}"
CONTAINER_NAME="${CONTAINER_NAME:-tgi}"

TGI_IMAGE="ghcr.io/huggingface/text-generation-inference:${TGI_VERSION}"
HEALTH_TIMEOUT_SEC=600  # TGI can take ~5–10 min on first run (model download + warmup)

# ── Colors ─────────────────────────────────────────────────────────────────────

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m'

log_info()  { echo -e "${BLUE}[INFO]${NC} $*"; }
log_pass()  { echo -e "${GREEN}[PASS]${NC} $*"; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC} $*"; }
log_fail()  { echo -e "${RED}[FAIL]${NC} $*"; }
log_phase() { echo -e "\n${CYAN}══ $* ══${NC}\n"; }

# ── Prerequisites ─────────────────────────────────────────────────────────────

require_root() {
  if [ "$EUID" -ne 0 ]; then
    log_fail "Run as root: sudo bash $0"
    exit 1
  fi
}

check_gpu() {
  log_phase "Check GPU"
  if ! command -v nvidia-smi >/dev/null 2>&1; then
    log_fail "nvidia-smi not found. Use an Alibaba Cloud GPU image with NVIDIA driver pre-installed."
    log_info "See: https://www.alibabacloud.com/help/en/ecs/user-guide/nvidia-driver-installation"
    exit 1
  fi
  if ! nvidia-smi -L >/dev/null 2>&1; then
    log_fail "nvidia-smi runs but no GPU detected. Check the instance type and driver status."
    exit 1
  fi
  local gpu_name
  gpu_name=$(nvidia-smi --query-gpu=name --format=csv,noheader | head -1)
  local vram_mib
  vram_mib=$(nvidia-smi --query-gpu=memory.total --format=csv,noheader,nounits | head -1)
  log_pass "GPU: ${gpu_name} (${vram_mib} MiB VRAM)"
}

# ── Docker ────────────────────────────────────────────────────────────────────

install_docker() {
  log_phase "Install Docker"
  if command -v docker >/dev/null 2>&1; then
    log_pass "Docker already installed: $(docker --version)"
    return
  fi
  log_info "Installing Docker via get.docker.com ..."
  curl -fsSL https://get.docker.com -o /tmp/get-docker.sh
  sh /tmp/get-docker.sh
  systemctl enable --now docker
  log_pass "Docker installed"
}

install_nvidia_container_toolkit() {
  log_phase "Install nvidia-container-toolkit"
  if docker info 2>/dev/null | grep -q "Runtimes:.*nvidia"; then
    log_pass "nvidia runtime already registered with Docker"
    return
  fi

  # NVIDIA apt repository
  curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey \
    | gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg
  curl -fsSL https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list \
    | sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g' \
    > /etc/apt/sources.list.d/nvidia-container-toolkit.list

  apt-get update -qq
  apt-get install -y -qq nvidia-container-toolkit
  nvidia-ctk runtime configure --runtime=docker
  systemctl restart docker

  if ! docker info 2>/dev/null | grep -q "Runtimes:.*nvidia"; then
    log_fail "nvidia runtime still not visible to Docker after install"
    exit 1
  fi
  log_pass "nvidia-container-toolkit configured"
}

verify_gpu_in_docker() {
  log_info "Verifying GPU visibility inside Docker ..."
  if ! docker run --rm --gpus all nvidia/cuda:12.4.0-base-ubuntu22.04 nvidia-smi -L >/dev/null 2>&1; then
    log_fail "Docker cannot access the GPU. Check 'docker info | grep -i runtime' and restart Docker."
    exit 1
  fi
  log_pass "GPU is visible to Docker"
}

# ── TGI ───────────────────────────────────────────────────────────────────────

prepare_cache_dir() {
  log_phase "Prepare model cache"
  mkdir -p "$HF_CACHE_DIR"
  log_pass "HF cache directory: $HF_CACHE_DIR"
}

pull_tgi_image() {
  log_phase "Pull TGI image"
  log_info "Pulling $TGI_IMAGE (first run may take several minutes) ..."
  docker pull "$TGI_IMAGE"
  log_pass "TGI image pulled"
}

stop_existing_container() {
  if docker ps -a --format '{{.Names}}' | grep -q "^${CONTAINER_NAME}$"; then
    log_info "Stopping existing '${CONTAINER_NAME}' container ..."
    docker stop "$CONTAINER_NAME" >/dev/null 2>&1 || true
    docker rm   "$CONTAINER_NAME" >/dev/null 2>&1 || true
  fi
}

start_tgi() {
  log_phase "Start TGI container"
  local hf_env=()
  if [ -n "$HF_ENDPOINT" ]; then
    log_info "Using HF mirror: $HF_ENDPOINT"
    hf_env+=("-e" "HF_ENDPOINT=${HF_ENDPOINT}")
  fi

  log_info "Serving ${MODEL} on 0.0.0.0:${TGI_PORT} (dtype=${DTYPE})"
  docker run -d \
    --name "$CONTAINER_NAME" \
    --restart unless-stopped \
    --gpus all \
    -p "${TGI_PORT}:80" \
    -v "${HF_CACHE_DIR}:/data" \
    "${hf_env[@]}" \
    "$TGI_IMAGE" \
    --model-id "$MODEL" \
    --dtype "$DTYPE" \
    --num-shard 1 \
    >/dev/null
  log_pass "Container started: $CONTAINER_NAME"
}

wait_for_health() {
  log_phase "Wait for /health"
  log_info "First run downloads the model weights — this can take 5–10 minutes."
  log_info "Tail the log in another shell: docker logs -f $CONTAINER_NAME"
  local elapsed=0
  while [ $elapsed -lt $HEALTH_TIMEOUT_SEC ]; do
    if curl -sf "http://localhost:${TGI_PORT}/health" >/dev/null 2>&1; then
      log_pass "TGI is healthy after ${elapsed}s"
      return 0
    fi
    sleep 5
    elapsed=$((elapsed + 5))
    if [ $((elapsed % 60)) -eq 0 ]; then
      log_info "still waiting ... (${elapsed}s elapsed)"
    fi
  done
  log_fail "TGI did not become healthy within ${HEALTH_TIMEOUT_SEC}s"
  log_info "Inspect with: docker logs --tail 100 $CONTAINER_NAME"
  exit 1
}

smoke_test() {
  log_phase "Smoke test (inference + decoder_input_details + top_n_tokens)"

  # Basic generate
  local gen_resp
  gen_resp=$(curl -sf -X POST "http://localhost:${TGI_PORT}/generate" \
    -H "Content-Type: application/json" \
    -d '{"inputs":"What is 2+2? Answer with just the number.",
         "parameters":{"max_new_tokens":5}}')
  if ! echo "$gen_resp" | grep -q "generated_text"; then
    log_fail "/generate did not return generated_text"
    echo "response: $gen_resp"
    exit 1
  fi
  log_pass "/generate basic: $(echo "$gen_resp" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("generated_text","")[:60])')"

  # decoder_input_details + top_n_tokens (required by FunAI verifier)
  local did_resp
  did_resp=$(curl -sf -X POST "http://localhost:${TGI_PORT}/generate" \
    -H "Content-Type: application/json" \
    -d '{"inputs":"Hi",
         "parameters":{"max_new_tokens":1,
                       "details":true,
                       "decoder_input_details":true,
                       "top_n_tokens":5}}')
  if ! echo "$did_resp" | grep -q "details"; then
    log_fail "/generate with decoder_input_details failed — FunAI verifier needs this"
    echo "response: $did_resp"
    exit 1
  fi
  log_pass "/generate with details + top_n_tokens works"

  # Version check
  local info_resp
  info_resp=$(curl -sf "http://localhost:${TGI_PORT}/info")
  local version
  version=$(echo "$info_resp" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("version",""))' || echo "?")
  log_pass "TGI reports version: $version"
}

print_summary() {
  log_phase "Done"
  local public_ip private_ip
  public_ip=$(curl -s --max-time 3 https://ipv4.icanhazip.com 2>/dev/null || echo "<public-ip>")
  private_ip=$(ip -4 addr show | awk '/inet / && !/127\./ {print $2}' | cut -d/ -f1 | head -1)

  cat <<EOF

  Model:        ${MODEL}
  TGI:          ${TGI_VERSION}
  dtype:        ${DTYPE}
  Container:    ${CONTAINER_NAME}
  Internal URL: http://${private_ip}:${TGI_PORT}
  Public URL:   http://${public_ip}:${TGI_PORT}

  Quick checks:
    curl http://localhost:${TGI_PORT}/health
    curl http://localhost:${TGI_PORT}/info

  Logs:     docker logs -f ${CONTAINER_NAME}
  Stop:     docker stop  ${CONTAINER_NAME}
  Restart:  docker start ${CONTAINER_NAME}

  From your dev box (run the funai-chain end-to-end test against this endpoint):
    TGI_ENDPOINT=http://${public_ip}:${TGI_PORT} make test-e2e-real

  SECURITY REMINDER:
    TGI has no authentication. Restrict ${TGI_PORT} to your CI / office egress IPs
    in the Alibaba Cloud security group. Never open 0.0.0.0/0.

EOF
}

# ── Main ──────────────────────────────────────────────────────────────────────

require_root
check_gpu
install_docker
install_nvidia_container_toolkit
verify_gpu_in_docker
prepare_cache_dir
pull_tgi_image
stop_existing_container
start_tgi
wait_for_health
smoke_test
print_summary
