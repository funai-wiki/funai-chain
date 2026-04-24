#!/bin/bash
# e2e-mock-fraud.sh — Run the full P2P e2e pipeline in fraud-injection mode.
#
# Identical to scripts/e2e-mock-inference.sh but sets E2E_FRAUD_MODE=1, which
# in turn sets FUNAI_TEST_CORRUPT_RECEIPT=1 on every P2P node. Worker-side
# receipt.ResultHash gets XOR'd by 0xFF before being signed — the SDK then
# sees a mismatch between the content it reassembled from StreamTokens and
# the hash in the receipt, and submits a MsgFraudProof. This exercises the
# full fraud path: Worker corruption → SDK detection → FraudProof tx.
#
# Do NOT run this against a production chain. Every task intentionally
# produces a slashable receipt.
#
# Usage:
#   bash scripts/e2e-mock-fraud.sh
set -euo pipefail

export E2E_FRAUD_MODE=1
exec bash "$(dirname "$0")/e2e-mock-inference.sh" "$@"
