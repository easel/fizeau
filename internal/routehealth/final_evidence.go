package routehealth

import (
	"strings"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
)

// FinalEvidenceMode controls whether a harness final may infer route-health
// failure evidence from legacy diagnostic text.
type FinalEvidenceMode uint8

const (
	// FinalEvidenceTypedOnly admits failed finals only when their adapter
	// supplied a stable failure class. It is the safe zero-value mode.
	FinalEvidenceTypedOnly FinalEvidenceMode = iota
	// FinalEvidenceAllowLegacyText permits classless legacy/native finals to
	// infer a stable failure class from their error diagnostic.
	FinalEvidenceAllowLegacyText
)

// AttemptFromFinal converts one delivered harness final into route-health
// evidence. Successful finals are admitted without a failure class. Failed
// finals require a stable typed class unless legacy text inference is
// explicitly enabled. Unknown modes reject all finals.
func AttemptFromFinal(final harnesses.FinalData, mode FinalEvidenceMode) (Attempt, bool) {
	if mode != FinalEvidenceTypedOnly && mode != FinalEvidenceAllowLegacyText {
		return Attempt{}, false
	}
	actual := final.RoutingActual
	if actual == nil {
		return Attempt{}, false
	}
	status := strings.TrimSpace(final.Status)
	harness := strings.TrimSpace(actual.Harness)
	provider := strings.TrimSpace(actual.Provider)
	if status == "" || (harness == "" && provider == "") {
		return Attempt{}, false
	}

	failureClass := strings.ToLower(strings.TrimSpace(actual.FailureClass))
	if failureClass == "" && mode == FinalEvidenceAllowLegacyText {
		failureClass = classifyLegacyFailure(final.Error)
	}
	attempt := Attempt{
		Harness:        harness,
		Provider:       provider,
		Model:          strings.TrimSpace(actual.Model),
		ServerInstance: strings.TrimSpace(actual.ServerInstance),
		Status:         status,
		Reason:         failureClass,
		Error:          strings.TrimSpace(final.Error),
	}
	if final.DurationMS > 0 {
		attempt.Duration = time.Duration(final.DurationMS) * time.Millisecond
	}
	if base, endpoint, ok := splitProviderRef(attempt.Provider); ok {
		attempt.Provider = base
		attempt.Endpoint = endpoint
	}
	if Succeeded(strings.ToLower(status)) {
		return attempt, true
	}
	if !IsFeedbackFailureClass(failureClass) {
		return Attempt{}, false
	}
	return attempt, true
}

// IsFeedbackFailureClass reports whether class is stable route-selection
// feedback. Unknown and semantic task failures are deliberately excluded.
func IsFeedbackFailureClass(class string) bool {
	switch strings.ToLower(strings.TrimSpace(class)) {
	case "availability", "protocol", "transport", "credential_invalid", "quota_exhausted":
		return true
	default:
		return false
	}
}

// IsDispatchFailureClass reports whether class can describe dispatch
// mechanics. Credential and quota failures affect route selection but must
// not independently mark an endpoint unreachable.
func IsDispatchFailureClass(class string) bool {
	switch strings.ToLower(strings.TrimSpace(class)) {
	case "availability", "protocol", "transport":
		return true
	default:
		return false
	}
}

func classifyLegacyFailure(errorText string) string {
	message := strings.ToLower(strings.TrimSpace(errorText))
	switch {
	case message == "":
		return ""
	case strings.Contains(message, "no provider configured"),
		strings.Contains(message, "not available"),
		strings.Contains(message, "exhausted"),
		strings.Contains(message, "not configured"),
		strings.Contains(message, "binary not found"):
		return "availability"
	case strings.Contains(message, "timeout"),
		strings.Contains(message, "deadline"),
		strings.Contains(message, "connection"),
		strings.Contains(message, "refused"),
		strings.Contains(message, "no such host"),
		strings.Contains(message, "transport"),
		strings.Contains(message, "dial tcp"),
		strings.Contains(message, "network is unreachable"),
		strings.Contains(message, "no route to host"),
		strings.Contains(message, "i/o timeout"):
		return "transport"
	case strings.Contains(message, "http "),
		strings.Contains(message, "status "),
		strings.Contains(message, "bad request"),
		strings.Contains(message, "unauthorized"),
		strings.Contains(message, "not found"),
		strings.Contains(message, "unsupported"):
		return "protocol"
	default:
		return ""
	}
}
