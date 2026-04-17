FunAI_V52_Final.md
FunAI V5.2 Architecture Design Complete Specification (Final)
Based on V5.1_Final + per-task VRF second verification + 99/1 reward distribution + no DA + overspend self-protection · Lightning scheme · million TPS architecture

Part 1: Core Philosophy
1.1 Five First Principles
The work done is correct: seed determined → logits determined → sampling determined (ChaCha20 + shared seed) → text determined → hash determined. Full-chain determinism. Cheating is structurally impossible to succeed.
Cannot stop: No single point of failure affects the service. Even if all inference goes down, the chain only handles settlement.
Anyone can join: Permissionless entry. Having a GPU + staking FAI qualifies you as a Worker.
Those who cannot survive will die: Market-driven pricing. The protocol does not intervene.
The chain is a bank, not an exchange: The chain only handles deposits, settlements, and staking. The entire inference process is off-chain.
1.2 Relationship and Differences with Bitcoin
FunAI draws on Bitcoin's economic security philosophy: making cheating unprofitable, rather than preventing cheating.

But there are three fundamental differences that require additional mechanisms:

Distribution model vs competition model: Bitcoin miners not mining doesn't affect others. FunAI Workers accepting orders but not working affects users. → Jail mechanism needed.
Probabilistic verification vs deterministic verification: Bitcoin transactions are either valid or invalid. FunAI logits are considered correct within epsilon. → False positive rate control needed (P99.9).
Quality issues vs binary judgment: Bitcoin has no "poor quality" transactions. FunAI has "7B pretending to be 70B" quality fraud. → Random second verificationing needed.
Part 2: Architecture Overview
2.1 Three-Layer Architecture
Layer 1: Cosmos Chain (Bank)
Deposits, withdrawals, settlement, bookkeeping, block production. Low frequency. A few transactions per second. Uses CometBFT consensus + Cosmos SDK modules.

Layer 2: libp2p Network (Trading Floor)
Dispatching, order acceptance, inference, verification, signature exchange. High frequency. Hundreds of thousands of messages per second. Fully P2P. Does not go through the chain. Subscribes by model_id topic for precise delivery.

Layer 3: SDK (User Interface)
Model selection, price suggestions, timeout suggestions, streaming display, automatic retry. Pure client-side. The chain knows nothing about it.

2.2 What Happens On-Chain (Low Frequency)
MsgDeposit — User deposit
MsgWithdraw — User/Worker withdrawal
MsgRegisterWorker — Worker registration
MsgModelProposal — Model proposal
MsgBatchSettlement — Batch settlement (core, constructed by Proposer, only packages tasks in CLEARED state) → instant distribution
MsgUnjail — Unjail a jailed node
MsgFraudProof — User reports fake content
MsgSecondVerificationResult — Random second verification/third-verification results
EndBlocker: second_verification_rate / third_verification_rate dynamic calculation (per epoch) + block reward distribution (99% inference + 1% verification/second verification) + Worker exit processing + committee rotation
2.3 What Happens on P2P (High Frequency)
User signs inference request → Leader
Leader computes VRF ranking → directly assigns rank #1 Worker
Worker accept → executes inference → streams P2P direct to user (typewriter effect)
Worker broadcasts InferReceipt → entire network
Worker computes VRF verifier ranking → directly sends prompt + complete output to top 3 → verifiers do teacher forcing + logits check + sampling check (when temperature > 0) → sign PASS/FAIL → P2P broadcast
Nodes receiving VerifyResult recompute VRF to verify the submitter's legitimacy → first 3 legitimate results count → Worker collects signatures → complete evidence in hand
Evidence broadcasted via P2P → Proposer picks it up when producing a block and packages it on-chain
2.4 On-Chain vs SDK vs P2P Boundaries
Decision criteria: Might it need to change in the future? Might change → SDK/P2P. Cannot change → On-chain.

On-chain (carved in stone)	P2P (real-time business)	SDK (user experience)
Balance / deposit / withdrawal	Inference request transmission	Timeout suggestion values
Batch settlement	Dispatch assignment / accept	Price suggestion values
Penalty records	Worker executes inference	Streaming return monitoring
stake / staking	Streaming results returned to user	model_name translation
model_id / epsilon	Verifier teacher forcing + signatures	GPU speed reference table
Block rewards	Worker collects verification signatures	Privacy protection (scrubbing/Tor/encryption)
Part 3: Lightning Scheme Explained
3.1 Why Not Settle Every Transaction On-Chain
When settling every transaction on-chain, each inference requires 5-6 on-chain transactions. CometBFT handles about 2000 TPS. 2000 / 5 = 400 inference TPS. That's 2500x short of a million TPS. The chain is the ceiling.

3.2 The Lightning Scheme Concept
Analogous to Bitcoin's Lightning Network: the chain only handles deposits and settlements, inference happens off-chain.

But simpler than Lightning Network: No bidirectional channels needed (user payments are one-way). No routing needed (not a multi-hop network). No penalty transactions needed (expired signatures automatically become void).

3.3 User Payment: Pre-Deposit Account
Users first deposit to their inference balance:

MsgDeposit { user: address, amount: uint128 }
On-chain account structure:

InferenceAccount {
  owner:   address
  balance: uint128     // Available balance
}
One deposit supports hundreds of inferences. Depositing is a low-frequency operation (e.g., weekly/monthly), not a TPS bottleneck.

3.4 User-Signed P2P Request Message
InferRequest (signed portion) {
  model_id:       bytes32
  prompt_hash:    bytes32        // SHA256(prompt), signature covers hash not plaintext
  fee:            uint64
  expire_block:   uint64         // Signature validity period, default 24 hours
  user_seed:      bytes32        // Required when temperature > 0, can be left empty when temperature = 0
  temperature:    uint16         // Fixed-point number, 0 = argmax, 10000 = 1.0, max 20000 = 2.0
  timestamp:      uint64         // Anti-replay
  user_pubkey:    bytes32
  user_signature: bytes64
}

The P2P message also carries the prompt plaintext (not within the signature scope).
Leader verifies SHA256(prompt) == prompt_hash upon receipt, ensuring the plaintext has not been tampered with.

task_id = hash(user_pubkey + model_id + prompt_hash + timestamp)
The same task_id can only be settled once on-chain. Anti-replay relies on task_id uniqueness (on-chain records of settled task_ids).

3.5 Overspend Protection
User requests go through P2P not on-chain, creating a time gap between requests and deductions. Overspend prevention relies on three layers:

Layer 1: Leader local tracking (soft protection)

available = on_chain_balance - local_pending_total
New request cost <= available → accept
Layer 2: Worker self-protection

Worker checks on-chain balance before accept
Balance < fee x safety_factor (e.g., 3x) → reject
Layer 3: On-chain fallback

BatchSettlement processes entry by entry → insufficient balance → that entry REFUNDED → skip
Overspending is not worthwhile: user's balance fully spent + must deposit again (on-chain transaction + gas), sustained attacks are costly.

3.6 Meaning of expire_block
expire_block is the check's validity period, not the user's wait time.

Default expire_block = current block height + 17,280 (24 hours). Within 24 hours, Worker gathers complete evidence + submits settlement → gets paid. After 24 hours, the signature becomes void → no one can deduct the user's money → automatically safe.

How long the user actually waits: controlled by SDK. If tokens are streaming out, keep waiting. If no tokens, SDK automatically retries after a few seconds (same task_id). The 24-hour expiry is a background matter. The user doesn't know.

SDK automatic tiered expire: Small tasks (<1000 tokens) 30 minutes. Medium tasks 2 hours. Large tasks 6 hours. max_expire = 24-hour on-chain hard limit.

3.7 Assignment Mode + Competition Fallback
Normal flow = assignment, one inference done by one person:

Leader computes VRF ranking → rank #1 Worker → dispatch → that person alone computes. Not multiple people computing simultaneously. Efficient. No GPU waste.

After SDK retries = automatically becomes competition:

Worker A accepted but didn't work → SDK received no tokens for 5 seconds → retries with same task_id → Leader dispatches again → Worker B receives.

Worker B's internal reasoning: Is there an InferReceipt for this task_id on P2P? No → A hasn't finished → I finish and get paid → accept. Yes → A finished → I'd work for nothing → reject.

Competition is not protocol-enforced; it's Workers' own game theory on whether to participate.

3.8 task_id Unique Settlement
task_id = hash(user_pubkey + model_id + prompt_hash + timestamp). SDK retries use the same timestamp = same task_id. Multiple Workers may all complete it. The first to settle gets paid. Later ones are rejected on-chain. Worker worked for nothing.

Effect: Workers have incentive to complete orders as fast as possible after accepting. Delay = someone else settles first = worked for nothing. No fines needed. The market naturally punishes.

3.9 Comparison with Other Off-Chain Schemes
Scheme	Settlement delay	Verification cost	TPS limit	Suitable for FunAI
OP Rollup	7 days	Requires on-chain GPU	~100k	No
ZK Rollup	Instant	Inference x1000	~1M	No (ZK-ML immature)
State Channel	Instant	Off-chain	~1M	No (one-to-many mismatch)
Plasma	~1 day	Sub-chain consensus	~100k	No (obsolete)
FunAI Lightning Scheme	~15s (90% instant)	Inference x5%	~1M	Yes
FunAI's security model = Lightning (signature is evidence, expiry is safety) + every task verified + 10% random second verification. 90% instant settlement, 10% settled after second verification. Not OP's "optimistic assumption."

Part 4: Worker Nodes
4.1 Registration Fields
Field	Type	Description
pubkey	bytes32	On-chain identity
stake	uint128	Staking amount (VRF weight, not a deposit)
endpoint	string	P2P address
gpu_model	string	GPU model
gpu_vram_gb	uint16	VRAM
gpu_count	uint8	Number of GPUs
supported_models	Vec	Installed models
operator_id	bytes32	Operator identifier (self-declared)
stake is not a deposit. It will not be slashed (except for FraudProof and 3rd jail tombstone, both of which slash 5%). stake is VRF weight = competitive priority. Like Bitcoin miners' hashrate: hashrate is not a deposit, it's a competitive resource.

4.2 Seven Parallel Duties
Role	What it does	Resources used	Who
P2P node	Forwards messages, maintains routing table, gossip	CPU/network	All registered Workers (always running)
Inference executor	GPU runs model	GPU	1 selected by ranking
Verifier	GPU runs teacher forcing + sampling check	GPU	Executing Worker computes VRF and pushes prompt + complete output to top 3; first 3 to submit results count
SecondVerifier	GPU runs teacher forcing (review)	GPU	Per-task VRF purely random selects 15-20 candidates; first 3 to submit results count
Leader	Dispatches to topic	CPU	1 per model_id, 30s rotation
Proposer	Packages transactions into blocks	CPU	CometBFT built-in rotation
Validator	Consensus signing for block production	CPU	VRF selects 100-person committee
4.3 Data Retention Obligation
All nodes participating in a task (Leader, Worker, verifiers) must retain task-related data for 7 days:

