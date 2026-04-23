# E2E Real Inference — RunPod 4090 / Qwen2.5 Smoke (Runs 1a–1c on 0.5B, Run 2 on 7B)

| | |
|---|---|
| **Date** | 2026-04-23 10:47 CST Run 1a, 11:12 CST Run 1b, 11:16 CST Run 1c, 11:52 CST Run 2 |
| **Operator** | dmldevai |
| **Test driver** | `scripts/e2e-real-inference.sh` — patched this session to add `BATCH_WAIT_SEC` env var (§12) |
| **Verdict** | **PASS** on Runs 1c and 2 — full path through dispatch → inference → worker teacher-force → verifier teacher-force → `MsgBatchSettlement` broadcast on chain, on both 0.5B and 7B |
| **Runs** | 1a `PARTIAL PASS` (422) / 1b `PARTIAL PASS` (cleanup killed verify) / 1c **`PASS`** (0.5B) / 2 **`PASS`** (7B) |
| **Next** | §14 remaining work for pre-mainnet (primary ones: token ordering artefact from §13, token-by-token fallback O(N) bottleneck, script cosmetic issues) |

---

## 1. Executive summary

Full end-to-end smoke of the FunAI stack (chain + 4 P2P nodes + SDK client) against a
cloud-hosted TGI endpoint — RunPod RTX 4090 (24 GB VRAM) serving
`Qwen/Qwen2.5-0.5B-Instruct` via TGI `3.3.6-dev0` through the pod's HTTPS proxy URL.
Three iterations on the same 0.5B model, same pod, same script driver:

1. **Run 1a** (§§2–9). Stock TGI default, stock script. Primary inference and SDK hash
   check pass (1.75 s). Verification path silently blocked: all 4 P2P nodes' worker
   teacher-force pass 422's out because TGI's default `--max-top-n-tokens=5` is below
   the 256 the Go code requires (V5.2 §8.3). No receipts with logits, no verifier
   comparison, no on-chain settlement. The "19/19 assertions passed" the script reports
   is narrowly about *infrastructure* — the assertions don't touch verification.
2. **Run 1b** (§10). TGI restarted with `MAX_TOP_N_TOKENS=256`. 422 gone, worker
   teacher-force succeeds (falls back to token-by-token because TGI v3 returns empty
   `prefill`), worker dispatches `VerifyPayload` to 3 verifiers, verifiers start — then
   all 3 get `context canceled` at steps 4–8 because the script's 10 s Phase-6 wait
   ends and `cleanup` kills the P2P node processes. Logs show
   `11:12:52 node.go:413: FunAI P2P Node shutting down...` in lockstep with the cancels.
3. **Run 1c** (§11). Script patched with `BATCH_WAIT_SEC=60` default-preserving env var
   (§12). Verifiers complete: 9+ `verify result ... pass=true` events per node for
   task `b5529f9abd132194...`, and **all 4 P2P nodes broadcast a `MsgBatchSettlement`
   tx** to the chain with `entries=1 gas=202000` — the first run that exercises the
   full settlement path end to end.

**Step-by-step progression across runs:**

| Step | 1a | 1b | 1c | 2 |
|---|:---:|:---:|:---:|:---:|
| Model | 0.5B | 0.5B | 0.5B | **7B** |
| Worker primary inference via TGI | ✓ | ✓ | ✓ | ✓ |
| SDK hash round-trip check | ✓ | ✓ | ✓ | ✓ |
| Worker teacher-force logit capture | ✗ (422) | ✓ | ✓ | ✓ |
| Worker `VerifyPayload` dispatch | ✗ | ✓ | ✓ | ✓ |
| Verifier teacher-force completes | ✗ | ✗ (cleanup) | ✓ | ✓ |
| Verifier `VerifyResult` published | ✗ | ✗ | ✓ | ✓ |
| Proposer builds `MsgBatchSettlement` | ✗ | ✗ | ✓ | ✓ |
| `BatchSettlement` tx broadcast on chain | ✗ | ✗ | ✓ (4 txs) | ✓ (4 txs) |
| Primary inference latency (SDK round-trip) | 1.75 s | 1.71 s | 2.23 s | **2.92 s** |
| Full settlement latency (SDK send → tx broadcast) | — | — | ~13 s | **~40 s** |
| Output tokens | 9 | 8 | 10 | 34 |
| Script assertions | 19/19 | 19/19 | 20/20 | 20/20 |

