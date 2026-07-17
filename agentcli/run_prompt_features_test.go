package agentcli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/easel/fizeau"
)

func TestBuildServiceExecuteRequest_ForwardsPromptFeatures(t *testing.T) {
	req := buildServiceExecuteRequest(serviceExecuteRequestParams{
		Prompt:                "Reply with OK.",
		SystemPrompt:          "System prompt",
		Harness:               "codex",
		Policy:                "default",
		SelectedProvider:      "local",
		RequestedModel:        "qwen3.5-27b",
		ResolvedModel:         "Qwen3.5-27B-MLX-8bit",
		EstimatedPromptTokens: 321,
		RequiresTools:         true,
	})

	if req.Harness != "codex" {
		t.Fatalf("Harness=%q, want codex", req.Harness)
	}
	if req.Model != "Qwen3.5-27B-MLX-8bit" {
		t.Fatalf("Model=%q, want resolved model", req.Model)
	}
	if req.Provider != "local" {
		t.Fatalf("Provider=%q, want local", req.Provider)
	}
	if req.EstimatedPromptTokens != 321 {
		t.Fatalf("EstimatedPromptTokens=%d, want 321", req.EstimatedPromptTokens)
	}
	if !req.RequiresTools {
		t.Fatal("RequiresTools=false, want true")
	}
}

