package fizeau_test

import (
	"encoding/json"
	"testing"
	"time"

	fizeau "github.com/easel/fizeau"
)

// TestPublicQuotaAndAccountStatusJSONContract pins the consumer-visible
// projection without constructing the private harness snapshot that feeds it.
// The same-package structural fixture test owns that adapter seam.
func TestPublicQuotaAndAccountStatusJSONContract(t *testing.T) {
	capturedAt := time.Date(2026, 5, 14, 17, 0, 0, 0, time.UTC)
	quotaPayload, err := json.Marshal(fizeau.QuotaState{
		CapturedAt: capturedAt,
		Fresh:      true,
		Source:     "quota cache",
		Status:     "ok",
	})
	if err != nil {
		t.Fatalf("Marshal(QuotaState): %v", err)
	}
	var quota map[string]any
	if err := json.Unmarshal(quotaPayload, &quota); err != nil {
		t.Fatalf("Unmarshal(QuotaState): %v", err)
	}
	for _, key := range []string{"windows", "captured_at", "fresh", "source", "status"} {
		if _, ok := quota[key]; !ok {
			t.Errorf("QuotaState JSON missing %q: %s", key, quotaPayload)
		}
	}
	for _, privateKey := range []string{"routing_preference", "RoutingPreference", "detail", "account"} {
		if _, ok := quota[privateKey]; ok {
			t.Errorf("QuotaState JSON leaked private key %q: %s", privateKey, quotaPayload)
		}
	}

	accountPayload, err := json.Marshal(fizeau.AccountStatus{
		Authenticated: true,
		Email:         "user@example.com",
		PlanType:      "subscription",
		Source:        "account cache",
		CapturedAt:    capturedAt,
		Fresh:         true,
		Detail:        "cached evidence",
	})
	if err != nil {
		t.Fatalf("Marshal(AccountStatus): %v", err)
	}
	var account map[string]any
	if err := json.Unmarshal(accountPayload, &account); err != nil {
		t.Fatalf("Unmarshal(AccountStatus): %v", err)
	}
	for _, key := range []string{"Authenticated", "Email", "PlanType", "Source", "CapturedAt", "Fresh", "Detail"} {
		if _, ok := account[key]; !ok {
			t.Errorf("AccountStatus JSON missing %q: %s", key, accountPayload)
		}
	}
}
