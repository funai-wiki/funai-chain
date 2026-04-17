# FunAI V5.2 Full-Role Integration Test Plan V3

> Based on the `FunAI_V52_Final.md` spec and code implementation design
> V3 revision: Added 20 edge/exception cases + 1 new partition (V) on top of V2 (117 cases), strengthened 4 existing cases
> Roles covered: User, Worker, Verifier, SecondVerifier, Leader, Proposer, Validator
> Modules covered: x/settlement, x/worker, x/modelreg, x/vrf, x/reward, p2p/types

---

## 1. Role-Module Mapping

| Role | On-Chain Module | Off-Chain (P2P) | Core Responsibility |
|------|---------|-----------|---------|
| User | settlement (deposit/withdraw) | InferRequest (signed request) | Initiate inference, pay fees |
| Worker | worker (registration/staking/jail) | InferReceipt (inference proof) | Execute inference, be verified/second verificationed |
| Verifier | settlement (verification count) | VerifyResult (verification result) | Teacher forcing verification |
| SecondVerifier | settlement (second verification judgment) | SecondVerificationResponse (second verification result) | Random re-examination |
| Leader | vrf (election/heartbeat) | AssignTask (dispatch) | Per-model dispatch scheduling |
| Proposer | settlement (batch packaging) | — | Package settlement transactions |
| Validator | vrf (committee) | — | Consensus signing and block production |

---

## 2. Test Partition Overview

| Partition | Domain | Case Count | Roles Covered | Corresponding Module |
|------|------|--------|---------|---------|
| A | User Lifecycle | **7** | User | x/settlement |
| B | Worker jail/streak triggered via settlement | 2 | Worker, Proposer | x/settlement → x/worker |
| C | Settlement (Proposer) Normal Flow | **8** | User, Worker, Verifier, Proposer | x/settlement |
| D | Settlement Exception Flow | **11** | User, Worker, Proposer | x/settlement |
| E | Second verification (SecondVerifier) Judgment | **9** | SecondVerifier, Worker, Verifier | x/settlement |
| F | Re-second verification Four-Quadrant Judgment | **5** | SecondVerifier, Worker, Verifier | x/settlement |
| G | Second verification/Re-second verification Timeout | **4** | SecondVerifier | x/settlement |
| H | FraudProof | **5** | User, Worker | x/settlement |
| I | Block Reward Contribution Counting | 3 | Verifier, SecondVerifier, Worker | x/settlement, x/reward |
| J | Dynamic Second-Verification Rate | **5** | — | x/settlement |
| K | Second verification Fund Distribution | 2 | SecondVerifier | x/settlement |
| L | Overspend Protection | **3** | User | x/settlement |
| M | Task Cleanup | 1 | — | x/settlement |
| N | End-to-End Full Pipeline (Settlement Module) | 4 | User, Worker, Verifier, Proposer, Validator | x/settlement |
| O | Parameter Validation | 5 | — | x/settlement |
| P | Model Registry Lifecycle | 14 | Worker, Proposer | x/modelreg |
| Q | VRF Election | **15** | Leader, Worker, Verifier, SecondVerifier, Validator | x/vrf |
| R | P2P Inference Message Structure | 8 | User, Worker, Verifier, SecondVerifier, Leader | p2p/types |
| S | Worker Full Lifecycle | **13** | Worker | x/worker |
| T | Inference End-to-End (Cross-Module Integration) | **10** | All roles | Cross-module |
| U | Multi-Node E2E (Real Nodes + CLI) | 5 | All roles | Cross-module |
| V | **Economic Conservation and Genesis Round-Trip** | **4** | — | Cross-module |
| **Total** | | **142** | | |

> V2→V3 net increase of **25 cases** (20 new + 4 from new partition V + 1 strengthened counted as new), total 117→142.

---

## 3. Detailed Test Cases

---

### A. User Lifecycle

**Test objective**: Verify InferenceAccount creation, deposit accumulation, withdrawal deduction, exception rejection, and boundary inputs.

**Spec reference**: §3.4 User Signed Request (balance locking), §12 Settlement on-chain settlement.

**Precondition**: setupKeeper() creates a clean environment.

| ID | Test Name | Description | Precondition | Test Steps | Expected Result | Spec Clause |
|------|--------|------|---------|---------|---------|-----------|
| A1 | TestUser_A1_FirstDeposit | First deposit creates account | None | ProcessDeposit(user, 50000 FAI) | 1. account created from nothing 2. balance = 50000 FAI | §12 InferenceAccount |
| A3 | TestUser_A3_Withdraw | Normal withdrawal | Deposit 10 FAI | ProcessWithdraw(3 FAI) | balance = 7 FAI | §12 |
| A4 | TestUser_A4_WithdrawExceedsBalance | Withdrawal exceeds balance | Deposit 1 FAI | ProcessWithdraw(2 FAI) | Returns error, balance unchanged | §12 fallback |
| A5 | TestUser_A5_WithdrawNoAccount | Withdrawal with no account | None | ProcessWithdraw(1 ufai) | Returns error | §12 |
| **A6** | **TestUser_A6_ZeroDeposit** | **Zero amount deposit** | None | ProcessDeposit(user, 0 ufai) | **Returns error, does not create account** | §12 zero-value boundary |
| **A7** | **TestUser_A7_WrongDenomDeposit** | **Wrong denom deposit** | None | ProcessDeposit(user, 100 stake) | **Returns error, denom must be ufai** | §12 denom validation |
| **A8** | **TestUser_A8_WithdrawExactBalance** | **Exact full balance withdrawal** | Deposit 5 FAI | ProcessWithdraw(5 FAI) | **balance = 0 (account retained but balance is zero)** | §12 boundary |

