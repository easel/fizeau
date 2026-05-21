// Package openai implements a agent.Provider for any OpenAI-compatible API
// endpoint (LM Studio, Ollama, OpenAI, Azure, Groq, Together, OpenRouter).
package openai

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	agent "github.com/easel/fizeau/internal/core"
	"github.com/easel/fizeau/internal/provider/quotaheaders"
	reasoningpolicy "github.com/easel/fizeau/internal/reasoning"
	"github.com/easel/fizeau/internal/runtimesignals"
	"github.com/easel/fizeau/internal/sdk/openaicompat"
	"github.com/openai/openai-go/option"
)

// Provider implements agent.Provider for OpenAI-compatible APIs.
type Provider struct {
	client             *openaicompat.Client
	model              string
	modelPattern       string            // regex filter for auto-discovery; "" means first model
	knownModels        map[string]string // catalog-recognized model IDs (modelID → catalogRef)
	baseURL            string            // stored for lazy model discovery
	apiKey             string            // stored for lazy model discovery
	providerName       string
	providerSystem     string
	capabilities       *ProtocolCapabilities
	usageCost          func(rawUsage string) (*agent.CostAttribution, bool)
	serverAddress      string
	serverPort         int
	reasoningDefault   reasoningpolicy.Reasoning
	modelReasoningWire map[string]string
	logger             *slog.Logger

	// lazy model discovery — runs at most once per Provider instance
	discoverOnce     sync.Once
	discoverErr      error
	discoveredModels []ScoredModel // full ranked list; populated on first use when model == ""
}

// Config holds configuration for the OpenAI-compatible provider.
type Config struct {
	BaseURL      string // e.g., "http://localhost:1234/v1" for LM Studio
	APIKey       string // optional for local providers
	Model        string // e.g., "qwen3.5-7b", "gpt-4o". Empty = auto-discover.
	ProviderName string // logical provider identity; default "openai"
	// ProviderSystem is the telemetry/cost system identity. When empty, it
	// defaults to "openai". Concrete provider wrappers set their own type.
	ProviderSystem string
	ModelPattern   string // case-insensitive regex to prefer among auto-discovered models
	// KnownModels maps concrete model IDs to catalog target IDs for the
	// agent.openai surface. Models present in this map are ranked higher during
	// auto-selection. Populated by the config layer from the model catalog;
	// nil disables catalog-aware ranking.
	KnownModels map[string]string
	Headers     map[string]string // extra HTTP headers (OpenRouter, Azure, etc.)
	Reasoning   reasoningpolicy.Reasoning
	// Capabilities supplies provider-owned protocol capability claims. When nil,
	// direct openai.Provider callers use OpenAI protocol defaults.
	Capabilities *ProtocolCapabilities
	// UsageCostAttribution extracts provider-owned gateway cost metadata from
	// the raw usage object, when that provider reports one.
	UsageCostAttribution func(rawUsage string) (*agent.CostAttribution, bool)
	// ModelReasoningWire maps a concrete model ID to the catalog
	// reasoning_wire value for that model. Recognized values are "provider"
	// (default), "model_id" (model name encodes reasoning level — strip the
	// reasoning field at serialization), "none" (model has no reasoning
	// surface — reject explicit non-off requests pre-flight), "effort"
	// (always emit reasoning.effort string), and "tokens" (always emit
	// reasoning.max_tokens int). Models not listed default to "provider",
	// preserving existing behavior.
	ModelReasoningWire map[string]string
	// Logger is used for structured diagnostic logging. When nil, slog.Default() is used.
	Logger *slog.Logger
	// QuotaHeaderParser, when set, overrides the default OpenAI rate-limit
	// header parser. OpenRouter uses this to install
	// quotaheaders.ParseOpenRouter even though it goes through this
	// OpenAI-compatible provider implementation.
	QuotaHeaderParser func(http.Header, time.Time) quotaheaders.Signal
	// QuotaSignalObserver receives parsed rate-limit signals on every
	// response. The service layer wires this to the provider quota state
	// machine. Both QuotaHeaderParser and QuotaSignalObserver must be set
	// for header-driven exhaustion tracking to activate.
	QuotaSignalObserver func(quotaheaders.Signal)
}

