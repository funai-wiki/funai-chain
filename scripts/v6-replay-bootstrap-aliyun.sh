#!/bin/bash
# v6-replay-bootstrap-aliyun.sh — from a fresh Alibaba Cloud GPU ECS instance
# to a completed Phase 1a acceptance run in one command.
#
# Target host:
#   - Alibaba Cloud ECS of class ecs.gn7i-* (A10) or equivalent (A100 / L20 / 4090).
#   - Ubuntu 22.04 LTS with NVIDIA driver 535+ pre-installed (use an Alibaba Cloud
#     GPU image).
#   - Outbound internet (PyPI, hf-mirror.com, GitHub).
#   - ≥ 40 GB free on the data disk (torch + transformers + model weights).
#
# What this script does (all idempotent — safe to re-run):
#   1. Verifies GPU + driver.
#   2. Installs Python 3.10+ toolchain if missing.
#   3. Creates/uses a venv under VENV_DIR, installs pinned PyTorch + deps.
#   4. Clones or updates the funai-chain repo at REPO_DIR, checks out BRANCH.
#   5. Pre-downloads the model weights via hf-mirror.com so the test itself
#      fails fast on compute / determinism issues rather than on network.
#   6. Runs `pytest scripts/v6_replay/test_phase1.py` with output captured.
#   7. Writes a verdict.json next to the pytest log under RESULTS_DIR.
#   8. Prints a one-line PASS / INVESTIGATE / FAIL summary and exits with the
#      corresponding status code.
#
# Usage (on the ECS instance, as root or sudo):
#   curl -O <repo-raw-url>/scripts/v6-replay-bootstrap-aliyun.sh
#   sudo bash v6-replay-bootstrap-aliyun.sh
#
# Or, from a local box that can ssh to the ECS:
#   ssh aliyun-box 'cd /root/funai-chain && sudo bash scripts/v6-replay-bootstrap-aliyun.sh'
#
# Overridable via env:
#   MODEL             Qwen/Qwen2.5-3B-Instruct       HF model id (C0 baseline).
#                                                    Set Qwen/Qwen2.5-0.5B-Instruct for a
#                                                    fast pipeline smoke test (~1 GB download).
#   HF_ENDPOINT       https://hf-mirror.com          China-friendly mirror.
#   REPO_URL          https://github.com/funai-network/funai-chain.git
#   REPO_DIR          /data/funai-chain
#   BRANCH            research/v6-replay-poc
#   VENV_DIR          /data/v6-replay-venv
#   RESULTS_DIR       /data/v6-replay-results         Pytest output + verdict.json
#   TORCH_CUDA        cu121                           PyTorch CUDA wheel tag
#   DEVICE            cuda
#
# Example runs:
#   sudo bash v6-replay-bootstrap-aliyun.sh
#   sudo MODEL=Qwen/Qwen2.5-0.5B-Instruct bash v6-replay-bootstrap-aliyun.sh
#   sudo BRANCH=my/feature TORCH_CUDA=cu124 bash v6-replay-bootstrap-aliyun.sh

set -euo pipefail

# ── Configuration ─────────────────────────────────────────────────────────────

MODEL="${MODEL:-Qwen/Qwen2.5-3B-Instruct}"
HF_ENDPOINT="${HF_ENDPOINT:-https://hf-mirror.com}"
REPO_URL="${REPO_URL:-https://github.com/funai-network/funai-chain.git}"
REPO_DIR="${REPO_DIR:-/data/funai-chain}"
BRANCH="${BRANCH:-research/v6-replay-poc}"
VENV_DIR="${VENV_DIR:-/data/v6-replay-venv}"
RESULTS_DIR="${RESULTS_DIR:-/data/v6-replay-results}"
TORCH_CUDA="${TORCH_CUDA:-cu121}"
DEVICE="${DEVICE:-cuda}"
# PYTHON lets you pick the interpreter used to create the venv. Set to
# python3.11 on Alibaba Cloud Linux 3 (system python3 is 3.6.8, too old).
PYTHON="${PYTHON:-python3}"

STAMP="$(date -u +%Y%m%d-%H%M%S)"
RUN_DIR="${RESULTS_DIR}/phase1a-${STAMP}"
PYTEST_LOG="${RUN_DIR}/pytest.log"
VERDICT_FILE="${RUN_DIR}/verdict.json"

