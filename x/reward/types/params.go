package types

import (
	"cosmossdk.io/math"
	"github.com/cosmos/gogoproto/proto"
)

func init() {
	proto.RegisterType((*Params)(nil), "funai.reward.Params")
}

var (
	DefaultBaseBlockReward = math.NewInt(4_000_000_000) // 4000 FAI in ufai
	DefaultHalvingPeriod   = int64(26_250_000)
	DefaultFeeWeight       = math.LegacyNewDecWithPrec(85, 2) // 0.85 — amount-weight inside each pool
	DefaultCountWeight     = math.LegacyNewDecWithPrec(15, 2) // 0.15 — count-weight inside each pool
	DefaultEpochBlocks     = int64(100)
	DefaultTotalSupply     = math.NewInt(210_000_000_000_000_000) // 210B FAI in ufai

	// Block reward pool split (matches inference fee split 85/12/3):
	//   85% inference pool   — distributed to workers by fee*0.85 + count*0.15
	//   12% verifier pool    — distributed to verifiers+2nd/3rd-verifiers by fee*0.85 + count*0.15
	//    3% multi-verification fund (a.k.a. audit fund) — minted into settlement module account;
	//                          paid out per-epoch to 2nd/3rd-verifiers via DistributeMultiVerificationFund.
	DefaultInferenceWeight             = math.LegacyNewDecWithPrec(85, 2) // 0.85
	DefaultVerificationWeight          = math.LegacyNewDecWithPrec(12, 2) // 0.12
	DefaultMultiVerificationFundWeight = math.LegacyNewDecWithPrec(3, 2)  // 0.03

	BondDenom = "ufai"
)

type Params struct {
	BaseBlockReward             math.Int       `protobuf:"bytes,1,opt,name=base_block_reward,proto3" json:"base_block_reward"`
	HalvingPeriod               int64          `protobuf:"varint,2,opt,name=halving_period,proto3" json:"halving_period"`
	FeeWeight                   math.LegacyDec `protobuf:"bytes,3,opt,name=fee_weight,proto3" json:"fee_weight"`
	CountWeight                 math.LegacyDec `protobuf:"bytes,4,opt,name=count_weight,proto3" json:"count_weight"`
	EpochBlocks                 int64          `protobuf:"varint,5,opt,name=epoch_blocks,proto3" json:"epoch_blocks"`
	TotalSupply                 math.Int       `protobuf:"bytes,6,opt,name=total_supply,proto3" json:"total_supply"`
	InferenceWeight             math.LegacyDec `protobuf:"bytes,7,opt,name=inference_weight,proto3" json:"inference_weight"`
	VerificationWeight          math.LegacyDec `protobuf:"bytes,8,opt,name=verification_weight,proto3" json:"verification_weight"`
	MultiVerificationFundWeight math.LegacyDec `protobuf:"bytes,9,opt,name=multi_verification_fund_weight,proto3" json:"multi_verification_fund_weight"`
}

func (m *Params) ProtoMessage()  {}
func (m *Params) Reset()         { *m = Params{} }
func (m *Params) String() string { return "reward.Params" }

func DefaultParams() Params {
	return Params{
		BaseBlockReward:             DefaultBaseBlockReward,
		HalvingPeriod:               DefaultHalvingPeriod,
		FeeWeight:                   DefaultFeeWeight,
		CountWeight:                 DefaultCountWeight,
		EpochBlocks:                 DefaultEpochBlocks,
		TotalSupply:                 DefaultTotalSupply,
		InferenceWeight:             DefaultInferenceWeight,
		VerificationWeight:          DefaultVerificationWeight,
		MultiVerificationFundWeight: DefaultMultiVerificationFundWeight,
	}
}

func (p Params) Validate() error {
	if p.BaseBlockReward.IsNegative() {
		return ErrInvalidParams.Wrap("base block reward must be non-negative")
	}
	if p.HalvingPeriod <= 0 {
		return ErrInvalidParams.Wrap("halving period must be positive")
	}
	if p.FeeWeight.IsNegative() || p.FeeWeight.GT(math.LegacyOneDec()) {
		return ErrInvalidParams.Wrap("fee weight must be between 0 and 1")
	}
	if p.CountWeight.IsNegative() || p.CountWeight.GT(math.LegacyOneDec()) {
		return ErrInvalidParams.Wrap("count weight must be between 0 and 1")
	}
	if p.EpochBlocks <= 0 {
		return ErrInvalidParams.Wrap("epoch blocks must be positive")
	}
	if p.InferenceWeight.IsNegative() || p.InferenceWeight.GT(math.LegacyOneDec()) {
		return ErrInvalidParams.Wrap("inference weight must be between 0 and 1")
	}
	if p.VerificationWeight.IsNegative() || p.VerificationWeight.GT(math.LegacyOneDec()) {
		return ErrInvalidParams.Wrap("verification weight must be between 0 and 1")
	}
	if p.MultiVerificationFundWeight.IsNegative() || p.MultiVerificationFundWeight.GT(math.LegacyOneDec()) {
		return ErrInvalidParams.Wrap("audit fund weight must be between 0 and 1")
	}
	poolSum := p.InferenceWeight.Add(p.VerificationWeight).Add(p.MultiVerificationFundWeight)
	if !poolSum.Equal(math.LegacyOneDec()) {
		return ErrInvalidParams.Wrapf("inference + verification + multi_verification_fund weights must sum to 1, got %s", poolSum.String())
	}
	weightSum := p.FeeWeight.Add(p.CountWeight)
	if !weightSum.Equal(math.LegacyOneDec()) {
		return ErrInvalidParams.Wrapf("fee + count weights must sum to 1, got %s", weightSum.String())
	}
	return nil
}