// New creates a new OpenAI-compatible provider.
func New(cfg Config) *Provider {
	serverAddress, serverPort := endpointMetadata(cfg.BaseURL)
	providerSystem := "openai"
	if cfg.ProviderSystem != "" {
		providerSystem = cfg.ProviderSystem
	}
	providerName := cfg.ProviderName
	if providerName == "" {
		providerName = providerSystem
	}
	parser := cfg.QuotaHeaderParser
	if parser == nil && cfg.QuotaSignalObserver != nil {
		// Default to OpenAI-shaped header parsing when no explicit parser is
		// supplied. OpenRouter overrides this via Config.QuotaHeaderParser.
		parser = quotaheaders.ParseOpenAI
	}
	runtimeObserver := func(headers http.Header, latency time.Duration, callErr error) {
		runtimesignals.RecordResponse(providerName, headers, latency, providerSystem)
		if err := runtimesignals.Observe(context.Background(), providerName, providerSystem, cfg.BaseURL, headers, latency, callErr); err != nil && cfg.Logger != nil {
			cfg.Logger.Debug("runtime signal observe failed", "provider", providerName, "err", err)
		}
	}
	p := &Provider{
		client: openaicompat.NewClient(openaicompat.Config{
			BaseURL:               cfg.BaseURL,
			APIKey:                cfg.APIKey,
			Headers:               cfg.Headers,
			QuotaHeaderParser:     parser,
			QuotaSignalObserver:   cfg.QuotaSignalObserver,
			RuntimeSignalObserver: runtimeObserver,
		}),
		model:              cfg.Model,
		modelPattern:       cfg.ModelPattern,
		knownModels:        cfg.KnownModels,
		baseURL:            cfg.BaseURL,
		apiKey:             cfg.APIKey,
		providerName:       providerName,
		providerSystem:     providerSystem,
		capabilities:       cfg.Capabilities,
		usageCost:          cfg.UsageCostAttribution,
		serverAddress:      serverAddress,
		serverPort:         serverPort,
		reasoningDefault:   cfg.Reasoning,
		modelReasoningWire: cfg.ModelReasoningWire,
		logger:             cfg.Logger,
	}
	if p.logger == nil {
		p.logger = slog.Default()
	}
	return p
}

// DiscoveredModels returns the full ranked list of models discovered from the
// server's /v1/models endpoint. Returns nil if the provider has a statically
// configured model or if discovery has not yet run (i.e. no request has been
// made yet). Call EnsureDiscovered to force discovery without making a chat
// request.
func (p *Provider) DiscoveredModels() []ScoredModel {
	return p.discoveredModels
}

// EnsureDiscovered probes the server's /v1/models endpoint and caches the
// full ranked model list. It is a no-op when the provider has a statically
// configured model or when discovery has already run.
func (p *Provider) EnsureDiscovered(ctx context.Context) error {
	if p.model != "" {
		return nil
	}
	_, err := p.resolveModel(ctx)
	return err
}

// resolveModel returns the model to use for a request. If the provider was
// configured without a model it queries /v1/models once, ranks results, and
// caches both the full list and the selected model.
// Subsequent calls return the cached value without hitting the network.
func (p *Provider) resolveModel(ctx context.Context) (string, error) {
	if p.model != "" {
		return p.model, nil
	}
	p.discoverOnce.Do(func() {
		candidates, err := DiscoverModels(ctx, p.baseURL, p.apiKey)
		if err != nil {
			p.discoverErr = err
			return
		}
		ranked, err := RankModels(candidates, p.knownModels, p.modelPattern)
		if err != nil {
			p.discoverErr = err
			return
		}
		p.discoveredModels = ranked
		selected := SelectModel(ranked)
		if selected == "" {
			p.discoverErr = fmt.Errorf("openai: no models returned by %s/models", p.baseURL)
			return
		}
		p.model = selected
	})
	return p.model, p.discoverErr
}