# ── Colors ────────────────────────────────────────────────────────────────────

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
    exit 1
  fi
  if ! nvidia-smi -L >/dev/null 2>&1; then
    log_fail "nvidia-smi runs but no GPU detected. Check the instance type and driver status."
    exit 1
  fi
  local gpu_name vram_mib
  gpu_name=$(nvidia-smi --query-gpu=name --format=csv,noheader | head -1)
  vram_mib=$(nvidia-smi --query-gpu=memory.total --format=csv,noheader,nounits | head -1)
  log_pass "GPU: ${gpu_name} (${vram_mib} MiB VRAM)"
}

install_python_toolchain() {
  log_phase "Install Python 3.10+ and git (PYTHON=${PYTHON})"
  local pkg_mgr=""
  if command -v dnf >/dev/null 2>&1; then
    pkg_mgr=dnf
  elif command -v apt-get >/dev/null 2>&1; then
    pkg_mgr=apt-get
  fi

  if ! command -v "${PYTHON}" >/dev/null 2>&1 || ! command -v git >/dev/null 2>&1; then
    case "${pkg_mgr}" in
      dnf)
        # Alibaba Cloud Linux 3 / RHEL-family. System python3 ships as 3.6.x
        # which fails the version gate below; explicitly install 3.11 and
        # set PYTHON=python3.11 so the venv is created from a modern interpreter.
        dnf install -y python3.11 python3.11-pip git
        PYTHON=python3.11
        ;;
      apt-get)
        apt-get update
        apt-get install -y python3 python3-venv python3-pip git
        ;;
      *)
        log_fail "Neither dnf nor apt-get found. Install ${PYTHON} (>=3.10) and git manually, then re-run."
        exit 1
        ;;
    esac
  fi

  local ver
  ver=$("${PYTHON}" -c 'import sys; print("%d.%d" % sys.version_info[:2])')
  if ! "${PYTHON}" -c 'import sys; sys.exit(0 if sys.version_info >= (3, 10) else 1)'; then
    log_fail "${PYTHON} is ${ver}, need >= 3.10. Install python3.11 (dnf install python3.11) and re-run with PYTHON=python3.11."
    exit 1
  fi
  # python3-venv may ship separately on apt-based distros
  if ! "${PYTHON}" -c 'import venv' 2>/dev/null; then
    if [ "${pkg_mgr}" = "apt-get" ]; then
      apt-get install -y python3-venv
    else
      log_fail "${PYTHON} venv module missing. Install the venv package for your distro."
      exit 1
    fi
  fi
  log_pass "${PYTHON} ${ver}"
}

# ── Repo ─────────────────────────────────────────────────────────────────────

sync_repo() {
  log_phase "Sync repo ${REPO_URL} @ ${BRANCH}"
  if [ ! -d "${REPO_DIR}/.git" ]; then
    log_info "Cloning into ${REPO_DIR} ..."
    git clone "${REPO_URL}" "${REPO_DIR}"
  else
    log_info "Updating ${REPO_DIR} ..."
    git -C "${REPO_DIR}" fetch --prune
  fi
  git -C "${REPO_DIR}" checkout "${BRANCH}"
  git -C "${REPO_DIR}" pull --ff-only "origin" "${BRANCH}"
  local sha
  sha=$(git -C "${REPO_DIR}" rev-parse --short HEAD)
  log_pass "On ${BRANCH} @ ${sha}"
}

# ── Python venv ──────────────────────────────────────────────────────────────

setup_venv() {
  log_phase "Set up Python venv at ${VENV_DIR} (from ${PYTHON})"
  if [ ! -d "${VENV_DIR}" ]; then
    "${PYTHON}" -m venv "${VENV_DIR}"
  fi
  # shellcheck source=/dev/null
  source "${VENV_DIR}/bin/activate"
  pip install --upgrade pip wheel setuptools >/dev/null
  log_pass "Venv active: $(which python3) ($(python3 --version))"
}

