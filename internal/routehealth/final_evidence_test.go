package routehealth

import (
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
)

func TestFinalEvidenceSuccessAdmission(t *testing.T) {
	final := harnesses.FinalData{
		Status: " success ",
		RoutingActual: &harnesses.RoutingActual{
			Harness:  " fiz ",
			Provider: " local ",
			Model:    " qwen ",
		},
	}
	for _, mode := range []FinalEvidenceMode{FinalEvidenceTypedOnly, FinalEvidenceAllowLegacyText} {
		attempt, ok := AttemptFromFinal(final, mode)
		if !ok {
			t.Fatalf("mode %d rejected success", mode)
		}
		if attempt.Harness != "fiz" || attempt.Provider != "local" || attempt.Model != "qwen" || attempt.Status != "success" {
			t.Fatalf("mode %d attempt = %+v", mode, attempt)
		}
	}

	invalid := []harnesses.FinalData{
		{Status: "success"},
		{RoutingActual: &harnesses.RoutingActual{Harness: "fiz"}},
		{Status: "success", RoutingActual: &harnesses.RoutingActual{}},
	}
	for i, final := range invalid {
		if _, ok := AttemptFromFinal(final, FinalEvidenceTypedOnly); ok {
			t.Fatalf("invalid final %d admitted: %+v", i, final)
		}
	}
}

func TestFinalEvidenceTypedFailureClasses(t *testing.T) {
	for _, class := range []string{"availability", "protocol", "transport", "credential_invalid", "quota_exhausted", "capability"} {
		t.Run(class, func(t *testing.T) {
			attempt, ok := AttemptFromFinal(failedFinal("  "+class+"  ", "diagnostic"), FinalEvidenceTypedOnly)
			if !ok || attempt.Reason != class {
				t.Fatalf("attempt = %+v, ok=%v, want class %q", attempt, ok, class)
			}
		})
	}
}

func TestFinalEvidenceTypedOnlyRejectsClasslessAndUnknownSemanticFailures(t *testing.T) {
	for _, tc := range []struct {
		name       string
		class      string
		diagnostic string
	}{
		{name: "classless unsupported", diagnostic: "requested task is unsupported by this validator"},
		{name: "classless network prose", diagnostic: "connection reset while validating expected output"},
		{name: "unknown http prose", class: "unknown", diagnostic: "HTTP 503 while checking task output"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if attempt, ok := AttemptFromFinal(failedFinal(tc.class, tc.diagnostic), FinalEvidenceTypedOnly); ok {
				t.Fatalf("semantic failure admitted: %+v", attempt)
			}
		})
	}
}

func TestFinalEvidenceLegacyTextFallback(t *testing.T) {
	for _, tc := range []struct {
		name       string
		diagnostic string
		want       string
	}{
		{name: "availability", diagnostic: "provider binary not found", want: "availability"},
		{name: "transport", diagnostic: "dial tcp: connection refused", want: "transport"},
		{name: "protocol", diagnostic: "HTTP 502 bad gateway", want: "protocol"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			attempt, ok := AttemptFromFinal(failedFinal("", tc.diagnostic), FinalEvidenceAllowLegacyText)
			if !ok || attempt.Reason != tc.want {
				t.Fatalf("attempt = %+v, ok=%v, want %q", attempt, ok, tc.want)
			}
		})
	}
	if attempt, ok := AttemptFromFinal(failedFinal("", "validator rejected malformed output"), FinalEvidenceAllowLegacyText); ok {
		t.Fatalf("unclassified diagnostic admitted: %+v", attempt)
	}
	if attempt, ok := AttemptFromFinal(failedFinal("unknown", "dial tcp: connection refused"), FinalEvidenceAllowLegacyText); ok {
		t.Fatalf("typed unknown fell back to prose: %+v", attempt)
	}
}

func TestFinalEvidenceSplitsEndpointProviderAndPreservesServerInstance(t *testing.T) {
	final := failedFinal("protocol", "bad response framing")
	final.DurationMS = 125
	final.RoutingActual.Provider = " vendor @ primary "
	final.RoutingActual.ServerInstance = " server-a "
	attempt, ok := AttemptFromFinal(final, FinalEvidenceTypedOnly)
	if !ok {
		t.Fatal("qualified final rejected")
	}
	if attempt.Provider != "vendor" || attempt.Endpoint != "primary" || attempt.ServerInstance != "server-a" {
		t.Fatalf("route identity = %+v", attempt)
	}
	if attempt.Duration != 125*time.Millisecond {
		t.Fatalf("duration = %s, want 125ms", attempt.Duration)
	}

	final.RoutingActual.Provider = "vendor@"
	attempt, ok = AttemptFromFinal(final, FinalEvidenceTypedOnly)
	if !ok || attempt.Provider != "vendor@" || attempt.Endpoint != "" {
		t.Fatalf("invalid qualification changed identity: %+v, ok=%v", attempt, ok)
	}
}

func TestFinalEvidenceInvalidModeRejects(t *testing.T) {
	for _, final := range []harnesses.FinalData{
		{Status: "success", RoutingActual: &harnesses.RoutingActual{Harness: "fiz", Model: "qwen"}},
		failedFinal("transport", "dial tcp: refused"),
	} {
		if attempt, ok := AttemptFromFinal(final, FinalEvidenceMode(255)); ok {
			t.Fatalf("invalid mode admitted %+v", attempt)
		}
	}
}

func TestFinalEvidenceDispatchClassPredicate(t *testing.T) {
	for _, class := range []string{"availability", "protocol", "transport", " TRANSPORT "} {
		if !IsDispatchFailureClass(class) {
			t.Errorf("class %q should be dispatch evidence", class)
		}
	}
	for _, class := range []string{"", "credential_invalid", "quota_exhausted", "capability", "unknown"} {
		if IsDispatchFailureClass(class) {
			t.Errorf("class %q should not be dispatch evidence", class)
		}
	}
}

func failedFinal(class, diagnostic string) harnesses.FinalData {
	return harnesses.FinalData{
		Status: "failed",
		Error:  diagnostic,
		RoutingActual: &harnesses.RoutingActual{
			Harness:      "fiz",
			Provider:     "vendor",
			Model:        "model-a",
			FailureClass: class,
		},
	}
}