func (p *Provider) Chat(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, opts agent.Options) (agent.Response, error) {
	model, err := p.resolveModel(ctx)
	if err != nil {
		return agent.Response{}, err
	}
	if opts.Model != "" {
		model = opts.Model
	}

	reqOpts, reasoningEmitted, err := p.compatRequestOptions(model, opts)
	if err != nil {
		return agent.Response{}, err
	}

	result, err := p.client.Chat(ctx, model, messages, tools, reqOpts)
	if err != nil {
		return agent.Response{}, fmt.Errorf("openai: %w", err)
	}

	resp := agent.Response{
		Model:        result.Model,
		Content:      result.Content,
		FinishReason: result.FinishReason,
		ToolCalls:    result.ToolCalls,
		Usage:        result.Usage,
	}
	resp.Attempt = p.attemptMetadata(model, result.Model, &agent.CostAttribution{
		Source: agent.CostSourceUnknown,
	}, reasoningEmitted)
	if cost := p.costAttribution(result.RawUsage); cost != nil {
		resp.Attempt.Cost = cost
	}
	return resp, nil
}

// SessionStartMetadata reports the broad provider identity and configured model
// that should be recorded on session.start events.
func (p *Provider) SessionStartMetadata() (string, string) {
	return p.providerName, p.model
}

// ChatStartMetadata reports the resolved provider system and upstream server
// identity known when the provider is constructed.
func (p *Provider) ChatStartMetadata() (string, string, int) {
	return p.providerSystem, p.serverAddress, p.serverPort
}

// ChatStream implements agent.StreamingProvider for token-level streaming.
func (p *Provider) ChatStream(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, opts agent.Options) (<-chan agent.StreamDelta, error) {
	model, err := p.resolveModel(ctx)
	if err != nil {
		return nil, err
	}
	if opts.Model != "" {
		model = opts.Model
	}

	reqOpts, reasoningEmitted, err := p.compatRequestOptions(model, opts)
	if err != nil {
		return nil, err
	}

	return p.client.ChatStream(ctx, model, messages, tools, reqOpts, openaicompat.StreamHooks{
		Cost: p.costAttribution,
		Attempt: func(responseModel string, cost *agent.CostAttribution) *agent.AttemptMetadata {
			return p.attemptMetadata(model, responseModel, streamAttemptCost(cost), reasoningEmitted)
		},
	})
}

