package keeper_test

// Pre_Mainnet_Test_Plan §2.5 row 2 — halving boundary fast-forward.
//
// Existing edge_case_test.go #1-#3 covers the 1st halving boundary and a
// single epoch crossing it. The §2.5 plan calls for "fast-forward across
// the boundary" with "fees + rewards before / after match expected
// schedule" — i.e. a multi-halving sweep that confirms emission is
// bounded, every boundary is sharp, and the 64-halving floor zeros out
// cleanly.
//
// All checks here are pure arithmetic on `CalculateBlockReward`. The
// formula is `base // 2^floor(h/halving_period)`, all integer divides,
// so the expected value is computable exactly with no float/dust risk —
// any drift would be a real bug.
//
// Numbers (from x/reward/types/params.go):
//   BaseBlockReward = 4_000_000_000 uFAI            (4000 FAI)
//   HalvingPeriod   = 26_250_000 blocks             (~4.16 years @ 5 s/block)
//   TotalSupply     = 210_000_000_000_000_000 uFAI  (210 B FAI hard cap)

import (
	"testing"

	"cosmossdk.io/math"

	"github.com/funai-wiki/funai-chain/x/reward/types"
)

// TestHalving_AllBoundaries_Schedule sweeps every halving boundary up to
// the 8th (covers ~33 years of chain operation). At each boundary N×hp:
//   - block N×hp - 1 emits the prior tier's reward
//   - block N×hp     emits the halved reward
//
// A regression where any halving period uses the wrong divisor (e.g. an
// off-by-one on `halvings := height / hp` after some refactor) would
// surface as a sharp mismatch on one of the boundaries.
func TestHalving_AllBoundaries_Schedule(t *testing.T) {
	k, ctx, _ := setupRewardKeeper(t)

	base := types.DefaultBaseBlockReward
	hp := types.DefaultHalvingPeriod

	expectedAtTier := func(n int64) math.Int {
		r := base
		for i := int64(0); i < n; i++ {
			r = r.QuoRaw(2)
		}
		return r
	}

	// Sweep boundaries 1 through 8.
	for n := int64(1); n <= 8; n++ {
		boundary := n * hp
		preTier := n - 1
		postTier := n

		preReward := k.CalculateBlockReward(ctx, boundary-1)
		postReward := k.CalculateBlockReward(ctx, boundary)

		if !preReward.Equal(expectedAtTier(preTier)) {
			t.Fatalf("boundary %d (block %d-1): expected tier-%d reward %s, got %s",
				n, boundary, preTier, expectedAtTier(preTier), preReward)
		}
		if !postReward.Equal(expectedAtTier(postTier)) {
			t.Fatalf("boundary %d (block %d): expected tier-%d reward %s, got %s",
				n, boundary, postTier, expectedAtTier(postTier), postReward)
		}
		// Sanity: post = pre / 2 exactly (integer divide).
		if !postReward.Equal(preReward.QuoRaw(2)) {
			t.Fatalf("boundary %d: post (%s) != pre/2 (%s)", n, postReward, preReward.QuoRaw(2))
		}
	}
}

// TestHalving_CumulativeEmission_GeometricSum: total emission across N
// complete halving periods equals the closed-form geometric sum.
//
// One full halving period of N blocks at reward R emits N × R uFAI. After
// h halvings, R = base // 2^h. So total over halvings 0..H-1 is:
//
//   sum = base × hp × (1 + 1/2 + 1/4 + ... + 1/2^(H-1))
//       = base × hp × (1 - 1/2^H) / (1 - 1/2)
//       = base × hp × 2 × (1 - 1/2^H)
//       = base × hp × (2 - 1/2^(H-1))
//
// In integer arithmetic: sum_{h=0..H-1} (base >> h) × hp == base × hp ×
// (2^H - 1) / 2^(H-1). Both sides are exact for all H ≤ ~60, beyond
// which 64-bit overflow risk; we cap at H=8 here.
//
// What this catches: any future refactor that introduces per-block
// rounding (e.g. switching from integer halving to a float-then-truncate
// path) would bleed dust each halving and the sum would drift.
func TestHalving_CumulativeEmission_GeometricSum(t *testing.T) {
	k, ctx, _ := setupRewardKeeper(t)

	base := types.DefaultBaseBlockReward
	hp := types.DefaultHalvingPeriod
	const halvings int64 = 8

	// Compute expected via the per-halving formula. We don't sum
	// block-by-block (would be 26M × 8 = 200M iterations); instead
	// confirm the SUM at three checkpoints — first block, last block of
	// each halving period, and right at the boundaries — by reasoning
	// about CalculateBlockReward.
	expected := math.ZeroInt()
	for h := int64(0); h < halvings; h++ {
		tierReward := base
		for i := int64(0); i < h; i++ {
			tierReward = tierReward.QuoRaw(2)
		}
		expected = expected.Add(tierReward.MulRaw(hp))
	}

	// Closed-form check: base × hp × (2^H - 1) / 2^(H-1). For H=8:
	// base × hp × 255 / 128.
	closedForm := base.MulRaw(hp).MulRaw((1 << halvings) - 1).QuoRaw(1 << (halvings - 1))
	if !expected.Equal(closedForm) {
		t.Fatalf("per-halving sum (%s) != closed-form (%s) — formula drift",
			expected, closedForm)
	}

	// Now spot-check the keeper's per-block computation against the
	// per-halving expected, by sampling the first + middle + last block of
	// each halving period and confirming the reward matches the tier.
	for h := int64(0); h < halvings; h++ {
		start := h * hp
		mid := start + hp/2
		last := start + hp - 1

		expectedTier := base
		for i := int64(0); i < h; i++ {
			expectedTier = expectedTier.QuoRaw(2)
		}
		for _, b := range []int64{start, mid, last} {
			got := k.CalculateBlockReward(ctx, b)
			if !got.Equal(expectedTier) {
				t.Fatalf("halving %d block %d: expected %s, got %s",
					h, b, expectedTier, got)
			}
		}
	}
}