> **New A6/A7/A8**: Zero value, wrong denom, exact balance are classic boundary cases not covered by A1-A5.

---

### B. Worker jail/streak Triggered via Settlement

(Unchanged from V2, B1-B2 total 2 cases)

---

### C. Settlement (Proposer) Normal Flow

**Test objective**: Verify ProcessBatchSettlement normal fee deduction logic, fee distribution ratios, EpochStats, and BatchRecord.

**Spec reference**: §11 Fee Distribution (SUCCESS: 95% Worker + 4.5% Verifier + 0.5% MultiVerificationFund; FAIL: 5% total charge), §12 Settlement flow.

**Precondition**: setupWithDeposit(user, 100000 FAI), second_verification_rate=0.

| ID | Test Name | Description | Precondition | Test Steps | Expected Result | Spec Clause |
|------|--------|------|---------|---------|---------|-----------|
| C1 | TestSettle_C1_SuccessBatch_UserCharged100Pct | SUCCESS charges full fee | Deposit | 10 SUCCESS entries, fee=1 FAI | user balance decreases by 10 FAI | §11.1 |
| C2 | TestSettle_C2_FailBatch_UserCharged5Pct | FAIL charges only 5% fee | Deposit | 1 FAIL entry, fee=1 FAI | user balance decreases by 0.05 FAI | §11.2 FailSettlementFeeRatio=50/1000 |
| C3 | TestSettle_C3_MixedBatch | Mixed SUCCESS + FAIL | Deposit | 5 SUCCESS + 2 FAIL | balance = deposit - 5×fee - 2×(fee×5%) streak=5, jail=2 | §11.1, §11.2 |
| C4 | TestSettle_C4_EpochStats | EpochStats correctly updated | Deposit | 8 SUCCESS + 2 FAIL | TotalSettled=10, FailSettled=2 | §12 |
| C5 | TestSettle_C5_BatchRecord | BatchRecord correctly stored | Deposit | 3 SUCCESS entries | ResultCount=3, Proposer address correct | §12 |
| C6 | TestSettle_C6_FeeDistribution_Exact | SUCCESS fee exact distribution verification | Deposit | 1 SUCCESS entry, fee=100000 ufai | Worker receives 95000, Verifier×3 receive 4500 total (1500 each), MultiVerificationFund increases by 500. **Additional assertion: user_debit == executor + verifiers + multi_verification_fund** | §11.1 |
| **C7** | **TestSettle_C7_DustFee_VerifierRemainder** | **Dust distribution with tiny fee** | Deposit | 1 SUCCESS entry, fee=10 ufai, 3 verifiers | **verifier_total = 10×45/1000=0 ufai; executor = 10-0-0 = 10 ufai. Verify no panic, amount conservation holds** | §11.1 boundary |
| **C8** | **TestSettle_C8_SingleEntryBatch** | **Single-entry batch** | Deposit | 1 SUCCESS entry, batch size=1 | **Merkle tree single leaf node correct, ResultCount=1, BatchRecord correct** | §12 |

> **New C7**: Economic conservation still holds when division truncation causes verifier allocation to be 0 with tiny amounts.
> **New C8**: Single-entry batch is a merkle tree boundary (only 1 leaf).
> **Strengthened C6**: Added `user_debit == executor + verifiers + multi_verification_fund` total conservation assertion.

---

### D. Settlement Exception Flow

**Test objective**: Verify various rejection/skip scenarios in settlement, ensuring fund safety.

**Spec reference**: §12 Settlement validation rules, §10 Proposer misbehavior penalties.

| ID | Test Name | Description | Precondition | Test Steps | Expected Result | Spec Clause |
|------|--------|------|---------|---------|---------|-----------|
| D1 | TestSettle_D1_DuplicateTaskId | Duplicate task_id | Settled once first | Same task_id submitted again | ResultCount=0, skipped | §3.4 task_id uniqueness |
| D2 | TestSettle_D2_ExpiredSignature | Expired signature | Deposit | expire_block=50 < current=100 | ResultCount=0, skipped | §3.4 signature validity period |
| D3 | TestSettle_D3_FraudMarkedTask | FRAUD-marked task_id | SetFraudMark(tid) | Settle that tid | ResultCount=0, skipped | §15 FraudProof |
| D4 | TestSettle_D4_InsufficientBalance | Insufficient balance | Deposit 0.5 FAI | Task with fee=1 FAI | ResultCount=0, balance unchanged | §12 overspend protection |
| D5 | TestSettle_D5_MerkleRootMismatch | Merkle root mismatch | Deposit | Submit forged merkle root | Returns error + Proposer jail | §12 Proposer validation |
| D6 | TestSettle_D6_ResultCountMismatch | ResultCount mismatch | Deposit | msg.ResultCount=999 | Returns error + Proposer jail | §12 Proposer validation |
| D7 | TestSettle_D7_MissingSigHashes | Missing signature hashes | Deposit | 1st entry no sig, 2nd has sig | Only settles 2nd entry, ResultCount=1 | §12 entry validation |
| D8 | TestSettle_D8_UnauthorizedProposer | Unauthorized Proposer submission | Deposit | Call BatchSettle with non-Proposer address | Returns error or Proposer identity check fails | §12 |
| **D9** | **TestSettle_D9_EmptyBatch** | **Empty batch** | Deposit | entries=[], ResultCount=0 | **Returns error or ResultCount=0, does not create BatchRecord (or creates empty record with ResultCount=0)** | §12 empty input |
| **D10** | **TestSettle_D10_TamperedProposerSig** | **Tampered ProposerSig** | Deposit | Correct merkle root + random bytes replacing ProposerSig | **Signature verification fails, returns error (no jail, per P1-6)** | §12 signature verification |
| **D11** | **TestSettle_D11_AllEntriesSkipped** | **All entries skipped** | Settled once first (generating settled tasks) | Submit again with same task_ids | **BatchRecord.ResultCount=0, user balance unchanged, EpochStats not incremented** | §12 deduplication |