// compatRequestOptions is the canonical openai-compat wire-field assembler
// and, per ADR-007 §3, the architectural home for any future
// (model_family × reasoning_state × profile) sampling-composition rule.
// Reasoning policy and the resolved sampling profile both reach this seam;
// when a profile field needs to be clipped or substituted because the
// model is in a thinking state (or vice-versa), the rule lives here — not
// in the resolver, not in the catalog.
//
// v1 ships without an active composition rule. The seeded "code" profile
// (T=0.6, top_p=0.95, top_k=20) matches Qwen3.x's published thinking-mode
// precise-coding recommendation exactly, and is also non-greedy enough to
// be safe in the non-thinking-mode case. When a future profile adds a
// value that diverges between thinking and non-thinking states for some
// family, add the clip rule here; for now, all five sampler fields pass
// through unchanged regardless of reasoning state.
func (p *Provider) compatRequestOptions(model string, opts agent.Options) (openaicompat.RequestOptions, reasoningpolicy.Reasoning, error) {
	extra, reasoningEmitted, err := p.reasoningRequestOptions(model, opts)
	if err != nil {
		return openaicompat.RequestOptions{}, "", err
	}
	temperature := opts.Temperature
	topP := opts.TopP
	if nativeOpenAIUsesDefaultSamplingOnly(p.providerSystem, model) {
		temperature = nil
		topP = nil
	}
	// Non-standard sampling fields (top_k, min_p, repetition_penalty) ride
	// as top-level body extras on OpenAI-compatible local/provider wires.
	// OpenAI proper rejects these as unknown parameters, so only send them
	// to compatibility providers that are known to accept them.
	if p.providerSystem != "openai" {
		if opts.TopK != nil {
			extra = append(extra, option.WithJSONSet("top_k", *opts.TopK))
		}
		if opts.MinP != nil {
			extra = append(extra, option.WithJSONSet("min_p", *opts.MinP))
		}
		if opts.RepetitionPenalty != nil {
			extra = append(extra, option.WithJSONSet("repetition_penalty", *opts.RepetitionPenalty))
		}
	}
	return openaicompat.RequestOptions{
		MaxTokens:         opts.MaxTokens,
		Temperature:       temperature,
		TopP:              topP,
		TopK:              opts.TopK,
		MinP:              opts.MinP,
		RepetitionPenalty: opts.RepetitionPenalty,
		Seed:              opts.Seed,
		Stop:              opts.Stop,
		ExtraOptions:      extra,
		CachePolicy:       opts.CachePolicy,
	}, reasoningEmitted, nil
}

func nativeOpenAIUsesDefaultSamplingOnly(providerSystem, model string) bool {
	if providerSystem != "openai" {
		return false
	}
	return strings.HasPrefix(strings.ToLower(model), "gpt-5")
}

func (p *Provider) attemptMetadata(requestedModel, responseModel string, cost *agent.CostAttribution, reasoningEmitted reasoningpolicy.Reasoning) *agent.AttemptMetadata {
	if cost == nil {
		cost = &agent.CostAttribution{Source: agent.CostSourceUnknown}
	}
	return &agent.AttemptMetadata{
		ProviderName:     p.providerName,
		ProviderSystem:   p.providerSystem,
		ServerAddress:    p.serverAddress,
		ServerPort:       p.serverPort,
		RequestedModel:   requestedModel,
		ResponseModel:    responseModel,
		ResolvedModel:    responseModel,
		ReasoningEmitted: string(reasoningEmitted),
		Cost:             cost,
	}
}

func (p *Provider) costAttribution(rawUsage string) *agent.CostAttribution {
	if p.usageCost == nil {
		return nil
	}
	cost, _ := p.usageCost(rawUsage)
	return cost
}

func applyReasoningAliasMap(policy reasoningpolicy.Policy, aliasMap map[string]string) (reasoningpolicy.Policy, bool) {
	if policy.Kind != reasoningpolicy.KindNamed || len(aliasMap) == 0 {
		return policy, false
	}
	mapped, ok := aliasMap[string(policy.Value)]
	if !ok || mapped == "" || mapped == string(policy.Value) {
		return policy, false
	}
	return reasoningpolicy.Policy{
		Kind:  reasoningpolicy.KindNamed,
		Value: reasoningpolicy.Reasoning(mapped),
	}, true
}