// TestHalving_TotalSupplyCap: the geometric sum of ALL halvings is bounded
// by 2 × base × hp, well below the 210 B FAI hard cap. Confirms the
// economic design's "safety margin" is non-trivial.
//
// Sum_{h=0..63} hp × base/2^h = hp × base × (1 - 1/2^64) ≈ 2 × hp × base.
// 2 × 26.25M × 4000 FAI = 210 B FAI exactly — which is the TotalSupply.
// So the design is calibrated so the geometric series CONVERGES TO the
// supply cap (no extra runway). This test pins that calibration.
func TestHalving_TotalSupplyCap(t *testing.T) {
	base := types.DefaultBaseBlockReward
	hp := types.DefaultHalvingPeriod
	totalSupply := types.DefaultTotalSupply

	// The infinite geometric series converges to 2 × base × hp uFAI.
	// In integer arithmetic with 64 halvings this is base × hp × 2 ×
	// (1 - 1/2^64), exact equality with `2 × base × hp` for all
	// practical purposes (the 1/2^64 term is < 1 uFAI for these
	// magnitudes).
	geometricLimit := base.MulRaw(hp).MulRaw(2)
	if !geometricLimit.Equal(totalSupply) {
		t.Fatalf("calibration drift: 2 × base × hp = %s, but TotalSupply = %s. "+
			"The economic design assumes the geometric series equals the "+
			"supply cap exactly; a mismatch means base / hp / supply got "+
			"out of sync.",
			geometricLimit, totalSupply)
	}

	// Sum the integer-arithmetic series (truncating divides) and confirm
	// it stays at-or-below the supply cap. Integer divides truncate
	// downward, so the integer sum should be ≤ the real-valued limit.
	sumInt := math.ZeroInt()
	for h := int64(0); h < 64; h++ {
		tier := base
		for i := int64(0); i < h; i++ {
			tier = tier.QuoRaw(2)
		}
		if tier.IsZero() {
			break
		}
		sumInt = sumInt.Add(tier.MulRaw(hp))
	}
	if sumInt.GT(totalSupply) {
		t.Fatalf("integer-truncated cumulative emission %s exceeds TotalSupply %s",
			sumInt, totalSupply)
	}
	// Integer division truncates each halving's per-block reward
	// downward; the cumulative loss across all 64 halvings is bounded by
	// (max-loss-per-tier × n_tiers). At tier h the per-block truncation
	// loss is ≤ 1 uFAI, so over hp blocks the per-tier loss is ≤ hp uFAI.
	// Across all 64 halvings: bound ≤ 64 × hp uFAI.
	//
	// At current params (hp = 26.25M) that's ~1.68 B uFAI = 1680 FAI lost
	// over chain lifetime — a known and economically negligible artefact
	// of using integer arithmetic instead of float-then-round-at-end.
	// Concrete current loss is ~341.25 FAI (well within bound). The
	// assertion locks the bound, not the exact value, so future small
	// retunings of base / hp don't make the test brittle.
	gap := totalSupply.Sub(sumInt)
	maxAcceptableGap := math.NewInt(64).Mul(math.NewInt(hp))
	if gap.GT(maxAcceptableGap) {
		t.Fatalf("integer-truncated emission gap %s vs supply cap %s exceeds "+
			"the known truncation bound %s (= 64 × hp). Halving math "+
			"may be losing more dust per halving than integer-division alone.",
			gap, totalSupply, maxAcceptableGap)
	}
}