4. **Run 2** (§13). Pod reconfigured for `Qwen/Qwen2.5-7B-Instruct` (FP16, ~15 GB weights
   in the 24 GB card). Same `MAX_TOP_N_TOKENS=256` + `BATCH_WAIT_SEC=90`. Primary inference
   2.92 s (vs 0.5B's 2.23 s, ~30 % slower, within the ratio one would expect for 7B vs 0.5B
   on the same hardware). Verifier token-by-token teacher-force takes ~18 s for a 34-token
   output — the fallback is `O(N_tokens)` with RunPod-proxy-per-token overhead, which
   becomes the dominant cost at 7B-size outputs. Full settlement path: verify dispatch
   at 20 s, verifier `pass=true` at 38 s, 4 `MsgBatchSettlement` txs broadcast at 40 s.
   Verdict: `PASS`, **20/20** assertions. Output has an unexpected **token-ordering
   artefact** — generated text reads as scrambled words even at `temperature=0`
   (e.g. `"You correct're! to2+ The answer 2 is 4. Well done any! questions have other you"`);
   `pass=true` still holds because the hash check compares bytes, not coherent text, and
   worker + verifier see the identical scrambled byte sequence. Flagged in §13 as the
   one real thing that needs investigation before mainnet.

**Conclusion.** Both 0.5B and 7B smoke are green end-to-end on a real cloud GPU.
Pre-mainnet work remaining is the token-ordering artefact, the `O(N)` teacher-force
fallback latency, and the 4-proposer-race behaviour visible in Runs 1c and 2 — all in §14.

---

## 2. Environment

### Pod (RunPod)

| Component | Value |
|---|---|
| Region | RunPod secure cloud (pod IP `103.196.86.97`, now replaced — see §2.2) |
| GPU | NVIDIA RTX 4090, 24 564 MiB VRAM, driver 580.126.20 |
| Host | 503 GB RAM, 128 vCPU (shared — not all ours), 30 GB container / 1.2 PB `/workspace` mfs |
| Image | `ghcr.io/huggingface/text-generation-inference:3.3.6` |
| TGI runtime | version reported as `3.3.6-dev0`, router sha `efb94e0`, docker_label `sha-efb94e0` |
| Model | `Qwen/Qwen2.5-0.5B-Instruct`, model_sha `7ae557604adf67be50417f59c2c2f167def9a775` |
| TGI config (advertised via `/info`) | `max_input_tokens=32767`, `max_total_tokens=32768`, `max_concurrent_requests=128`, `max_client_batch_size=4`, `max_best_of=2` |
| TGI start args | **default** — `top_n_tokens` cap is default 5 |
| Pod exposure | HTTP Services: Port 80 → proxied HTTPS URL. No direct TCP for :80; only SSH direct TCP at :22 12908 (from the pre-rebuild pod; rebuilt pod uses ssh.runpod.io proxy — no PTY) |

### Pod (RunPod) — 2.2 connectivity note

This pod is the *second* of the session; the first (`jchtewdsn27vk5`, PyTorch base image) was
terminated after discovering the base image has no docker CLI inside, making a
`ghcr.io/huggingface/text-generation-inference` container impossible to launch from within.
The second pod (`sbd58cifejhsq3`, name `open_scarlet_giraffe`) uses the TGI image directly
as the pod's root container, so TGI is `PID 1`-ish and reachable through RunPod's HTTPS
proxy without any in-pod Docker. Trade-off: no sshd inside, so debug access is via the
Pod's "Logs" tab in the RunPod console, not a shell.

### Dev host

| Component | Value |
|---|---|
| Repo | `github.com/funai-wiki/funai-chain`, branch `mainnet-readiness/v6-dispatch-capacity-testnet`, HEAD `c4d0b24` (feat(v6): per-worker batch capacity — wire + test) |
| Binaries | `./build/{funaid,funai-node,e2e-client}` — pre-built, not rebuilt this session (no go in PATH) |
| Concurrent testnet | A long-running local testnet at `~/.funai-testnet` (4-node, auto-started from crontab) occupies the default port range. E2E script invoked with port overrides: `P2P_PORT_BASE=56656 RPC_PORT_BASE=56657 API_PORT_BASE=31317 GRPC_PORT_BASE=39090 P2P_LIBP2P_PORT_BASE=15001` |
| E2E testnet dir | `/tmp/funai-e2e-real` (preserved via `--no-cleanup`) |

---

## 3. What the script actually tested

`scripts/e2e-real-inference.sh` in `--no-cleanup` mode, 6 phases:

| Phase | Scope | Assertions |
|---|---|---|
| 0 Preflight | TGI reachability, version probe, feature probes (`decoder_input_details`, `top_n_tokens`, `/tokenize`) | 4 |
| 1 Chain testnet | Init 4 nodes, start chain, produce blocks | 1 |
| 2 Workers + deposit | Register 4 workers with key extraction, stake, bank send, 1000 FAI deposit, query inference account | 7 |
| 3 P2P nodes | Start 4 P2P nodes, bootstrap peer, worker-list refresh | 1 |
| 4 Inference | SDK sends one `Infer("What is 2+2?", temp=0)`, waits for result | 3 |
| 5 P2P flow | grep logs for `dispatched`, `completed`, `receipt`, `verify` keywords | 2 strict + 2 warn-only (receipt / verify not found) |
| 6 Settlement | 10 s wait, grep for `BuildBatch` / `BatchSettlement` keywords | 0 strict, info-only |

