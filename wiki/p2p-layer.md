# P2P Layer

The P2P layer is FunAI Chain's off-chain inference network, built on libp2p. It handles dispatch, inference execution, verification, signature exchange, and leader coordination -- everything that does not need consensus finality. The chain itself [never processes inference](architecture.md).

Sources: [FunAI V52 Final Design Spec](../docs/FunAI_V52_Final.md), [P2P README](../p2p/README.md)

## Leader Election

- **1 Leader per `model_id`** every **30 seconds** (one epoch).
- VRF ranking with **alpha = 1.0**, seed = `model_id || sub_topic_id || epoch_block_hash`.
- The node with the **lowest score** becomes Leader.
- The same VRF simultaneously produces **rank #2** and **rank #3** as backup Leaders.

## Dispatch

Every **100ms**, the Leader pulls a batch of pending requests from its local mempool and assigns them to Workers:

1. Workers are ranked using VRF (alpha = 1.0, seed = `task_id || block_hash`). Busy workers are skipped.
2. The top-ranked available Worker receives the task and has **1 second** to accept or reject.
3. On reject or timeout, the task falls to the next rank.
4. After **3 consecutive silent ranks**, the task returns to the mempool for retry.

**Worker busy release:** A Worker is marked as free after streaming its last message to the user SDK. The Leader observes the `InferReceipt` published on the P2P network.

## Failover

If **1.5 seconds** pass with no Leader activity (no dispatches, no heartbeats), all Workers switch to **rank #2**, who immediately begins dispatching.

**Brain-split resolution:** If two Leaders coexist temporarily (e.g., network partition), Workers use deterministic VRF ranking to deduplicate by `task_id` -- each task is only executed once. At the 30-second epoch boundary, the next VRF election produces a single Leader, and the split converges naturally.

## Sub-Topic Splitting

When throughput exceeds a single Leader's capacity, the P2P layer automatically splits into sub-topics:

```
N = ceil(recent_tps / 500)
sub_topic_id = hash(task_id) % N
```

`N` is recalculated **per epoch** based on recent observed TPS. Each sub-topic gets its own independent Leader via VRF election.

### Scale Table

| Stage | TPS | Leaders | Workers |
|-------|-----|---------|---------|
| Early | 10 | 1 | 150 |
| Growth | 2,000 | 4 | 6,000 |
| Mature | 50,000 | 100 | 300,000 |
| Extreme | 1,000,000 | 2,000 | 3,000,000 |

## Node Roles

Every node can serve up to **7 simultaneous roles**:

1. **Worker** -- executes inference tasks
2. **Verifier** -- performs teacher-forcing [verification](verification.md)
3. **SecondVerifier** -- conducts post-settlement second verifications
4. **Leader** -- dispatches tasks within a model sub-topic
5. **Proposer** -- constructs `MsgBatchSettlement` for on-chain [settlement](settlement.md)
6. **Validator** -- participates in CometBFT consensus
7. **SDK-user** -- submits inference requests as a client

## Data Retention

All participating nodes retain task data (prompts, outputs, receipts, signatures) for **7 days**. After 7 days, data is pruned automatically. This window covers the maximum second verification and dispute resolution period.

## Overspend Checks

The Leader performs the first layer of [overspend protection](overspend-protection.md) by tracking pending fees locally. Workers perform the second layer by checking on-chain balances before accepting tasks.

## Related Pages

- [Architecture overview](architecture.md) -- three-layer design
- [Verification protocol](verification.md) -- teacher forcing and sampling checks
- [Settlement state machine](settlement.md) -- how completed tasks become payouts
- [Model registry](model-registry.md) -- model activation and running thresholds
- [Overspend protection](overspend-protection.md) -- three-layer balance safety
