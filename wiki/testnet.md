# Testnet Configuration

This page covers how to join the FunAI public testnet and run a local testnet for development. The public testnet uses the same binaries as mainnet with testnet-specific genesis parameters.

Sources: [Join_Testnet.md](../docs/Join_Testnet.md), [ops-runbook.md](../docs/ops-runbook.md)

---

## Network Parameters

| Parameter | Value |
|-----------|-------|
| Chain ID | `funai-testnet-1` |
| EVM Chain ID | `123123123` |
| Block time | 5 seconds |
| Token denom | `ufai` (1 FAI = 1,000,000 ufai) |
| Min gas price | `0ufai` |
| Min worker stake | 10,000 FAI |
| Tx fee | 500 ufai |

---

## Seed Node & Boot Peer

| Service | Address |
|---------|---------|
| Seed node | `34.87.21.99` |
| Seed node ID | `ea774f06157b0b61d5f87c6ba6467689af3adb81@34.87.21.99:46656` |
| TGI backend | `34.143.145.204:8080` (Qwen2.5-0.5B-Instruct, TGI 3.3.6) |
| libp2p boot peer | `/ip4/34.87.21.99/tcp/5001/p2p/12D3KooWB6vEj2Cc7SMRK1GG5p5b2pBp8cwtdFaF6uot55nLH8rb` |

---

## Port Table

| Port | Protocol | Exposure | Purpose |
|------|----------|----------|---------|
| 46656 | TCP | **Must be open** | CometBFT P2P |
| 46657 | TCP | Localhost only | CometBFT RPC |
| 21317 | TCP | Localhost only | Cosmos REST API |
| 5001 | TCP | **Must be open** | libp2p P2P |

Firewall rule: only ports 46656 and 5001 need to be reachable from the internet. RPC and REST should stay behind a reverse proxy or bound to localhost in production.

---

## Joining the Testnet

### Step 1 -- Build

```bash
make build-all    # produces ./build/funaid and ./build/funai-node
```

### Step 2 -- Initialize the node

```bash
./build/funaid init my-node --chain-id funai-testnet-1
```

### Step 3 -- Get the testnet genesis

Download the testnet `genesis.json` and place it in `~/.funaid/config/genesis.json`. The genesis file defines initial validators, chain parameters, and the EVM chain ID.

### Step 4 -- Configure seed node

Edit `~/.funaid/config/config.toml`:

```toml
seeds = "ea774f06157b0b61d5f87c6ba6467689af3adb81@34.87.21.99:46656"
```

### Step 5 -- Start the chain node and sync

```bash
./build/funaid start
```

Wait until `catching_up` becomes `false` in the RPC status response:

```bash
curl -s http://localhost:46657/status | jq '.result.sync_info.catching_up'
```

### Step 6 -- Create a worker key

```bash
./build/funaid keys add worker --keyring-backend test
```

### Step 7 -- Get test tokens

Request test FAI from the faucet or an existing testnet operator. You need enough for staking (10,000 FAI minimum) plus transaction fees.

### Step 8 -- Register and stake as a Worker

Submit `MsgRegisterWorker` with your pubkey, endpoint, GPU info, and supported models. Stake at least 10,000 FAI. See [Worker Registration](../docs/Join_Testnet.md) for the exact CLI command and required fields.

### Step 9 -- Deposit inference balance

```bash
./build/funaid tx settlement deposit 1000000000ufai --from worker --fees 500ufai
```

### Step 10 -- Start the P2P node

```bash
FUNAI_BOOT_PEERS="/ip4/34.87.21.99/tcp/5001/p2p/12D3KooWB6vEj2Cc7SMRK1GG5p5b2pBp8cwtdFaF6uot55nLH8rb" \
FUNAI_CHAIN_RPC="http://localhost:46657" \
FUNAI_TGI_ENDPOINT="http://34.143.145.204:8080" \
./build/funai-node
```

### Step 11 -- Send an inference request (optional)

Use the [SDK](sdk.md) or send a direct P2P request to verify end-to-end connectivity.

---

## Worker Registration Details

Worker registration requires `MsgRegisterWorker` with:

- **pubkey** -- secp256k1 public key used for P2P message signing.
- **endpoint** -- publicly reachable address (IP:port or domain).
- **GPU info** -- GPU model and VRAM for dispatch scoring.
- **models** -- list of `model_id` values the worker supports. Each model must be proposed via [MsgModelProposal](msg-types.md) before workers can declare it.

Minimum stake is 10,000 FAI. After registration, declare installed models with `MsgDeclareInstalled`. A model activates when `installed_stake >= 2/3 AND workers >= 4 AND operators >= 4` (see [Model Registry](model-registry.md)).

---

## Private Key Security

The P2P signing key and the chain transaction key are **separate keys** with different risk profiles:

| Key | Purpose | Leak consequence |
|-----|---------|-----------------|
| Chain tx key | Sign on-chain transactions (deposit, withdraw, register) | Full fund theft |
| P2P signing key | Sign off-chain inference receipts | Forge receipts leading to [jail/slash](jail-and-slashing.md), but **cannot steal funds** |

**If your P2P key is compromised:**

1. Submit a worker exit transaction immediately using your chain tx key.
2. Generate a new P2P signing key.
3. Re-register with the new key.

The attacker can forge receipts that get your worker jailed or slashed, but they cannot withdraw your staked or deposited funds. See [settlement state machine](settlement.md) for how jailing works and [jail and slashing](jail-and-slashing.md) for slash amounts.

---

## Local Testnet

### Multi-node local testnet

```bash
make testnet-init    # creates 4 nodes in /tmp/funai-testnet
make testnet-clean   # removes /tmp/funai-testnet
```

This provisions 4 validator nodes with pre-funded accounts and interconnected P2P seeds.

### Full end-to-end testnet (with P2P + TGI)

```bash
scripts/e2e-testnet.sh start    # chain + P2P nodes + TGI backend
scripts/e2e-testnet.sh stop     # tear down
```

This script starts the chain, registers workers, starts P2P nodes, and optionally connects to a TGI backend for live inference testing.

---

## Troubleshooting

| Symptom | Likely cause | Fix |
|---------|-------------|-----|
| `catching_up` stays `true` forever | Seed node unreachable or genesis mismatch | Verify firewall allows outbound 46656; confirm `genesis.json` matches testnet |
| `connection refused` on RPC | Chain node not running or RPC bound to wrong interface | Check `funaid` process is running; verify `laddr` in `config.toml` |
| `worker not found` after registration | Transaction not yet committed or wrong chain queried | Wait for next block; confirm `--chain-id funai-testnet-1` |
| No dispatch logs on P2P node | Boot peer unreachable or model not activated | Verify port 5001 is open; check model activation status via [model registry](model-registry.md) query |
| Inference timeout | TGI backend down or network latency | Confirm TGI endpoint is reachable: `curl http://34.143.145.204:8080/health` |
| `insufficient balance` on settlement | Deposit too low for accumulated inference fees | Top up with `MsgDeposit`; see [overspend protection](overspend-protection.md) for the three-layer balance check |

---

## Related Pages

- [Architecture](architecture.md)
- [Settlement](settlement.md)
- [Model Registry](model-registry.md)
- [Jail and Slashing](jail-and-slashing.md)
- [Overspend Protection](overspend-protection.md)
- [Operations Runbook](operations.md)