> **New D9**: Empty batch is an input boundary, code must correctly reject or generate empty record without panic.
> **New D10**: D5 tests merkle mismatch which triggers jail, D10 tests the independent signature tampering verification path (per P1-6 no jail, only rejection).
> **New D11**: Correctness of BatchRecord state and user balance when all entries are skipped.

---

### E. Second verification (SecondVerifier) Judgment

**Test objective**: Verify second verification result confirmation/reversal logic against original verification results, including majority decision and abnormal inputs.

**Spec reference**: §13.6 Four second verification judgment outcomes.

```
              Second verification Result
              PASS         FAIL
Original   SUCCESS   Confirm(CLEARED)   Overturn(FAILED) → Worker+PASS verifiers jail
Result     FAIL      Overturn(SETTLED)   Confirm(FAIL_SETTLED) → Worker jail
```

**Precondition**: setupWithDeposit, third_verification_rate=0, SecondVerificationPendingTask set up.

| ID | Test Name | Description | Original State | Second verification Result | Expected Result | Spec Clause |
|------|--------|------|---------|---------|---------|-----------|
| E1 | TestSecond verification_E1_SuccessSecond verificationPass | Confirm SUCCESS | SUCCESS | 3×PASS | TaskSettled, streak+1 | §13.6 Quadrant 1 |
| E2 | TestSecond verification_E2_SuccessSecond verificationFail_Overturn | Overturn SUCCESS→FAIL | SUCCESS | 3×FAIL | TaskFailed, Worker jail, PASS verifiers jail | §13.6 Quadrant 2 |
| E3 | TestSecond verification_E3_FailSecond verificationPass_Overturn | Overturn FAIL→SUCCESS | FAIL | 3×PASS | TaskSettled, FAIL verifiers jail | §13.6 Quadrant 3 |
| E4 | TestSecond verification_E4_FailSecond verificationFail_Confirm | Confirm FAIL | FAIL | 3×FAIL | TaskFailSettled, Worker jail | §13.6 Quadrant 4 |
| E5 | TestSecond verification_E5_InsufficientResults | Insufficient second verification results | SUCCESS | Only 2 submitted | Remains pending, judgment not triggered | §13.3 Requires 3 results |
| E6 | TestSecond verification_E6_SecondVerifierIsOriginalVerifier | SecondVerifier = original verifier | SUCCESS | verifier1 submits second verification | Rejected, returns error | §13.3 Exclusion rule |
| E7 | TestSecond verification_E7_MajorityDecision_2Pass1Fail | Majority decision 2:1 | SUCCESS | 2×PASS + 1×FAIL | Majority PASS → CLEARED (confirms SUCCESS) | §13.6 Majority decision |
| **E8** | **TestSecond verification_E8_ResultAfterTimeout** | **Second verification result submitted after timeout** | SUCCESS | 3×PASS submitted after SecondVerificationTimeout expires | **Rejected, pending already removed, returns ErrSecond verificationNotPending** | §13.7 No action after timeout |
| **E9** | **TestSecond verification_E9_DuplicateSecondVerifierSubmission** | **Same second verifier submits twice** | SUCCESS | second verifier1 submits PASS twice | **Second submission rejected, only counted once** | §13.3 Deduplication |

> **New E8**: G1 tests timeout cleanup, but doesn't test whether second verification results arriving after timeout are correctly rejected. This is a timing race scenario.
> **New E9**: Same second verifier submitting multiple times for the same task could cause vote counting errors or reward overpayment.

---

### F. Re-second verification Four-Quadrant Judgment

**Test objective**: Verify third-verification confirmation/reversal logic against original second verification results, and majority decision.

**Spec reference**: §14.2 Four third-verification judgment outcomes.

```
                Re-second verification Result
                PASS               FAIL
Original   Second verification PASS   Confirm→settle per original verification    Overturn→FAILED + original PASS second verifiers jail
Second verification      Second verification FAIL   Overturn→settle per original verification + original FAIL second verifiers jail    Confirm→maintain second verification judgment
```

**Precondition**: setupWithDeposit, third_verification_rate=0, SecondVerificationPendingTask with IsThird verification=true + SecondVerificationRecord set up.