install_deps() {
  log_phase "Install PyTorch (${TORCH_CUDA}) + transformers + deps"
  # shellcheck source=/dev/null
  source "${VENV_DIR}/bin/activate"
  # torch must come first so its pinned CUDA wheel resolves against the
  # PyTorch index, not PyPI (PyPI ships a CPU-only wheel).
  if ! python3 -c 'import torch; assert torch.cuda.is_available()' 2>/dev/null; then
    pip install --index-url "https://download.pytorch.org/whl/${TORCH_CUDA}" "torch>=2.1,<3"
  else
    log_info "PyTorch already installed with CUDA"
  fi
  # Install the rest of the requirements, explicitly excluding torch so a
  # CPU-only wheel can't clobber the CUDA-enabled install above.
  grep -v '^\s*torch\b' "${REPO_DIR}/scripts/v6_replay/requirements.txt" \
    | pip install -r /dev/stdin
  python3 -c 'import torch, transformers; print(f"torch {torch.__version__} + transformers {transformers.__version__}")'
  python3 -c 'import torch; assert torch.cuda.is_available(), "CUDA not available after install"; print(f"CUDA device: {torch.cuda.get_device_name(0)}")'
  log_pass "Deps ready"
}

# ── Model weights ────────────────────────────────────────────────────────────

preload_model() {
  log_phase "Pre-download model weights (${MODEL})"
  # shellcheck source=/dev/null
  source "${VENV_DIR}/bin/activate"
  export HF_ENDPOINT
  python3 - <<PY
import os
os.environ["HF_ENDPOINT"] = "${HF_ENDPOINT}"
from transformers import AutoModelForCausalLM, AutoTokenizer
print(f"downloading via HF_ENDPOINT={os.environ['HF_ENDPOINT']}: ${MODEL}")
AutoTokenizer.from_pretrained("${MODEL}")
AutoModelForCausalLM.from_pretrained("${MODEL}", torch_dtype="float16")
print("model weights cached")
PY
  log_pass "Model weights ready"
}

# ── Run acceptance test ─────────────────────────────────────────────────────

run_phase1a() {
  log_phase "Run Phase 1a acceptance (pytest)"
  mkdir -p "${RUN_DIR}"
  # shellcheck source=/dev/null
  source "${VENV_DIR}/bin/activate"

  # PYTHONPATH so `scripts.v6_replay` resolves as a package from the repo root.
  # Env vars pass through to test_phase1.py's module-level MODEL / DEVICE.
  local exit_code=0
  (
    cd "${REPO_DIR}"
    export HF_ENDPOINT
    V6_MODEL="${MODEL}" V6_DEVICE="${DEVICE}" \
      python3 -m pytest -v \
        scripts/v6_replay/test_phase1.py \
        2>&1 | tee "${PYTEST_LOG}"
  ) || exit_code=$?

  # Extract the maximum abs-err values pytest printed (if any assertion fired).
  local max_abs_err
  max_abs_err=$(grep -oE 'max_abs_err=[0-9eE+\-.]+' "${PYTEST_LOG}" | sort -u | paste -sd, - || true)

  # Emit verdict.json; result is derived from pytest exit + observed max_abs_err.
  local result
  if [ "${exit_code}" -eq 0 ]; then
    result="PASS"
  elif [ -n "${max_abs_err}" ]; then
    result="KILL"
  else
    result="INVESTIGATE"
  fi

  cat > "${VERDICT_FILE}" <<JSON
{
  "phase": "1a",
  "result": "${result}",
  "pytest_exit_code": ${exit_code},
  "max_abs_err_seen": "${max_abs_err:-none}",
  "config": {
    "model": "${MODEL}",
    "device": "${DEVICE}",
    "branch": "${BRANCH}",
    "commit": "$(git -C "${REPO_DIR}" rev-parse HEAD)",
    "hf_endpoint": "${HF_ENDPOINT}",
    "torch_cuda": "${TORCH_CUDA}",
    "timestamp_utc": "${STAMP}"
  },
  "artifacts_dir": "${RUN_DIR}"
}
JSON

  log_info "Results: ${RUN_DIR}"
  log_info "Full log:   ${PYTEST_LOG}"
  log_info "Verdict:    ${VERDICT_FILE}"

  case "${result}" in
    PASS)
      log_pass "Phase 1a: PASS — max_abs_err == 0.0, determinism floor reached on this hardware"
      ;;
    INVESTIGATE)
      log_warn "Phase 1a: INVESTIGATE — pytest failed but no numeric drift reported; likely setup/import error"
      ;;
    KILL)
      log_fail "Phase 1a: KILL — observed max_abs_err: ${max_abs_err}"
      ;;
  esac
  return "${exit_code}"
}

# ── Main ────────────────────────────────────────────────────────────────────

main() {
  require_root
  check_gpu
  install_python_toolchain
  sync_repo
  setup_venv
  install_deps
  preload_model
  run_phase1a
}

main "$@"