Total asserted: 19. All 19 passed. But phases 5 and 6 are log-grep heuristics, not semantic checks — their warn-only failures are already telling us something the overall "PASS" count hides.

---

## 4. Results

### 4.1 What passed (genuinely)

- Chain reached height 3 in 90 s, kept producing (final height at exit ≈ 27).
- All 4 workers registered on-chain, each staked 10 000 FAI and operator_id set.
- User (validator0 address re-purposed — §6 has the bug note) deposited 1 000 FAI; inference account query succeeded.
- P2P mesh formed: 4 nodes running, libp2p 15001–15004, "dispatch active" from all 4.
- **SDK → P2P → TGI → back** round trip completed in **1.749 s**. Task id `dd9f8e48646e8fb992f54a0a2e974603`. 9 generated tokens. Output `"  4 answer is.The\n\n<|endoftext|>"`. SDK's payload-hash integrity check passed.

### 4.2 What silently failed

```
# one line per P2P node, all 4 nodes hit this
dispatch.go:264: dispatch: worker.HandleTask: teacher force for logits:
  TGI error 422: {"error":"Input validation error: `top_n_tokens` must be >= 0 and <= 5. Given: 256"}
```

| Layer | Observed |
|---|---|
| Teacher-force logit capture | **0 / 4** nodes produced usable receipt logits. All 422'd. |
| Verifier teacher-force comparison | **No record** in any log — no receipt with logits to compare against. |
| Batch settlement | **None** within 10 s. Root cause above + timeout shorter than `BATCH_INTERVAL × N_blocks_for_audit`. |

### 4.3 Script heuristic mis-hits (cosmetic but worth fixing)

These are annotated as `[WARN]` in the script output, and they are correct to warn, but the
downstream `ALL CHECKS PASSED` count ignores them:

| Script grep | Actual log wording | Impact |
|---|---|---|
| `receipt for task` | code emits different phrasing | Phase 5 "no receipt event" warn |
| `verified task\|verify result` | verification path never ran this time | Phase 5 "no verification event" warn — correctly so |
| `BuildBatch\|BatchSettlement` | never triggered in 10 s window | Phase 6 "no batch settlement yet" info |

### 4.4 Script bug — Phase 6 queries the wrong address

Phase 2 deposits from `validator0` (because SDK-user derived address had no bank balance),
then Phase 6 queries `user0`'s inference account. `user0` never had a deposit — the query
trivially returns `inference account not found`. The error is emitted but the assertion
count doesn't care. Reported here as §6 first item.

---

## 5. Root cause of the 422 and the fix

### 5.1 Why `top_n_tokens=256`

