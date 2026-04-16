# Operations Runbook

This page covers day-to-day operations for running FunAI Chain nodes and P2P inference nodes, including configuration, monitoring, troubleshooting, and upgrades.

Sources: [ops-runbook.md](../docs/ops-runbook.md), [Phase4_Full_Network_Guide.md](../docs/Phase4_Full_Network_Guide.md)

---

## Quick Start

```bash
# 1. Build both binaries
make build-all

# 2. Initialize and start the chain node
make init
make start

# 3. Start the P2P inference node (separate terminal)
FUNAI_CHAIN_RPC="http://localhost:26657" \
FUNAI_TGI_ENDPOINT="http://localhost:8080" \
FUNAI_WORKER_ADDR="funai1..." \
FUNAI_MODELS="model_id_1,model_id_2" \
FUNAI_BOOT_PEERS="/ip4/34.87.21.99/tcp/5001/p2p/12D3KooWB6vEj2Cc7SMRK1GG5p5b2pBp8cwtdFaF6uot55nLH8rb" \
./build/funai-node
```

For testnet-specific setup, see [Testnet Configuration](testnet.md).

---

## Environment Variables

The `funai-node` binary is configured entirely through environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `FUNAI_LISTEN_ADDR` | `/ip4/0.0.0.0/tcp/4001` | libp2p listen address |
| `FUNAI_CHAIN_RPC` | `http://localhost:26657` | CometBFT RPC endpoint for chain queries |
| `FUNAI_TGI_ENDPOINT` | `http://localhost:8080` | TGI (Text Generation Inference) backend URL |
| `FUNAI_WORKER_ADDR` | (required) | Bech32 worker address on-chain |
| `FUNAI_MODELS` | (required) | Comma-separated list of `model_id` values to serve |
| `FUNAI_BOOT_PEERS` | (required) | Multiaddr(s) of libp2p bootstrap peers |
| `FUNAI_SIGNING_KEY` | (required) | Hex-encoded secp256k1 private key for P2P message signing |
| `FUNAI_LOG_LEVEL` | `info` | Log verbosity: `debug`, `info`, `warn`, `error` |
| `FUNAI_METRICS_ADDR` | `:9100` | Prometheus metrics listen address |
| `FUNAI_DATA_DIR` | `~/.funai-node` | Local data store directory (7-day retention) |

---

## Chain Configuration

### config.toml (CometBFT)

Key settings in `~/.funaid/config/config.toml`:

| Setting | Default | Notes |
|---------|---------|-------|
| `proxy_app` | `tcp://127.0.0.1:26658` | ABCI connection |
| `[p2p] laddr` | `tcp://0.0.0.0:26656` | P2P listen address |
| `[rpc] laddr` | `tcp://127.0.0.1:26657` | RPC listen address (keep localhost in production) |
| `[consensus] timeout_commit` | `5s` | Block time target |
| `[p2p] seeds` | (empty) | Set to seed node for [testnet](testnet.md) |

### app.toml (Cosmos SDK)

Key settings in `~/.funaid/config/app.toml`:

| Setting | Default | Notes |
|---------|---------|-------|
| `minimum-gas-prices` | `0ufai` | Minimum accepted gas price |
| `[api] address` | `tcp://localhost:1317` | REST API endpoint |
| `[grpc] address` | `0.0.0.0:9090` | gRPC endpoint |
| `[evm] chain-id` | `123123123` | EVM chain ID (must match genesis) |

See [EVM Integration](evm-integration.md) for EVM-specific configuration.

---

## Key Management

### Validator keys

Managed through the Cosmos SDK keyring:

```bash
# Create a new key
funaid keys add validator --keyring-backend file

# List keys
funaid keys list --keyring-backend file

# Export for backup
funaid keys export validator --keyring-backend file
```

### Worker signing keys