| ID | Test Name | Description | Original Second verification | Re-second verification Result | Expected Result | Spec Clause |
|------|--------|------|---------|---------|---------|-----------|
| F1 | TestThird verification_F1_Second verificationPassThird verificationPass | Confirm second verification PASS | PASS | 3×PASS | TaskSettled | §14.2 Quadrant 1 |
| F2 | TestThird verification_F2_Second verificationPassThird verificationFail | Overturn second verification PASS | PASS | 3×FAIL | TaskFailed + second verifiers+Worker+verifiers jail | §14.2 Quadrant 2 |
| F3 | TestThird verification_F3_Second verificationFailThird verificationPass | Overturn second verification FAIL | FAIL | 3×PASS | TaskSettled + original FAIL second verifiers jail | §14.2 Quadrant 3 |
| F4 | TestThird verification_F4_Second verificationFailThird verificationFail | Confirm second verification FAIL | FAIL | 3×FAIL | TaskFailed + Worker jail | §14.2 Quadrant 4 |
| **F5** | **TestThird verification_F5_MajorityDecision_2Pass1Fail** | **Re-second verification majority decision 2:1** | FAIL | **2×PASS + 1×FAIL** | **Majority PASS → overturn second verification FAIL → TaskSettled + original FAIL second verifiers jail** | §14.2 Majority decision |

> **New F5**: E7 covers second verification majority decision but F1-F4 are all unanimous votes; third-verification also has majority decision logic that needs coverage.

---

### G. Second verification/Re-second verification Timeout

**Test objective**: Verify that original result takes effect after timeout, pending cleanup, and precise boundaries.

**Spec reference**: §13.7 Second verification timeout (12h/8640 blocks), §14.3 Re-second verification timeout (24h/17280 blocks).

| ID | Test Name | Description | Precondition | Test Steps | Expected Result | Spec Clause |
|------|--------|------|---------|---------|---------|-----------|
| G1 | TestTimeout_G1_SecondVerificationTimeout_OriginalSuccess | Second verification timeout (original SUCCESS) | SecondVerificationTimeout=10, submitted=1 | blockHeight=15 | Original SUCCESS takes effect, pending removed | §13.7 |
| G2 | TestTimeout_G2_SecondVerificationTimeout_OriginalFail | Second verification timeout (original FAIL) | SecondVerificationTimeout=10, submitted=1 | blockHeight=15 | Original FAIL takes effect, pending removed | §13.7 |
| G3 | TestTimeout_G3_Third verificationTimeout | Re-second verification timeout | Third verificationTimeout=20, submitted=1 | blockHeight=25 | Original second verification result takes effect, pending removed | §14.3 |
| **G4** | **TestTimeout_G4_ExactBoundary** | **Precise timeout boundary** | SecondVerificationTimeout=10, submitted=1 | **1. height=10: HandleSecondVerificationTimeouts → 0 (not timed out) 2. height=11: HandleSecondVerificationTimeouts → 1 (just timed out)** | **height=submitted+timeout is the precise boundary** | §13.7 boundary |

> **New G4**: G1 uses height=15 which is far beyond the timeout point, does not test the precise boundary at submitted+timeout.

---

### H. FraudProof

**Test objective**: Verify FraudProof behavior in both before-settlement/after-settlement timing scenarios, as well as invalid evidence and special states.

**Spec reference**: §15 FraudProof — Worker slash 5% + tombstone, user refund.

| ID | Test Name | Description | Timing | Test Steps | Expected Result | Spec Clause |
|------|--------|------|------|---------|---------|-----------|
| H1 | TestFraud_H1_BeforeSettlement | FraudProof arrives first | Fraud before settlement | 1. ProcessFraudProof 2. ProcessBatchSettlement | FRAUD marked, settlement skipped, balance unchanged, Worker slashed | §15.1 |
| H2 | TestFraud_H2_AfterSettlement | FraudProof arrives after | Settlement before fraud | 1. ProcessBatchSettlement 2. ProcessFraudProof | State changes to FRAUD, Worker slashed, 95% executor fee clawed back | §15.2 |
| H3 | TestFraud_H3_DuplicateFraudProof | Duplicate FraudProof | — | Same task_id submitted twice | Second one rejected | §15 |
| H4 | TestFraud_H4_InvalidWorkerSig | FraudProof with invalid Worker signature | — | Submit FraudProof with forged WorkerContentSig | Rejected, Worker not slashed | §15 signature verification |
| **H5** | **TestFraud_H5_FraudOnPendingSecond verificationTask** | **FraudProof submitted for task under second verification** | Second verification in progress | 1. Batch settlement triggers second verification (PENDING_AUDIT) 2. Submit FraudProof for same task | **FRAUD marking overrides second verification state, pending cleared, Worker slashed** | §15 vs §13 priority |

> **New H5**: Timing race between FraudProof and second verification. Fraud should take priority over second verification (conclusive evidence vs random re-examination), but V2 did not cover this.

---

### I. Block Reward Contribution Counting

(Unchanged from V2, I1-I3 total 3 cases)

---

### J. Dynamic Second-Verification Rate

**Test objective**: Verify dynamic calculation formula and boundary protection for second verification rate and third-verification rate.

**Spec reference**: §13.5 second_verification_rate = base × (1 + 10 × fail_ratio), clamped to [min, max].

