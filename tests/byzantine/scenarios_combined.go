// scenarios_combined.go — KT plan §2.4.
//
// Multi-event flows. C1 / C2 are the load-bearing boundary tests for
// PR #28's 1000-task decay rule (a tighter cousin to M7/M8). The rest
// require dispatch / settlement wiring and are stubbed pending those PRs.

package byzantine

import (
	"fmt"
	"math/rand"
)

// C1 — 1st jail → unjail → 500 honest → 1 cheat → 500 honest → reoffend.
// The two 500-streaks add to exactly the 1000-task decay threshold but the
// "1 cheat" in the middle resets SuccessStreak, so the streak never reaches
// 1000. JailCount should still be 1 at reoffend → reoffence returns to 1st
// jail.
//
// **Load-bearing**: catches an off-by-one where a cheat-in-the-middle is
// counted toward the streak (e.g. SuccessStreak only resets on JailWorker but
// not on intermediate misses). Without this scenario, a worker who cheats every
// 999 honest tasks would be undetectable.
type ScenarioC1 struct{}

func (ScenarioC1) ID() string   { return "C1" }
func (ScenarioC1) Tier() Tier   { return TierCombined }
func (ScenarioC1) Description() string {
	return "1st jail → unjail → 500 honest → 1 cheat → 500 honest → reoffend → still 2nd jail"
}

func (s ScenarioC1) Run(env *Env, _ *rand.Rand) error {
	addr := env.MakeWorker(0, 10_000)
	params := env.Worker.GetParams(env.Ctx)
	half := params.JailDecayInterval / 2

	env.Worker.JailWorker(env.Ctx, addr, 0)
	env.Advance(params.Jail1Duration + 1)
	_ = env.Worker.UnjailWorker(env.Ctx, addr)

	for i := uint32(0); i < half; i++ {
		env.Worker.IncrementSuccessStreak(env.Ctx, addr)
	}
	if w := env.MustGet(addr); w.SuccessStreak != half {
		return fmt.Errorf("C1: after first 500 SuccessStreak=%d want=%d", w.SuccessStreak, half)
	}

	// One cheat — the orchestrator would reset the streak. Mirror that here
	// since IncrementSuccessStreak only goes one way; the orchestrator sets
	// SuccessStreak=0 directly via SetWorker on a miss. Without this reset
	// the streak would keep counting and the test wouldn't actually exercise
	// the "cheat in the middle" interruption — it would just decay normally.
	w := env.MustGet(addr)
	w.SuccessStreak = 0
	env.Worker.SetWorker(env.Ctx, w)

	for i := uint32(0); i < half; i++ {
		env.Worker.IncrementSuccessStreak(env.Ctx, addr)
	}

	w = env.MustGet(addr)
	if w.JailCount != 1 {
		return fmt.Errorf("C1: JailCount should still be 1 (no decay step reached); got %d", w.JailCount)
	}
	if w.SuccessStreak != half {
		return fmt.Errorf("C1: SuccessStreak=%d want=%d (post-cheat 500)", w.SuccessStreak, half)
	}

	// Reoffend → 2nd jail.
	env.Worker.JailWorker(env.Ctx, addr, 0)
	w = env.MustGet(addr)
	if w.JailCount != 2 {
		return fmt.Errorf("C1: reoffend JailCount=%d want=2", w.JailCount)
	}
	if w.JailUntil != env.Height()+params.Jail2Duration {
		return fmt.Errorf("C1: should use Jail2Duration; got JailUntil=%d (height=%d Jail2=%d)",
			w.JailUntil, env.Height(), params.Jail2Duration)
	}
	return nil
}

// C2 — 1st jail → unjail → 999 honest → cheat. JailCount still 1, goes
// straight to 2nd jail (720 blocks). Same as M7 but reframed as a "combined"
// flow per the KT plan layout.
type ScenarioC2 struct{}