// reasoningRequestOptions builds per-request options. For thinking models
// (Qwen3, DeepSeek-R1 etc.) apply provider-specific non-standard body fields
// only when the concrete provider declares the matching wire support.
func (p *Provider) reasoningRequestOptions(model string, opts agent.Options) ([]option.RequestOption, reasoningpolicy.Reasoning, error) {
	policy, err := reasoningpolicy.Parse(opts.Reasoning)
	if err != nil {
		return nil, "", err
	}
	explicitRequest := policy.IsSet()
	if !explicitRequest {
		policy, err = reasoningpolicy.Parse(p.reasoningDefault)
		if err != nil {
			return nil, "", err
		}
	}

	if !policy.IsSet() || policy.Kind == reasoningpolicy.KindAuto {
		return nil, "", nil
	}

	// Catalog reasoning_wire metadata gates the wire shape before any
	// provider-specific encoding runs. Models flagged as reasoning_wire=none
	// have no reasoning surface at all; an explicit non-off request is a
	// catalog/wire mismatch and must surface as an error rather than be
	// silently dropped. Models flagged as reasoning_wire=model_id encode the
	// reasoning level in the model name (e.g. fixed-variant Qwen3.6) and the
	// upstream endpoint cannot honor an external reasoning toggle, so the
	// reasoning field must be stripped from the wire body.
	catalogWire := p.modelReasoningWireFor(model)
	switch catalogWire {
	case "none":
		if explicitRequest && !policy.IsExplicitOff() {
			return nil, "", fmt.Errorf("openai: model %q has reasoning_wire=none; explicit reasoning=%q is not supported", model, policy.Value)
		}
		return nil, "", nil
	case "model_id":
		return nil, "", nil
	}

	if !p.SupportsThinking() {
		if policy.IsExplicitOff() {
			return nil, reasoningpolicy.ReasoningOff, nil
		}
		if explicitRequest {
			return nil, "", fmt.Errorf("openai: reasoning=%q is not supported by provider type %q", policy.Value, p.providerSystem)
		}
		return nil, "", nil
	}

	if policy.IsExplicitOff() {
		switch p.thinkingWireFormat() {
		case ThinkingWireFormatQwen:
			// For Qwen wire format, emit enable_thinking: false for any model unless strict matching is enabled
			// and the model is not Qwen-named. Providers like llamaserver should emit the envelope regardless.
			if !isQwenModel(model) && p.strictThinkingModelMatch() {
				if explicitRequest {
					return nil, "", fmt.Errorf("openai: qwen reasoning control is not supported for model %q on provider type %q", model, p.providerSystem)
				}
				return nil, "", nil
			}
			return []option.RequestOption{
				option.WithJSONSet("chat_template_kwargs", map[string]interface{}{
					"enable_thinking": false,
				}),
			}, reasoningpolicy.ReasoningOff, nil
		case ThinkingWireFormatOpenRouter:
			return []option.RequestOption{option.WithJSONSet("reasoning", map[string]interface{}{
				"effort": "none",
			})}, reasoningpolicy.ReasoningOff, nil
		case ThinkingWireFormatOpenAIEffort:
			return []option.RequestOption{option.WithJSONSet("think", false)}, reasoningpolicy.ReasoningOff, nil
		}
		return nil, reasoningpolicy.ReasoningOff, nil
	}

	if p.thinkingWireFormat() == ThinkingWireFormatOpenRouter {
		return openRouterReasoningOptions(policy, model, catalogWire)
	}
	if p.thinkingWireFormat() == ThinkingWireFormatOpenAIEffort {
		return openAIEffortReasoningOptions(policy, p.protocolCapabilities().ReasoningAliasMap, p.logger, model, p.providerSystem)
	}
	// For Qwen wire format, only enforce strict model matching if explicitly requested by the provider.
	// Providers like llamaserver (which run arbitrary GGUF models) should emit Qwen controls for any model
	// and let the server ignore them if unsupported. L1 introspection can refine this at construction time.
	if p.thinkingWireFormat() == ThinkingWireFormatQwen && !isQwenModel(model) && p.strictThinkingModelMatch() {
		if explicitRequest {
			return nil, "", fmt.Errorf("openai: qwen reasoning control is not supported for model %q on provider type %q", model, p.providerSystem)
		}
		return nil, "", nil
	}

	// Qwen and ThinkingMap providers can only express a token budget on the
	// wire. If the catalog declares effort wire for this model, degrade
	// gracefully: snap KindTokens to the nearest PortableBudgets tier so the
	// downstream budget calculation uses a standard value. The model still
	// works; the catalog entry is simply mis-classified.
	if catalogWire == "effort" {
		p.logger.Warn("catalog declares effort wire but provider only supports budget wire",
			"model", model,
			"provider", p.providerSystem,
		)
		if policy.Kind == reasoningpolicy.KindTokens {
			tier := reasoningpolicy.NearestTierForTokens(policy.Tokens)
			policy = reasoningpolicy.Policy{Kind: reasoningpolicy.KindNamed, Value: tier}
		}
	}

	thinkingBudget, err := reasoningpolicy.BudgetFor(policy, nil, 0)
	if err != nil {
		return nil, "", fmt.Errorf("openai: %w", err)
	}
	if thinkingBudget <= 0 {
		return nil, "", nil
	}
	emitted := reasoningpolicy.ReasoningTokens(thinkingBudget)

	switch p.thinkingWireFormat() {
	case ThinkingWireFormatQwen:
		return []option.RequestOption{
			option.WithJSONSet("chat_template_kwargs", map[string]interface{}{
				"enable_thinking": true,
				"thinking_budget": thinkingBudget,
			}),
		}, emitted, nil
	case "", ThinkingWireFormatThinkingMap:
		return []option.RequestOption{option.WithJSONSet("thinking", map[string]interface{}{
			"type":          "enabled",
			"budget_tokens": thinkingBudget,
		})}, emitted, nil
	default:
		return nil, "", fmt.Errorf("openai: unsupported thinking wire format %q for provider type %q", p.thinkingWireFormat(), p.providerSystem)
	}
}