| ID | Test Name | Description | Input | Expected Rate | Spec Clause |
|------|--------|------|------|----------|-----------|
| J1 | TestSecondVerificationRate_J1_Normal | 0% failure rate | total=1000, fail=0 | 100 (10%) | §13.5 base=100 |
| J2 | TestSecondVerificationRate_J2_HighFailRate | 10% failure rate | total=100, fail=10 | 200 (20%) = 100×(1+10×0.1) | §13.5 |
| J3 | TestSecondVerificationRate_J3_ClampMax | 50% failure rate | total=100, fail=50 | 300 (30%, max clamped) | §13.5 SecondVerificationRateMax=300 |
| J4 | TestSecondVerificationRate_J4_Third verificationNormal | 0% overturn rate | second verification_total=100, overturn=0 | 10 (1%) | §14.1 Third verificationBaseRate=10 |
| **J5** | **TestSecondVerificationRate_J5_ZeroTotalTasks** | **Zero tasks division-by-zero protection** | **total=0, fail=0** | **100 (base rate, no division-by-zero panic)** | §13.5 division-by-zero protection |

> **New J5**: In the first epoch or idle epoch where total=0, `fail_ratio = fail/total` would divide by zero. This is a P1 security issue.

---

### K. Second verification Fund Distribution

(Unchanged from V2, K1-K2 total 2 cases)

---

### L. Overspend Protection

**Test objective**: Verify graceful degradation when partial/all tasks in a batch have insufficient balance, and boundary conditions.

**Spec reference**: §12 On-chain fallback (Overspend Protection).

| ID | Test Name | Description | Precondition | Test Steps | Expected Result | Spec Clause |
|------|--------|------|---------|---------|---------|-----------|
| L1 | TestOverspend_L1_PartialInsufficientBalance | Partial insufficient balance | Deposit 2 FAI | 3 entries × 1 FAI SUCCESS | Only 2 settled, balance=0, 3rd entry status is skipped (not error), corresponding Worker is not jailed and streak not incremented | §12 |
| **L2** | **TestOverspend_L2_ExactBalance** | **Balance exactly equals fee** | Deposit 1 FAI | 1 entry × 1 FAI SUCCESS | **ResultCount=1, balance is precisely 0, Worker streak++ normal** | §12 precise boundary |
| **L3** | **TestOverspend_L3_FailTaskOverspend** | **FAIL scenario overspend** | Deposit 40000 ufai | 1 FAIL entry, fee=1 FAI (fail charges fee×50/1000 = 50000 ufai) | **balance 40000 < 50000, skipped, balance unchanged, Worker not jailed** | §12 FAIL overspend |

> **New L2**: Balance exactly equal to fee is a precise boundary, verifies it doesn't incorrectly skip and doesn't go negative.
> **New L3**: V2 only tests SUCCESS overspend, FAIL also has overspend scenarios (5% fail fee > balance).

---

### M. Task Cleanup

(Unchanged from V2, M1 total 1 case)

---

### N. End-to-End Full Pipeline (Within Settlement Module)

(Unchanged from V2, N1-N4 total 4 cases)

---

### O. Parameter Validation

(Unchanged from V2, O1-O5 total 5 cases)

---

### P. Model Registry Lifecycle

(Unchanged from V2, all 14 cases P1-P14 retained)

---

### Q. VRF Election

**Test objective**: Verify VRF unified formula correctness across scenarios, and extreme candidate situations.

| ID | Test Name | Description | Input | Expected Result | Spec Clause |
|------|--------|------|------|---------|-----------|
| Q1 | TestVRF_Q1_Deterministic | Same input produces same score | Same seed+pubkey+stake+alpha | score1 == score2 | §6.1 |
| Q2 | TestVRF_Q2_AlphaDispatch_StakeWeighted | α=1.0 higher stake produces lower score | stake=1000 vs 100000 | scoreLarge < scoreSmall | §6.1 |
| Q3 | TestVRF_Q3_AlphaSecond verification_StakeIgnored | α=0.0 stake has no effect | Different stakes, α=0.0 | Scores are identical | §6.1 |
| Q4 | TestVRF_Q4_AlphaVerification_SqrtWeight | α=0.5 between the two extremes | Compare dispatch ratio vs verification ratio | dispatch separation > verification separation | §6.1 |
| Q5 | TestVRF_Q5_SelectLeader_Normal | Normal election | Register 3 Workers | Returns leader addr, LeaderInfo written | §6.2 |
| Q6 | TestVRF_Q6_SelectLeader_NoWorkers | No available Workers | Empty list | Returns ErrNoEligibleWorkers | §6.2 |
| Q7 | TestVRF_Q7_SelectWorkerForTask | Select inference Worker | Register online Workers | Returns Worker with lowest score, α=1.0 | §6.2 |
| Q8 | TestVRF_Q8_SelectVerifiers_ExcludeExecutor | Select verifiers | Register 5 Workers, 1 is executor | Returns 3, does not include executor, α=0.5 | §9.1 |
| Q9 | TestVRF_Q9_SelectSecondVerifiers_ExcludeAll | Select second verifiers | Register 10 Workers, exclude Worker+3 Verifiers | Returns 3, does not include Worker and Verifiers, α=0.0 | §13.3 |
| Q10 | TestVRF_Q10_CommitteeRotation | Committee rotation | Initial committee | New committee generated after CommitteeRotation period | §6.2 |
| Q11 | TestVRF_Q11_LeaderHeartbeatTimeout | Leader heartbeat timeout | LeaderInfo.LastHeartbeat expired | Triggers re-election | §6.2 |
| Q12 | TestVRF_Q12_RankWorkers_TieBreaking | Ranking tie-breaking | Construct two Workers with same score | By address lexicographic order (deterministic) | §6.1 |
| Q13 | TestVRF_Q13_SelectVerifiers_ExcludeBusy | Exclude busy Workers | Register 5 Workers, 2 have IsBusy=true | Returned 3 do not include busy Workers | §9.1 |
| **Q14** | **TestVRF_Q14_AllWorkersBusy** | **All Workers are busy** | Register 5 Workers, all IsBusy=true | **Returns empty list or ErrNoAvailableWorkers** | §9.1 extreme case |
| **Q15** | **TestVRF_Q15_SingleWorkerAllRoles** | **Only 1 Worker available** | Only 1 Worker registered, that Worker is selected as executor | **Verifier candidate pool is empty → cannot select 3 verifiers, returns error or degrades gracefully** | §9.1 insufficient candidates |