Prompt plaintext
Complete output (all tokens generated by Worker)
InferReceipt (containing Worker logits, final_seed, sampled_tokens)
VerifyResult
SecondVerifiers request data from original nodes via P2P. 5 nodes each retain data; normal second verifications complete within tens of seconds (timeout upper limit 12 hours), data is sufficient.

4.4 Delegation Inference Pool (Reserved)
Not implemented in early stages. Interface reserved. Enabled after the network matures.

Inference pools are not "computing one problem together," but "accepting orders together, computing different problems separately, stably sharing income." Same as Bitcoin mining pools.

On-chain reserved: MsgDelegate (delegate stake), MsgUndelegate (21-day unbonding). Pool total stake = sum of all delegations. VRF uses total stake. Block rewards are automatically distributed to delegators by stake proportion. Internal GPU scheduling within the pool is the pool's own business; the chain doesn't manage it.

Part 5: Model Registry
5.1 model_id and epsilon
model_id = SHA256(weight_hash || quant_config_hash || runtime_image_hash)

epsilon is empirically tested by the proposer (100 prompts x 2+ GPU types x 3 runs, take P99.9). Workers verify independently before deciding whether to install.

Hardware differences are typically below 0.01 (differs only at the 2nd decimal place). Model substitution differences are above 1.0 (differs before the decimal point). A 100x safety margin in between.

5.2 Activation and Running Thresholds
Activation: installed_stake_ratio >= 2/3 AND workers >= 4 AND operators >= 4

Running: installed_stake_ratio >= 1/10 AND installed_worker_count >= 10 → can dispatch. If either condition is not met → no dispatch, automatically recovers when conditions are met again. Rationale for worker count minimum: inference 1 + verification 3 = 4 people in rigid occupation, remaining 6 serve as second verification candidates (second verifications need 3 results, 6 people are sufficient; the ideal 15-20 candidates is a redundancy target for full load, not a hard minimum)

ACTIVE never rolls back. Whether dispatching is possible is dynamic.

Part 6: Dispatch Rules
6.1 VRF Weighted Unified Formula
All ranking selections use a unified formula:

score = hash(seed || pubkey) / stake^α

Lower score → higher rank
Scenario	α	seed	Effect
Dispatch	1.0	task_id || block_hash	Selection probability proportional to stake
Verification	0.5	task_id || result_hash	Selection probability proportional to sqrt(stake)
Second verification	0.0	task_id || post_verification_block_hash	Equal probability random
Re-second verification	0.0	task_id || post_second verification_block_hash	Equal probability random
Leader election	1.0	model_id || sub_topic_id || epoch_block_hash	1 per model_id (or sub_topic) every 30s, stake-weighted
Validator committee	1.0	epoch_block_hash	100 people selected every 10 minutes, stake-weighted
Example (A stakes 10,000 FAI, B stakes 1,000 FAI):

alpha=1.0: A's selection probability is 10x that of B
alpha=0.5: A's selection probability is sqrt(10) ≈ 3.16x that of B
alpha=0.0: A and B have equal probability
Why dispatch uses pure stake without random: Adding random would cause whales to split accounts. Pure stake means splitting yields zero benefit. Small holders' income volatility is high → solved by delegation inference pools, not by adjusting VRF weights.

Why verification adds random: Verification only takes 0.4 seconds; small GPUs can do it. Adding randomness reduces collusion probability.

Why second verification uses pure random: Pure random is the safest; whale probability of controlling second verifiers drops by 3400x.

6.2 Dispatch Process (VRF Ranking + Leader Direct Assignment)
Leader election: A Leader is selected for each model_id every 30 seconds. The one with the lowest score is the Leader. Simultaneously produces rank#2, rank#3 alternates.

Leader heartbeat broadcast: Every 100ms, takes a batch of tasks from the mempool for processing.

Dispatch steps:

1. Leader computes all Workers' score rankings for each task (alpha=1.0)
2. Skips busy_workers (Workers currently executing inference)
3. Sends dispatch request to rank #1 (non-busy) Worker via P2P
4. Worker replies accept / reject within 1 second
   - accept → added to busy_workers, sends prompt, starts timer
   - reject or 1-second timeout → falls through to next non-busy rank
   - 3 consecutive ranks all silent → task returns to mempool, retry next round
5. Worker finishes sending the last message to user SDK → busy released (no need to wait for InferReceipt broadcast or verification completion)
   Leader observes Worker's InferReceipt from P2P → confirms busy released
