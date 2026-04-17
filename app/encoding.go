package app

import (
	"fmt"

	"cosmossdk.io/x/tx/signing"
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/codec/address"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/std"
	authtx "github.com/cosmos/cosmos-sdk/x/auth/tx"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	gogoproto "github.com/cosmos/gogoproto/proto"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/cosmos/cosmos-sdk/client"
	evmtypes "github.com/cosmos/evm/x/vm/types"
)

// EncodingConfig specifies the concrete encoding types to use.
type EncodingConfig struct {
	InterfaceRegistry codectypes.InterfaceRegistry
	Codec             codec.Codec
	TxConfig          client.TxConfig
	Amino             *codec.LegacyAmino
}

// MakeEncodingConfig creates an EncodingConfig with proper address codecs for CLI usage.
func MakeEncodingConfig() EncodingConfig {
	addressCodec := address.Bech32Codec{Bech32Prefix: Bech32PrefixAccAddr}

	// Custom signer that extracts the first string field (creator/authority/proposer/etc.)
	// from the V2 dynamic message via protobuf reflection. The SDK unpacks our hand-written
	// gogoproto types into dynamicpb.Message instances, so Go interface assertions won't work.
	firstFieldSigner := func(msg proto.Message) ([][]byte, error) {
		md := msg.ProtoReflect()
		fields := md.Descriptor().Fields()
		if fields.Len() == 0 {
			return nil, fmt.Errorf("message %s has no fields", md.Descriptor().FullName())
		}
		firstField := fields.Get(0)
		if firstField.Kind() != protoreflect.StringKind {
			return nil, fmt.Errorf("first field of %s is not a string", md.Descriptor().FullName())
		}
		addrStr := md.Get(firstField).String()
		addrBz, err := addressCodec.StringToBytes(addrStr)
		if err != nil {
			return nil, err
		}
		return [][]byte{addrBz}, nil
	}

	signingOpts := signing.Options{
		AddressCodec:          addressCodec,
		ValidatorAddressCodec: address.Bech32Codec{Bech32Prefix: Bech32PrefixValAddr},
	}

	// EVM custom signers (cosmos/evm MsgEthereumTx uses a special signer)
	signingOpts.CustomGetSigners = map[protoreflect.FullName]signing.GetSignersFunc{
		evmtypes.MsgEthereumTxCustomGetSigner.MsgType: evmtypes.MsgEthereumTxCustomGetSigner.Fn,
	}

	for _, name := range []protoreflect.FullName{
		"funai.settlement.MsgDeposit",
		"funai.settlement.MsgWithdraw",
		"funai.settlement.MsgBatchSettlement",
		"funai.settlement.MsgFraudProof",
		"funai.settlement.MsgSecondVerificationResult",
		"funai.worker.MsgRegisterWorker",
		"funai.worker.MsgExitWorker",
		"funai.worker.MsgUpdateModels",
		"funai.worker.MsgStake",
		"funai.worker.MsgUnjail",
		"funai.modelreg.MsgModelProposal",
		"funai.modelreg.MsgUpdateModelStats",
		"funai.modelreg.MsgDeclareInstalled",
		"funai.vrf.MsgSubmitVRFProof",
		"funai.vrf.MsgLeaderHeartbeat",
		"funai.vrf.MsgReportLeaderTimeout",
		"funai.reward.MsgUpdateParams",
	} {
		signingOpts.DefineCustomGetSigners(name, firstFieldSigner)
	}

	interfaceRegistry, err := codectypes.NewInterfaceRegistryWithOptions(codectypes.InterfaceRegistryOptions{
		ProtoFiles:     gogoproto.HybridResolver,
		SigningOptions: signingOpts,
	})
	if err != nil {
		panic(err)
	}

	std.RegisterInterfaces(interfaceRegistry)
	authtypes.RegisterInterfaces(interfaceRegistry)
	banktypes.RegisterInterfaces(interfaceRegistry)
	stakingtypes.RegisterInterfaces(interfaceRegistry)

	ModuleBasics.RegisterInterfaces(interfaceRegistry)

	appCodec := codec.NewProtoCodec(interfaceRegistry)
	legacyAmino := codec.NewLegacyAmino()
	txCfg := authtx.NewTxConfig(appCodec, authtx.DefaultSignModes)

	return EncodingConfig{
		InterfaceRegistry: interfaceRegistry,
		Codec:             appCodec,
		TxConfig:          txCfg,
		Amino:             legacyAmino,
	}
}
