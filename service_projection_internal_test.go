package fizeau

import (
	"testing"

	"github.com/easel/fizeau/internal/harnesses"
)

// TestQuotaStatusProjectionAdapterCoversStatesAndReason is the narrow
// same-package seam for the private CONTRACT-004-to-public quota adapter.
// Public JSON shape and full structural fixtures are covered separately.
func TestQuotaStatusProjectionAdapterCoversStatesAndReason(t *testing.T) {
	for _, state := range []harnesses.QuotaStateValue{
		harnesses.QuotaOK,
		harnesses.QuotaStale,
		harnesses.QuotaBlocked,
		harnesses.QuotaUnavailable,
		harnesses.QuotaUnauthenticated,
		harnesses.QuotaUnknown,
	} {
		t.Run(string(state), func(t *testing.T) {
			got := projectQuotaStatus(harnesses.QuotaStatus{State: state})
			if got == nil || got.Status != string(state) || got.LastError != nil {
				t.Fatalf("projectQuotaStatus(%q) = %#v", state, got)
			}
		})
	}

	if got := projectQuotaStatus(harnesses.QuotaStatus{Reason: "probe unavailable"}); got == nil || got.Status != "probe unavailable" {
		t.Fatalf("reason-only projection = %#v, want status probe unavailable", got)
	}
}

// TestAccountSnapshotProjectionAdapterPreservesUnknownAndUnauthenticated
// protects the two non-authenticated CONTRACT-004 account conventions that
// the successful structural fixtures do not exercise.
func TestAccountSnapshotProjectionAdapterPreservesUnknownAndUnauthenticated(t *testing.T) {
	unknown := projectAccountSnapshot(harnesses.AccountSnapshot{
		Source: "harness has no native account file",
		Fresh:  true,
		Detail: "no auth evidence on disk",
	})
	if unknown == nil || unknown.Authenticated || unknown.Unauthenticated || !unknown.Fresh || unknown.Detail != "no auth evidence on disk" {
		t.Fatalf("unknown account projection = %#v", unknown)
	}

	unauthenticated := projectAccountSnapshot(harnesses.AccountSnapshot{
		Unauthenticated: true,
		Source:          "credential cache",
		Detail:          "credential missing",
	})
	if unauthenticated == nil || unauthenticated.Authenticated || !unauthenticated.Unauthenticated || unauthenticated.Source != "credential cache" || unauthenticated.Detail != "credential missing" {
		t.Fatalf("unauthenticated account projection = %#v", unauthenticated)
	}
}
