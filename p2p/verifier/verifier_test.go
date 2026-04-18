package verifier

import (
	"testing"
)

// E14: Guard against degenerate logits that would let a Worker+Verifier collusion
// attack pass teacher-forcing verification trivially.

func TestIsLogitsDegenerate_AllZero(t *testing.T) {
	logits := [5]float32{0, 0, 0, 0, 0}
	if !isLogitsDegenerate(logits) {
		t.Fatal("all-zero logits must be flagged as degenerate (E14 collusion attack)")
	}
}

func TestIsLogitsDegenerate_AllSameNonZero(t *testing.T) {
	// Attack variant: same nonzero constant across all 5 positions → variance = 0.
	// A legitimate model never produces identical logits at 5 independent VRF-selected
	// token positions, so this must be rejected.
	logits := [5]float32{2.5, 2.5, 2.5, 2.5, 2.5}
	if !isLogitsDegenerate(logits) {
		t.Fatal("all-same nonzero logits must be flagged as degenerate")
	}
}

func TestIsLogitsDegenerate_NearConstantBypass(t *testing.T) {
	// Sub-threshold variance — attacker adds tiny float32 noise to evade an all-zero
	// guard. Variance here is ~2e-9, below the 1e-6 threshold.
	logits := [5]float32{0.1, 0.10005, 0.09997, 0.10003, 0.09998}
	if !isLogitsDegenerate(logits) {
		t.Fatal("near-constant logits must be flagged as degenerate (low-variance bypass)")
	}
}

func TestIsLogitsDegenerate_RealisticLogprobs(t *testing.T) {
	// Typical TGI logprob values at 5 independent top-1 positions of an 8B model:
	// variance well above threshold.
	logits := [5]float32{-0.12, -3.4, -1.8, -0.05, -2.7}
	if isLogitsDegenerate(logits) {
		t.Fatal("realistic logprobs must NOT be flagged as degenerate")
	}
}

func TestIsLogitsDegenerate_SingleOutlier(t *testing.T) {
	// 4 zeros + 1 nonzero: variance = 4*(−0.2)² + 1*(0.8)² / 5 ≈ 0.192
	// Well above threshold — should NOT be flagged.
	logits := [5]float32{0, 0, 0, 0, 1.0}
	if isLogitsDegenerate(logits) {
		t.Fatal("single nonzero outlier must NOT be flagged as degenerate")
	}
}

func TestIsLogitsDegenerate_LargeMagnitudeRealistic(t *testing.T) {
	// Raw logits (not logprobs) can span a wide range. Still non-degenerate.
	logits := [5]float32{12.3, -4.1, 7.8, 0.2, -9.5}
	if isLogitsDegenerate(logits) {
		t.Fatal("large-magnitude realistic logits must NOT be flagged as degenerate")
	}
}

func TestIsLogitsDegenerate_NegativeConstant(t *testing.T) {
	// All-same negative value is still degenerate (variance 0).
	logits := [5]float32{-1.5, -1.5, -1.5, -1.5, -1.5}
	if !isLogitsDegenerate(logits) {
		t.Fatal("all-same negative logits must be flagged as degenerate")
	}
}

func TestIsLogitsDegenerate_BoundaryVariance(t *testing.T) {
	// Variance exactly around the 1e-6 threshold. Spread the 5 values so that
	// population variance is comfortably above threshold.
	logits := [5]float32{1.0, 1.002, 0.998, 1.003, 0.997}
	if isLogitsDegenerate(logits) {
		t.Fatal("logits with variance above threshold must NOT be flagged")
	}
}
