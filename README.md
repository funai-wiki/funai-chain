# FunAI Chain

A decentralized AI inference network that separates settlement from computation. The chain acts as a bank -- handling deposits, withdrawals, staking, and reward distribution -- while all inference happens off-chain over a peer-to-peer network.

## Architecture

FunAI uses a three-layer architecture:

| Layer | Role | Technology |
|-------|------|------------|
| **L1 -- Cosmos Chain** | Deposits, withdrawals, settlements, staking, block rewards | CometBFT v0.38.17 + Cosmos SDK v0.50.13 |
| **L2 -- P2P Network** | Dispatch, inference, verification, signature exchange | libp2p pubsub per model topic |
| **L3 -- Client SDK** | Model selection, pricing hints, streaming, auto-retry, privacy | Client-side only |

The chain never processes inference. Users pre-deposit funds, sign off-chain requests, Workers execute inference and collect signatures, and Proposers batch-settle on-chain. This "Lightning Scheme" design enables the network to scale beyond one million inferences per second without being bottlenecked by blockchain throughput.

## Key Features

- **Permissionless participation** -- anyone with a GPU and sufficient stake can join as a Worker
- **Cryptographic verification** -- every inference is verified through teacher forcing, deterministic sampling (ChaCha20), and random auditing
- **VRF-based selection** -- unified formula `score = hash(seed || pubkey) / stake^a` for dispatch, verification, auditing, and leader election
- **Economically rational security** -- cheating is structurally unprofitable; misbehaving Workers are jailed and slashed
- **Batch settlement** -- Proposers aggregate cleared tasks into on-chain batch transactions for efficient settlement
- **Three-layer overspend protection** -- Leader tracking, Worker self-check, and on-chain fallback ensure users cannot overspend

## Quick Start

### Prerequisites

- Go 1.25+
- Make
- [buf](https://buf.build/) (for protobuf codegen)

### Build

```bash
make build-all    # Build both funaid + funai-node binaries to ./build/
make proto        # Generate protobuf code
make lint         # Run linter
```

### Run a Local Testnet

```bash
make build
make init           # Initialize single-node chain (chain-id: funai-1)
make start          # Start the chain node
```

### Multi-Node Testnet

```bash
make testnet-init   # Initialize multi-node testnet
make testnet-clean  # Remove testnet data
```

### Test

```bash
make test          # Run all tests with race detection
make bench         # Run benchmarks
```

### Docker

```bash
make docker-build  # Build Docker images for both binaries
```

## Directory Structure

```
cmd/
  funaid/          -- Cosmos chain node binary
  funai-node/      -- P2P inference node binary
app/               -- Cosmos app wiring (genesis, config, encoding)
x/                 -- Custom Cosmos SDK modules
  worker/          -- Worker registration, stake, jail/unjail
  modelreg/        -- Model proposals, activation thresholds
  settlement/      -- User balances, BatchSettlement, FraudProof
  reward/          -- Block reward distribution
  vrf/             -- Unified VRF scoring
p2p/               -- Off-chain P2P layer (leader, worker, verifier, proposer)
sdk/               -- Client SDK (privacy, streaming)
tests/e2e/         -- End-to-end tests
bench/             -- Benchmarks
scripts/           -- Testnet initialization scripts
monitoring/        -- Prometheus/Grafana configurations
docs/              -- Design documents and guides
```

## Token

| Property | Value |
|----------|-------|
| Token | FAI |
| Denomination | `ufai` (1 FAI = 1,000,000 ufai) |
| Total Supply | 210,000,000,000 FAI |
| Block Reward | 4,000 FAI/block |
| Halving Interval | ~4.16 years |
| Block Time | 5 seconds |
| Reward Split (with inference) | 99% inference contribution, 1% verification/audit |
| Reward Split (no inference) | 100% to consensus committee |

## Chain Information

| Property | Value |
|----------|-------|
| Chain ID | `funai_123123123-3` |
| EVM Chain ID | `123123123` |
| Bech32 Prefix | `funai` |
| Daemon | `funaid` |
| Node Home | `$HOME/.funaid` |
| Key Algorithm | secp256k1 |
| Cosmos SDK | v0.50.13 |
| CometBFT | v0.38.17 |

## Documentation

- [SDK Developer Guide](docs/guides/SDK_Developer_Guide.md) -- integrate FunAI inference into your application
- [Worker Operator Guide](docs/guides/Worker_Operator_Guide.md) -- run a GPU inference Worker
- [Validator Guide](docs/guides/Validator_Guide.md) -- operate a chain validator node
- [Join Testnet](docs/guides/Join_Testnet.md) -- connect to the public testnet

## License

TBD

## Links

- GitHub: [https://github.com/funai-wiki/funai-chain](https://github.com/funai-wiki/funai-chain)