The P2P signing key is a hex-encoded secp256k1 private key passed via `FUNAI_SIGNING_KEY`. This key is **separate** from the chain transaction key -- see [Testnet: Private Key Security](testnet.md#private-key-security) for the risk model.

### X25519 encryption keys

Auto-generated on first run and stored in `FUNAI_DATA_DIR`. Used for P2P message encryption.

### Production recommendations

- Use HashiCorp Vault or Kubernetes Secrets for all private keys.
- Never store keys in environment files committed to version control.
- Rotate the P2P signing key periodically by re-registering the worker.
- Keep the chain transaction key on a separate, hardened machine when possible.

---

## Monitoring

### Prometheus Metrics

All metrics are exposed on the `FUNAI_METRICS_ADDR` endpoint (default `:9100/metrics`). Grafana dashboards are provided in `monitoring/`.

| Metric | Normal range | Alert threshold | Description |
|--------|-------------|-----------------|-------------|
| `funai_inference_latency_seconds` | p95 < 10s | p99 > 30s | End-to-end inference latency including dispatch, execution, and verification |
| `funai_verification_latency_seconds` | p95 < 0.6s | p99 > 2s | Teacher forcing verification time (single forward pass) |
| `funai_settlement_total` | Growing | No new settlements for 5 min | Cumulative count of settled tasks in `MsgBatchSettlement` |
| `funai_audit_rate_permille` | 80--120 | < 50 or > 300 | Current audit sampling rate in permille (100 = 10%). See [settlement](settlement.md) for dynamic rate details |
| `funai_worker_jail_total` | 0 | Any increase | Cumulative jail events. Any jail warrants investigation -- see [jail and slashing](jail-and-slashing.md) |
| `funai_leader_failover_total` | 0 | > 3/hour | Leader failover events. Frequent failovers indicate network instability or leader node issues |
| `funai_p2p_connected_peers` | >= 3 | < 2 | Connected libp2p peers. Below 2 means the node is effectively isolated |

### Alert examples

```yaml
# Prometheus alerting rule examples
- alert: InferenceLatencyHigh
  expr: histogram_quantile(0.99, funai_inference_latency_seconds_bucket) > 30
  for: 5m

- alert: SettlementStalled
  expr: increase(funai_settlement_total[5m]) == 0
  for: 5m

- alert: WorkerJailed
  expr: increase(funai_worker_jail_total[1m]) > 0
```

---

## Troubleshooting

| Symptom | Likely cause | Fix |
|---------|-------------|-----|
| Node not producing blocks | Validator not in active set or insufficient voting power | Check `funaid status`; verify stake meets minimum; check if jailed |
| P2P connection failure | Firewall blocking ports or wrong boot peers | Verify ports 26656/5001 are open; confirm boot peer multiaddr |
| Settlement stuck (no new `funai_settlement_total`) | Proposer down or no CLEARED tasks | Check proposer logs; verify tasks are reaching CLEARED state via [settlement pipeline](settlement.md) |
| Worker jailed | Failed verification or timeout | Check `jail_count` -- 1st/2nd jail is recoverable via `MsgUnjail` after cooldown. 3rd = permanent tombstone. See [jail and slashing](jail-and-slashing.md) |
| Audit rate anomaly (too high or too low) | Dynamic rate adjustment responding to network conditions | Rates are dynamic (5--30% audit, 0.5--5% re-audit) -- verify the rate is within bounds. Sustained out-of-range values indicate a parameter misconfiguration |
| High leader failover rate | Leader node unstable or network partition | Check leader node health and connectivity; review `funai_p2p_connected_peers` on the leader |

---

## Deployment and Upgrades

### Parameter updates (no binary change)

On-chain parameters can be updated through governance proposals without restarting nodes:

```bash
funaid tx gov submit-proposal param-change proposal.json --from validator --fees 500ufai
```

Governable parameters include audit rates, reward splits, and stake minimums.

### Software upgrades (binary swap)

For binary upgrades that change state machine logic:

1. Coordinate a halt height across all validators.
2. Set `halt-height` in `app.toml` or pass `--halt-height` on the command line.
3. Wait for the node to stop at the designated height.
4. Replace the `funaid` binary (and `funai-node` if needed).
5. Remove `halt-height` from config.
6. Restart the node.

```bash
# Example: halt at height 50000
funaid start --halt-height 50000
# ... node halts ...
cp ./build/funaid-v2 ./build/funaid
funaid start
```

---

## Mainnet Genesis Checklist

Critical parameters that must be verified before mainnet genesis:

| Parameter | Value | Notes |
|-----------|-------|-------|
| `ColdStartFreeBlocks` | 51,840 | ~3 days of free blocks at 5s block time |
| `BlockReward` | 4,000 FAI | Per-block reward, see [tokenomics](tokenomics.md) |
| `HalvingInterval` | 26,250,000 | ~4.16 years at 5s blocks |
| `MinWorkerStake` | 10,000 FAI | Minimum stake for worker registration |
| `AuditBaseRate` | 100 | Base audit rate in permille (10%) |
| `CommitteeSize` | 100 | Number of validators in consensus committee |
| `UnbondingTime` | 1,814,400s | 21 days |
| `BondDenom` | `ufai` | Must match all module configs |
| VRF seed | Non-empty | Initial [VRF](vrf.md) seed must be set in genesis |
| Operators | >= 4 | Minimum distinct operators for model activation |

---

## Emergency Procedures

### Emergency halt

```bash
# Option 1: Set halt-height in app.toml (graceful)
# Edit ~/.funaid/config/app.toml: halt-height = <current_height + 1>

# Option 2: Stop the process immediately
systemctl stop funaid
```

### State export

```bash
funaid export > state_backup.json
```

This produces a full state export that can be used to restart the chain from a known-good state or to migrate to a new chain ID.

### Recovery from state export

```bash
funaid init new-node --chain-id funai_123123123-3
cp state_backup.json ~/.funaid/config/genesis.json
funaid start
```

---

## Related Pages

- [Worker Operator Guide](../docs/guides/Worker_Operator_Guide.md) — Setup, staking, model management, reputation
- [Validator Guide](../docs/guides/Validator_Guide.md) — VRF committee, block rewards, governance
- [Testnet Configuration](testnet.md)
- [Architecture](architecture.md)
- [Settlement](settlement.md)
- [Jail and Slashing](jail-and-slashing.md)
- [Tokenomics](tokenomics.md)
- [VRF](vrf.md)
- [EVM Integration](evm-integration.md)
