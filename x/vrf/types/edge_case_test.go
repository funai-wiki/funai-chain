package types_test

// Edge-case and boundary-condition tests for the VRF module.

import (
	"math/big"
	"testing"

	"cosmossdk.io/math"

	"github.com/funai-wiki/funai-chain/x/vrf/types"
)

// ============================================================
// 1. Very large stake values → no overflow/panic
// ============================================================

func TestComputeScore_VeryLargeStake(t *testing.T) {
	seed := []byte("large_stake_seed")
	pubkey := []byte("pubkey_large")
	// 210 billion FAI = 210_000_000_000 * 10^6 ufai
	stake := math.NewInt(210_000_000_000_000_000)

	score := types.ComputeScore(seed, pubkey, stake, types.AlphaDispatch)
	if score == nil {
		t.Fatal("score should not be nil with very large stake")
	}
	if score.Sign() < 0 {
		t.Fatal("score should not be negative")
	}
}

// ============================================================
// 2. Stake of 1 (minimum possible)
// ============================================================

func TestComputeScore_StakeOne(t *testing.T) {
	seed := []byte("min_stake_seed")
	pubkey := []byte("pubkey_min")
	stake := math.NewInt(1)

	for _, alpha := range []types.VRFAlpha{types.AlphaDispatch, types.AlphaVerification, types.AlphaSecondThirdVerification} {
		score := types.ComputeScore(seed, pubkey, stake, alpha)
		if score == nil {
			t.Fatalf("alpha=%v: score should not be nil with stake=1", alpha)
		}
		if score.Sign() < 0 {
			t.Fatalf("alpha=%v: score should not be negative", alpha)
		}
	}
}

// ============================================================
// 3. Same pubkey different seeds → different scores (randomness)
// ============================================================

func TestComputeScore_ManyDifferentSeeds_AllDifferent(t *testing.T) {
	pubkey := []byte("pubkey_fixed")
	stake := math.NewInt(10000)

	scores := make(map[string]bool)
	for i := 0; i < 100; i++ {
		seed := []byte("seed_" + string(rune(i)))
		score := types.ComputeScore(seed, pubkey, stake, types.AlphaDispatch)
		key := score.Text('e', 50)
		if scores[key] {
			t.Fatalf("collision at seed %d", i)
		}
		scores[key] = true
	}
}

// ============================================================
// 4. Alpha=0 (audit): different stakes, same score
// ============================================================

func TestComputeScore_AlphaZero_AllStakesSameScore(t *testing.T) {
	seed := []byte("alpha_zero_test")
	pubkey := []byte("pubkey_az")

	stakes := []int64{1, 100, 10000, 1_000_000, 999_999_999}
	var firstScore *big.Float
	for _, s := range stakes {
		score := types.ComputeScore(seed, pubkey, math.NewInt(s), types.AlphaSecondThirdVerification)
		if firstScore == nil {
			firstScore = score
		} else if score.Cmp(firstScore) != 0 {
			t.Fatalf("alpha=0: stake %d should give same score as stake 1", s)
		}
	}
}

// ============================================================
// 5. Alpha=1 (dispatch): 10x stake → ~10x lower score
// ============================================================

func TestComputeScore_AlphaOne_ProportionalToStake(t *testing.T) {
	seed := []byte("proportional_test")
	pubkey := []byte("pubkey_prop")

	score1 := types.ComputeScore(seed, pubkey, math.NewInt(1000), types.AlphaDispatch)
	score10 := types.ComputeScore(seed, pubkey, math.NewInt(10000), types.AlphaDispatch)

	// score = hash / stake, so score1 / score10 should be ~10
	ratio := new(big.Float).Quo(score1, score10)
	ratioFloat, _ := ratio.Float64()

	if ratioFloat < 9.0 || ratioFloat > 11.0 {
		t.Fatalf("expected ratio ~10 for 10x stake difference, got %f", ratioFloat)
	}
}

// ============================================================
// 6. Alpha=0.5 (verification): 100x stake → ~10x lower score (√100=10)
// ============================================================