> **New Q14**: Fault tolerance behavior when all Workers are busy.
> **New Q15**: VRF election degradation logic when insufficient candidates; single Worker cannot simultaneously be executor and verifier.

---

### R. P2P Inference Message Structure Validation

(Unchanged from V2, R1-R10 total 8 cases)

---

### S. Worker Full Lifecycle

| ID | Test Name | Description | Precondition | Test Steps | Expected Result | Spec Clause |
|------|--------|------|---------|---------|---------|-----------|
| S1 | TestWorkerLife_S1_ColdStartRegister | Cold-start period free registration | blockHeight ≤ ColdStartFreeBlocks | RegisterWorker | Status=Active, Stake=0 | §4.1 Cold start |
| S2 | TestWorkerLife_S2_NormalRegister | Normal period requires staking | blockHeight > ColdStartFreeBlocks | RegisterWorker | Status=Active, Stake=MinStake | §4.1 |
| S3 | TestWorkerLife_S3_DuplicateRegister | Duplicate registration | Already registered | RegisterWorker same address | ErrWorkerAlreadyRegistered | §4.1 |
| S4 | TestWorkerLife_S4_AddStake | Add stake | Already registered | AddStake(amount) | Stake accumulates | §4.1 |
| S5 | TestWorkerLife_S5_UpdateModels | Update model list | Already registered | UpdateModels([C,D]) | Old index deleted, new index created | §4.1 |
| S6 | TestWorkerLife_S6_JailProgressive | Three-stage progressive jail | Already registered | 3× JailWorker | jail_count=1,2,3; 3rd time Tombstoned=true | §10.1 |
| S7 | TestWorkerLife_S7_UnjailAfterCooldown | Unjail after cooldown | Jailed, JailUntil has passed | Unjail | Jailed=false, Status=Active | §10.2 |
| S8 | TestWorkerLife_S8_UnjailBeforeCooldown | Unjail before cooldown | Jailed, JailUntil not reached | Unjail | Rejected, returns error | §10.2 |
| S9 | TestWorkerLife_S9_SuccessStreakReset | 50 consecutive successes reset jail_count | jail_count=2 | IncrementSuccessStreak × 50 | SuccessStreak=0, JailCount=0 | §10.3 |
| S10 | TestWorkerLife_S10_ExitWorkerWaitPeriod | Exit waiting period | Already registered Active | 1. ExitWorker → Exiting 2. ProcessExitingWorkers(before 21d) → unchanged 3. ProcessExitingWorkers(after 21d) → Exited | Status: Active→Exiting→Exited | §4.4 |
| S11 | TestWorkerLife_S11_TombstonedCannotReRegister | Cannot re-register after Tombstone | S6 3rd jail, Tombstoned=true | RegisterWorker with same address | Rejected, returns ErrWorkerTombstoned | §10.1 permanent ban |
| **S12** | **TestWorkerLife_S12_InsufficientStakeRegister** | **Insufficient stake registration** | blockHeight > ColdStartFreeBlocks, bankKeeper balance < MinStake | RegisterWorker | **Returns ErrInsufficientStake or similar error, Worker not registered** | §4.1 minimum stake |
| **S13** | **TestWorkerLife_S13_SlashToZero** | **Consecutive slashing until stake reaches zero** | Already registered, Stake=100 ufai | SlashWorker multiple times (5%×N) | **Behavior when stake→0: may trigger tombstone or maintain jail state, no panic** | §10.1 slash boundary |

> **New S12**: Insufficient stake during normal period should be rejected, preventing zero-stake Workers from entering the network.
> **New S13**: Consecutive slashing to zero is an economic model boundary, need to confirm no underflow or panic.

---

### T. Inference End-to-End (Cross-Module Integration)

