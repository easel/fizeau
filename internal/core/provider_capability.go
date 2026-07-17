package core

import (
	"errors"
	"fmt"
)

// ErrProviderCapabilityMissing is the sentinel for a provider/server reporting
// that a feature or capacity required to serve the request is unavailable on
// the active route (for example, mlx_lm's "NotImplementedError:
// RotatingKVCache Quantization NYI" or a residual context overflow). The
// condition is deterministic for the same route and request, so the agent loop
// must not retry without an effective compaction or changed caller constraints.
var ErrProviderCapabilityMissing = errors.New("agent: provider capability missing")

// ProviderCapabilityMissingErrorCode is the stable string code carried on
// ProviderCapabilityMissingError.Code so external callers can pattern-match
// without depending on Go type identity.
const ProviderCapabilityMissingErrorCode = "PROVIDER_CAPABILITY_MISSING"

// ProviderCapabilityContextCapacity identifies a provider/model route whose
// accepted request still exceeds the upstream server's context capacity.
const ProviderCapabilityContextCapacity = "context_capacity"

// ProviderCapabilityMissingError reports that an upstream provider rejected
// the request because the selected route cannot supply a required feature or
// capacity under the current request and configuration. It carries a stable
// Code, the extracted Capability name (when one can be parsed out of the server
// message), and the raw ServerMessage so logs and tests can inspect either.
//
// Use errors.As to extract Code/Capability/ServerMessage in routing or
// telemetry; errors.Is(err, ErrProviderCapabilityMissing) matches without
// requiring callers to know the concrete struct.
type ProviderCapabilityMissingError struct {
	// Code is the stable error code; always equals ProviderCapabilityMissingErrorCode.
	Code string
	// Capability is the missing capability name extracted from the server
	// message (e.g. "RotatingKVCache Quantization"). Empty when the upstream
	// message did not name a recognizable capability.
	Capability string
	// ServerMessage is the raw error message returned by the upstream server.
	ServerMessage string
	// Cause is the underlying transport error, preserved for chained Unwrap
	// callers that want the original SDK error.
	Cause error
}

func (e *ProviderCapabilityMissingError) Error() string {
	if e.Capability != "" {
		return fmt.Sprintf("agent: provider capability missing: server cannot serve request (%s); upstream said: %s",
			e.Capability, e.ServerMessage)
	}
	return fmt.Sprintf("agent: provider capability missing: upstream said: %s", e.ServerMessage)
}

// Unwrap reports the sentinel so errors.Is(err, ErrProviderCapabilityMissing)
// works through wrapping. Use UnwrapCause for the underlying transport error.
func (e *ProviderCapabilityMissingError) Unwrap() error {
	return ErrProviderCapabilityMissing
}

// UnwrapCause returns the underlying transport error captured at classification.
func (e *ProviderCapabilityMissingError) UnwrapCause() error {
	return e.Cause
}

func (e *ProviderCapabilityMissingError) Is(target error) bool {
	switch target {
	case ErrProviderCapabilityMissing:
		return true
	}
	switch target.(type) {
	case *ProviderCapabilityMissingError:
		return true
	}
	return false
}

func providerContextCapacityError(cause error) error {
	if cause == nil {
		return nil
	}
	return &ProviderCapabilityMissingError{
		Code:          ProviderCapabilityMissingErrorCode,
		Capability:    ProviderCapabilityContextCapacity,
		ServerMessage: cause.Error(),
		Cause:         cause,
	}
}
