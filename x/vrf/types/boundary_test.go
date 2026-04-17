package types_test

// Boundary and edge-case tests for the VRF module — supplementary to existing tests.

import (
	"math/big"
	"testing"

	"cosmossdk.io/math"

	"github.com/funai-wiki/funai-chain/x/vrf/types"
)

// ============================================================
// B1. Extreme stake ratio: 1000x difference in dispatch (α=1.0)
// ============================================================

func TestComputeScore_ExtremeStakeRatio_Dispatch(t *testing.T) {
	seed := []byte("extreme_ratio_test")
	pubkey := []byte("pubkey_extreme")

	scoreSmall := types.ComputeScore(seed, pubkey, math.NewInt(1), types.AlphaDispatch)
	scoreLarge := types.ComputeScore(seed, pubkey, math.NewInt(1000), types.AlphaDispatch)

	ratio := new(big.Float).Quo(scoreSmall, scoreLarge)
	ratioFloat, _ := ratio.Float64()

	// Should be exactly 1000x for α=1.0
	if ratioFloat < 990.0 || ratioFloat > 1010.0 {
		t.Fatalf("expected ratio ~1000 for 1000x stake with α=1.0, got %f", ratioFloat)
	}
}

// ============================================================
// B2. Repeated calls same input → stable output
// ============================================================

func TestComputeScore_StabilityAcrossRepeatedCalls(t *testing.T) {
	seed := []byte("stability_test")
	pubkey := []byte("pubkey_stability")
	stake := math.NewInt(50000)

	first := types.ComputeScore(seed, pubkey, stake, types.AlphaVerification)
	for i := 0; i < 100; i++ {
		s := types.ComputeScore(seed, pubkey, stake, types.AlphaVerification)
		if s.Cmp(first) != 0 {
			t.Fatalf("iteration %d produced different score", i)
		}
	}
}

// ============================================================
// B3. All-zero-stake ranking with dispatch
// ============================================================

func TestRankWorkers_AllZeroStake(t *testing.T) {
	seed := []byte("all_zero_stake")
	workers := []types.RankedWorker{
		{Address: "w1", Pubkey: []byte("p1"), Stake: math.ZeroInt()},
		{Address: "w2", Pubkey: []byte("p2"), Stake: math.ZeroInt()},
		{Address: "w3", Pubkey: []byte("p3"), Stake: math.ZeroInt()},
	}

	ranked := types.RankWorkers(seed, workers, types.AlphaDispatch)
	if len(ranked) != 3 {
		t.Fatalf("expected 3, got %d", len(ranked))
	}

	// Should still produce a valid ordering (by raw hash)
	for i := 0; i < len(ranked)-1; i++ {
		if ranked[i].Score.Cmp(ranked[i+1].Score) > 0 {
			t.Fatalf("not sorted at index %d", i)
		}
	}
}

// ============================================================
// B4. Single worker ranking → always rank 1
// ============================================================

func TestRankWorkers_SingleWorker_AllAlphas(t *testing.T) {
	seed := []byte("single_worker_test")
	for _, alpha := range []types.VRFAlpha{types.AlphaDispatch, types.AlphaVerification, types.AlphaSecondThirdVerification} {
		workers := []types.RankedWorker{
			{Address: "w_solo", Pubkey: []byte("pub_solo"), Stake: math.NewInt(10000)},
		}
		ranked := types.RankWorkers(seed, workers, alpha)
		if len(ranked) != 1 || ranked[0].Address != "w_solo" {
			t.Fatalf("alpha=%v: single worker should be rank 1", alpha)
		}
	}
}

// ============================================================
// B5. Negative stake → should handle gracefully (no panic)
// ============================================================

func TestComputeScore_NegativeStake(t *testing.T) {
	seed := []byte("negative_stake_test")
	pubkey := []byte("pubkey_neg")

	// math.NewInt(-100) is a valid math.Int; ComputeScore should handle it
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("negative stake should not panic: %v", r)
		}
	}()

	score := types.ComputeScore(seed, pubkey, math.NewInt(-100), types.AlphaDispatch)
	// When stake is negative, BigInt will be negative, division may produce negative score
	// or it will be treated as zero — either way, should not panic
	_ = score
}

// ============================================================
// B6. Very long seed and pubkey
// ============================================================

func TestComputeScore_LongInputs(t *testing.T) {
	seed := make([]byte, 10000)
	pubkey := make([]byte, 10000)
	for i := range seed {
		seed[i] = byte(i % 256)
		pubkey[i] = byte((i + 7) % 256)
	}

	score := types.ComputeScore(seed, pubkey, math.NewInt(10000), types.AlphaDispatch)
	if score == nil {
		t.Fatal("should handle very long inputs")
	}
	if score.Sign() < 0 {
		t.Fatal("score should not be negative")
	}
}

// ============================================================
// B7. Alpha dispatch vs verification vs audit: same inputs, different scores
// ============================================================

func TestComputeScore_DifferentAlphas_DifferentScores(t *testing.T) {
	seed := []byte("alpha_compare")
	pubkey := []byte("pubkey_cmp")
	stake := math.NewInt(10000)

	s1 := types.ComputeScore(seed, pubkey, stake, types.AlphaDispatch)
	s2 := types.ComputeScore(seed, pubkey, stake, types.AlphaVerification)
	s3 := types.ComputeScore(seed, pubkey, stake, types.AlphaSecondThirdVerification)

	// All three should be different (different alpha → different division)
	if s1.Cmp(s2) == 0 {
		t.Fatal("dispatch and verification scores should differ")
	}
	if s2.Cmp(s3) == 0 {
		t.Fatal("verification and audit scores should differ")
	}
	if s1.Cmp(s3) == 0 {
		t.Fatal("dispatch and audit scores should differ")
	}
}