// openAIEffortReasoningOptions builds the flat top-level reasoning_effort wire
// used by ds4 / deepseek-v4-flash. Named tiers are emitted as-is; token
// budgets are snapped to the nearest PortableBudgets tier via NearestTierForTokens.
// Alias maps from live introspection are applied after snapping so ds4's
// low/medium/xhigh→high aliasing is reflected on the wire.
func openAIEffortReasoningOptions(policy reasoningpolicy.Policy, aliasMap map[string]string, logger *slog.Logger, model, providerSystem string) ([]option.RequestOption, reasoningpolicy.Reasoning, error) {
	var tier reasoningpolicy.Reasoning
	switch policy.Kind {
	case reasoningpolicy.KindTokens:
		tier = reasoningpolicy.NearestTierForTokens(policy.Tokens)
	case reasoningpolicy.KindNamed:
		tier = policy.Value
	default:
		return nil, "", fmt.Errorf("openai: unsupported OpenAI effort reasoning policy kind %q", policy.Kind)
	}
	normalized, changed := applyReasoningAliasMap(reasoningpolicy.Policy{Kind: reasoningpolicy.KindNamed, Value: tier}, aliasMap)
	if changed && logger != nil {
		logger.Warn("reasoning alias map overrode requested tier",
			"model", model,
			"provider", providerSystem,
			"reasoning_intent", string(tier),
			"reasoning_emitted", string(normalized.Value),
			"reasoning_emitted_reason", providerSystem+" alias map",
		)
		tier = normalized.Value
	}
	return []option.RequestOption{option.WithJSONSet("reasoning_effort", string(tier))}, tier, nil
}

// modelReasoningWireFor returns the catalog reasoning_wire value for the
// given model, defaulting to "provider" when the model is not catalog-known.
// Returned values are normalized: only "model_id" and "none" trigger
// catalog-driven wire overrides; everything else falls through to the
// provider's protocol capability path.
func (p *Provider) modelReasoningWireFor(model string) string {
	if p.modelReasoningWire == nil || model == "" {
		return ""
	}
	return p.modelReasoningWire[model]
}

func isQwenModel(model string) bool {
	return strings.Contains(strings.ToLower(model), "qwen")
}