func TestRun_PassesPromptFeaturesIntoServiceRequest(t *testing.T) {
	oldExecuteViaService := executeViaServiceFn
	t.Cleanup(func() {
		executeViaServiceFn = oldExecuteViaService
	})

	var gotReq fizeau.ServiceExecuteRequest
	executeViaServiceFn = func(ctx context.Context, req fizeau.ServiceExecuteRequest, selection providerSelection, logDir string, serviceConfig fizeau.ServiceConfig) (cliExecutionResult, error) {
		gotReq = req
		return cliExecutionResult{Status: "success"}, nil
	}

	configureRunEnv(t, "test-model")

	workDir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := Run(Options{
		Args: []string{
			"run",
			"--harness", "codex",
			"--policy", "default",
			"--max-iter", "1",
			"--json",
			"--work-dir", workDir,
			"-p", "Reply with OK.",
		},
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if code != 0 {
		t.Fatalf("Run exit = %d, want 0; stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if gotReq.Harness != "codex" {
		t.Fatalf("Harness=%q, want codex", gotReq.Harness)
	}
	if gotReq.Policy != "default" {
		t.Fatalf("Policy=%q, want default", gotReq.Policy)
	}
	if gotReq.Model != "" {
		t.Fatalf("Model=%q, want empty for service-side codex routing", gotReq.Model)
	}
	if gotReq.Provider != "" {
		t.Fatalf("Provider=%q, want empty for service-side codex routing", gotReq.Provider)
	}
	if gotReq.EstimatedPromptTokens <= 0 {
		t.Fatalf("EstimatedPromptTokens=%d, want > 0", gotReq.EstimatedPromptTokens)
	}
	if gotReq.RequiresTools {
		t.Fatal("RequiresTools=true, want false for prompt-only codex run")
	}
}

func TestRun_PassesToolRequirementIntoServiceRequest(t *testing.T) {
	oldExecuteViaService := executeViaServiceFn
	t.Cleanup(func() {
		executeViaServiceFn = oldExecuteViaService
	})

	var gotReq fizeau.ServiceExecuteRequest
	executeViaServiceFn = func(ctx context.Context, req fizeau.ServiceExecuteRequest, selection providerSelection, logDir string, serviceConfig fizeau.ServiceConfig) (cliExecutionResult, error) {
		gotReq = req
		return cliExecutionResult{Status: "success"}, nil
	}

	configureRunEnv(t, "test-model")

	workDir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := Run(Options{
		Args: []string{
			"run",
			"--harness", "codex",
			"--policy", "default",
			"--anchors",
			"--work-dir", workDir,
			"-p", "Reply with OK.",
		},
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if code != 0 {
		t.Fatalf("Run exit = %d, want 0; stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !gotReq.RequiresTools {
		t.Fatal("RequiresTools=false, want true when explicit tool support is enabled")
	}
}

func TestRun_JSONResolveRouteFailureDoesNotReportStaleSelection(t *testing.T) {
	oldNewService := newServiceFn
	t.Cleanup(func() {
		newServiceFn = oldNewService
	})

	newServiceFn = func(opts fizeau.ServiceOptions) (fizeau.FizeauService, error) {
		return stubFizeauService{
			executeFn: func(ctx context.Context, req fizeau.ServiceExecuteRequest) (<-chan fizeau.ServiceEvent, error) {
				ch := make(chan fizeau.ServiceEvent, 1)
				ch <- finalServiceEvent(t, fizeau.ServiceFinalData{
					Status: "failed",
					Error:  "ResolveRoute: no live provider supports prompt of 3 tokens with tools=false at policy default",
				})
				close(ch)
				return ch, nil
			},
		}, nil
	}

	req := buildServiceExecuteRequest(serviceExecuteRequestParams{
		Prompt:                "Reply with OK.",
		Harness:               "codex",
		Policy:                "default",
		SelectedProvider:      "lmstudio",
		SelectedRoute:         "qwen3.5-27b",
		ResolvedModel:         "qwen3.5-27b",
		EstimatedPromptTokens: 3,
	})
	result, err := executeViaService(context.Background(), req, providerSelection{
		Route:         "qwen3.5-27b",
		Provider:      "lmstudio",
		ResolvedModel: "qwen3.5-27b",
	}, "", nil)
	if err != nil {
		t.Fatalf("executeViaService error: %v", err)
	}
	if result.Status != "failed" {
		t.Fatalf("Status=%q, want failed", result.Status)
	}

	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if strings.Contains(string(raw), "qwen3.5-27b") {
		t.Fatalf("result JSON should not report stale resolved model: %s", string(raw))
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal result JSON: %v\njson=%s", err, string(raw))
	}
	for _, key := range []string{"selected_provider", "selected_route", "resolved_model", "model"} {
		if _, ok := payload[key]; ok {
			t.Fatalf("result JSON unexpectedly included %q: %s", key, string(raw))
		}
	}
}

func configureRunEnv(t *testing.T, model string) {
	t.Helper()
	t.Setenv("FIZEAU_PROVIDER", "lmstudio")
	t.Setenv("FIZEAU_BASE_URL", "http://127.0.0.1:1/v1")
	t.Setenv("FIZEAU_MODEL", model)
	t.Setenv("FIZEAU_SKILLS_DIR", "-")
}

func finalServiceEvent(t *testing.T, payload any) fizeau.ServiceEvent {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return fizeau.ServiceEvent{
		Type:     "final",
		Sequence: 1,
		Time:     time.Unix(1, 0).UTC(),
		Data:     raw,
	}
}

type stubFizeauService struct {
	executeFn func(context.Context, fizeau.ServiceExecuteRequest) (<-chan fizeau.ServiceEvent, error)
}

func (s stubFizeauService) Execute(ctx context.Context, req fizeau.ServiceExecuteRequest) (<-chan fizeau.ServiceEvent, error) {
	return s.executeFn(ctx, req)
}

func (stubFizeauService) Continue(context.Context, fizeau.ServiceContinuationRequest) (<-chan fizeau.ServiceEvent, error) {
	panic("unexpected Continue call")
}

func (stubFizeauService) PreparePortableRuntime(context.Context, fizeau.PortableRuntimeRequest) (*fizeau.PortableRuntimeBundle, error) {
	panic("unexpected PreparePortableRuntime call")
}

func (stubFizeauService) TailSessionLog(context.Context, string) (<-chan fizeau.ServiceEvent, error) {
	panic("unexpected TailSessionLog call")
}

func (stubFizeauService) ListHarnesses(context.Context) ([]fizeau.HarnessInfo, error) {
	panic("unexpected ListHarnesses call")
}

func (stubFizeauService) ListProviders(context.Context) ([]fizeau.ProviderInfo, error) {
	panic("unexpected ListProviders call")
}

func (stubFizeauService) ListModels(context.Context, fizeau.ModelFilter) ([]fizeau.ModelInfo, error) {
	panic("unexpected ListModels call")
}

func (stubFizeauService) ListPolicies(context.Context) ([]fizeau.PolicyInfo, error) {
	panic("unexpected ListPolicies call")
}

func (stubFizeauService) HealthCheck(context.Context, fizeau.HealthTarget) error {
	panic("unexpected HealthCheck call")
}

func (stubFizeauService) ResolveRoute(context.Context, fizeau.RouteRequest) (*fizeau.RouteDecision, error) {
	panic("unexpected ResolveRoute call")
}

func (stubFizeauService) RecordRouteAttempt(context.Context, fizeau.RouteAttempt) error {
	panic("unexpected RecordRouteAttempt call")
}

func (stubFizeauService) RouteStatus(context.Context) (*fizeau.RouteStatusReport, error) {
	panic("unexpected RouteStatus call")
}

func (stubFizeauService) UsageReport(context.Context, fizeau.UsageReportOptions) (*fizeau.UsageReport, error) {
	panic("unexpected UsageReport call")
}

func (stubFizeauService) ListSessionLogs(context.Context) ([]fizeau.SessionLogEntry, error) {
	panic("unexpected ListSessionLogs call")
}

func (stubFizeauService) WriteSessionLog(context.Context, string, io.Writer) error {
	panic("unexpected WriteSessionLog call")
}

func (stubFizeauService) ReplaySession(context.Context, string, io.Writer) error {
	panic("unexpected ReplaySession call")
}