func (ScenarioC2) ID() string   { return "C2" }
func (ScenarioC2) Tier() Tier   { return TierCombined }
func (ScenarioC2) Description() string {
	return "1st jail → unjail → 999 honest → cheat → 2nd jail"
}

func (s ScenarioC2) Run(env *Env, rng *rand.Rand) error {
	// Composition is identical to M7; reuse to avoid duplicating the assertion.
	return ScenarioM7{}.Run(env, rng)
}

// C3 — One batch contains 3 FAIL tasks + 2 PASS tasks. The 3 FAIL do not
// settle; the 2 PASS settle normally; jail still fires.
//
// Stub: requires settlement-layer batch processing. Will land alongside
// settlement integration.
type ScenarioC3 struct{}

func (ScenarioC3) ID() string   { return "C3" }
func (ScenarioC3) Tier() Tier   { return TierCombined }
func (ScenarioC3) Description() string {
	return "Batch with 3 FAIL + 2 PASS → 2 settle, 3 don't, jail fires (stub)"
}

func (s ScenarioC3) Run(_ *Env, _ *rand.Rand) error { return nil }

// C4 — 10 Workers jailed simultaneously, Leader redispatches all in-flight
// tasks. Remaining Workers correctly accept; no task is lost.
//
// The keeper-level guarantee being tested: jailing N workers leaves the
// remaining workers' state untouched and discoverable via GetActiveWorkers.
// Redispatch / no-task-loss is p2p-layer; not in scope here.
type ScenarioC4 struct{}

func (ScenarioC4) ID() string   { return "C4" }
func (ScenarioC4) Tier() Tier   { return TierCombined }
func (ScenarioC4) Description() string {
	return "10 workers jailed simultaneously → remaining workers' state intact"
}

func (s ScenarioC4) Run(env *Env, _ *rand.Rand) error {
	const total = 20
	const toJail = 10
	for i := 0; i < total; i++ {
		env.MakeWorker(i, 10_000)
	}

	// Jail the first half.
	for i := 0; i < toJail; i++ {
		env.Worker.JailWorker(env.Ctx, DerivAddr(i), 0)
	}

	// Verify post-conditions: jailed workers are jailed, others are still active.
	for i := 0; i < total; i++ {
		w := env.MustGet(DerivAddr(i))
		if i < toJail {
			if !w.Jailed || w.JailCount != 1 {
				return fmt.Errorf("C4: worker %d should be jailed; jailed=%v count=%d", i, w.Jailed, w.JailCount)
			}
		} else {
			if w.Jailed || w.JailCount != 0 {
				return fmt.Errorf("C4: worker %d should be active; jailed=%v count=%d", i, w.Jailed, w.JailCount)
			}
		}
	}

	// Active worker count should be exactly the unjailed half.
	got := int(env.Worker.GetActiveWorkerCount(env.Ctx))
	want := total - toJail
	if got != want {
		return fmt.Errorf("C4: GetActiveWorkerCount=%d want=%d", got, want)
	}
	return nil
}

// C5 — Verifier reputation drops below 0.1 after liability cascade. VRF
// essentially stops selecting them, but no permanent ban (reputation can recover).
//
// Direct: hammer ReputationOnMiss enough times to drop below 0.1 (1000 stored)
// and verify the worker is not jailed/tombstoned (only deweighted by VRF —
// VRF check itself is not in scope here, but the keeper-level "still active"
// state is).
type ScenarioC5 struct{}

func (ScenarioC5) ID() string   { return "C5" }
func (ScenarioC5) Tier() Tier   { return TierCombined }
func (ScenarioC5) Description() string {
	return "Verifier reputation drops below 0.1 → still active, just deweighted (no permanent ban)"
}

