package types

import "fmt"

type GenesisState struct {
	Params            Params             `protobuf:"bytes,1,opt,name=params,proto3" json:"params"`
	InferenceAccounts []InferenceAccount `protobuf:"bytes,2,rep,name=inference_accounts,proto3" json:"inference_accounts"`
	BatchRecords      []BatchRecord      `protobuf:"bytes,3,rep,name=batch_records,proto3" json:"batch_records"`
}

func (m *GenesisState) ProtoMessage()  {}
func (m *GenesisState) Reset()         { *m = GenesisState{} }
func (m *GenesisState) String() string { return "settlement.GenesisState" }

func DefaultGenesis() *GenesisState {
	return &GenesisState{
		Params:            DefaultParams(),
		InferenceAccounts: []InferenceAccount{},
		BatchRecords:      []BatchRecord{},
	}
}

func (gs GenesisState) Validate() error {
	if err := gs.Params.Validate(); err != nil {
		return fmt.Errorf("invalid params: %w", err)
	}
	seenUsers := make(map[string]bool)
	for _, ia := range gs.InferenceAccounts {
		if seenUsers[ia.Address] {
			return fmt.Errorf("duplicate inference account for address %s", ia.Address)
		}
		seenUsers[ia.Address] = true
		if err := ia.Validate(); err != nil {
			return fmt.Errorf("invalid inference account for %s: %w", ia.Address, err)
		}
	}
	return nil
}