`p2p/inference/tgi.go` requests 256 top tokens per position in four call sites (primary
`/generate` with details, first teacher-force pass, fallback token-by-token pass, and the
verifier's recomputation pass). The 256 comes from V5.2 §8.3: when temperature > 0 and the
verifier has to re-derive the deterministic-sampling CDF to pick the same token as the
worker, the candidate set must cover the full plausible head of the distribution. 256 is
the chosen budget.

### 5.2 Why TGI rejects

TGI's `--max-top-n-tokens` flag caps the maximum value any request can send. Default is 5.
Any request above the cap is rejected with HTTP 422 at the validation layer, before the
model runs. This is per-server-startup config.

### 5.3 Fix

On TGI start, add `--max-top-n-tokens=256`. In a RunPod pod using the TGI image as the root
container, this is set via either:

- **Container start command override**: edit the Pod template and set the start command
  (currently blank, meaning the image's default entrypoint runs with only the env-var
  defaults) to include `--max-top-n-tokens 256 --max-input-tokens 4096 --max-total-tokens 8192`.
- **Environment variable**: TGI also honours `MAX_TOP_N_TOKENS=256` env var (see HF TGI
  launcher config), which RunPod lets us set in the Pod's Environment Variables list
  without rebuilding the container.

Either works; env var is lower-friction because it doesn't require finding and editing
the template's start command. The next run uses this.

### 5.4 Related: script preflight missed this

`scripts/e2e-real-inference.sh` Phase 0 does probe `top_n_tokens=5` (line 266:
`"top_n_tokens":5`) and the probe *succeeded* this session (returned 0 alternatives and
warned — the probe doesn't distinguish "server caps at 5 so our request of 5 returned 5"
from "server returned nothing for 5"). It never probes at the value the Worker actually
uses (256). Proposed script fix: add a 256-value probe and fail-fast if it 422's.

---

## 6. Known issues surfaced by this run

| # | Issue | Where | Severity | Action |
|---|---|---|---|---|
| 1 | TGI default `--max-top-n-tokens=5` blocks Worker teacher-force | TGI startup | **P0 blocker for verification** | Set `MAX_TOP_N_TOKENS=256` env var on next Pod deploy — before Run 2 |
| 2 | Preflight probes `top_n_tokens=5` (passes even when server capped), never probes `=256` (the real code path) | `scripts/e2e-real-inference.sh:262-284` | P1 | Add a second probe with `"top_n_tokens":256` that fails fast on 422 |
| 3 | Phase 6 queries `user0`'s settlement account; Phase 2 deposited as `validator0` | `scripts/e2e-real-inference.sh:902-908` | P2 cosmetic | Fix the address used in Phase 6 query, or better, derive a single user-address constant and plumb it through |
| 4 | Phase 5 greps `"receipt for task"` and `"verified task\|verify result"` which don't appear in current P2P logs | `scripts/e2e-real-inference.sh:845-855` | P2 cosmetic | Update grep patterns to match the code's current log wording |
| 5 | Phase 6 10 s settlement wait is shorter than the time needed for a task to traverse VERIFIED → CLEARED → packaged into a BatchSettlement tx | `scripts/e2e-real-inference.sh:878` | P2 | Widen to ≥ 30 s, or actively poll for a settlement tx instead of sleeping |
| 6 | `--no-cleanup` leaves `/tmp/funai-e2e-real` but doesn't checkpoint the configuration env vars used to start the run, making repro awkward | convention | P3 | Write a `run-config.env` alongside the testnet dir |

Issues 2-6 are local repo issues and have been logged here without fix — they don't block
Run 2 if Issue 1 is resolved.

---

## 7. What Run 2 (Qwen2.5-7B) was expected to verify — and did

All four originally-planned Run 2 bullets are met:

| Expectation | Outcome (see §13 for detail) |
|---|---|
| Worker teacher-force pass produces a receipt with per-position logits | ✓ token-by-token fallback works, all 4 nodes produce logits |
| At least one verifier transitions the task through chain state | ✓ 3 verifiers each publish `pass=true`, task lands in a `MsgBatchSettlement` |
| A `MsgBatchSettlement` tx observed on chain with the task id | ✓ 4 txs broadcast (one per P2P node, §13.1) |
| Latency budget at 7B FP16 for ~10-token output < 3 s SDK round-trip | ✓ actually delivered **2.92 s** for a 34-token output (primary path only); the full settlement path takes ~40 s, dominated by token-by-token fallback — §13.2 and §14 P0 |

Run 2 also surfaced the **token-ordering artefact** (§13.4) — not a blocker for the
smoke verdict but logged as §14 P0.

---

## 8. Raw artifacts

Captured alongside this report (split by run):

```
docs/testing/reports/2026-04-23-1047-e2e-real-runpod-4090/
├── report.md                            this file
├── tgi_info.json                        TGI /info at Run 1a (0.5B, default-config pod)
├── tgi_info_run2.json                   TGI /info at Run 2 (7B, reconfigured pod)
│
├── e2e_stdout_run1a.log                 Run 1a full stdout, ANSI stripped (has 422)
├── sdk_client_run1a.log
├── p2p_node{0,1,2,3}_run1a_events.log   422 errors in these
│
├── e2e_stdout_run1b.log                 Run 1b (422 fix, but Phase 6 cleanup too short)
│
├── e2e_stdout_run1c.log                 Run 1c — first green (0.5B, BATCH_WAIT_SEC=60)
├── sdk_client_run1c.log
├── p2p_node{0,1,2,3}_run1c_events.log   verify pass=true + BatchSettlement txs
│
├── e2e_stdout_run2.log                  Run 2 — 7B green, 20/20
├── sdk_client_run2.log                  7B output with the token-ordering artefact
└── p2p_node{0,1,2,3}_run2_events.log    7B verify pass=true + 4 BatchSettlement txs
```

Evidence locations:

- Run 1a's 422 error → `p2p_node*_run1a_events.log`, 10:47:07 – 10:47:08.
- Run 1b's premature cleanup → `e2e_stdout_run1b.log` and P2P logs (obsoleted by this
  report since we didn't keep per-node Run 1b grep files; the stdout captures the
  `node.go:413: FunAI P2P Node shutting down...` vs `context canceled` lockstep).
- Run 1c's 0.5B full settlement → `p2p_node{i}_run1c_events.log` contains
  `verify result for task b5529f9abd132194.. pass=true` (multiple) and
  `BatchSettlement tx broadcast hash=<64-hex> entries=1 gas=202000`.