// ============================================================
// B8. RankWorkers with mixed zero and non-zero stakes
// ============================================================

func TestRankWorkers_MixedStakes(t *testing.T) {
	seed := []byte("mixed_stake_seed")
	workers := []types.RankedWorker{
		{Address: "w_zero1", Pubkey: []byte("pz1"), Stake: math.ZeroInt()},
		{Address: "w_big", Pubkey: []byte("pbig"), Stake: math.NewInt(1_000_000)},
		{Address: "w_zero2", Pubkey: []byte("pz2"), Stake: math.ZeroInt()},
		{Address: "w_small", Pubkey: []byte("psmall"), Stake: math.NewInt(100)},
	}

	ranked := types.RankWorkers(seed, workers, types.AlphaDispatch)
	if len(ranked) != 4 {
		t.Fatalf("expected 4, got %d", len(ranked))
	}

	// Verify sorted
	for i := 0; i < len(ranked)-1; i++ {
		if ranked[i].Score.Cmp(ranked[i+1].Score) > 0 {
			t.Fatalf("not sorted at index %d", i)
		}
	}
}

// ============================================================
// B9. Stake=1 with all alphas produces positive scores
// ============================================================

func TestComputeScore_StakeOne_AllAlphas(t *testing.T) {
	seed := []byte("stake_one_all")
	pubkey := []byte("pub_one")
	stake := math.NewInt(1)

	for _, alpha := range []types.VRFAlpha{types.AlphaDispatch, types.AlphaVerification, types.AlphaSecondThirdVerification} {
		score := types.ComputeScore(seed, pubkey, stake, alpha)
		if score == nil || score.Sign() <= 0 {
			t.Fatalf("alpha=%v: stake=1 should produce positive score", alpha)
		}
	}
}

// ============================================================
// B10. RankWorkers preserves address-to-score mapping
// ============================================================

func TestRankWorkers_PreservesMapping(t *testing.T) {
	seed := []byte("mapping_test")
	workers := []types.RankedWorker{
		{Address: "w_A", Pubkey: []byte("pA"), Stake: math.NewInt(1000)},
		{Address: "w_B", Pubkey: []byte("pB"), Stake: math.NewInt(5000)},
		{Address: "w_C", Pubkey: []byte("pC"), Stake: math.NewInt(3000)},
	}

	ranked := types.RankWorkers(seed, workers, types.AlphaVerification)

	// Verify each worker's score matches independent computation
	for _, r := range ranked {
		var pubkey []byte
		switch r.Address {
		case "w_A":
			pubkey = []byte("pA")
		case "w_B":
			pubkey = []byte("pB")
		case "w_C":
			pubkey = []byte("pC")
		}
		expected := types.ComputeScore(seed, pubkey, r.Stake, types.AlphaVerification)
		if r.Score.Cmp(expected) != 0 {
			t.Fatalf("worker %s: score mismatch after ranking", r.Address)
		}
	}
}

// ============================================================
// B11. RankWorkers determinism across different input orderings
// ============================================================

func TestRankWorkers_DeterministicOrderIndependent(t *testing.T) {
	seed := []byte("order_independent")

	workersA := []types.RankedWorker{
		{Address: "w1", Pubkey: []byte("p1"), Stake: math.NewInt(1000)},
		{Address: "w2", Pubkey: []byte("p2"), Stake: math.NewInt(5000)},
		{Address: "w3", Pubkey: []byte("p3"), Stake: math.NewInt(3000)},
	}
	workersB := []types.RankedWorker{
		{Address: "w3", Pubkey: []byte("p3"), Stake: math.NewInt(3000)},
		{Address: "w1", Pubkey: []byte("p1"), Stake: math.NewInt(1000)},
		{Address: "w2", Pubkey: []byte("p2"), Stake: math.NewInt(5000)},
	}

	rankedA := types.RankWorkers(seed, workersA, types.AlphaDispatch)
	rankedB := types.RankWorkers(seed, workersB, types.AlphaDispatch)

	for i := range rankedA {
		if rankedA[i].Address != rankedB[i].Address {
			t.Fatalf("index %d: %s vs %s — ranking should be independent of input order",
				i, rankedA[i].Address, rankedB[i].Address)
		}
	}
}

// ============================================================
// B12. ComputeScore with stake=MaxInt → no panic
// ============================================================

func TestComputeScore_MaxStake(t *testing.T) {
	seed := []byte("max_stake_test")
	pubkey := []byte("pub_max")

	// Use a very large integer (2^128)
	maxStake, _ := math.NewIntFromString("340282366920938463463374607431768211456")

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("max stake should not panic: %v", r)
		}
	}()

	for _, alpha := range []types.VRFAlpha{types.AlphaDispatch, types.AlphaVerification, types.AlphaSecondThirdVerification} {
		score := types.ComputeScore(seed, pubkey, maxStake, alpha)
		if score == nil {
			t.Fatalf("alpha=%v: max stake should produce a score", alpha)
		}
	}
}
