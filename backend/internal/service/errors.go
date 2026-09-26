package service

import "errors"

var (
	ErrInvalidTransition = errors.New("requested status transition is not allowed")
	ErrInvalidInput      = errors.New("business input validation failed")
	ErrUnauthorized      = errors.New("invalid username or password")
	ErrInactiveUser      = errors.New("user account is inactive")
	ErrForbidden         = errors.New("role is not permitted for this operation")
	ErrLocked            = errors.New("resolved record is immutable")
	ErrProofAccepted     = errors.New("accepted proof is pinned and cannot be edited")
	ErrProofNotAccepted  = errors.New("proof has not been accepted for the current batch version")
	ErrProofStale        = errors.New("proof was pinned against an outdated batch configuration version and cannot be released")
	ErrProofWrongRun     = errors.New("proof belongs to a different print batch")
	ErrDecisionNoProof   = errors.New("a release decision must select at least one accepted proof of the current batch version")
)