Verifiability: Rankings are publicly computable; any node can recompute. Leader selected a non-highest rank → can be detected but not separately punished (similar to BTC miners' selective transaction packaging, an industry convention). Leader rotates every 30 seconds; the cheating window is extremely short.

Leader failover (network-wide monitoring): All Workers subscribed to that model_id topic monitor Leader activity:

Receives dispatch → checks if sender is current Leader → processes only if yes
1.5s without seeing any Leader activity (dispatch, forwarding, etc.) → all Workers network-wide synchronously switch: accept rank#2's dispatches
rank#2 starts dispatching → takeover complete
Split brain (two Leaders dispatching simultaneously) → rankings are deterministic and identical, both compute the same rank#1 → Workers deduplicate by task_id → zero impact. 30s epoch ends and naturally converges
Leader automatic splitting: N = ceil(recent_tps / 500), N >= 1, no upper limit. sub_topic_id = hash(task_id) % N. Automatically computed each epoch. Seamless switching.

Scenario	TPS	N (sub_leader count)	Worker count
Early stage	10	1	150
Growth stage	2,000	4	6,000
Mature stage	50,000	100	300,000
Limit	1,000,000	2,000	3,000,000
Worker pre-acceptance self-assessment: Worker sees the task's model_id, max_tokens, fee, user balance. Self-evaluates: Is my GPU fast enough? Is the user's balance sufficient? Yes → accept. No → reject. The chain doesn't manage this.

Part 7: Inference Execution
7.1 Complete Timeline (500 tokens / 70B / H100)
T=0.0s    SDK sends InferRequest via P2P directly to Leader(model_id)
T=0.0s    Leader receives, verifies signature+balance, places in local mempool
T=0.1s    Leader's next batch processing round, computes ranking → rank #1 = Worker A
T=0.1s    Leader dispatches to Worker A via P2P
T=0.3s    Worker A accept (~200ms round trip)
T=0.3s    Leader sends prompt to Worker A via P2P
T=0.6s    User sees first token (streaming P2P direct transmission)
T=8.3s    Worker A inference complete, P2P broadcasts InferReceipt (excludes prompt and complete output)
T=8.3s    Worker A locally computes VRF verifier ranking → sends prompt + complete output via P2P directly to top 3
T=8.4s    3 candidates receive prompt + complete output → locally compute VRF to confirm selection → run teacher forcing
T=9.0s    3 verifiers complete teacher forcing (~0.6s, one forward pass with prompt + complete output), P2P broadcast VerifyResult
T=8.9s    3/3 PASS → VERIFIED
T=13.7s   Next block arrives → block_hash determined
          Proposer computes: VRF(task_id + block_hash) < second_verification_rate?
          ├── NO (90%) → CLEARED → enters BatchSettlement → settlement and distribution (~15s to account)
          └── YES (10%) → PENDING_AUDIT → triggers second verification
T≈25s     Second verification complete (if selected) → CLEARED → settlement and distribution
User experience: ~0.6s to see the first token, ~8.3s inference complete. 90% of tasks ~15s instant settlement. 10% second verificationed ~25s settlement. All asynchronous in background; user doesn't notice.

7.2 What If a Worker Accepts but Doesn't Work
No need for on-chain timeout judgment. SDK retry + someone else taking the order resolves it within seconds.

T=0s     User sends signed request
T=0.2s   Leader dispatches → Worker A accepts
T=0.2s+  Worker A doesn't work
T=5.2s   SDK received no tokens for 5 seconds → automatic retry (same task_id)
T=5.3s   Leader dispatches again → Worker B
T=5.5s   Worker B accept → checks P2P for no InferReceipt → worth doing → starts execution
T=13.5s  Worker B completes → gathers verification signatures → VERIFIED → 90% probability instant CLEARED → B gets paid
Worker A eventually finishes → submits settlement → on-chain: task_id already settled → rejected → A worked for nothing.

7.3 InferReceipt Format
InferReceipt {
  task_id:          bytes32
  worker_logits:    [float32; 5]   // Logits at 5 output positions specified by VRF
  result_hash:      bytes32        // SHA256(complete output)

  // Following fields are only populated when temperature > 0; all zeros when temperature = 0
  final_seed:       bytes32        // SHA256(user_seed || dispatch_block_hash || task_id)
  sampled_tokens:   [uint32; 5]    // Token IDs sampled at the 5 positions corresponding to worker_logits

  worker_sig:       bytes64        // Worker's signature over all above content
}
The 5 positions of sampled_tokens are the same as worker_logits, determined by VRF position selection rules: Hash(task_id + result_hash) determines the 5 logits sampling positions. Workers cannot choose positions favorable to themselves.

New field size: 32 + 20 = 52 bytes. InferReceipt total size increases by approximately 25%. P2P bandwidth impact is negligible.

Part 8: Deterministic Evidence Chain
This is the security core of the entire protocol. Cheating is not "punished after success" but "structurally impossible to succeed."

seed determined: final_seed = SHA256(user_seed || dispatch_block_hash || task_id)
  ↓
logits determined: same input + same model → same logits (within epsilon)
  ↓
text determined: logits + seed + protocol-level sampling algorithm → deterministic pseudorandom sampling → same text
  ↓
hash determined: hash(text) = result_hash
8.1 Two Phases of Inference
Phase A: Forward Pass (GPU, heavy work)
  Input: prompt + model_weights
  Output: logits (raw score vector for each candidate token)
  Verification method: V5.2's existing logits 4/5 matching rule
  Relationship with temperature: None

Phase B: Sampling (CPU, light work)
  Input: logits + temperature + seed
  Output: token_id
  Verification method: Deterministic sampling check defined in this section
  Relationship with temperature: temperature determines how concentrated the probability distribution is
When temperature = 0, Phase B degenerates to argmax (selects the token corresponding to the maximum logit), the result is deterministic, no additional check needed. V5.2's existing mechanism fully covers this case.

When temperature > 0, Phase B requires random sampling. The core work = turning this random process into a deterministic reproducible process.

8.2 Deterministic Seed
final_seed = SHA256(user_seed || dispatch_block_hash || task_id)
user_seed: Provided by the user in InferRequest (covered by signature, Worker cannot modify)
dispatch_block_hash: The block_hash of the block containing the dispatch VRF (publicly available on-chain, queryable by all nodes; dispatch_vrf_seed already includes block_hash, reusing the same block here)
task_id: Unique task identifier (hash(user_pubkey + model_id + prompt_hash + timestamp))
The three inputs come from the user, the chain, and the task itself respectively. No single party can control final_seed:

The user doesn't know dispatch_block_hash (the block may not have been produced when the request was sent) → the user cannot preselect a seed to manipulate output
The Worker doesn't know user_seed (until the request is received) → the Worker cannot prepare forged results in advance
Anyone can independently verify the correctness of final_seed after the fact (dispatch_block_hash is queryable on-chain)
When temperature = 0, user_seed can be left empty and final_seed does not participate in computation.

8.3 Protocol-Level Sampling Algorithm
Uses ChaCha20 stream cipher (RFC 8439) as the pseudorandom number generator (PRNG), fixed.

Rationale: Mature cross-language implementations (Go, Rust, C, Python all have standard library or well-known third-party library implementations), bit-for-bit consistent output, no platform dependency, performance far exceeds requirements.

PRNG initialization:

key     = final_seed[0:32]             // 32 bytes
nonce   = uint64_le(token_position)    // 8 bytes, little-endian, zero-padded to 12 bytes
counter = 0
Each token position independently initializes a ChaCha20 instance. Does not depend on the state of the previous position. Can be parallelized.

Generating random numbers:

Take the first 8 bytes of ChaCha20 output stream → interpret as uint64 little-endian → convert to float64 → divide by 2^64 → yields uniform distribution in [0, 1)
Sampling pipeline (for each token position, order cannot be swapped, float32 precision throughout):

Input: raw_logits (float32 array, length = vocab_size), temperature (uint16 fixed-point), final_seed, position
Precision: All intermediate computations use float32. float64 must not be used. Cross-implementation consistency depends on this constraint.

Step 1: Temperature Scaling
  If temperature == 0 → argmax(raw_logits) → return directly, skip subsequent steps
  temp_float = (float32)(temperature) / (float32)(10000.0)
  scaled_logits[i] = raw_logits[i] / temp_float           // float32 division

Step 2: Softmax
  max_logit = max(scaled_logits)
  exp_logits[i] = expf(scaled_logits[i] - max_logit)      // float32 exp (expf, not exp)
  sum_exp = sum(exp_logits)                                // float32 accumulation, token_id 0 to vocab_size-1 order
  probs[i] = exp_logits[i] / sum_exp

Step 3: Sampling
  u = ChaCha20_random(final_seed, position)                // See above, returns float64 but only used for comparison
  cumsum = (float32)(0.0)
  for i in 0..vocab_size:
    cumsum += probs[i]                                     // float32 accumulation
    if (float64)(cumsum) > u:                              // Promoted to float64 for comparison, avoiding precision loss
      return i                                             // Selected token_id = i
  return vocab_size - 1                                    // Floating-point fallback
V1 limitation: Only supports temperature as a sampling parameter (uint16 fixed-point, 10000 = 1.0, range 0-20000). Does not support top-p, top-k, repetition_penalty, frequency_penalty, presence_penalty, logit_bias. Each additional sampling parameter requires the verifier's sampling pipeline to precisely reproduce that parameter's processing logic; more parameters increase the risk of cross-implementation inconsistency. Subsequent versions will add them one by one.

stop_tokens: Defined by the model corresponding to model_id, not user-configurable. All nodes read the same stop_tokens list from the model configuration.

8.4 Verifier and SecondVerifier Sampling Check
After verifiers and second verifiers complete the logits comparison (V5.2's existing step), if temperature > 0, they additionally perform sampling verification:

Verify final_seed: expected_seed = SHA256(user_seed || dispatch_block_hash || task_id), mismatch → FAIL (seed forgery)
For the 5 VRF positions, re-execute deterministic sampling using the verifier's own logits
Compare the verifier's sampling results with the Worker's reported sampled_tokens
Combine logits matching + sampling matching to determine the final result (see Section 9.2)
Time cost: seed verification = 1 SHA256 (microsecond level) + ChaCha20 initialization and sampling for 5 positions (approximately 10 microseconds each) = total < 0.1 milliseconds. Total verification time changes from ~600ms (teacher forcing) to ~600.1ms. 0.017% increase. Imperceptible.

8.5 Compatibility with temperature = 0
When temperature = 0:

user_seed can be left empty in InferRequest, temperature = 0
final_seed and sampled_tokens are all zeros in InferReceipt
Verifiers skip sampling check and only perform V5.2's existing logits check
All behavior is completely identical to V5.2 without this section's content
Nodes running older software (that don't recognize the temperature field) default temperature to 0; behavior is unchanged. All-zero new fields don't affect signature verification. No hard fork needed.

8.6 Cheating Types and Detection
Small model substitution (7B pretending to be 70B): Logits difference far exceeds epsilon (typically > 1.0, epsilon typically < 0.01). V5.2 logits verification directly FAILs. Unrelated to temperature.

Cache replay (returning old results): When temperature = 0, if the prompt is completely identical, the cached result is the correct result -- not cheating. When temperature > 0, each request's final_seed is different (due to different user_seed/dispatch_block_hash/timestamp), so cached sampled_tokens are based on the old seed → sampling check FAILs.

Sampling-phase tampering (logits correct but deliberately selecting wrong token): This is the cheating type this section specifically addresses. The Worker computed correct logits but deliberately selected a different token (inserting ads, replacing content). V5.2 logits verification would PASS (logits are correct), but sampling check would FAIL (Worker's claimed sampled_tokens are inconsistent with deterministic recomputation results).

Detection rate depends on whether the cheated positions are selected by VRF. 5 sampling positions / total generated tokens = sampling coverage rate. Tampering with 1 token in 500-token output → single detection probability = 5/500 = 1%. With 3 verifiers independently verifying, probability of at least one detecting ≈ 3%. Combined with 15% second verification rate, final evasion probability ≈ 97% x 85% ≈ 82%.

However: The Worker doesn't know which 5 positions will be checked. Large-scale tampering (replacing entire passages) causes detection rate to increase sharply. Changing 1 token has minimal impact and isn't worth risking jail. Supplementary defense: V5.2's FraudProof mechanism remains effective -- user SDK compares result_hash with actually received content; upon discovering tampering, reports on-chain → slash 5% + tombstone. Sampling check is the real-time defense; FraudProof is the after-the-fact safety net; the two layers complement each other.

Hash verification is delegated to the user SDK: After the Worker finishes streaming tokens, the last message carries a complete content signature. The SDK performs post-hoc comparison. Mismatch → FraudProof.

Part 9: Verification Mechanism
9.1 How Verifiers Are Selected
Worker pushes, does not go through Leader. After the executing Worker completes inference, it locally computes verifier rankings:

score = hash(task_id || result_hash || pubkey) / stake^0.5    // α=0.5
Excludes the executing Worker itself. Candidate pool: only Workers whose supported_models include this model_id.

After the executing Worker computes the ranking, it proactively sends prompt + complete output via P2P directly to the top 3 ranked. Upon receiving, candidates locally compute VRF to confirm they are indeed selected → run teacher forcing (prompt + complete output in one forward pass to get logits at all output positions) → logits check + sampling check (when temperature > 0) → broadcast VerifyResult. The first 3 to submit results receive verification fees.

Symmetric with dispatch logic: Leader computes ranking and pushes the task to rank#1 Worker; Worker computes ranking and pushes prompt to top 3 verifiers. Network-wide verifiable (VRF formula is public).

Input includes result_hash → Worker can only compute who the verifiers are after completing the inference result → bribery window is extremely short. Does not depend on Leader; verification doesn't stop if Leader goes down.

Worker deliberately doesn't send prompt + complete output → no one verifies → won't be VERIFIED → no settlement → Worker worked for nothing. Self-destructive behavior; no punishment needed.

9.2 Verification Results and Judgment Rules
When temperature = 0 (V5.2 original rules, unchanged):

Each verifier compares the 5 VRF-designated logits positions. Each position difference < epsilon counts as a match. At least 4 out of 5 matching is PASS. Fewer than 4 is FAIL.

Logits matches	Judgment
5/5	PASS
4/5	PASS (allows 1 GPU floating-point jitter)
3/5 or fewer	FAIL
4/5 vs 5/5 reduces false positives by 500x. False negative rate unchanged (a cheater's 5 positions all mismatch; it's impossible for 4 to match).

When temperature > 0 (new sampling check):

Each position has two checks: logits match + sampling match. A total of 10 checkpoints (5 logits + 5 sampling).

Sampling match judgment rule (for each position p):

Verifier uses their own logits + final_seed to execute deterministic sampling → gets my_token
Compare with Worker's reported sampled_tokens[p]
my_token == worker_token → sampling match
my_token != worker_token → check the logits difference d at that position:
d < epsilon/2 → logits are nearly identical yet sampling differs = cheating suspicion → sampling mismatch
d >= epsilon/2 → logits themselves differ causing sampling divergence (floating-point boundary effect) → sampling exemption (counted as neither match nor mismatch)
Combined judgment:

Logits match	Sampling match + exemption	Judgment
4+/5	4+/5	PASS
4+/5	3/5	PASS (total mismatch = 2, tolerated)
4+/5	2/5 or fewer	FAIL (sampling cheating)
3/5 or fewer	Any	FAIL (logits fail, same as V5.2)
Total mismatch = 10 - (logits match count + sampling match count + sampling exemption count). Mismatch ≤ 2 → maintain PASS. Mismatch ≥ 3 → FAIL.

3/3 PASS → VERIFIED (SUCCESS). Any FAIL → VERIFIED (FAIL). Next block's VRF determines whether to second verification.

9.3 Inference Fee Distribution
SUCCESS task (user charged 100% fee):

Role	Proportion
Worker	95.0%
Verifier 1	1.5%
Verifier 2	1.5%
Verifier 3	1.5%
Second verification fund	0.5%
Total	100.0%
FAIL task (user only charged 5% fee): Worker gets nothing; verifiers each 1.5% + multi-verification fund 0.5% = 5%.

Second verification fund is settled each epoch, no accumulation or deficit. Second verification pool total = all settled transactions x original fee x 0.5% (both SUCCESS and FAIL tasks contribute 0.5%; for FAIL, this 0.5% comes from the 5% charged to user). Computed once per epoch in EndBlocker: second verification person-times = second verificationed transactions x 3 people + third-verificationed transactions x 3 people. Per person-time fee = second verification pool total / second verification person-times.

Verifiers get paid whether PASS or FAIL. Work done = pay received. Earning the same regardless means no incentive to lie.

9.4 Complete Verification Flow
1. Worker broadcasts InferReceipt to P2P (containing worker_logits, result_hash, final_seed, sampled_tokens, worker_sig)
2. Worker locally computes verifier ranking (alpha=0.5), proactively sends prompt + complete output via P2P directly to top 3 ranked
3. Candidates receive prompt + complete output → locally compute VRF to confirm selection
   → run teacher forcing (~0.6 seconds, one forward pass with prompt + complete output → gets logits at all output positions)
   → compare VRF-designated 5 positions → logits judgment (4/5 rule)
4. If temperature > 0 → additionally execute sampling check (< 0.1ms):
   a. Pre-consistency check:
      - SHA256(complete output) == InferReceipt.result_hash, mismatch → FAIL (output inconsistent with hash)
      - For 5 VRF positions p, sampled_tokens[p] == complete_output[positions[p]], mismatch → FAIL (Worker self-contradictory)
   b. Verify final_seed = SHA256(user_seed || dispatch_block_hash || task_id), mismatch → FAIL (seed forgery)
   c. For 5 VRF positions, execute deterministic sampling using verifier's own logits + final_seed (Section 8.3)
   d. Compare verifier's sampling results with InferReceipt.sampled_tokens → combined logits + sampling judgment (Section 9.2)
5. Sign and broadcast to P2P regardless of PASS or FAIL
6. Nodes receiving VerifyResult recompute VRF ranking, verify that submitter's pubkey is indeed in the candidate ranking
   → Discard results from illegitimate verifiers (prevents Worker from pushing to colluding fake verifiers)
7. First 3 legitimate results submitted → verification ends → status = VERIFIED (SUCCESS or FAIL)
8. Next block arrives → nodes compute VRF(task_id + block_hash) < second_verification_rate?
   - NO → CLEARED → complete evidence broadcast via P2P → Proposer packages into BatchSettlement
   - YES → PENDING_AUDIT → triggers second verification → after second verification completion check third-verification VRF → 99% CLEARED / 1% PENDING_REAUDIT
9. Rank 4-8 candidates also locally compute VRF knowing they are alternates → within 2 seconds if P2P doesn't have 3 VerifyResults
   → proactively request prompt + complete output from Worker → verify → submit (first 3 legitimate results get paid)
10. Still not enough → wait 30 seconds → Worker recomputes ranking with new block_height → pushes new batch of top 3. Unlimited retries.
9.5 Verifier Game Theory
Choice	Income	Risk
Actually compute	0.015 FAI	Zero
Lazy-copy Worker and say PASS	0.015 FAI	Jointly jailed during second verification
Earning the same. But being lazy carries risk. Rational actors choose to actually compute.

9.6 What If No One Verifies
Worker pushes prompt + complete output to top 3. If someone doesn't respond → rank 4-8 candidates see that P2P doesn't have enough 3 VerifyResults → after 2 seconds proactively request prompt + complete output from Worker → verify → submit. Still not enough → wait 30 seconds → Worker recomputes ranking with new block_height → pushes new batch of top 3. Unlimited retries. Only limit: expire_block (24 hours), more than enough.

The entire process does not depend on Leader. Worker is the sole distributor of the verification prompt (Worker has the prompt plaintext and is motivated to advance verification to get settlement).

Part 10: Settlement Mechanism
10.1 Design Principle: No Settlement Before Second verification
Tasks selected for second verification are not settled until second verification is complete (cheaters can't get money). Tasks not selected are settled instantly (90% of tasks arrive in ~15s).

Why not escrow everything for 24h: The vast majority of tasks are normal; no need to freeze funds. Per-task VRF second verification can only determine whether to second verification in the block after verification completes; Workers don't know beforehand and cannot exploit this.

10.2 Complete Evidence in Worker's Hands
User-signed request (proves user authorized payment)
Worker-signed InferReceipt (proves Worker did the work)
3 verifier-signed VerifyResults (proves results are correct or incorrect)
= 4+ signatures = complete payment voucher

10.3 Settlement State Transitions
State	Meaning
VERIFIED	Verification complete (3/3 PASS or with FAIL), waiting for next block to determine whether to second verification
CLEARED	Not second verificationed OR second verification/third-verification passed → can settle and distribute
PENDING_AUDIT	Selected for second verification → waiting for second verification results → no settlement
PENDING_REAUDIT	Selected for third-verification after second verification → waiting for third-verification results → no settlement
FAILED	Second verification/third-verification failed → no settlement → jail
VERIFIED (verification complete, carrying original result: SUCCESS or FAIL)
    │
    ▼ Next block arrives → VRF(task_id + block_hash) < second_verification_rate?
    │
    ├── NO (90%) → CLEARED → enters BatchSettlement → settled per original result (~15s)
    │                         ├── Original SUCCESS → user charged 100%, Worker 95% + verifiers 4.5% + second verification pool 0.5%
    │                         └── Original FAIL → user charged 5%, verifiers 4.5% + second verification pool 0.5%, Worker jail
    │
    └── YES (10%) → PENDING_AUDIT → triggers second verification
                        │
                        ▼ Second verification complete → VRF(task_id + post_second verification_block_hash) < third_verification_rate?
                        │
                        ├── NO (99%) → Second verification result takes effect
                        │     ├── Confirmed (second verification consistent with original) → CLEARED → settled per original result
                        │     └── Overturned (second verification inconsistent with original)
                        │           ├── SUCCESS→FAIL → FAILED → no settlement → Worker jail + verifier jail
                        │           └── FAIL→SUCCESS → CLEARED → settled as success → original FAIL verifiers jail
                        │
                        └── YES (1%) → PENDING_REAUDIT → triggers third-verification
                              │
                              ├── Re-second verification confirms → second verification result takes effect (same as above)
                              └── Re-second verification overturns → original second verifiers jail
                                    ├── Second verification PASS overturned → FAILED → no settlement
                                    └── Second verification FAIL overturned → CLEARED → settled per original verification result
90% of tasks arrive in ~15s. ~9.9% arrive ~25s after second verification. ~0.1% arrive ~40s after third-verification. Workers get paid instantly the vast majority of the time.

10.4 MsgBatchSettlement
BatchSettlement is a Cosmos SDK Msg containing complete task details:

MsgBatchSettlement {
  batch_id:      uint64
  proposer:      bytes32
  merkle_root:   bytes32        // Merkle root of all entries
  entries:       [SettlementEntry; N]   // Complete details
  proposer_sig:  bytes64
}

SettlementEntry ≈ 200 bytes {
  task_id:          bytes32
  user_pubkey:      bytes32
  worker_pubkey:    bytes32
  verifiers:        [bytes32; 3]
  fee:              uint128
  status:           uint8          // SUCCESS or FAIL
  user_sig_hash:    bytes32        // User signature digest
  worker_sig_hash:  bytes32        // Worker signature digest
  verify_sig_hashes:[bytes32; 3]   // 3 verifier signature digests
}

Each batch contains 1,000-10,000 tasks (Proposer's choice)
1,000 entries ≈ 200KB
Only tasks in CLEARED state are packaged by the Proposer into BatchSettlement. PENDING_AUDIT / PENDING_REAUDIT tasks wait until second verification/third-verification completes and they become CLEARED before being packaged. After on-chain processing, only the merkle_root is persisted. Does not depend on an external DA layer.

10.5 On-Chain Settlement Logic
When BatchSettlement goes on-chain, each entry is processed:

Batch-level verification:

1. Proposer signature valid ✓
2. merkle_root consistent with entries ✓
3. result_count consistent with actual entry count ✓
If not passed → entire batch rejected
Per-entry processing:

Skip conditions (skip if any are met):
  · task_id already settled (prevent duplicates)
  · task_id marked as FRAUD
  · Signature expired (expire_block < current_block)
  · Insufficient user balance
  · Signature verification failed

Passed → instant distribution (only CLEARED tasks are packaged here)
SUCCESS task distribution:

user.balance -= fee
Worker       += 95%
Verifiers each += 1.5%
Second verification fund   += 0.5%
FAIL task distribution:

user.balance -= fee × 5% (only deduct verification service fee)
Verifiers each += 1.5% (work done = pay received)
Second verification fund   += 0.5%
Worker gets nothing + jail_count += 1
10.6 Second verification Overturn Handling
Second verification overturns only occur on PENDING_AUDIT tasks (these tasks were not settled before second verification; there is no "clawback" issue):

Second verification overturn SUCCESS→FAIL (Worker cheated + verifiers were lazy):

No settlement (money hasn't been paid out)
Worker.jail_count += 1
Original PASS verifiers each jail_count += 1
Second verification overturn FAIL→SUCCESS (verifiers maliciously framed Worker):

Distribute as success: Worker += 95%, verifiers each += 1.5%, multi-verification fund += 0.5%
Original FAIL verifiers each jail_count += 1
Worker unaffected
10.7 Re-Second verification Overturn Handling
Re-second verification overturns only occur on PENDING_REAUDIT tasks (second verification complete but not settled; there is no "clawback" issue):

Re-second verification overturn second verification PASS→FAIL (second verifiers lazily passed problematic work):

No settlement (money hasn't been paid out)
Worker.jail_count += 1
Original PASS verifiers each jail_count += 1
Original PASS second verifiers each jail_count += 1
Re-second verification overturn second verification FAIL→PASS (second verifiers maliciously overturned correct results):

Settle per original verification result (Worker innocent → settle as success; Worker actually FAIL → settle as failure)
Original FAIL second verifiers each jail_count += 1
Re-second verification confirms second verification result (no overturn):

Second verification PASS + third-verification PASS → CLEARED → normal settlement
Second verification FAIL + third-verification FAIL → FAILED → no settlement → Worker jail (+ verifier jail if applicable)
Note: CLEARED instantly-settled tasks will not be overturned by second verification/third-verification. If a Worker sent fake content to a user, the user SDK submits a FraudProof to handle it (Section 12.4), which requires clawing back already-settled funds.

10.8 User Experience
SDK handles silently. User sends request, receives streaming results; all background settlement/second verification is imperceptible.

The only case where the user is notified: FraudProof (Worker sent completely different fake content) → SDK pops up "Anomaly detected, automatically reported."

10.9 On-Chain task_id State Cleanup
task_id deduplication records are deleted on-chain 1000 blocks after settlement completion. Signatures have expired + already settled; won't grow indefinitely.

Part 11: (Deleted)
The DisputeProposer forced settlement mechanism has been deleted. CometBFT rotates Proposers every 5 seconds; within 24 hours there are 17,280 different Proposers taking turns. It's impossible for a single CLEARED task to be skipped by all Proposers. No dedicated dispute mechanism is needed.

Part 12: Penalty System
12.1 Two-Layer Security Model
Layer	Corresponds to	Cheating cost	Penalty needed?
Inference layer	PoW-like (GPU electricity)	Real physical cost	No. Wasted electricity is the punishment.
Consensus layer	Pure PoS (signatures)	Nearly zero	Yes. CometBFT slashing.
12.2 Penalty Trigger Sources
Five trigger sources, all using the same jail progression:

Source 1 (Second verification overturn SUCCESS→FAIL): Worker actually cheated but verifiers let it pass → Worker jail + original PASS verifiers jail
Source 2 (Second verification overturn FAIL→SUCCESS): Worker had no issues but verifiers misjudged/maliciously judged FAIL → original FAIL verifiers jail
Source 3 (Original verification FAIL + settlement confirmation): Worker actually cheated → Worker jail (executed at FAIL settlement)
Source 4 (Re-second verification overturn second verification PASS→FAIL): SecondVerifiers lazily passed problematic work → original PASS second verifiers jail + Worker jail + original PASS verifiers jail
Source 5 (Re-second verification overturn second verification FAIL→PASS): SecondVerifiers maliciously overturned correct results → original FAIL second verifiers jail
Division of labor:

Layer	Catches what	How triggered
Verification	Worker cheating (real-time)	Verification FAIL → jail after CLEARED settlement
Second verification	Worker cheating + verifier laziness (all PASS slipped through)	Second verification overturn → Worker jail + verifier jail
Second verification	Verifier malice (framing Worker)	Second verification overturn FAIL→SUCCESS → verifier jail
Re-second verification	SecondVerifier laziness (let problems pass)	Re-second verification overturn PASS→FAIL → second verifier jail + Worker jail + verifier jail
Re-second verification	SecondVerifier malice (overturned correct results)	Re-second verification overturn FAIL→PASS → second verifier jail
User SDK	Worker sent fake content	MsgFraudProof → slash 5% + tombstone
12.3 Jail Mechanism (Cosmos Style)
jail = lockup. No fines. No stake deduction. But stake is frozen and cannot be unbonded.

During jail: Cannot accept inference orders, cannot do verification, cannot participate in consensus, cannot unbond stake.

Count	Lockup duration	Consequence
1st	10 minutes (120 blocks)	Wait 10 minutes → MsgUnjail → resume
2nd	1 hour (720 blocks)	Wait 1 hour → MsgUnjail → resume
3rd	Permanent	slash 5% + tombstone permanent ban
50 consecutive successful completions → jail_count resets to zero. The definition of "1 task" varies by role: Worker = 1 inference, verifier = 1 verification, second verifier = 1 second verification, Leader = 1 dispatch epoch, Proposer = 1 block production. All roles share the same counter and the same progression mechanism.

Worker, Leader, Proposer, verifier, second verifier all share the same jail progression mechanism.

12.4 FraudProof
The only slash scenario. Worker used the correct model and computed the correct result, verification passed, but sent different content to the user.

User SDK computes hash after receiving all tokens. Mismatch → MsgFraudProof on-chain.

Two timing scenarios:

FraudProof arrives on-chain first → task_id marked FRAUD → BatchSettlement checks this flag → skips → user's money not deducted.
BatchSettlement arrives on-chain first (Proposer packaged quickly) → already settled → FraudProof arrives later → claw back Worker's 95% and refund user + Worker slash.
Regardless of timing, the Worker will be slashed + tombstoned, and the user gets their money back.

Worker: slash 5% stake → compensate user → tombstone permanent ban.

Preventing user-forged FraudProof: After the Worker finishes streaming tokens, the last message carries sig(hash(complete content)). The user can't get the Worker's private key → can't forge it.

12.5 On-Chain Storage
worker → {
  jail_count:      uint8       // Cumulative jail count (resets after 50 consecutive successes)
  jailed:          bool
  jail_until:      uint64      // Earliest block height for unjail
  tombstoned:      bool
  success_streak:  uint32      // Consecutive success count
}
12.6 False Positive Analysis
Verification false positive (original FAIL but second verification overturns): Second verification overturn = correction mechanism, Worker unharmed. False-positive verifiers get jailed.

Note: Only the 10% of FAIL tasks that are second verificationed have a chance of being corrected. The 90% of FAIL tasks that are not second verificationed are settled per original result → Worker directly jailed. Therefore, false positive cost is backstopped by the 4/5 matching rule: under the 4/5 rule, the false positive rate is approximately 0.003% (when single-position epsilon error is 0.1%, the probability of >=2 positions simultaneously having errors ≈ C(5,2) x 0.001^2 ≈ 0.001%), about 30 false positive FAILs per 1 million daily inferences. Of these, 27 (90%) result in Workers bearing a first-time jail of 10 minutes; 3 (10%) are overturned by second verification with Workers unharmed.

Second verification false positive: Majority false positive = one in a million. Three consecutive times to slash = impossible.

Part 13: Random Second verification
13.1 Why It's Needed
Verifier VRF's 0.5 random reduces collusion probability but not to zero. Without second verifications → zero-risk collusion for profit. Large mining pools would absolutely do this.

Second verificationing FAIL results is also needed: without second verificationing FAIL → malicious verifiers can frame honest Workers at zero cost.

The sole reason second verifications exist = fear of verifier laziness.

Layer	Deters whom	Logic
Verification	Workers	You cheat, I FAIL you
Second verification	Verifiers	You're lazy, I catch you and jointly jail you
Re-second verification	SecondVerifiers	You're lazy, I check again
13.2 Four Core Principles
Whether to second verification is only known after each task's verification is complete (unpredictable)
Second-verification rate dynamically adjusts following network health (not fixed)
More work means more second verifications with no upper limit (automatically fair)
No settlement before second verification (cheaters can't get money)
13.3 Why Not "N Times Per Worker Per Day"
Fatal vulnerability: Worker knows it's been second verificationed for the day → remaining orders are 100% safe → cheat worry-free. Verifiers also know → be lazy worry-free. Fixed count = telling Workers when it's safe.

Per-task 10% second verification: Task 1 is 10%. Task 2 is still 10%. Task 100 is still 10%. Each task is independently random. There is no "done being second verificationed so it's safe."

13.4 Second verification Trigger Mechanism (Per-Task VRF)
After verification completes, the Proposer computes in the next block:

VRF(task_id + post_verification_block_hash) < second_verification_rate?
The block_hash at verification time doesn't exist yet. Workers can't compute it. Verifiers can't compute it. No one knows. Until the next block arrives.

NO (90%) → CLEARED → enters BatchSettlement → instant settlement
YES (10%) → PENDING_AUDIT → triggers second verification → no settlement until second verification completes
13.5 Second-Verification Rate Dynamic Adjustment
Not fixed. Automatically computed each epoch.

base_rate = 10%.

second_verification_rate = base_rate × (1 + 10 × recent_fail_ratio + 50 × recent_second verification_fail)
recent_fail_ratio = failed settlement ratio in the previous epoch. recent_second verification_fail = second verification failure ratio in the previous epoch. Floor 5%. Ceiling 30%.

When the network is normal, second_verification_rate ≈ 10%. Someone cheats → fail_ratio rises → second_verification_rate rises → more checks. Large-scale cheating → second_verification_rate rises to 30%.

Automatically fair by task count: Worker A does 100 tasks/day x 10% = 10 second verificationed. Worker B does 10,000 tasks/day x 10% = 1,000 second verificationed. More work means more second verifications. No manual tiering needed.

13.6 SecondVerifier Process
VRF purely random (alpha=0.0) selects 15-20 second verification candidates (excluding original Worker and original verifiers).

First check P2P: already 3 second verification results for this task_id → don't second verification → save GPU
Fewer than 3 → request prompt + complete output from original nodes via P2P (Leader/Worker/verifiers all retain for 7 days) → run teacher forcing (~0.6 seconds, one forward pass with prompt + complete output)
Compare own logits with Worker logits (4/5 rule)
If temperature > 0 → additionally execute sampling check (same Section 8.4 flow as verifiers, < 0.1ms)
Broadcast to P2P regardless of PASS or FAIL
First 3 to submit results receive second verification fees. Later ones do not.
On-chain judgment (after MsgSecondVerificationResult goes on-chain):

Original result	Second verification majority	Handling
SUCCESS	Second verification PASS (>=2/3)	Confirmed → CLEARED → settlement
SUCCESS	Second verification FAIL (>=2/3)	Overturned → FAILED → no settlement → Worker jail + verifier jail
FAIL	Second verification PASS (>=2/3)	Overturned → CLEARED → settle as success → original FAIL verifiers jail
FAIL	Second verification FAIL (>=2/3)	Confirmed → CLEARED → settle as failure → Worker jail
Note: The above handling is executed after the third-verification VRF check. After second verification completion, first check VRF(task_id + post_second verification_block_hash) < third_verification_rate?; 99% no third-verification → immediately execute per the table above; 1% third-verification → PENDING_REAUDIT → wait for third-verification results before executing (third-verification can overturn second verification conclusions, see Section 14.2).

13.7 Economics
Expected profit for a Worker cheating on one task:

Conservative assumption: verifier laziness probability p = 20% (far higher than equilibrium); all 3 verifiers must be lazy to slip through: p^3 = 0.8%.

99.2% verification FAIL → jail 10 minutes → loss of 600 x 0.01025 = 6.15 FAI (daily average income per second: 886 FAI/day / 86,400 seconds = 0.01025 FAI/second; during jail most of the time the GPU would be idle anyway, cannot use GPU full-load income for calculation). 0.8% slips past verification → of which 10% are second verificationed → no settlement before second verification → caught. 0.8% x 90% = 0.72% slip through to settlement → earn 0.95 FAI.

Expected value = 0.992 x (-6.15) + 0.0072 x 0.95 = -6.10 FAI. Cheating on one task has an expected loss of 6.10 FAI.

Note: The actual equilibrium laziness rate is far below 20% (laziness expected value is negative → rational actors don't slack, see verifier analysis below). p = 20% is an extremely conservative assumption; the real slip-through rate is even lower.

Expected profit for a verifier being lazy once:

Laziness profit = saving 0.4 seconds of GPU for inference = 0.4 x 0.546 = 0.22 FAI. Laziness risk = 10% second verificationed → jointly jailed 10 minutes (first time) → loss of 600 x 0.01025 = 6.15 FAI (daily average income per second). Expected loss = 10% x 6.15 = 0.615 FAI.

Expected value = 0.22 - 0.615 = -0.40 FAI. Being lazy once has an expected loss of 0.40 FAI (using first-time jail lightest penalty + daily average income, doubly conservative). 2nd jail 1 hour → expected loss -3.47 FAI. Rational actors don't slack.

13.8 Second-Verification Rate Comparison
second_verification_base_rate	Laziness expected	On-chain overhead	CometBFT utilization	GPU proportion
5%	-0.09 FAI	1.74 tx/s	0.087%	0.007%
10%	-0.40 FAI	3.47 tx/s	0.17%	0.014%
15%	-0.70 FAI	5.2 tx/s	0.26%	0.021%
20%	-1.01 FAI	6.94 tx/s	0.35%	0.028%
10% second verification rate: first-time jail laziness expected loss 0.40 FAI. Although the absolute value is not large, laziness profit is only 0.22 FAI; expected return is always negative. And the 2nd jail of 1 hour → expected loss 3.47 FAI; progressive punishment scales rapidly. On-chain 0.17% imperceptible. GPU 0.014% imperceptible.

13.9 Slip-Through Rate at Different Laziness Rates
Slip-through = p^3 x [(1 - second_verification_rate) + second_verification_rate x p^3]

p (laziness rate)	Slip-through rate	Slips per day per 1M tasks
5%	0.0113%	113
10%	0.090%	900
20%	0.72%	7,200
50%	11.3%	113,000
Actual laziness rate estimated below 5% (laziness expected value is negative → rational actors don't slack).

13.10 On-Chain Overhead
At 100k Workers (10% second verification rate): ~300k MsgSecondVerificationResult per day = 3.47 tx/s. CometBFT utilization 0.17%. Approximately zero.

GPU overhead: ~1 second of second verification teacher forcing per GPU per day. Negligible.

13.11 Second-Verification Timeout
Initial second verification: 12-hour timeout. Re-second verification: 24-hour timeout.

Timeout handling = original verification result takes effect:

VERIFIED (SUCCESS) + second verification timeout → CLEARED, settle as SUCCESS
VERIFIED (FAIL) + second verification timeout → CLEARED, settle as FAIL
Timeout is most likely due to network issues or second verifiers not responding; Workers should not be additionally penalized for this. The original verification has 3 verifiers backing it, providing sufficient credibility as a fallback.

Re-second verification timeout follows the same principle: original second verification result takes effect.

Part 14: Re-Second verification Mechanism
14.1 Why It's Needed
Second verification is the second line of defense. Re-second verification is the third. Three layers are 128x safer than two layers, costing 1% more in second verification volume.

Layers    Cumulative people    Full laziness probability (p=20%)    Slips per day per 1M tasks
1 layer       3               p^3 = 0.8%                          8,000
2 layers      6               p^6 = 0.0064%                       64
3 layers      9               p^9 = 0.0000512%                    0.5 (1 slip every two days)
14.2 Mechanism
After second verification results come in, the next block computes:

VRF(task_id + post_second verification_block_hash) < third_verification_rate?
third_verification_rate dynamically adjusts, same principle as second verification rate:

third_verification_base_rate = 1%

third_verification_rate = third_verification_base_rate × (1 + 10 × recent_second verification_overturn_ratio + 50 × recent_third_verification_overturn)
recent_second verification_overturn_ratio = second verification overturn ratio in the previous epoch. recent_third_verification_overturn = third-verification overturn ratio in the previous epoch. Floor 0.5%. Ceiling 5%.

When the network is normal, third_verification_rate ≈ 1%. If second verifiers are massively lazy causing the overturn rate to rise → third_verification_rate automatically increases → more checks.

NO (99%) → Second verification result takes effect (PASS → CLEARED, FAIL → FAILED + jail)
YES (1%) → PENDING_REAUDIT → triggers third-verification → VRF purely random selects 15-20 new candidates → first 3 to submit results count
Re-second verifiers don't know they are third-verifiers. Same operations as second verification. Re-second verification VRF is also purely random. Also uses post-second verification block_hash. Unpredictable.

Re-second verification results can further overturn the initial second verification conclusion. Upon overturn, original second verifiers are jailed (same progression mechanism):

Original second verification result	Re-second verification majority	Handling
Second verification PASS (>=2/3)	Re-second verification FAIL (>=2/3)	Overturn → FAILED → no settlement → original PASS second verifiers jail + Worker jail + verifier jail
Second verification PASS (>=2/3)	Re-second verification PASS (>=2/3)	Confirmed → CLEARED → settlement
Second verification FAIL (>=2/3)	Re-second verification PASS (>=2/3)	Overturn → settle per original verification result → original FAIL second verifiers jail
Second verification FAIL (>=2/3)	Re-second verification FAIL (>=2/3)	Confirmed → FAILED → no settlement → Worker jail
14.3 On-Chain Overhead
Normal (1%): ~3,000 MsgSecondVerificationResult per day = 0.035 tx/s. Approximately zero.

Extreme (5%): ~15,000 per day = 0.17 tx/s. Still approximately zero.

Part 15: Token Economic Model
15.1 Basic Parameters
Parameter	Value
$FAI	Sole network token
total_supply	210B FAI
block_reward	4,000 FAI/block
block_time	5 seconds
halving	26,250,000 blocks (~4.16 years)
epoch	100 blocks (500 seconds)
15.2 Reward Distribution
Epochs with inference:

99% distributed by inference contribution: w_i = 0.8 × (fee_i / sum_fee) + 0.2 × (count_i / sum_count). No work = 0.

1% distributed by verification/second verification count. Verifiers and second verifiers split evenly by the number of verifications/second verifications within the previous epoch.

Data source = on-chain records from the previous epoch (current epoch's BatchSettlement may not be fully on-chain yet). Only counts CLEARED contributions. PENDING_AUDIT doesn't count. FAILED doesn't count. Cheating tasks that fail second verification → no settlement → don't count as contribution → don't share rewards.

Why allocate 1% to verification/second verification:

Block rewards are far larger than inference fees (approximately 69x). Without allocation, verification loses money:

Role	Per-task income	Time cost	Per-second earnings (excluding block rewards)
Inference	0.95 FAI	8 seconds	0.119 FAI/s
Verification	0.015 FAI	0.4 seconds	0.0375 FAI/s
At GPU full load, spending 0.4 seconds verifying = forgoing 0.0476 FAI in inference income, earning 0.015 FAI verification fee, net loss of 0.033 FAI. After allocating 1% of block rewards:

Role	Daily total income	Total GPU time	Per-second earnings
Inference	874 FAI	1600 seconds	0.546 FAI/s
Verification	12.25 FAI	20 seconds	0.61 FAI/s
Verification earns 12% more per second than inference. Incentivizes honest computation. The gap is not large enough to cause everyone to rush for verification while ignoring inference.

Epochs without inference: 100% distributed by consensus committee signed block count. Only the current 100-person Validator committee receives rewards.

Why not distribute by stake + online status: Without inference, the chain doesn't know who is online. The only provably alive entities are consensus nodes -- they are signing blocks.

Computed once per epoch at epoch end. Not per block.

15.3 Cold Start
Genesis block hardcodes 4 genesis Workers + VRF seed. First 3 days no stake required for registration. Day 3 stake becomes mandatory. Without inference, block rewards are 100% distributed by consensus committee signed block count.

Part 16: TPS Architecture
16.1 Why It Can Reach Millions
Inference requests don't go on-chain → inference TPS has no upper limit (P2P)
InferReceipt doesn't go on-chain → doesn't consume chain TPS
Verification results don't go on-chain → don't consume chain TPS
BatchSettlement uses merkle root compression → 1 settlement transaction covers thousands of inferences
GPU is the only bottleneck. More GPUs = more TPS.
16.2 Settlement Capacity
BatchSettlement contains complete details:
  1,000 tasks × 200 bytes/task ≈ 200KB/batch
CometBFT max_block_bytes = 22MB
Each block can hold ~110 batches × 1,000 tasks = ~110,000 tasks
1 block every 5 seconds → ~22,000 task settlements per second
Expand each batch to 10,000 tasks → ~220,000 task settlements per second
Settlement capacity is ample (actual inference TPS is GPU-limited, hitting capacity bottleneck far before settlement capacity)
16.3 Utilizing Surplus On-Chain TPS
On-chain is mostly idle. Put useful statistics there:

Worker cumulative statistics (aggregated per epoch): total_tasks, total_fee_earned, last_active_block

model_id real-time statistics (aggregated per epoch): active_workers, tps_last_epoch, avg_fee, total_tasks_24h

Part 17: System Resilience
Consensus layer: CometBFT has built-in fault tolerance. < 1/3 offline has no impact. Committee rotates every 10 minutes.

Inference layer:

Scenario	Handling
Worker offline	Dispatch automatically skips, falls through to next rank
Verifier offline	Rank 4-8 candidates fill in after 2 seconds by requesting prompt + complete output; after 30s Worker recomputes ranking and pushes new top 3
Leader offline	All Workers network-wide monitor Leader activity; 1.5s no activity → synchronously switch to accept rank#2's dispatches
Leader split brain	Rankings are deterministically identical; Workers deduplicate by task_id; 30s epoch ends and naturally converges
Proposer offline	CometBFT automatically rotates (2 blocks = 10 seconds)
Proposer omits tasks	CometBFT rotates Proposer every 5 seconds; next Proposer naturally packages them
Extreme scenario (>1/3 simultaneously offline): Consensus pauses, inference P2P continues working. Nodes come back and automatically resume block production. Backlogged settlements go on-chain in one batch.

Part 18: Security Analysis
18.1 Collusion Probability
Verifier VRF weight alpha=0.5. When a large mining pool controls 30% stake:

Probability of being selected as executor = 30%
Single verifier selection probability ≈ 15% (due to sqrt(stake) weight)
All 3 verifiers being their own people = 15%^3 = 0.34%
Executor + 3 verifiers = 30% x 0.34% = 0.1%
Plus random second verification (alpha=0.0) → collusion expected profit is negative.

18.2 Three Lines of Defense
Slip-through probability = p^(layers x people per layer), p = laziness probability

p      Three layers p^9      Daily slips (1M tasks)
10%    One in a billion      ≈ 0
20%    0.0000512%            0.5
50%    0.2%                  2,000
18.3 Per-Task Second verification Security Gain
Per-task VRF second verification additionally provides:

No settlement before second verification → cheating tasks get no money → zero revenue (safer than escrow clawback)
90% instant settlement → Worker cash flow is good (faster than 24h escrow)
No safety window → each task has independent 10% probability → no "done being second verificationed so I can cheat worry-free"
Bidirectional correction: second verification overturn SUCCESS→FAIL doesn't settle (money not paid out); overturn FAIL→SUCCESS settles as success
18.4 Industry Conventions (Not Addressed)
MEV, whale advantage, mining pool centralization, rich-get-richer, full on-chain transparency, block producer transaction censorship, governance controlled by whales, poor users squeezed out during congestion. All blockchains have these. Not specific to FunAI.

Part 19: SDK Privacy Protection
All three layers are in the SDK. Zero on-chain changes.

Layer 1: Content scrubbing (on by default). SDK automatically replaces phone numbers/ID numbers/emails with placeholders before sending. Automatically restores upon return.

Layer 2: Tor transport (off by default). User IP and Worker IP are mutually invisible. Adds 200-500ms latency.

Layer 3: Transport encryption (on by default). P2P communication between SDK and Leader, and between Leader and Worker uses TLS encryption. Prompt plaintext and complete output are visible to the following roles: Leader (forwards prompt during dispatch), executing Worker, 3 verifiers (Worker computes VRF ranking and directly sends prompt + complete output to top 3; only sends to more during timeout fallback). On-chain stores only prompt_hash and result_hash, not plaintext. Under normal circumstances, 5 roles see the original text (Leader + Worker + 3 verifiers). If second verificationed, an additional 3 second verifiers request data via P2P; if third-verificationed, another 3 third-verifiers. Worst case 11 roles (10% x 1% = 0.1% probability). Each time verifiers/second verifiers/third-verifiers are randomly selected by VRF; exposure is controllable.

Part 20: P2P Spam Prevention
P2P messages don't go on-chain = zero gas = can be spammed. Three walls of defense:

Wall 1: IP rate limiting (P2P network layer). Same IP limited to 100 messages per second. Exceeding disconnects. Built into libp2p.

Wall 2: Address rate limiting (application layer). Same user address limited to 10 messages per second. Exceeding discards without signature verification.

Wall 3: Balance check (business layer). Valid signature + sufficient balance → process. Otherwise discard. Attackers must have on-chain balance to spam = real money.

Part 21: Cosmos SDK Integration
21.1 Reused Modules
Module	Purpose
CometBFT	BFT consensus + P2P (for consensus layer)
x/auth	Account signatures
x/bank	Transfers + balance management
x/slashing	Consensus layer double-sign slashing
x/gov	policy_version upgrade voting
x/mint	Minting 4000 FAI/block
21.2 Custom Modules
Module	Purpose
x/worker	Worker registration / stake / jail / unjail / tombstone / statistics
x/modelreg	Model proposals / activation / running thresholds / suggested pricing
x/settlement	User balance management / BatchSettlement / per-task second verification / FraudProof
x/reward	Block reward distribution (two rule sets for with/without inference)
vrf/	VRF library (unified formula score = hash(seed || pubkey) / stake^alpha)
21.3 Worker vs Validator
Workers register in x/worker (no quantity limit). Validator = 100 people selected from Workers by VRF. x/worker's EndBlocker computes VRF every 120 blocks (10 minutes) → writes new Validator set. CometBFT receives new Validator set → seamless switchover.

Part 22: On-Chain Transaction Types
Transaction	Who submits	Description
MsgDeposit	User	Deposit
MsgWithdraw	User/Worker	Withdrawal
MsgRegisterWorker	Worker	Registration
MsgModelProposal	Anyone	Model proposal
MsgDeclareInstalled	Worker	Declare installed
MsgBatchSettlement	Packaged by Proposer	Batch settlement (contains complete details, only packages CLEARED tasks) → instant distribution
MsgSecondVerificationResult	Packaged by Proposer	Second verification/third-verification results (second verifiers broadcast P2P, Proposer picks up)
MsgFraudProof	User SDK	Report fake content
MsgUnjail	Jailed node	Unjail (Worker, Leader, Proposer, verifier, second verifier can all submit)
MsgDelegate	User	Delegate stake (reserved)
MsgUndelegate	User	Undelegate (reserved)
Part 23: Frozen Parameters Table
Economic Model
Parameter	Value
total_supply	210B FAI
block_reward	4,000 FAI/block
halving_period	26,250,000 blocks
reward_inference_weight	99% (with inference)
reward_verification_weight	1% (with inference, by verification/second verification count)
reward_inference_formula	80% fee + 20% count
reward_verification_formula	Split evenly by verification/second verification count
reward_data_source	On-chain records from the previous epoch
reward_cleared_only	Yes (only counts CLEARED contributions)
empty_epoch_reward	100% distributed by consensus committee signed block count
epoch_length	100 blocks (500 seconds)
User Payment
Parameter	Value
signature_expire_max	17,280 blocks (24 hours)
executor_fee_ratio	95%
verifier_fee_ratio	4.5% (3 people each 1.5%)
multi_verification_fund_ratio	0.5%
overspend_protection	Leader local tracking + Worker self-protection + on-chain REFUNDED
Worker
Parameter	Value
min_stake	10,000 FAI (adjustable by governance)
exit_wait	21 days
cold_start_free	First 3 days
data_retention	7 days (all participating nodes retain task data)
Verification
Parameter	Value
verifier_dispatch	Worker push (Worker computes VRF ranking, directly sends prompt + complete output to top 3; candidates self-fill after 2s if fewer than 3)
verifier_required	3/3 all PASS required for successful settlement
logits_sample_positions	5
logits_match_required	4/5 (at least 4 matches = PASS)
result_stop_threshold	3 results ends verification
rebroadcast_interval	30 seconds
Deterministic Sampling
Parameter	Value
temperature_type	uint16 fixed-point encoding
temperature_scale	10000 (10000 = 1.0)
temperature_max	20000 (temperature > 2.0 not accepted)
temperature_default	0 (argmax, compatible with V5.2)
prng_algorithm	ChaCha20 (RFC 8439)
prng_key_source	final_seed[0:32]
prng_nonce_source	uint64_le(token_position), zero-padded to 12 bytes
sampling_method	CDF cumulative probability method (sequential accumulation, cumsum > u selects)
sampling_verify_positions	5 (reuses logits VRF positions)
sampling_mismatch_tolerance	1 (maximum 1 mismatch allowed across 5 positions)
sampling_boundary_threshold	epsilon/2 (logits difference below this value means sampling mismatch is treated as cheating)
softmax_precision	float32
softmax_overflow_protection	Subtract maximum (log-sum-exp)
softmax_accumulation_order	token_id from 0 to vocab_size-1 sequential accumulation
total_mismatch_threshold	2 (logits + sampling total mismatch <= 2 → PASS, >= 3 → FAIL)
v1_supported_params	temperature only (subsequent versions will gradually add top-p, top-k, etc.)
Settlement
Parameter	Value
settlement_model	Per-task VRF second verification (no settlement before second verification, non-second verificationed instant settlement)
settlement_states	VERIFIED → CLEARED (90%) / PENDING_AUDIT (10%) → CLEARED / PENDING_REAUDIT (0.1%) / FAILED
success_settlement	user charged 100%: Worker 95% + verifiers 4.5% + second verification pool 0.5%
fail_settlement	user charged 5%: verifiers 4.5% + second verification pool 0.5% + Worker jail
overturn_success_to_fail	No settlement (PENDING_AUDIT money not paid) + Worker jail + original PASS verifiers jail
overturn_fail_to_success	Distribute as success (PENDING_AUDIT money not paid, directly settle as SUCCESS) + original FAIL verifiers jail
third_verification_overturn_pass_to_fail	No settlement (PENDING_REAUDIT money not paid) + original PASS second verifiers jail + Worker jail + verifiers jail
third_verification_overturn_fail_to_pass	Settle per original verification result + original FAIL second verifiers jail
fraud_action	Full refund to user + Worker slash 5% + tombstone
settlement_requires_cleared	Yes (PENDING_AUDIT / PENDING_REAUDIT not settled)
Second verification
Parameter	Value
second verification_trigger	Per-task VRF (next block after verification completion, VRF parameters see "Dispatch and Election" table)
second_verification_base_rate	10%
second_verification_rate_min	5%
second_verification_rate_max	30%
second_verification_rate_formula	base_rate × (1 + 10 × recent_fail_ratio + 50 × recent_second verification_fail)
second verification_scope	Both SUCCESS and FAIL tasks can be selected
second verification_candidates	15-20
second verification_required	3 (first 3 to submit count)
second verification_match_threshold	2/3 majority
multi_verification_fund_ratio	0.5%
multi_verification_fund_type	Settled each epoch
second_verification_timeout	12 hours
second_verification_timeout_action	Original verification result takes effect (CLEARED)
second verification_data_source	P2P request from original nodes (nodes retain for 7 days)
Re-Second verification
Parameter	Value
third_verification_trigger	Per-task VRF (next block after second verification completion, VRF parameters see "Dispatch and Election" table)
third_verification_base_rate	1%
third_verification_rate_min	0.5%
third_verification_rate_max	5%
third_verification_rate_formula	third_verification_base_rate × (1 + 10 × recent_second verification_overturn_ratio + 50 × recent_third_verification_overturn)
third_verification_candidates	15-20
third_verification_required	3
third_verification_timeout	24 hours
third_verification_timeout_action	Original second verification result takes effect
Penalty
Parameter	Value
jail_trigger_sources	Verification FAIL confirmation + second verification overturn + third-verification overturn (same counter)
jail_1_duration	120 blocks (10 minutes)
jail_2_duration	720 blocks (1 hour)
jail_3_action	slash 5% + tombstone
jail_freeze_unstake	true
success_reset	50 consecutive tasks resets to zero
fraud_proof_action	slash 5% + tombstone
jail_applies_to	Worker, Leader, Proposer, verifier, second verifier (shared)
Dispatch and Election
Parameter	Value
dispatch_vrf_alpha	1.0 (pure stake)
dispatch_vrf_seed	task_id || block_hash
verify_vrf_alpha	0.5
verify_vrf_seed	task_id || result_hash
second verification_vrf_alpha	0.0 (pure random)
second verification_vrf_seed	task_id || post_verification_block_hash
third_verification_vrf_alpha	0.0 (pure random)
third_verification_vrf_seed	task_id || post_second verification_block_hash
leader_vrf_alpha	1.0 (stake-weighted)
leader_vrf_seed	model_id || sub_topic_id || epoch_block_hash
validator_vrf_alpha	1.0 (stake-weighted)
validator_vrf_seed	epoch_block_hash
accept_timeout	1 second
max_fallback	3 (dispatch falls through at most 3 ranks)
leader_tps_threshold	500 (auto-split when exceeded)
leader_epoch	30 seconds
leader_failover	1.5 seconds (all Workers network-wide monitor Leader activity, timeout triggers synchronous switch to rank#2)
Proposer BatchSettlement Verification
Parameter	Value
batch_merkle_mismatch	Entire batch rejected + Proposer jail
batch_count_mismatch	Entire batch rejected + Proposer jail
batch_sig_invalid	Entire batch rejected
entry_sig_invalid	Single entry skipped
entry_task_duplicate	Single entry skipped
entry_expired	Single entry skipped
entry_balance_insufficient	Single entry skipped (REFUNDED)
Model Registry
Parameter	Value
model_activation	installed_stake >= 2/3 AND workers >= 4 AND operators >= 4
model_service	installed_stake >= 1/10 AND installed_worker_count >= 10
Consensus
Parameter	Value
block_time	5 seconds
committee_size	100
committee_rotation	10 minutes
consensus_threshold	70%
Part 24: Pending Confirmation
#	Issue	Status
1	BLS aggregate signature specifics (which signatures to aggregate, on-chain verification logic)	TBD
2	"Verifier laziness is a pseudo-problem" depends on the premise that laziness rate p < 30%	Risk reduced with second verification joint jail + second verification overturn; not addressing for now
Part 25: Change Log Relative to V5.1
#	Change	V5.1 Design	V5.2 Design	Rationale
1	Remove Slots	50 Slots with independent nonces	Single balance + task_id uniqueness	Simplify implementation; P2P requests don't need nonces
2	Overspend protection	Undefined	Leader tracking + Worker self-protection + on-chain REFUNDED	Slots also can't fully prevent overspend; self-protection is more practical
3	Settlement model	Instant settlement (BatchSettlement distributes directly)	Per-task VRF second verification: 90% instant settlement + 10% settled after second verification	No settlement before second verification prevents cheaters getting money; 90% instant preserves Worker cash flow
4	FAIL also second verificationed	Only second verification SUCCESS tasks	Both SUCCESS and FAIL second verificationed	Not second verificationing FAIL → malicious verifiers can frame Workers at zero cost
5	Remove DA	Details stored in DA layer	Details directly in BatchSettlement transaction body	Remove external dependency; nodes retaining 7 days is sufficient for second verification
6	Data retention	Undefined	Participating nodes retain 7 days	Second verification data source, replacing DA
7	BatchSettlement unlimited	1-2 per block	Unlimited as standard Msg	Original design's settlement TPS was only 40k
8	Second verification joint liability simplified	epsilon/20 logits distance comparison	Second verification FAIL → all original PASS verifiers jail	epsilon/20 false-positives same GPU model
9	VRF unified formula	Conceptual description without mathematical definition	score = hash(seed || pubkey) / stake^alpha	Fill in mathematical definition needed for implementation
10	Leader failover	Insufficient detail	1.5s lazy detection + network-wide Worker monitoring + split-brain task_id dedup	No on-chain resources consumed; simple and reliable
11	Leader cheating punishment	Undefined	MsgLeaderMisbehavior	Deleted (#27)
12	DisputeProposer	Undefined	MsgDisputeProposer	Deleted (#26)
13	Proposer batch verification	Undefined	merkle/count mismatch rejects entire batch + jail	Fill in handling of malicious Proposer submissions
14	Second verification timeline	Unscheduled daily	Second verification timeout 12h + third-verification timeout 24h	Second verification triggers in next block after verification; timeout as fallback
15	DA layer	TBD (Celestia/Avail/custom)	Not needed	Local node retention of 7 days as replacement
16	Block reward distribution	100% by inference contribution	99% inference + 1% verification/second verification	Without allocation, verification loses money (net loss 0.033 FAI/task); allocating 1% makes verification earn 12% more per second than inference
17	Second verification model	N times per Worker per day + 12h scheduling	Per-task VRF random second verification (base 10%, dynamic 5-30%)	Fixed count has safety window; per-task is independently random per task with no window
18	Settlement model	All instant settlement (no second verification blocking)	90% instant settlement + 10% settled after second verification (no settlement before second verification)	Per-task VRF second verification only blocks the 10% selected; 90% instant preserves cash flow
19	Second verification candidate count	8-10	15-20	Increased redundancy to ensure 3 results are gathered quickly
20	Second-verification rate adjustment	Fixed	Dynamic: base_rate × (1 + 10 × fail_ratio + 50 × second verification_fail), 5-30%	Follows network health; low overhead when normal, auto-escalates when cheating occurs
21	Settlement state machine	None	VERIFIED → CLEARED/PENDING_AUDIT → CLEARED/FAILED	Clear state transitions; no settlement before second verification
22	Reward data source	Undefined	Previous epoch's CLEARED records	Current epoch data is incomplete
23	FAIL settlement fee	user charged 100%	user charged 5% (verification service fee)	On FAIL, user only pays verifier labor fee, not Worker
24	Re-second verification overturn punishment	Undefined	Re-second verification overturn → original second verifiers jail (same progression)	Fill in "third-verification deters second verifiers" punishment implementation
25	PENDING_REAUDIT state	None	Post-second verification third-verification selection → PENDING_REAUDIT → no settlement until third-verification complete	Consistent with second verification: no settlement before third-verification, avoiding clawback
26	Delete DisputeProposer	MsgDisputeProposer forced settlement	Deleted	CometBFT rotates Proposer every 5 seconds; impossible for all to skip one task
27	Delete MsgLeaderMisbehavior	MsgLeaderMisbehavior report	Deleted	P2P accept doesn't go on-chain making evidence impossible; similar to BTC miners' selective packaging, an industry convention
28	Verifier Worker push	Leader precisely assigns top 3	Worker computes VRF and directly sends prompt + complete output to top 3; candidates fill in after 2s	Does not depend on Leader; verification doesn't stop if Leader goes down; symmetric with dispatch logic
29	P2P node role	6 duties	7 (added P2P node)	P2P node is the always-running base role
30	accept_timeout	200ms	1 second	Cross-continental RTT ~150ms; 200ms too aggressive
31	Running threshold worker count	Only looks at stake ratio	installed_worker_count >= 10	Can't gather enough people for inference+verification+second verification
32	Leader fault tolerance	Heartbeat scheme (extra overhead)	1.5s lazy detection + network-wide Worker monitoring of Leader activity	Zero extra bandwidth; network-wide synchronous switching
33	VerifyResult VRF check	Undefined	Recompute VRF upon receiving VerifyResult; discard illegitimate verifier results	Prevents Worker from pushing to colluding fake verifiers
34	State machine distinguishes confirm/overturn	Second verification FAIL uniformly → FAILED	Confirm original FAIL → CLEARED settle as failure; overturn SUCCESS→FAIL → FAILED no settlement	Original state machine oversimplified; two cases require different handling
35	MsgUnjail scope	Worker/Leader	All jailed roles can submit	Jail applies to 5 roles; Unjail should too
36	jail_count reset definition	"50 tasks" undefined	Defined by role: Worker=inference, verifier=verification, second verifier=second verification, Leader=epoch, Proposer=block production	Non-Worker roles can't use "50 inferences"
37	Laziness economics analysis correction	Used 2nd jail (1 hour)	Uses 1st jail (10 minutes), conservative estimate	Analysis should use lightest punishment to prove it's still unprofitable
38	VRF scenario completion	Only defined dispatch/verification/second verification 3 types	Added third-verification/Leader election/Validator committee, total 6 types	All VRF elections need explicit seed and alpha
39	Jail loss methodology correction	0.546 FAI/s (GPU full load)	0.01025 FAI/s (daily average income 886/86400)	GPU is only used 1600 seconds per day (1.85%); full-load calculation overestimates by 53x
40	model_activation complete conditions	Only wrote stake >= 2/3	stake >= 2/3 AND workers >= 4 AND operators >= 4	Section 5.2 has three joint conditions; Section 23 was missing the latter two
41	False positive rate precision	P99.9 rough estimate <=1000/day	4/5 rule precisely calculated ≈ 30/day (0.003%)	C(5,2) x 0.001^2 ≈ 0.001%
42	Busy state release timing	After Leader receives InferReceipt	After Worker sends last message to SDK (without waiting for InferReceipt)	Worker releases busy faster, can accept new orders sooner
43	Accept behavior	Retains accept / reject two replies	—	Worker can proactively reject (e.g., overloaded); Leader receives reject or 1-second timeout then falls through
44	Leader VRF seed adds sub_topic_id	model_id || epoch_block_hash	model_id || sub_topic_id || epoch_block_hash	When N > 1, each sub_topic needs a different Leader
45	Deterministic sampling verification	Verifiers only verify logits, not sampling	When temperature > 0, additionally verify sampling: deterministic seed + ChaCha20 PRNG + protocol-level sampling algorithm	When temperature > 0, Worker may have correct logits but deliberately select wrong token (insert ads); logits verification cannot detect this
46	InferRequest adds temperature	No temperature field	temperature: uint16 fixed-point (0=argmax, 10000=1.0)	User specifies temperature, covered by signature, Worker cannot tamper
47	InferReceipt adds final_seed + sampled_tokens	Only worker_logits + result_hash	Added final_seed(bytes32) + sampled_tokens([uint32;5])	Verifiers/second verifiers need these fields for sampling verification
48	Worker sends complete output to verifiers	Worker only sends prompt to verifiers	Worker sends prompt + complete output to verifiers	Verifiers need complete output for teacher forcing to get logits at each position
49	VRF position selection fixes circular dependency	Hash(task_id + receipt_hash) selects positions	Hash(task_id + result_hash) selects positions	receipt_hash contains sampled_tokens → sampled_tokens depends on positions → positions depend on receipt_hash → infinite loop. Changed to result_hash (determined once inference completes)
50	vrf_nonce binds to explicit block	final_seed = SHA256(user_seed || vrf_nonce || task_id), "current block" undefined	final_seed = SHA256(user_seed || dispatch_block_hash || task_id)	Different nodes might use different blocks → seed inconsistency → verification always FAILs. Bind to dispatch VRF's block_hash, queryable by all nodes
51	Sampling pre-consistency check	Verifiers directly do sampling comparison	First verify SHA256(complete output)==result_hash AND sampled_tokens[p]==complete_output[positions[p]]	Prevents Worker from submitting self-consistent but false InferReceipt (logits/sampled_tokens don't match actual output)
52	Sampling boundary threshold relaxed	epsilon/10 (logits difference below this value means sampling mismatch is treated as cheating)	epsilon/2	epsilon/10 too strict; same GPU same model expf ULP differences can cause sampling divergence to be misjudged as cheating
53	Unified teacher forcing terminology	"prefill" and "teacher forcing" used interchangeably, 0.4s time cost	Unified to "teacher forcing", time cost updated to ~0.6s	prefill only processes prompt; teacher forcing processes prompt + complete output; verifiers do the latter. Longer input takes longer
FunAI V5.2 Final — End of Document