| ID | Test Name | Description | Modules Involved | Verification Points | Spec Clause |
|------|--------|------|---------|---------|-----------|
| T1 | TestInferE2E_T1_FullSuccessPath | Full success path (no second verification) | modelreg→vrf→settlement | Model ACTIVE, VRF selects Worker, fee distribution exact, BatchRecord + EpochStats intermediate states | §7.1 |
| T2 | TestInferE2E_T2_FullSecond verificationPath | Full second verification path (second verification PASS) | modelreg→vrf→settlement | Second verification triggered, VRF selects second verifiers (excludes original roles), 3×PASS → CLEARED | §13 |
| T3 | TestInferE2E_T3_Second verificationOverturn | Second verification overturn path | modelreg→vrf→settlement→worker | Original SUCCESS + second verification 3×FAIL, Worker jail, PASS verifiers jail, user refund | §13.6 Quadrant 2 |
| T4 | TestInferE2E_T4_WorkerTimeout_SDKRetry | Worker acceptance timeout, SDK resends | vrf→settlement | Worker A doesn't execute, SDK resends, Worker B completes, task_id deduplicated | §7.2 |
| T5 | TestInferE2E_T5_LeaderFailover | Leader failover | vrf | Leader heartbeat timeout, rank#2 takes over, new LeaderInfo | §6.2 |
| T6 | TestInferE2E_T6_ModelServicePause | Model service pause and resume | modelreg→worker | 10→8 Workers (pause) → 10 Workers (resume) | §5.2 |
| T7 | TestInferE2E_T7_Temperature0_ArgmaxPath | Temperature=0 uses argmax without ChaCha20 | vrf→settlement | When temp=0, verification uses logits-only (4/5 match), no sampling verification. Worker and Verifier results match | §8.3 temp=0 → argmax |
| T8 | TestInferE2E_T8_VerificationFail_DirectSettle | Verification phase FAIL → direct FAIL settlement | vrf→settlement→worker | 2 of 3 verifiers vote FAIL → verification fails → Worker jail, user charged only 5%, does not enter second verification flow | §9.2 majority FAIL |
| **T9** | **TestInferE2E_T9_EpochBoundarySettlement** | **Epoch boundary settlement** | settlement→reward | **Batch settlement at epoch's last block (height=200), EpochStats attributed to epoch=2 not epoch=3** | §12+§16 epoch calculation |
| **T10** | **TestInferE2E_T10_RewardHalvingBoundary** | **Reward halving boundary** | reward | **Epoch spans halving boundary (block 26249999→26250000). Verify CalculateEpochReward accumulates block by block: first half of blocks use original rate, second half use rate/2** | §16.1 halving precision |

> **New T9**: EpochStats should be attributed to the current epoch when settlement occurs at the epoch's last block.
> **New T10**: Rewards should be calculated block by block rather than using a single rate when an epoch spans the halving boundary.

---

### U. Multi-Node E2E (Real Nodes + CLI)

(Unchanged from V2, U1-U5 total 5 cases)

---

### V. Economic Conservation and Genesis Round-Trip (New Partition)

**Test objective**: Verify system-level invariants — total fund conservation and state can be fully exported/restored.

**Spec reference**: §11 Fee distribution conservation, §16 Reward minting conservation, chain state persistence.

| ID | Test Name | Description | Modules Involved | Test Steps | Expected Result | Spec Clause |
|------|--------|------|---------|---------|---------|-----------|
| **V1** | **TestConservation_V1_FeeConservation** | **Fee total conservation** | x/settlement | Deposit→10 SUCCESS+2 FAIL batch settlement | **user_debit(SUCCESS) == executor_received + verifier_received + multi_verification_fund; user_debit(FAIL) == verifier_fail_received + second verification_fail_fund. Total equation holds, error≤0** | §11 |
| **V2** | **TestConservation_V2_RewardMintConservation** | **Reward minting conservation** | x/reward | DistributeRewards(2 workers, 3 verifiers) | **total_minted == sum(all RewardRecord amounts). No dust leakage (error≤1 ufai per participant)** | §16 |
| **V3** | **TestGenesis_V3_SettlementRoundTrip** | **Settlement Genesis round-trip** | x/settlement | Deposit+settlement→ExportGenesis→InitGenesis→verify state | **All InferenceAccount, SettledTask, BatchRecord, EpochStats, Params fully restored** | Chain stability |
| **V4** | **TestConservation_V4_SameBlockDuplicateBatch** | **Two batches in same block referencing same task** | x/settlement | Construct two batches each containing task-1, execute sequentially | **First batch settles task-1, second batch skips task-1. User charged only once** | §3.4 task_id uniqueness |

> **New V1-V4**: V2 test plan lacked system-level conservation assertions and Genesis round-trip. V1/V2 ensure the economic model doesn't leak, V3 ensures state is complete after chain restart, V4 ensures correct deduplication semantics within the same block.

---

## 4. Role Coverage Matrix

| Role | Covered Partitions | Normal Scenarios | Exception Scenarios |
|------|---------|---------|---------|
| **User** | A, C, D, H, L, N, R, T, U, V | Deposit, withdraw, pay, initiate inference | Excess withdrawal, insufficient balance, overspend, **zero deposit, wrong denom, exact balance, FAIL overspend** |
| **Worker** | B, C, E, F, H, S, T, U | Register, stake, execute inference, streak++ | Progressive jail, slash, tombstone, re-registration rejected, accept task but don't execute, **insufficient stake, slash to zero** |
| **Verifier** | C, E, F, I, Q, R, T | Verification count, epoch count | Wrong vote results in jail, second verification overturn implicates, FAIL majority decision |
| **SecondVerifier** | E, F, G, I, K, Q, R, T | Four-quadrant judgment, majority decision, timeout, fund distribution | SecondVerifier=verifier rejected, insufficient results, **submission after timeout, duplicate submission** |
| **Leader** | Q, R, T | VRF election, dispatch, heartbeat | Heartbeat timeout failover, no Workers, **all busy** |
| **Proposer** | C, D, N, P, T, V | Package batch, propose model | Merkle mismatch jail, ResultCount mismatch jail, unauthorized Proposer, **empty batch, tampered signature, all-skipped batch** |
| **Validator** | N, Q, T, U | Committee election, block production consistency | Committee rotation |