func TestComputeScore_AlphaHalf_SqrtStake(t *testing.T) {
	seed := []byte("sqrt_test")
	pubkey := []byte("pubkey_sqrt")

	score1 := types.ComputeScore(seed, pubkey, math.NewInt(100), types.AlphaVerification)
	score100 := types.ComputeScore(seed, pubkey, math.NewInt(10000), types.AlphaVerification)

	ratio := new(big.Float).Quo(score1, score100)
	ratioFloat, _ := ratio.Float64()

	if ratioFloat < 8.0 || ratioFloat > 12.0 {
		t.Fatalf("expected ratio ~10 for 100x stake with √alpha, got %f", ratioFloat)
	}
}

// ============================================================
// 7. RankWorkers with many workers (100)
// ============================================================

func TestRankWorkers_LargeSet(t *testing.T) {
	seed := []byte("large_set_seed")
	workers := make([]types.RankedWorker, 100)
	for i := 0; i < 100; i++ {
		workers[i] = types.RankedWorker{
			Address: "worker_" + string(rune('A'+i%26)) + string(rune('0'+i/26)),
			Pubkey:  []byte("pub_" + string(rune(i))),
			Stake:   math.NewInt(int64(1000 + i*100)),
		}
	}

	ranked := types.RankWorkers(seed, workers, types.AlphaDispatch)
	if len(ranked) != 100 {
		t.Fatalf("expected 100 ranked workers, got %d", len(ranked))
	}

	// Verify sorted ascending
	for i := 0; i < len(ranked)-1; i++ {
		if ranked[i].Score.Cmp(ranked[i+1].Score) > 0 {
			t.Fatalf("not sorted at index %d", i)
		}
	}
}

// ============================================================
// 8. RankWorkers with equal stakes → still produces ordering (by hash)
// ============================================================

func TestRankWorkers_EqualStakes_OrderedByHash(t *testing.T) {
	seed := []byte("equal_stakes_seed")
	workers := []types.RankedWorker{
		{Address: "w_A", Pubkey: []byte("pubA"), Stake: math.NewInt(10000)},
		{Address: "w_B", Pubkey: []byte("pubB"), Stake: math.NewInt(10000)},
		{Address: "w_C", Pubkey: []byte("pubC"), Stake: math.NewInt(10000)},
		{Address: "w_D", Pubkey: []byte("pubD"), Stake: math.NewInt(10000)},
	}

	ranked := types.RankWorkers(seed, workers, types.AlphaDispatch)
	if len(ranked) != 4 {
		t.Fatalf("expected 4, got %d", len(ranked))
	}

	// Scores should all be different (different pubkeys)
	for i := 0; i < len(ranked)-1; i++ {
		if ranked[i].Score.Cmp(ranked[i+1].Score) == 0 {
			t.Fatalf("equal scores for different pubkeys at index %d (collision)", i)
		}
	}
}

// ============================================================
// 9. RankWorkers with zero stake workers
// ============================================================

func TestRankWorkers_ZeroStakeWorkers(t *testing.T) {
	seed := []byte("zero_stake_rank_seed")
	workers := []types.RankedWorker{
		{Address: "w_zero", Pubkey: []byte("pubZ"), Stake: math.ZeroInt()},
		{Address: "w_pos", Pubkey: []byte("pubP"), Stake: math.NewInt(10000)},
	}

	ranked := types.RankWorkers(seed, workers, types.AlphaDispatch)
	if len(ranked) != 2 {
		t.Fatalf("expected 2, got %d", len(ranked))
	}

	// Zero stake worker should have raw hash (no division), so potentially much higher score
	// This is fine — they'll rank lower
}

// ============================================================
// 10. Empty seed → still produces valid scores
// ============================================================

func TestComputeScore_EmptySeed(t *testing.T) {
	score := types.ComputeScore([]byte{}, []byte("pubkey"), math.NewInt(1000), types.AlphaDispatch)
	if score == nil {
		t.Fatal("empty seed should still produce a score")
	}
}

// ============================================================
// 11. Empty pubkey → still produces valid score
// ============================================================

