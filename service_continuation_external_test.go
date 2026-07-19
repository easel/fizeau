package fizeau_test

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	fizeau "github.com/easel/fizeau"
)

func TestServiceContinuationPublicSurfaceExternal(t *testing.T) {
	if fizeau.ContinuationRequireResume != "require_resume" ||
		fizeau.ContinuationPreferResume != "prefer_resume" ||
		fizeau.ContinuationFreshSession != "fresh_session" {
		t.Fatal("continuation policy wire values changed")
	}
	if fizeau.ContinuationResumed != "resumed" || fizeau.ContinuationFresh != "fresh" {
		t.Fatal("continuation disposition wire values changed")
	}

	logDir := t.TempDir()
	svc, err := fizeau.New(fizeau.ServiceOptions{
		ServiceConfig:       &stubServiceConfig{},
		QuotaRefreshContext: canceledPublicRefreshContext(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	request := fizeau.ServiceContinuationRequest{
		SessionID: "parent-fizeau-session",
		Prompt:    "continue the work",
		Policy:    fizeau.ContinuationPreferResume,
		FreshRequest: fizeau.ServiceExecuteRequest{
			SessionLogDir: logDir,
		},
		Metadata:      map[string]string{"bead_id": "fizeau-d33230fa"},
		CorrelationID: "continuation-surface",
	}

	if events, err := svc.Continue(context.Background(), fizeau.ServiceContinuationRequest{
		SessionID: request.SessionID,
		Prompt:    request.Prompt,
		Policy:    fizeau.ContinuationPolicy("future_policy"),
	}); events != nil || !errors.Is(err, fizeau.ErrContinuationPolicyInvalid) {
		t.Fatalf("invalid policy = (%v, %v), want nil/%v", events, err, fizeau.ErrContinuationPolicyInvalid)
	}
	if events, err := svc.Continue(context.Background(), fizeau.ServiceContinuationRequest{
		Prompt: request.Prompt,
		Policy: fizeau.ContinuationFreshSession,
	}); events != nil || !errors.Is(err, fizeau.ErrContinuationSessionUnavailable) {
		t.Fatalf("missing parent = (%v, %v), want nil/%v", events, err, fizeau.ErrContinuationSessionUnavailable)
	}
	if events, err := svc.Continue(context.Background(), request); events != nil || !errors.Is(err, fizeau.ErrContinuationSessionUnavailable) {
		t.Fatalf("missing parent continuation = (%v, %v), want nil/%v", events, err, fizeau.ErrContinuationSessionUnavailable)
	}

	t.Run("require resume rejects every nonzero fresh request", func(t *testing.T) {
		zeroTemperature := float32(0)
		for _, test := range []struct {
			name  string
			fresh fizeau.ServiceExecuteRequest
		}{
			{name: "prompt", fresh: fizeau.ServiceExecuteRequest{Prompt: "inner"}},
			{name: "empty nonnil metadata", fresh: fizeau.ServiceExecuteRequest{Metadata: map[string]string{}}},
			{name: "empty nonnil tools", fresh: fizeau.ServiceExecuteRequest{Tools: []fizeau.Tool{}}},
			{name: "zero pointer", fresh: fizeau.ServiceExecuteRequest{Temperature: &zeroTemperature}},
			{name: "zero policy pointer", fresh: fizeau.ServiceExecuteRequest{StallPolicy: &fizeau.StallPolicy{}}},
			{name: "session log path", fresh: fizeau.ServiceExecuteRequest{SessionLogDir: logDir}},
		} {
			t.Run(test.name, func(t *testing.T) {
				events, err := svc.Continue(context.Background(), fizeau.ServiceContinuationRequest{
					SessionID:    request.SessionID,
					Prompt:       request.Prompt,
					Policy:       fizeau.ContinuationRequireResume,
					FreshRequest: test.fresh,
				})
				if events != nil || err == nil || err.Error() != "continuation FreshRequest must be zero for require_resume" {
					t.Fatalf("nonzero FreshRequest = (%v, %v), want nil ordinary validation error", events, err)
				}
				for _, continuationErr := range []error{
					fizeau.ErrContinuationPolicyInvalid,
					fizeau.ErrContinuationSessionUnavailable,
					fizeau.ErrContinuationUnsupported,
				} {
					if errors.Is(err, continuationErr) {
						t.Fatalf("FreshRequest validation error unexpectedly classifies as %v", continuationErr)
					}
				}
			})
		}
	})

	t.Run("fresh policies reuse Execute preflight", func(t *testing.T) {
		invalid := []struct {
			name   string
			mutate func(*fizeau.ServiceExecuteRequest)
			want   string
		}{
			{name: "negative max tokens", mutate: func(req *fizeau.ServiceExecuteRequest) { req.MaxTokens = -1 }, want: "invalid MaxTokens -1"},
			{name: "invalid cache policy", mutate: func(req *fizeau.ServiceExecuteRequest) { req.CachePolicy = "future" }, want: "invalid CachePolicy"},
			{name: "negative power", mutate: func(req *fizeau.ServiceExecuteRequest) { req.MinPower = -1 }, want: "invalid MinPower -1"},
			{name: "inverted power bounds", mutate: func(req *fizeau.ServiceExecuteRequest) { req.MinPower, req.MaxPower = 8, 7 }, want: "invalid power bounds"},
			{name: "invalid role", mutate: func(req *fizeau.ServiceExecuteRequest) { req.Role = "BadRole" }, want: "invalid Role"},
		}
		for _, policy := range []fizeau.ContinuationPolicy{fizeau.ContinuationPreferResume, fizeau.ContinuationFreshSession} {
			for _, test := range invalid {
				t.Run(string(policy)+"/"+test.name, func(t *testing.T) {
					fresh := fizeau.ServiceExecuteRequest{SessionLogDir: logDir}
					test.mutate(&fresh)
					events, err := svc.Continue(context.Background(), fizeau.ServiceContinuationRequest{
						SessionID:     request.SessionID,
						Prompt:        request.Prompt,
						Policy:        policy,
						FreshRequest:  fresh,
						CorrelationID: "outer-valid",
					})
					if events != nil || err == nil || !strings.Contains(err.Error(), test.want) {
						t.Fatalf("invalid effective FreshRequest = (%v, %v), want nil error containing %q", events, err, test.want)
					}
				})
			}
		}
	})

	t.Run("outer overrides are effective before validation", func(t *testing.T) {
		for _, policy := range []fizeau.ContinuationPolicy{fizeau.ContinuationPreferResume, fizeau.ContinuationFreshSession} {
			t.Run(string(policy), func(t *testing.T) {
				events, err := svc.Continue(context.Background(), fizeau.ServiceContinuationRequest{
					SessionID: request.SessionID,
					Prompt:    request.Prompt,
					Policy:    policy,
					FreshRequest: fizeau.ServiceExecuteRequest{
						Prompt:        "discarded inner prompt",
						Metadata:      map[string]string{"role": "INVALID INNER METADATA"},
						CorrelationID: "invalid inner correlation id",
						SessionLogDir: logDir,
					},
					Metadata:      map[string]string{"source": "outer"},
					CorrelationID: "outer-valid",
				})
				if events != nil || !errors.Is(err, fizeau.ErrContinuationSessionUnavailable) {
					t.Fatalf("valid overrides = (%v, %v), want nil/%v", events, err, fizeau.ErrContinuationSessionUnavailable)
				}
			})
		}

		events, err := svc.Continue(context.Background(), fizeau.ServiceContinuationRequest{
			SessionID:     request.SessionID,
			Prompt:        request.Prompt,
			Policy:        fizeau.ContinuationFreshSession,
			CorrelationID: "invalid outer correlation id",
		})
		var correlationErr *fizeau.CorrelationIDNormalizationError
		if events != nil || !errors.As(err, &correlationErr) {
			t.Fatalf("invalid outer CorrelationID = (%v, %T %v), want nil typed normalization error", events, err, err)
		}
	})

	t.Run("valid envelopes require completed parent lineage", func(t *testing.T) {
		for _, policy := range []fizeau.ContinuationPolicy{
			fizeau.ContinuationRequireResume,
			fizeau.ContinuationPreferResume,
			fizeau.ContinuationFreshSession,
		} {
			events, err := svc.Continue(context.Background(), fizeau.ServiceContinuationRequest{
				SessionID:     request.SessionID,
				Prompt:        request.Prompt,
				Policy:        policy,
				Metadata:      map[string]string{"source": "outer"},
				CorrelationID: "outer-valid",
			})
			if events != nil || !errors.Is(err, fizeau.ErrContinuationSessionUnavailable) {
				t.Fatalf("valid %s envelope = (%v, %v), want nil/%v", policy, events, err, fizeau.ErrContinuationSessionUnavailable)
			}
		}
	})

	entries, err := os.ReadDir(logDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("rejected continuation created session artifacts: %v", entries)
	}

	for _, value := range []any{
		fizeau.ServiceContinuationRequest{},
		fizeau.ServiceFinalData{},
		fizeau.DrainExecuteResult{},
		fizeau.SessionStartData{},
		fizeau.SessionEndData{},
	} {
		typ := reflect.TypeOf(value)
		for i := 0; i < typ.NumField(); i++ {
			name := strings.ToLower(typ.Field(i).Name)
			if strings.Contains(name, "native") && strings.Contains(name, "session") {
				t.Fatalf("public %s leaks native session field %q", typ.Name(), typ.Field(i).Name)
			}
		}
	}
}
