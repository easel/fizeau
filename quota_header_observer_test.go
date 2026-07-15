package fizeau

import (
	"testing"
	"time"

	"github.com/easel/fizeau/internal/provider/quotaheaders"
)

func TestSignalObserverSharesRoutingVisibleState(t *testing.T) {
	svc := &service{providerQuota: NewProviderQuotaStateStore()}
	observer := svc.quotaSignalObserver("openai")
	if observer == nil {
		t.Fatal("expected non-nil observer when service has a quota store")
	}
	var _ func(quotaheaders.Signal) = observer

	now := time.Now().UTC()
	retryAt := now.Add(15 * time.Minute)
	observer(quotaheaders.Signal{
		Present:           true,
		RemainingRequests: 0,
		RemainingTokens:   1000,
		ResetTime:         retryAt,
	})

	exhausted := svc.providerQuotaExhaustedUntil(now)
	if got, ok := exhausted["openai"]; !ok || !got.Equal(retryAt) {
		t.Fatalf("routing-visible exhaustion = %v, want openai=%v", exhausted, retryAt)
	}
}

func TestQuotaSignalObserverWrapperDelegation(t *testing.T) {
	tests := []struct {
		name     string
		svc      *service
		provider string
	}{
		{name: "nil service", svc: nil, provider: "openai"},
		{name: "nil store", svc: &service{}, provider: "openai"},
		{name: "blank provider", svc: &service{providerQuota: NewProviderQuotaStateStore()}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.svc.quotaSignalObserver(tt.provider); got != nil {
				t.Fatalf("quotaSignalObserver(%q) = non-nil, want nil", tt.provider)
			}
		})
	}
}