- Run 2's 7B full settlement → `p2p_node{i}_run2_events.log` contains the analogous
  `task 366e9bd3dab69997.. pass=true` and 4 `BatchSettlement tx broadcast ...` lines at
  11:53:24.
- Run 2's scrambled output → `sdk_client_run2.log`:
  `Output: " 4\n\nYou correct're! to2+ The answer 2 is 4. Well done any! ..."`.

---

## 9. References

- E2E driver: [`scripts/e2e-real-inference.sh`](../../../../scripts/e2e-real-inference.sh) (patched this session — see §12)
- TGI client with the `top_n_tokens=256` constant: [`p2p/inference/tgi.go`](../../../../p2p/inference/tgi.go)
- C0 batching report that defines the Option B teacher-force pattern: [`../2026-04-20-1329-c0-fail/report.md`](../2026-04-20-1329-c0-fail/report.md) §5.1
- TGI launcher flag reference: <https://huggingface.co/docs/text-generation-inference/reference/launcher#maxtopntokens>
- V5.2 §8.3 (256-candidate budget for deterministic sampling verification): [`docs/protocol/FunAI_V52_Final.md`](../../../protocol/FunAI_V52_Final.md)

---

## 10. Run 1b — 422 fix applied, verification cut off by Phase 6 cleanup

**Time**: 2026-04-23 11:07–11:13 CST.

**Change vs Run 1a**: Pod stopped, Environment Variable `MAX_TOP_N_TOKENS=256` added,
Pod restarted. Model and pod ID unchanged at the app level (but the Pod ID happens to
change to `s7w63tmo0w0gbh` because RunPod re-created the container on restart —
HTTPS proxy URL updated accordingly to `https://s7w63tmo0w0gbh-80.proxy.runpod.net`).
Script driver unchanged from Run 1a.

**Pre-run probe**: `top_n_tokens=256` now returns HTTP 200 with a 256-candidate
`top_tokens` array. 422 is gone.

**Primary inference**: Task `6a653eac26aefdeecdd3576aea6d7f56`. Output
`"  4\n\nThe answer is<|endoftext|>"`. 8 tokens. Latency 1.71 s. `Verified: true` at
the SDK-hash level.

**Verification path — new in 1b**:

- 11:12:41 Leader dispatches task.
- 11:12:42 Worker kicks off primary inference + teacher-force on TGI.
  `TeacherForce: prefill empty (TGI v3?), falling back to token-by-token` — this is
  the `tgi.go:413` fallback kicking in and succeeding. All 4 P2P nodes (one acting as
  worker, the other three as prospective verifiers) log this at 11:12:42 – 11:12:49.
- 11:12:47 `worker: verify dispatch: 3 candidates (need >=3)` — the `VerifyPayload`
  publish.
- 11:12:48 – 11:12:49 Three verifiers begin their token-by-token teacher-force over
  the RunPod HTTPS proxy.
- **11:12:52 `node.go:413: FunAI P2P Node shutting down...`** — the script's `cleanup`
  trap fires because Phase 6's 10 s wait has ended. Each in-flight verifier's HTTP
  POST to `https://.../generate` dies with `context canceled` at a step between 4 and
  8 of its 9-token loop.

**Root cause identified**: Phase 6 `sleep 10` is too short when TGI latency per
`/generate` call is ~500 ms over RunPod's HTTPS proxy and the verifier fallback runs
9 sequential calls. Fix is in §12.

**Verdict**: `PARTIAL PASS`. Worker-side verification substrate works. Verifier-side
gets cut off by script timing, not by code bug.

---

## 11. Run 1c — full E2E including on-chain BatchSettlement

**Time**: 2026-04-23 11:16 CST.

**Change vs Run 1b**: Script invoked with `BATCH_WAIT_SEC=60` (the patch in §12).
Same TGI pod, same model, same Go binaries. Nothing else.

**Primary inference**: Task `b5529f9abd132194434ecd0eb9b4092f`. Output
`"  4\n\nThe answer is 4.<|endoftext|>"`. 10 tokens. Latency 2.23 s.

**Verification path — now complete**:

