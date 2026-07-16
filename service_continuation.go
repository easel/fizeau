package fizeau

import (
	"context"
	"errors"
	"reflect"
)

// ContinuationPolicy controls whether Continue must resume harness-owned
// conversation state or may start a fresh child session.
type ContinuationPolicy string

const (
	ContinuationRequireResume ContinuationPolicy = "require_resume"
	ContinuationPreferResume  ContinuationPolicy = "prefer_resume"
	ContinuationFreshSession  ContinuationPolicy = "fresh_session"
)

// ContinuationDisposition records what an accepted continuation actually did.
type ContinuationDisposition string

const (
	ContinuationResumed ContinuationDisposition = "resumed"
	ContinuationFresh   ContinuationDisposition = "fresh"
)

// ServiceContinuationRequest asks Fizeau to continue a completed Fizeau
// session. SessionID is always a Fizeau session ID; provider- and
// harness-native conversation identifiers remain behind the service boundary.
type ServiceContinuationRequest struct {
	SessionID     string
	Prompt        string
	Policy        ContinuationPolicy
	FreshRequest  ServiceExecuteRequest
	Metadata      map[string]string
	CorrelationID string
}

var (
	ErrContinuationPolicyInvalid      = errors.New("invalid continuation policy")
	ErrContinuationSessionUnavailable = errors.New("continuation session unavailable")
	ErrContinuationUnsupported        = errors.New("continuation unsupported")
)

// Continue validates the public continuation envelope without accepting a
// child session. Until continuation resolution and dispatch are available, a
// structurally valid request fails synchronously as unsupported and cannot
// open a session log or spawn a process.
func (s *service) Continue(_ context.Context, req ServiceContinuationRequest) (<-chan ServiceEvent, error) {
	if !validContinuationPolicy(req.Policy) {
		return nil, ErrContinuationPolicyInvalid
	}
	if req.SessionID == "" {
		return nil, ErrContinuationSessionUnavailable
	}
	if req.Prompt == "" {
		return nil, errors.New("continuation prompt is required")
	}
	switch req.Policy {
	case ContinuationRequireResume:
		if !reflect.ValueOf(req.FreshRequest).IsZero() {
			return nil, errors.New("continuation FreshRequest must be zero for require_resume")
		}
	case ContinuationPreferResume, ContinuationFreshSession:
		if err := validateServiceExecuteRequest(effectiveContinuationFreshRequest(req)); err != nil {
			return nil, err
		}
	}
	return nil, ErrContinuationUnsupported
}

func effectiveContinuationFreshRequest(req ServiceContinuationRequest) ServiceExecuteRequest {
	effective := req.FreshRequest
	effective.Prompt = req.Prompt
	effective.Metadata = req.Metadata
	effective.CorrelationID = req.CorrelationID
	return effective
}

func validContinuationPolicy(policy ContinuationPolicy) bool {
	switch policy {
	case ContinuationRequireResume, ContinuationPreferResume, ContinuationFreshSession:
		return true
	default:
		return false
	}
}
