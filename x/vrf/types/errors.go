package types

import (
	"cosmossdk.io/errors"
)

var (
	ErrInvalidParams      = errors.Register(ModuleName, 2, "invalid params")
	ErrInvalidGenesis     = errors.Register(ModuleName, 3, "invalid genesis state")
	ErrInvalidAddress     = errors.Register(ModuleName, 4, "invalid address")
	ErrInvalidVRFProof    = errors.Register(ModuleName, 5, "invalid VRF proof")
	ErrInvalidModelId     = errors.Register(ModuleName, 6, "invalid model id")
	ErrLeaderNotFound     = errors.Register(ModuleName, 7, "leader not found for model")
	ErrNotLeader          = errors.Register(ModuleName, 8, "sender is not the current leader")
	ErrInsufficientProofs = errors.Register(ModuleName, 9, "insufficient timeout proofs")
	ErrNoEligibleWorkers  = errors.Register(ModuleName, 10, "no eligible workers available")
	ErrCommitteeNotFound  = errors.Register(ModuleName, 11, "committee not found")
	ErrSeedNotFound       = errors.Register(ModuleName, 12, "VRF seed not found")
	ErrWorkerBusy         = errors.Register(ModuleName, 13, "selected worker is busy")
	ErrLeaderTimeout      = errors.Register(ModuleName, 14, "leader has timed out")
)