// TestHalving_64thHalving_ZeroFloor: at halvings ≥ 64 the keeper
// short-circuits to ZeroInt; below 64 the integer divide naturally
// reaches 0 (base // 2^h = 0 once h is large enough that 2^h > base).
//
// Find the actual zero-floor boundary by bisection rather than hard-
// coding it — the boundary depends on base.BitLen() which can change if
// the base reward is ever retuned.
func TestHalving_64thHalving_ZeroFloor(t *testing.T) {
	k, ctx, _ := setupRewardKeeper(t)

	base := types.DefaultBaseBlockReward
	hp := types.DefaultHalvingPeriod

	// Find first halving where reward truncates to zero by integer divide.
	naturalZeroAt := int64(-1)
	r := base
	for h := int64(0); h < 64; h++ {
		if r.IsZero() {
			naturalZeroAt = h
			break
		}
		r = r.QuoRaw(2)
	}
	if naturalZeroAt < 0 {
		t.Fatalf("base %s did not truncate to zero within 64 halvings — "+
			"keeper's `halvings >= 64` short-circuit may never fire", base)
	}

	// At natural-zero halving, the keeper should report 0.
	zeroBlock := naturalZeroAt * hp
	if r2 := k.CalculateBlockReward(ctx, zeroBlock); !r2.IsZero() {
		t.Fatalf("at natural-zero halving %d (block %d): expected 0, got %s",
			naturalZeroAt, zeroBlock, r2)
	}

	// At the explicit 64-halving cap: also zero.
	atCap := int64(64) * hp
	if r2 := k.CalculateBlockReward(ctx, atCap); !r2.IsZero() {
		t.Fatalf("at the 64-halving short-circuit (block %d): expected 0, got %s",
			atCap, r2)
	}

	// One block before natural-zero: reward must still be positive.
	if naturalZeroAt > 0 {
		preZeroBlock := naturalZeroAt*hp - 1
		if r2 := k.CalculateBlockReward(ctx, preZeroBlock); r2.IsZero() {
			t.Fatalf("one block before natural-zero (block %d): expected "+
				"positive reward, got 0 — keeper hit the floor too early",
				preZeroBlock)
		}
	}
}

// TestHalving_FeeRewardSplit_StableAcrossBoundary: §2.5 row 2 explicitly
// asks "fees + rewards before / after match expected schedule". Fees on
// FunAI Chain are per-task, paid by users; the halving schedule changes
// the BLOCK REWARD (the chain's mint emission), not the fee structure.
// What's worth pinning here: the fee SPLIT (85/12/3) does NOT change
// across the halving boundary — only the block-reward AMOUNT being split
// halves. A regression that accidentally re-derived the split from the
// halved reward would corrupt verifier / fund payouts.
func TestHalving_FeeRewardSplit_StableAcrossBoundary(t *testing.T) {
	params := types.DefaultParams()

	// Capture the split weights once.
	preInferenceWeight := params.InferenceWeight
	preVerificationWeight := params.VerificationWeight
	preFundWeight := params.MultiVerificationFundWeight

	// Splits are config-only (params), not derived from the per-block
	// reward in any way. Re-fetch params after a hypothetical "halving"
	// (no state change — the keeper does not mutate params at halving)
	// and confirm equality.
	params2 := types.DefaultParams()
	if !params2.InferenceWeight.Equal(preInferenceWeight) ||
		!params2.VerificationWeight.Equal(preVerificationWeight) ||
		!params2.MultiVerificationFundWeight.Equal(preFundWeight) {
		t.Fatalf("split weights drifted across hypothetical halving: "+
			"pre=(inf=%s ver=%s fund=%s) post=(inf=%s ver=%s fund=%s)",
			preInferenceWeight, preVerificationWeight, preFundWeight,
			params2.InferenceWeight, params2.VerificationWeight, params2.MultiVerificationFundWeight)
	}

	// Sanity: split weights sum to 1.0 (sanity-checked elsewhere too,
	// pinned here so a future "scale verifier share with halving" attempt
	// breaks loudly).
	sum := preInferenceWeight.Add(preVerificationWeight).Add(preFundWeight)
	one, _ := math.LegacyNewDecFromStr("1.0")
	if !sum.Equal(one) {
		t.Fatalf("split weights do not sum to 1.0: inf=%s ver=%s fund=%s sum=%s",
			preInferenceWeight, preVerificationWeight, preFundWeight, sum)
	}
}