---

## 5. Fee Distribution Verification Matrix

**Spec §11 Frozen Parameters**:

| Scenario | User Charge | Worker | Verifier (3 people) | Second verification Fund |
|------|---------|--------|--------------|---------|
| SUCCESS (no second verification) | 100% fee | 95% (950/1000) | 4.5% (45/1000) | 0.5% (5/1000) |
| FAIL | 5% fee | 0% | 4.5% of 5% | 0.5% of 5% |
| FraudProof (already settled) | Claw back 95% executor fee | -slash 5% stake | — | — |
| **Dust (fee=10 ufai)** | **100% fee** | **~10 (remainder)** | **0 (truncated)** | **0 (truncated)** |

**Tests that should cover this**: C1 (SUCCESS 100%), C2 (FAIL 5%), C6 (exact amounts+conservation), **C7 (dust truncation)**, H2 (FraudProof clawback), K1 (multi-verification fund weighted by count), T1 (full pipeline distribution), **V1 (total conservation)**

---

## 6. V2→V3 Change Summary

### New Cases (20)

| ID | Scenario | Category | Priority |
|------|------|------|--------|
| A6 | Zero amount deposit | Zero-value boundary | P2 |
| A7 | Wrong denom deposit | Input validation | P2 |
| A8 | Exact full balance withdrawal | Precise boundary | P3 |
| C7 | Tiny fee dust distribution | Economic model boundary | P2 |
| C8 | Single-entry batch | Merkle tree boundary | P3 |
| D9 | Empty batch | Empty input | P2 |
| D10 | Tampered ProposerSig | Signature security | P2 |
| D11 | All entries skipped | Deduplication boundary | P3 |
| E8 | Second verification result submitted after timeout | Timing race | P2 |
| E9 | Same second verifier duplicate submission | Deduplication security | P2 |
| F5 | Re-second verification majority decision 2:1 | Critical logic | P2 |
| G4 | Precise timeout boundary | Precise boundary | P3 |
| H5 | FraudProof on task under second verification | State race | P2 |
| J5 | Zero tasks division-by-zero protection | Division-by-zero safety | P1 |
| L2 | Balance exactly equals fee | Precise boundary | P3 |
| L3 | FAIL scenario overspend | Overspend boundary | P2 |
| Q14 | All Workers busy | Extreme candidates | P2 |
| Q15 | Only 1 Worker available | Insufficient candidates | P2 |
| S12 | Insufficient stake registration | Economic constraint | P2 |
| S13 | Consecutive slash to zero | Slash boundary | P2 |

### New Partition V (4 cases)

| ID | Scenario | Category | Priority |
|------|------|------|--------|
| V1 | Fee total conservation | Economic conservation | P1 |
| V2 | Reward minting conservation | Economic conservation | P1 |
| V3 | Settlement Genesis round-trip | State persistence | P2 |
| V4 | Two batches in same block deduplication | Concurrency safety | P2 |

### Strengthened Cases (1)

| ID | Before | After |
|------|------|------|
| C6 | Amount received by each party | Added **user_debit == executor + verifiers + multi_verification_fund** total conservation assertion |

### Priority Distribution

| Priority | Count | Description |
|--------|------|------|
| P1 | 3 | J5 (division by zero), V1 (fee conservation), V2 (reward conservation) — not fixing may cause chain panic or fund loss |
| P2 | 15 | Security/economic boundaries — not fixing may cause state inconsistency or exploitability |
| P3 | 6 | Precise boundaries — not fixing means functionality is basically normal but behavior doesn't fully match expectations |

---

## 7. Execution Tier Recommendations

| Tier | Included Partitions | Case Count | Execution Method | Who Is Responsible |
|----|---------|--------|---------|--------|
| **L1 Unit Tests** | A-O, P, Q, R, S, V | ~110 | `go test ./...` automated run | Engineer daily CI |
| **L2 Cross-Module Integration** | N, T, V | ~16 | Requires `setupCrossModuleEnv()` | Engineer + SecondVerifier |
| **L3 Multi-Node E2E** | U | 5 | Real nodes + Docker + GPU | Jms's 5090 machine |

---

## 8. Edge/Exception Coverage Assessment Matrix

| Dimension | V2 Score | V3 Score | Improvement |
|------|---------|---------|--------|
| Normal path | 9/10 | 9/10 | No change |
| Exception rejection | 8/10 | 9/10 | +D9/D10/D11, E8/E9 |
| Boundary values | 6/10 | 9/10 | +A6/A7/A8, C7/C8, G4, L2/L3, J5 |
| Security adversarial | 7/10 | 9/10 | +D10, E9, H5, Q14/Q15, S12/S13 |
| Economic conservation | 5/10 | 9/10 | +C6 strengthened, V1, V2, C7 |
| Cross-module integration | 7/10 | 9/10 | +T9, T10, V3, V4 |
