package types

import "fmt"

// Params defines the parameters for the modelreg module per V5.2 frozen parameters.
type Params struct {
	ActivationStakeRatio  float64 `protobuf:"fixed64,1,opt,name=activation_stake_ratio,proto3" json:"activation_stake_ratio"`
	ServiceStakeRatio     float64 `protobuf:"fixed64,2,opt,name=service_stake_ratio,proto3" json:"service_stake_ratio"`
	MinEligibleWorkers    uint32  `protobuf:"varint,3,opt,name=min_eligible_workers,proto3" json:"min_eligible_workers"`
	MinUniqueOperators    uint32  `protobuf:"varint,4,opt,name=min_unique_operators,proto3" json:"min_unique_operators"`
	MinServiceWorkerCount uint32  `protobuf:"varint,5,opt,name=min_service_worker_count,proto3" json:"min_service_worker_count"`
}

func (m *Params) ProtoMessage()  {}
func (m *Params) Reset()         { *m = Params{} }
func (m *Params) String() string { return "modelreg.Params" }

func DefaultParams() Params {
	return Params{
		ActivationStakeRatio:  2.0 / 3.0,
		ServiceStakeRatio:     2.0 / 3.0, // Audit KT §6: anti-sybil, same as activation
		MinEligibleWorkers:    4,
		MinUniqueOperators:    4,
		MinServiceWorkerCount: 10,
	}
}

func (p Params) Validate() error {
	if p.ActivationStakeRatio <= 0 || p.ActivationStakeRatio > 1 {
		return fmt.Errorf("activation_stake_ratio must be in (0, 1], got %f", p.ActivationStakeRatio)
	}
	if p.ServiceStakeRatio <= 0 || p.ServiceStakeRatio > 1 {
		return fmt.Errorf("service_stake_ratio must be in (0, 1], got %f", p.ServiceStakeRatio)
	}
	if p.MinEligibleWorkers == 0 {
		return fmt.Errorf("min_eligible_workers must be > 0")
	}
	if p.MinUniqueOperators == 0 {
		return fmt.Errorf("min_unique_operators must be > 0")
	}
	return nil
}