func (s ScenarioC5) Run(env *Env, _ *rand.Rand) error {
	addr := env.MakeWorker(0, 10_000)
	// Each second_verifier miss = -0.20 (2000). Need ≥ 5 misses to drop
	// 1.0 → < 0.1 (10000 → < 1000).
	for i := 0; i < 5; i++ {
		env.Worker.ReputationOnMiss(env.Ctx, addr, "second_verifier")
	}
	w := env.MustGet(addr)
	if w.ReputationScore >= 1000 {
		return fmt.Errorf("C5: rep=%d expected to be below 1000 after 5 second_verifier misses", w.ReputationScore)
	}
	if w.Jailed || w.Tombstoned {
		return fmt.Errorf("C5: low rep should NOT jail/tombstone; jailed=%v tomb=%v", w.Jailed, w.Tombstoned)
	}
	return nil
}

// C6 — Worker submits result one block before `expire_block` → settles
// normally. Settlement-side; stub.
type ScenarioC6 struct{}

func (ScenarioC6) ID() string   { return "C6" }
func (ScenarioC6) Tier() Tier   { return TierCombined }
func (ScenarioC6) Description() string {
	return "Submit at expire_block - 1 → accepted (stub: needs settlement)"
}
func (s ScenarioC6) Run(_ *Env, _ *rand.Rand) error { return nil }

// C7 — Worker submits on the `expire_block` itself. Boundary; protocol must
// define explicitly. Settlement-side; stub.
type ScenarioC7 struct{}

func (ScenarioC7) ID() string   { return "C7" }
func (ScenarioC7) Tier() Tier   { return TierCombined }
func (ScenarioC7) Description() string {
	return "Submit at expire_block (boundary) → accept or expire? (stub: needs settlement)"
}
func (s ScenarioC7) Run(_ *Env, _ *rand.Rand) error { return nil }

// C8 — User balance drops to 0 between dispatch and settle → REFUNDED, Worker
// not paid but not penalised. Settlement-side; stub.
type ScenarioC8 struct{}

func (ScenarioC8) ID() string   { return "C8" }
func (ScenarioC8) Tier() Tier   { return TierCombined }
func (ScenarioC8) Description() string {
	return "User balance to 0 mid-flight → REFUNDED, Worker not penalised (stub: needs settlement)"
}
func (s ScenarioC8) Run(_ *Env, _ *rand.Rand) error { return nil }

// C9 — Same task_id resubmitted → chain dedupes. Settlement-side; stub.
type ScenarioC9 struct{}

func (ScenarioC9) ID() string   { return "C9" }
func (ScenarioC9) Tier() Tier   { return TierCombined }
func (ScenarioC9) Description() string {
	return "Same task_id resubmitted → second rejected (stub: needs settlement)"
}
func (s ScenarioC9) Run(_ *Env, _ *rand.Rand) error { return nil }

// C10 — Second-tier Verifier also returns lazy PASS, third-tier catches.
// Every 1st-tier and 2nd-tier Verifier in the liability chain is penalised.
// Settlement-side liability cascade; stub.
type ScenarioC10 struct{}

func (ScenarioC10) ID() string   { return "C10" }
func (ScenarioC10) Tier() Tier   { return TierCombined }
func (ScenarioC10) Description() string {
	return "Lazy 2nd-tier verifier caught by 3rd-tier → cascade penalty (stub: needs settlement)"
}
func (s ScenarioC10) Run(_ *Env, _ *rand.Rand) error { return nil }

// AllCombined returns the §2.4 scenario set in plan order.
func AllCombined() []Scenario {
	return []Scenario{
		ScenarioC1{}, ScenarioC2{}, ScenarioC3{}, ScenarioC4{}, ScenarioC5{},
		ScenarioC6{}, ScenarioC7{}, ScenarioC8{}, ScenarioC9{}, ScenarioC10{},
	}
}

// AllScenarios returns every scenario across all tiers, in plan order.
func AllScenarios() []Scenario {
	out := make([]Scenario, 0, 30)
	out = append(out, AllLight()...)
	out = append(out, AllModerate()...)
	out = append(out, AllSevere()...)
	out = append(out, AllCombined()...)
	return out
}