// openRouterReasoningOptions builds the OpenRouter nested reasoning object.
// model is included for structured-warning logs.
// wire is the catalog reasoning_wire value for the model:
//   - "" or "provider": pick wire shape from policy.Kind (backwards-compat)
//   - "effort": always emit reasoning.effort string; snap KindTokens to nearest tier
//   - "tokens": always emit reasoning.max_tokens int; expand KindNamed via PortableBudgets
func openRouterReasoningOptions(policy reasoningpolicy.Policy, model, wire string) ([]option.RequestOption, reasoningpolicy.Reasoning, error) {
	reasoning := map[string]interface{}{}
	var emitted reasoningpolicy.Reasoning
	switch wire {
	case "effort":
		var tier reasoningpolicy.Reasoning
		switch policy.Kind {
		case reasoningpolicy.KindTokens:
			tier = reasoningpolicy.NearestTierForTokens(policy.Tokens)
		case reasoningpolicy.KindNamed:
			tier = policy.Value
			if tier == reasoningpolicy.ReasoningMax {
				tier = reasoningpolicy.ReasoningXHigh
			}
		default:
			return nil, "", fmt.Errorf("openai: unsupported OpenRouter reasoning policy %q for effort wire", policy.Kind)
		}
		effort := string(tier)
		switch effort {
		case "minimal", "low", "medium", "high", "xhigh":
			reasoning["effort"] = effort
			emitted = tier
		default:
			return nil, "", fmt.Errorf("openai: unsupported OpenRouter reasoning effort %q", tier)
		}
	case "tokens":
		var budget int
		switch policy.Kind {
		case reasoningpolicy.KindTokens:
			budget = policy.Tokens
		case reasoningpolicy.KindNamed:
			budget = reasoningpolicy.BudgetForNamed(policy.Value)
			if budget <= 0 {
				return nil, "", fmt.Errorf("openai: named reasoning %q has no portable token budget for tokens wire", policy.Value)
			}
		default:
			return nil, "", fmt.Errorf("openai: unsupported OpenRouter reasoning policy %q for tokens wire", policy.Kind)
		}
		reasoning["max_tokens"] = budget
		emitted = reasoningpolicy.ReasoningTokens(budget)
	default:
		// "" or "provider": today's behavior — pick wire shape from policy.Kind.
		switch policy.Kind {
		case reasoningpolicy.KindTokens:
			reasoning["max_tokens"] = policy.Tokens
			emitted = reasoningpolicy.ReasoningTokens(policy.Tokens)
		case reasoningpolicy.KindNamed:
			effort := string(policy.Value)
			if policy.Value == reasoningpolicy.ReasoningMax {
				effort = string(reasoningpolicy.ReasoningXHigh)
			}
			switch effort {
			case "minimal", "low", "medium", "high", "xhigh":
				reasoning["effort"] = effort
				emitted = reasoningpolicy.Reasoning(effort)
			default:
				return nil, "", fmt.Errorf("openai: unsupported OpenRouter reasoning effort %q", policy.Value)
			}
		default:
			return nil, "", fmt.Errorf("openai: unsupported OpenRouter reasoning policy %q", policy.Kind)
		}
	}
	return []option.RequestOption{option.WithJSONSet("reasoning", reasoning)}, emitted, nil
}

var _ agent.Provider = (*Provider)(nil)
var _ agent.StreamingProvider = (*Provider)(nil)

func endpointMetadata(baseURL string) (serverAddress string, serverPort int) {
	serverAddress = "api.openai.com"
	serverPort = 443

	if baseURL == "" {
		return serverAddress, serverPort
	}

	parsed, err := url.Parse(baseURL)
	if err != nil {
		return serverAddress, serverPort
	}

	host := parsed.Hostname()
	if host != "" {
		serverAddress = host
	}

	if port := parsed.Port(); port != "" {
		if parsedPort, err := strconv.Atoi(port); err == nil {
			serverPort = parsedPort
		}
	} else if strings.EqualFold(parsed.Scheme, "http") {
		serverPort = 80
	}

	return serverAddress, serverPort
}

func streamAttemptCost(cost *agent.CostAttribution) *agent.CostAttribution {
	if cost != nil {
		return cost
	}
	return &agent.CostAttribution{Source: agent.CostSourceUnknown}
}
