package types

import "cosmossdk.io/errors"

var (
	ErrAccountNotFound            = errors.Register(ModuleName, 2, "inference account not found")
	ErrInsufficientBalance        = errors.Register(ModuleName, 3, "insufficient balance")
	ErrTaskAlreadySettled         = errors.Register(ModuleName, 5, "task already settled")
	ErrTaskNotFound               = errors.Register(ModuleName, 6, "task record not found")
	ErrSignatureExpired           = errors.Register(ModuleName, 7, "signature expired past expire_block")
	ErrInvalidSignature           = errors.Register(ModuleName, 8, "invalid signature")
	ErrFraudMarked                = errors.Register(ModuleName, 10, "task marked as fraud")
	ErrInvalidSettlement          = errors.Register(ModuleName, 11, "invalid settlement data")
	ErrUnauthorized               = errors.Register(ModuleName, 12, "unauthorized")
	ErrSecondVerificationMismatch = errors.Register(ModuleName, 13, "audit verification mismatch")
	ErrDisputeNotReady            = errors.Register(ModuleName, 15, "dispute wait period not elapsed")
	ErrWrongDenom                 = errors.Register(ModuleName, 16, "wrong denomination, expected "+DefaultDenom)
)