| Time (CST) | Event |
|---|---|
| 11:16:04 | SDK sends `InferRequest` task_id=`b5529f9abd132194` |
| 11:16:06 | SDK receives `SUCCESS` with `Verified: true` |
| 11:16:11 | Worker publishes `VerifyPayload` to `/funai/model/qwen-test` (3 verifier candidates, all with `need >= 3`) |
| 11:16:16 – 11:16:19 | All 3 verifiers publish `VerifyResult` — received on all 4 nodes, logged as `dispatch: verify result for task b5529f9abd132194.. pass=true`. Multiple `pass=true` per node (each receiver sees each publisher's result). |
| 11:16:20 | Each of the 4 P2P nodes broadcasts its own `MsgBatchSettlement` tx to the chain — 4 distinct tx hashes, all with `entries=1 gas=202000 seq=0`: <br> node0 `99DD92EF7BAF0811…` <br> node1 `0E0964439869D37D…` <br> node2 `B0F08F52A90A91D2…` <br> node3 `67018D7BD7BD7E2C…` |

Four simultaneous `BatchSettlement` broadcasts per task is the expected behavior of
the current proposer candidate set — each of the 4 nodes thinks it might be the
proposer and races to submit, then the chain's dedup-by-merkle-root logic keeps only
one. Worth confirming in a follow-up test whether the chain's behavior here is
"first-wins and others fail" or "all fail gracefully with duplicate-root rejection" —
the broadcast alone doesn't tell us.

**Script assertion count**: 20/20 PASS (Phase 6's "Batch settlement initiated" now
triggers, pushing the total from 19 to 20).

**Still-unfixed script cosmetic issues** (both present in Runs 1a, 1b, 1c):

- Phase 6 "User balance after inference" query hits `user0`'s inference-account
  address (never deposited) — returns `inference account not found`. The assertion
  in question is info-only so the count still shows PASS, but the output is ugly.
  Issue #3 from §6.
- Phase 5's grep patterns for `receipt for task` / `verify result` still don't match
  the code's current log wording, so those two `[WARN]` lines persist even though
  verification actually works in 1c. Issue #4 from §6.

**Verdict**: `PASS` — full E2E on 0.5B.

---

## 12. Script patch this session

Single change to `scripts/e2e-real-inference.sh` addressing Issue #5 from §6:

```diff
+# Settlement observation window (Phase 6). Default 10s is only long enough when
+# TGI is local; remote TGI (e.g. RunPod HTTPS proxy) + token-by-token verifier
+# fallback needs 30-60s to complete verify → collect → batch → on-chain.
+BATCH_WAIT_SEC="${BATCH_WAIT_SEC:-10}"
```

```diff
-  log_info "Waiting for batch settlement (10s)..."
-  sleep 10
+  log_info "Waiting for batch settlement (${BATCH_WAIT_SEC}s)..."
+  sleep "$BATCH_WAIT_SEC"
```

Default is preserved at 10 s so local-TGI tests aren't slower than before.
Remote-TGI tests (anything going over a proxy / real internet) should set
`BATCH_WAIT_SEC=60` or higher. Not a code-path change — only the post-inference
observation window.

No other files changed.

---

## 13. Run 2 — Qwen2.5-7B-Instruct, full E2E green

**Time**: 2026-04-23 11:52 – 11:54 CST.

**Change vs Run 1c**: Pod stopped → environment variables edited:
`MODEL_ID=Qwen/Qwen2.5-7B-Instruct`, `MAX_INPUT_TOKENS=4096`,
`MAX_TOTAL_TOKENS=8192`. `MAX_TOP_N_TOKENS=256` preserved. Pod restarted.
RunPod assigned a new pod ID (`99bx2j3r91xcgh`) so HTTPS proxy URL changed to
`https://99bx2j3r91xcgh-80.proxy.runpod.net`. `/data` volume carried over;
7B weights downloaded fresh.

Script invoked with `BATCH_WAIT_SEC=90` (90 s, vs 60 s in Run 1c). The extra
30 s was a precaution for 7B token-by-token fallback; in the event, the whole
settlement completed in ~40 s, so 60 s would have been enough.

**Preflight probes at new pod:**

```
{
  "model_id": "Qwen/Qwen2.5-7B-Instruct",
  "model_sha": "a09a35458c702b33eeacc393d103063234e8bc28",
  "max_input_tokens": 4096,
  "max_total_tokens": 8192,
  "max_concurrent_requests": 128,
  "max_client_batch_size": 4,
  "version": "3.3.6-dev0"
}
```

- `/health` → HTTP 200.
- `/generate max_new_tokens=10` → `" 4\n\nYou're correct! The answer to"` in **1.73 s**
  (coherent English out of the raw endpoint — see §13.4 for why the E2E path's output
  is different).
- `/generate top_n_tokens=256` → HTTP 200 with 16 KB JSON containing the 256-element
  `top_tokens` array; confirms `MAX_TOP_N_TOKENS` env var propagated.

**Primary inference**: Task `366e9bd3dab6999736b4b3f4eb0cc55f`. 34 tokens. Latency
**2.92 s** (SDK round-trip, including primary TGI + worker teacher-force prep +
Leader/Worker handoff).

### 13.1 Verification timeline

| Time (CST) | Event |
|---|---|
| 11:52:44 | SDK sends `InferRequest task_id=366e9bd3dab69997` |
| 11:52:47 | Worker + all 4 nodes start teacher-force (`TeacherForce: prefill empty (TGI v3?), falling back to token-by-token`) |
| 11:53:04 – 11:53:05 | Worker publishes `VerifyPayload` to 3 verifiers. **~20 s** after the SDK send — most of this is the token-by-token teacher-force over 34 token positions (worker first does primary + captures logits via N sequential `/generate` calls, each roughly 500 ms over the RunPod HTTPS proxy). |
| 11:53:22 – 11:53:24 | Three verifiers publish `VerifyResult` with `pass=true`. Another **~18 s** of token-by-token work on the verifier side. |
| 11:53:24 | All 4 P2P nodes broadcast `MsgBatchSettlement` to the chain. Tx hashes: <br> `29A0E238F0ABE5754F248BA7FBF51A164CDE61AC192019DB84096B3CA0794D9B` <br> `533CD9D2E1537F393434B0F4DB9520EF4550E34A13C1D185EF869FE54D5764FC` <br> `5EA0EDB4947C05922E4786E4FD8291354B1C022B60BE7E8FC9B6A008162E9F16` <br> `7AB41957EB0A38D00C77ED00F363D6E5DB61500CF3F0B0DEC87CF617537F4207` <br> all with `entries=1 gas=202000 seq=0` |
| Chain height at end of 90 s window | 62 |

**Zero `context canceled` entries on any node.** Full settlement path ran to completion.

### 13.2 Latency breakdown

| Segment | 0.5B (Run 1c, 10 tokens) | 7B (Run 2, 34 tokens) | Notes |
|---|---:|---:|---|
| SDK send → `SUCCESS` returned | 2.23 s | 2.92 s | primary inference + coordination |
| `SUCCESS` → worker `VerifyPayload` published | 4–5 s | ~17 s | worker's own teacher-force pass; token-by-token is `O(tokens × proxy-latency)` |
| `VerifyPayload` → last `VerifyResult pass=true` | 5–8 s | ~18 s | verifier's token-by-token pass |
| last `VerifyResult` → `BatchSettlement` tx broadcast | ~1 s | ~0.5 s | proposer builds and submits |
| **SDK send → on-chain broadcast (total)** | **~13 s** | **~40 s** | |

The scaling from 10 → 34 tokens is super-linear (~3.1× latency for ~3.4× tokens), dominated
by the token-by-token fallback. This is `tgi.go:413`'s fallback path that activates because
TGI v3 returns empty `prefill` for `decoder_input_details`. If that fallback were replaced
by a single batched call, the worker and verifier teacher-force segments would both drop
from O(N × proxy-RTT) to roughly O(1 × model-forward), which for 7B on a 4090 is ~100–200 ms.
That's a pre-mainnet optimisation target (§14).

### 13.3 Script assertions

20/20 PASS. Same as Run 1c. All the cosmetic-bug caveats from §11 (Phase 6 query queries
user0 instead of depositing address; Phase 5 grep patterns don't match current log wording)
are still present in Run 2 output — they are not blocking but make the `[WARN]` / error
output noisier than the actual state of the system.

### 13.4 Output ordering artefact (worth investigating)

The 7B E2E output is:

```
" 4\n\nYou correct're! to2+ The answer 2 is 4. Well done any! questions have other you If or need further assistance feel,<|endoftext|>"
```

Read word-wise it's scrambled — `"You correct're!"` should be `"You're correct!"`, `"to2+"`
is odd, `"any! questions"` should be `"any other questions"`, etc. Yet:

- A direct `/generate` call to the same TGI endpoint with the same prompt returns perfectly
  coherent text: `" 4\n\nYou're correct! The answer to"` (see §13 preflight).
- Both worker and verifier see the identical byte sequence — their hashes match
  (`pass=true` 9 times in logs), so the 4-way consensus on the result is intact.

That contradiction is worth chasing before mainnet. Candidates:

1. **Token assembly on the worker side reorders tokens.** The worker reads TGI's response
   token IDs and text fields, then concatenates in some order that's NOT the sequence
   generated. If the token fields come in an order like "id ascending" or "position
   ascending with a hash", vs TGI's natural generation order, the result is scrambled
   text but consistent bytes. `sonic`'s warning `sonic only supports go1.17~1.23, but
   your environment is not suitable` may be a hint — JSON field ordering or array
   iteration order can be affected by JSON library.
2. **Greedy decoding (temperature=0) of a multi-turn / system-prompted model path**
   is producing the scramble, and what we see on bare `/generate` is a different path
   (no chat-template expansion?). Less likely but worth ruling out.
3. **The worker is applying a chat-template that injects system tokens which are then
   visible in the output**, e.g. tokens around assistant/system role markers. The
   `<|endoftext|>` in the E2E output vs clean completion on bare `/generate` could be
   a clue — the bare call doesn't hit an EOT token in 10 tokens, but E2E saw one.

The fact that the 0.5B Run 1c output `"  4\n\nThe answer is 4.<|endoftext|>"` was
coherent is interesting — either 0.5B's tokenisation is trivial enough to not expose
the bug, or 0.5B uses a different path. Needs reproduction on a second prompt to confirm
it's systematic rather than a one-off.

**Not a blocker for the smoke verdict** (consensus held, settlement happened) but a
blocker for user-facing output quality. Logged as §14 P0.

---

## 14. Remaining work

Ordered by priority for mainnet readiness.

### P0 — must fix before mainnet

| # | Issue | Evidence | Next step |
|---|---|---|---|
| 14.1 | **7B E2E output is token-order-scrambled** vs bare `/generate` on the same endpoint/model — see §13.4 | `sdk_client_run2.log` `Output: " 4\n\nYou correct're! to2+ The answer 2 is 4. ..."` |  Reproduce with a different prompt and different seed; diff worker's assembled output against a direct-TGI `/generate` byte-for-byte; inspect SDK/worker code that reassembles TGI's tokens; audit `sonic` JSON compatibility on Go 1.25 |
| 14.2 | **Token-by-token teacher-force fallback is O(N)** and dominates settlement latency at 7B output sizes — §13.2 shows 34 tokens take ~35 s across worker+verifier | `p2p/inference/tgi.go:413` fallback triggered on every teacher-force call because TGI v3 returns empty `prefill` | Investigate whether TGI 3.3.6 has a way to request prefill that actually returns logits (separate parameter / older API surface), or whether the FunAI side should move to a single `/generate max_new_tokens=0 decoder_input_details=true` on the *completed prompt+output* — i.e. a true teacher-force pass rather than N step-by-step generations |

### P1 — should fix before mainnet

| # | Issue | Evidence | Next step |
|---|---|---|---|
| 14.3 | **4 P2P nodes each broadcast an independent `MsgBatchSettlement` for the same task** — wasteful on chain, and the failure mode of the 3 losers is uncertain | Run 1c and Run 2 each show 4 tx hashes at the exact same timestamp, all `entries=1` | Confirm on-chain what happens to the 3 losing txs: are they rejected with a duplicate-merkle-root error (clean), do they double-pay rewards, or do they sit in mempool? If losers are cleanly rejected, add a proposer-election heuristic so only the elected proposer submits |
| 14.4 | **Script preflight never probes TGI at `top_n_tokens=256`** — Run 1a wasted a full 3-minute test cycle because the preflight probe only tests `=5` | `scripts/e2e-real-inference.sh:262-284` | Add a `top_n_tokens=256` probe that fails fast on 422 |

### P2 — cosmetic / quality-of-life

| # | Issue | Next step |
|---|---|---|
| 14.5 | Phase 6 queries `user0`'s inference account, Phase 2 deposits to `validator0`'s address → `inference account not found` in output on every run | Derive a single user-address constant and use it consistently |
| 14.6 | Phase 5 grep patterns for `receipt for task` / `verified task` / `verify result` don't match actual log wording → bogus `[WARN]` lines on every run including 1c and 2 | Update the grep patterns to match `dispatch: receipt for task` / `dispatch: verify result for task` |
| 14.7 | `--no-cleanup` preserves `/tmp/funai-e2e-real` but not the env-var snapshot used to start the run → repro is awkward | Dump a `run-config.env` alongside the testnet dir |
| 14.8 | `sonic` warning `sonic only supports go1.17~1.23, but your environment is not suitable` fires on every binary invocation — harmless but noisy and possibly related to §14.1 | Investigate if there's a sonic-compat Go 1.25 tag or if we should vendor a patched version |

### Deferred

| # | Item | Rationale |
|---|---|---|
| 14.9 | Stress test (N concurrent tasks, bigger prompts, longer outputs) | Smoke is green; stress is the next milestone, not blocking |
| 14.10 | Confirm 7B handles image/audio/video prompt types | Out of scope for this report — blocked by `project_multimodal_gap.md` (v1 protocol spec doesn't yet integrate multi-modal dispatch in codebase; text-locked at 7 layers) |
| 14.11 | Cross-hardware verification (same test on A100, L20, etc.) | Per `2026-04-21-v6-phase1a` — V6's A2 cross-hardware assumption — orthogonal test, already on the roadmap |

---

*End of Runs 1a / 1b / 1c (0.5B) and Run 2 (7B). Stack verified end-to-end on RunPod 4090 with real cloud TGI. Next: §14 remediation before mainnet.*