func TestComputeScore_EmptyPubkey(t *testing.T) {
	score := types.ComputeScore([]byte("seed"), []byte{}, math.NewInt(1000), types.AlphaDispatch)
	if score == nil {
		t.Fatal("empty pubkey should still produce a score")
	}
}

// ============================================================
// 12. RankWorkers preserves all workers (no dropping)
// ============================================================

func TestRankWorkers_PreservesAllWorkers(t *testing.T) {
	seed := []byte("preserve_test")
	workers := []types.RankedWorker{
		{Address: "w1", Pubkey: []byte("p1"), Stake: math.NewInt(1)},
		{Address: "w2", Pubkey: []byte("p2"), Stake: math.NewInt(999999999)},
		{Address: "w3", Pubkey: []byte("p3"), Stake: math.NewInt(500)},
	}

	ranked := types.RankWorkers(seed, workers, types.AlphaVerification)

	addresses := make(map[string]bool)
	for _, w := range ranked {
		addresses[w.Address] = true
	}

	for _, orig := range workers {
		if !addresses[orig.Address] {
			t.Fatalf("worker %s was dropped during ranking", orig.Address)
		}
	}
}

// ============================================================
// Q14. RankWorkers with all workers filtered out (empty/nil input)
// ============================================================

func TestRankWorkers_AllFilteredOut(t *testing.T) {
	seed := []byte("filtered_out_seed")

	// Empty slice
	ranked := types.RankWorkers(seed, []types.RankedWorker{}, types.AlphaDispatch)
	if len(ranked) != 0 {
		t.Fatalf("empty input should return empty result, got %d", len(ranked))
	}

	// Nil slice
	ranked = types.RankWorkers(seed, nil, types.AlphaDispatch)
	if len(ranked) != 0 {
		t.Fatalf("nil input should return nil or empty, got %d", len(ranked))
	}

	// Try all alpha values with empty slice to ensure no panic
	for _, alpha := range []types.VRFAlpha{types.AlphaDispatch, types.AlphaVerification, types.AlphaSecondThirdVerification} {
		result := types.RankWorkers(seed, []types.RankedWorker{}, alpha)
		if len(result) != 0 {
			t.Fatalf("alpha=%v: empty input should return empty result", alpha)
		}
	}
}

// ============================================================
// Q15. Single worker: can dispatch but not enough for 3 verifiers
// ============================================================

func TestRankWorkers_SingleWorker_InsufficientForVerification(t *testing.T) {
	seed := []byte("single_worker_verify_seed")

	worker := types.RankedWorker{
		Address: "sole_worker",
		Pubkey:  []byte("sole_pub"),
		Stake:   math.NewInt(50000),
	}

	// Single worker can be dispatched (rank #1)
	dispatchRanked := types.RankWorkers(seed, []types.RankedWorker{worker}, types.AlphaDispatch)
	if len(dispatchRanked) != 1 {
		t.Fatalf("dispatch: expected 1 ranked worker, got %d", len(dispatchRanked))
	}
	if dispatchRanked[0].Address != "sole_worker" {
		t.Fatalf("dispatch: expected sole_worker, got %s", dispatchRanked[0].Address)
	}

	// After excluding the executor, 0 candidates remain for verification.
	// RankWorkers with empty slice should return empty (no panic).
	verifyRanked := types.RankWorkers(seed, []types.RankedWorker{}, types.AlphaVerification)
	if len(verifyRanked) != 0 {
		t.Fatalf("verification with 0 candidates should return empty, got %d", len(verifyRanked))
	}

	// This means we cannot select 3 verifiers — the caller must handle this.
	// Verify that taking top-3 from an insufficient pool doesn't panic.
	twoWorkers := []types.RankedWorker{
		{Address: "v1", Pubkey: []byte("vp1"), Stake: math.NewInt(10000)},
		{Address: "v2", Pubkey: []byte("vp2"), Stake: math.NewInt(20000)},
	}
	verifyRanked = types.RankWorkers(seed, twoWorkers, types.AlphaVerification)
	if len(verifyRanked) != 2 {
		t.Fatalf("expected 2 ranked verifiers, got %d", len(verifyRanked))
	}
	// Caller would see len < 3 and know verification quorum is unmet.
}
