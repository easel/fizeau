package modelregistry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/easel/fizeau/internal/config"
	"github.com/easel/fizeau/internal/discoverycache"
	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/modelref"
	openaiadapter "github.com/easel/fizeau/internal/provider/openai"
	"github.com/easel/fizeau/internal/provider/utilization"
)

const (
	discoveryTTLHTTPRemote       = 24 * time.Hour
	discoveryTTLHTTPLocal        = time.Hour
	discoveryTTLPTY              = 24 * time.Hour
	discoveryRefreshDeadlineHTTP = 10 * time.Second
	discoveryRefreshDeadlinePTY  = 60 * time.Second
	limitSourceProviderAPI       = "provider_api"
	limitSourceCatalog           = "catalog"
)

type discoveredModel struct {
	Provider                  string
	ProviderType              string
	Harness                   string
	ID                        string
	CatalogID                 string
	Configured                bool
	EndpointName              string
	EndpointBaseURL           string
	ServerInstance            string
	Via                       Source
	DiscoveredAt              time.Time
	ContextWindow             int
	ContextWindowSource       string
	MaxCompletionTokens       int
	MaxCompletionTokensSource string
}

type modelDiscoveryEndpoint struct {
	Name           string
	BaseURL        string
	ServerInstance string
}

type providerDiscoveryResult struct {
	Models  []discoveredModel
	Sources map[string]SourceMeta
}

type discoveryPayload struct {
	CapturedAt                time.Time `json:"captured_at"`
	Models                    []string  `json:"models,omitempty"`
	ReasoningLevels           []string  `json:"reasoning_levels,omitempty"`
	ContextWindow             int       `json:"context_window,omitempty"`
	ContextWindowSource       string    `json:"context_window_source,omitempty"`
	MaxCompletionTokens       int       `json:"max_completion_tokens,omitempty"`
	MaxCompletionTokensSource string    `json:"max_completion_tokens_source,omitempty"`
	Source                    string    `json:"source,omitempty"`
}

func discoverProvider(ctx context.Context, providerName string, pc config.ProviderConfig, cache *discoverycache.Cache, opts AssembleOptions) providerDiscoveryResult {
	providerType := normalizeProviderType(pc.Type)
	switch providerType {
	case "claude", "codex", "grok":
		return discoverHarnessProvider(providerName, providerType, cache, opts)
	case "openai", "openrouter", "vidar-ds4", "sindri-llamacpp", "ds4", "lucebox", "lmstudio", "llama-server", "omlx", "rapid-mlx", "vllm", "ollama", "minimax", "qwen", "zai":
		result := discoverOpenAICompatibleProvider(ctx, providerName, pc, cache, opts)
		if hasPropsDiscovery(providerType) {
			result.merge(discoverPropsProvider(ctx, providerName, pc, cache, opts))
		}
		return result
	default:
		if strings.TrimSpace(pc.Model) == "" {
			return providerDiscoveryResult{Sources: map[string]SourceMeta{}}
		}
		return providerDiscoveryResult{
			Models: []discoveredModel{{
				Provider:        providerName,
				ProviderType:    providerType,
				ID:              strings.TrimSpace(pc.Model),
				EndpointName:    endpointNameForConfig(providerName, pc.BaseURL, pc.Endpoints),
				EndpointBaseURL: firstBaseURL(pc),
				ServerInstance:  firstServerInstance(pc),
				Via:             SourceNativeAPI,
				DiscoveredAt:    time.Now().UTC(),
			}},
			Sources: map[string]SourceMeta{
				providerName: {LastRefreshedAt: time.Now().UTC()},
			},
		}
	}
}

func discoverOpenAICompatibleProvider(ctx context.Context, providerName string, pc config.ProviderConfig, cache *discoverycache.Cache, opts AssembleOptions) providerDiscoveryResult {
	if cache == nil {
		return providerDiscoveryResult{Sources: map[string]SourceMeta{providerName: {Stale: true, Error: "discovery cache is nil"}}}
	}
	endpoints := discoveryEndpoints(providerName, pc)
	if len(endpoints) == 0 {
		endpoints = []modelDiscoveryEndpoint{{
			Name:           endpointNameForConfig(providerName, pc.BaseURL, nil),
			BaseURL:        firstBaseURL(pc),
			ServerInstance: firstServerInstance(pc),
		}}
	}
	result := providerDiscoveryResult{Sources: map[string]SourceMeta{}}
	for _, endpoint := range endpoints {
		endpointResult := discoverOpenAICompatibleEndpoint(ctx, providerName, providerTypeFromProviderConfig(pc), endpoint, pc.APIKey, discoveryTTLForProvider(pc), cache, opts)
		result.merge(endpointResult)
	}
	if len(result.Models) == 0 && strings.TrimSpace(pc.Model) != "" {
		fallback := modelsFromIDs([]string{pc.Model}, SourceNativeAPI, time.Now().UTC(), discoveryIdentity{
			Provider:        providerName,
			ProviderType:    providerTypeFromProviderConfig(pc),
			EndpointName:    endpointNameForConfig(providerName, pc.BaseURL, pc.Endpoints),
			EndpointBaseURL: firstBaseURL(pc),
			ServerInstance:  firstServerInstance(pc),
		})
		for i := range fallback.Models {
			fallback.Models[i].Configured = true
		}
		result.merge(fallback)
		meta := result.Sources[providerName]
		if meta.LastRefreshedAt.IsZero() {
			meta.LastRefreshedAt = time.Now().UTC()
		}
		result.Sources[providerName] = meta
	}
	return result
}

func discoverOpenAICompatibleEndpoint(ctx context.Context, providerName, providerType string, endpoint modelDiscoveryEndpoint, apiKey string, ttl time.Duration, cache *discoverycache.Cache, opts AssembleOptions) providerDiscoveryResult {
	src := discoverySource(endpointSourceName(providerName, endpoint.Name, endpoint.BaseURL, endpoint.ServerInstance), ttl, discoveryRefreshDeadlineHTTP)
	refresher := func(refreshCtx context.Context) ([]byte, error) {
		requestCtx, cancel := discoveryRequestContext(ctx, refreshCtx, opts.Refresh)
		defer cancel()
		ids, err := openaiadapter.DiscoverModels(requestCtx, strings.TrimRight(endpoint.BaseURL, "/"), apiKey)
		if err != nil {
			return nil, err
		}
		return json.Marshal(discoveryPayload{
			CapturedAt: time.Now().UTC(),
			Models:     ids,
			Source:     "openai-compatible:/v1/models",
		})
	}
	switch opts.Refresh {
	case RefreshForce:
		_ = cache.Refresh(src, refresher)
	case RefreshIfStale:
		_ = cache.MaybeRefreshSync(src, refresher)
	case RefreshNone:
		// no-op
	default:
		cache.MaybeRefresh(src, refresher)
	}
	return readDiscoveryCache(cache, src, providerName, SourceNativeAPI, discoveryIdentity{
		Provider:        providerName,
		ProviderType:    providerType,
		EndpointName:    endpoint.Name,
		EndpointBaseURL: endpoint.BaseURL,
		ServerInstance:  endpoint.ServerInstance,
	})
}

func discoverPropsProvider(ctx context.Context, providerName string, pc config.ProviderConfig, cache *discoverycache.Cache, opts AssembleOptions) providerDiscoveryResult {
	if cache == nil {
		return providerDiscoveryResult{Sources: map[string]SourceMeta{providerName + ":props": {Stale: true, Error: "discovery cache is nil"}}}
	}
	src := discoverySource(providerName+"-props", discoveryTTLHTTPLocal, discoveryRefreshDeadlineHTTP)
	refresher := func(refreshCtx context.Context) ([]byte, error) {
		requestCtx, cancel := discoveryRequestContext(ctx, refreshCtx, opts.Refresh)
		defer cancel()
		return fetchPropsDiscoveryPayload(requestCtx, firstBaseURL(pc))
	}
	switch opts.Refresh {
	case RefreshForce:
		_ = cache.Refresh(src, refresher)
	case RefreshIfStale:
		_ = cache.MaybeRefreshSync(src, refresher)
	case RefreshNone:
		// no-op
	default:
		cache.MaybeRefresh(src, refresher)
	}
	result := readDiscoveryCache(cache, src, providerName, SourcePropsAPI, discoveryIdentity{
		Provider:        providerName,
		ProviderType:    providerTypeFromProviderConfig(pc),
		EndpointName:    endpointNameForConfig(providerName, pc.BaseURL, pc.Endpoints),
		EndpointBaseURL: firstBaseURL(pc),
		ServerInstance:  firstServerInstance(pc),
	})
	renamed := make(map[string]SourceMeta, len(result.Sources))
	for _, meta := range result.Sources {
		renamed[providerName+":props"] = meta
	}
	result.Sources = renamed
	return result
}

func fetchPropsDiscoveryPayload(ctx context.Context, baseURL string) ([]byte, error) {
	if strings.TrimSpace(baseURL) == "" {
		return nil, fmt.Errorf("props discovery: base_url is required")
	}
	endpoint := utilization.ServerRoot(baseURL) + "/props"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("props discovery: build request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("props discovery: GET %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return nil, fmt.Errorf("props discovery: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("props discovery: read body: %w", err)
	}
	return json.Marshal(propsDiscoveryPayload(body, time.Now().UTC()))
}

func propsDiscoveryPayload(body []byte, capturedAt time.Time) discoveryPayload {
	details := parsePropsDiscoveryDetails(body)
	payload := discoveryPayload{
		CapturedAt:          capturedAt.UTC(),
		Models:              details.IDs,
		ReasoningLevels:     details.ReasoningLevels,
		ContextWindow:       details.ContextWindow,
		MaxCompletionTokens: details.MaxCompletionTokens,
		Source:              "props:/props",
	}
	if payload.ContextWindow > 0 {
		payload.ContextWindowSource = limitSourceProviderAPI
	}
	if payload.MaxCompletionTokens > 0 {
		payload.MaxCompletionTokensSource = limitSourceProviderAPI
	}
	return payload
}

func discoveryRequestContext(parent, refreshCtx context.Context, mode RefreshMode) (context.Context, context.CancelFunc) {
	if refreshCtx == nil {
		refreshCtx = context.Background()
	}
	if mode == RefreshBackground || parent == nil {
		return refreshCtx, func() {}
	}
	if deadline, ok := refreshCtx.Deadline(); ok {
		return context.WithDeadline(parent, deadline)
	}
	return parent, func() {}
}

type propsDiscoveryDetails struct {
	IDs                 []string
	ReasoningLevels     []string
	ContextWindow       int
	MaxCompletionTokens int
}

func parsePropsDiscovery(body []byte) ([]string, []string) {
	details := parsePropsDiscoveryDetails(body)
	return details.IDs, details.ReasoningLevels
}

func parsePropsDiscoveryDetails(body []byte) propsDiscoveryDetails {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return propsDiscoveryDetails{}
	}
	ids := make([]string, 0, 4)
	addString := func(v any) {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			ids = append(ids, strings.TrimSpace(s))
		}
	}
	addString(raw["id"])
	addString(raw["model_id"])
	if model, ok := raw["model"].(map[string]any); ok {
		addString(model["id"])
		addString(model["model_id"])
		addString(model["name"])
	}
	if runtime, ok := raw["runtime"].(map[string]any); ok {
		addString(runtime["model"])
		addString(runtime["model_id"])
	}
	// llama.cpp / lucebox-dflash props schema: the /v1/models id is a generic
	// alias (e.g. "dflash") that has no catalog power, but /props carries the
	// real model identity. Surface every identity hint so the catalog fuzzy
	// matcher can resolve it to a powered entry (e.g. "Qwen3.6 27B" -> qwen3.6-27b).
	addString(raw["model_alias"])
	if card, ok := raw["model_card"].(map[string]any); ok {
		addString(card["name"])
		if src, ok := card["source"].(string); ok {
			addString(lastPathSegment(src))
		}
	}
	if mp, ok := raw["model_path"].(string); ok {
		addString(modelNameFromPath(mp))
	}
	if server, ok := raw["server"].(map[string]any); ok && len(ids) == 0 {
		addString(server["name"])
	}
	if models, ok := raw["models"].([]any); ok {
		for _, item := range models {
			switch typed := item.(type) {
			case string:
				addString(typed)
			case map[string]any:
				addString(typed["id"])
				addString(typed["model_id"])
			}
		}
	}
	return propsDiscoveryDetails{
		IDs:                 uniqueSortedStrings(ids),
		ReasoningLevels:     parsePropsReasoningLevels(raw),
		ContextWindow:       parsePropsContextWindow(raw),
		MaxCompletionTokens: parsePropsMaxCompletionTokens(raw),
	}
}

func parsePropsContextWindow(raw map[string]any) int {
	if settings, ok := raw["default_generation_settings"].(map[string]any); ok {
		if n := positiveJSONInt(settings["n_ctx"]); n > 0 {
			return n
		}
	}
	if n := positiveJSONInt(raw["context_window"]); n > 0 {
		return n
	}
	if runtime, ok := raw["runtime"].(map[string]any); ok {
		return positiveJSONInt(runtime["max_ctx"])
	}
	return 0
}

func parsePropsMaxCompletionTokens(raw map[string]any) int {
	if card, ok := raw["model_card"].(map[string]any); ok {
		if n := positiveJSONInt(card["max_tokens"]); n > 0 {
			return n
		}
	}
	if n := positiveJSONInt(raw["max_completion_tokens"]); n > 0 {
		return n
	}
	if settings, ok := raw["default_generation_settings"].(map[string]any); ok {
		if params, ok := settings["params"].(map[string]any); ok {
			return positiveJSONInt(params["max_tokens"])
		}
	}
	return 0
}

func positiveJSONInt(value any) int {
	var n int64
	switch typed := value.(type) {
	case float64:
		n = int64(typed)
		if float64(n) != typed {
			return 0
		}
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			return 0
		}
		n = parsed
	case int:
		n = int64(typed)
	case int64:
		n = typed
	default:
		return 0
	}
	if n <= 0 || int64(int(n)) != n {
		return 0
	}
	return int(n)
}

// modelNameFromPath recovers a model name from a server-reported weights path by
// taking the base filename and stripping a known model-weight extension, e.g.
// "/opt/.../Qwen3.6-27B-Q4_K_M.gguf" -> "Qwen3.6-27B-Q4_K_M". Returns "" when
// the path has no usable base name.
func modelNameFromPath(p string) string {
	base := path.Base(strings.TrimSpace(p))
	if base == "." || base == "/" || base == "" {
		return ""
	}
	for _, ext := range []string{".gguf", ".safetensors", ".bin", ".mlx"} {
		if strings.HasSuffix(strings.ToLower(base), ext) {
			base = base[:len(base)-len(ext)]
			break
		}
	}
	return strings.TrimSpace(base)
}

// lastPathSegment returns the final segment of a URL or path, used to recover a
// model name from a model_card source URL (e.g. a HuggingFace repo URL ending in
// ".../Qwen3.6-27B").
func lastPathSegment(s string) string {
	s = strings.TrimRight(strings.TrimSpace(s), "/")
	if s == "" {
		return ""
	}
	if i := strings.LastIndexByte(s, '/'); i >= 0 {
		return strings.TrimSpace(s[i+1:])
	}
	return s
}

func parsePropsReasoningLevels(raw map[string]any) []string {
	reasoning, ok := raw["reasoning"].(map[string]any)
	if !ok {
		return nil
	}
	levels := stringsFromAny(reasoning["supported_efforts"])
	if len(levels) == 0 {
		levels = stringsFromAny(reasoning["reasoning_levels"])
	}
	if len(levels) == 0 {
		levels = stringsFromAny(reasoning["levels"])
	}
	if len(levels) == 0 {
		return nil
	}
	aliases := map[string]bool{}
	if rawAliases, ok := reasoning["aliases"].(map[string]any); ok {
		for key := range rawAliases {
			aliases[key] = true
		}
	}
	out := make([]string, 0, len(levels))
	for _, level := range levels {
		if aliases[level] {
			continue
		}
		out = append(out, level)
	}
	return uniqueSortedStrings(out)
}

func stringsFromAny(v any) []string {
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}

func discoverHarnessProvider(providerName, providerType string, cache *discoverycache.Cache, opts AssembleOptions) providerDiscoveryResult {
	src := discoverycache.Source{
		Tier:            "discovery",
		Name:            providerName,
		TTL:             discoveryTTLPTY,
		RefreshDeadline: discoveryRefreshDeadlinePTY,
	}
	if cache == nil {
		snapshot, err := harnesses.CachedModelDiscoverySnapshot(providerType, harnesses.EmbeddedDiscoverySource)
		if err != nil {
			return providerDiscoveryResult{Sources: map[string]SourceMeta{providerName: {Stale: true, Error: err.Error()}}}
		}
		return modelsFromIDs(snapshot.Models, SourceHarnessPTY, snapshot.CapturedAt, discoveryIdentity{
			Provider:       providerName,
			ProviderType:   providerType,
			Harness:        providerType,
			EndpointName:   providerName,
			ServerInstance: providerName,
		})
	}
	refresher := func(context.Context) ([]byte, error) {
		snapshot, err := harnesses.CachedModelDiscoverySnapshot(providerType, harnesses.EmbeddedDiscoverySource)
		if err != nil {
			return nil, err
		}
		return json.Marshal(discoveryPayload{
			CapturedAt:      snapshot.CapturedAt,
			Models:          snapshot.Models,
			ReasoningLevels: snapshot.ReasoningLevels,
			Source:          snapshot.Source,
		})
	}
	switch opts.Refresh {
	case RefreshForce:
		_ = cache.Refresh(src, refresher)
	case RefreshIfStale:
		_ = cache.MaybeRefreshSync(src, refresher)
	}
	result := readDiscoveryCache(cache, src, providerName, SourceHarnessPTY, discoveryIdentity{
		Provider:       providerName,
		ProviderType:   providerType,
		Harness:        providerType,
		EndpointName:   providerName,
		ServerInstance: providerName,
	})
	if len(result.Models) > 0 {
		return result
	}
	snapshot, err := harnesses.CachedModelDiscoverySnapshot(providerType, harnesses.EmbeddedDiscoverySource)
	if err != nil {
		if result.Sources == nil {
			result.Sources = map[string]SourceMeta{}
		}
		meta := result.Sources[providerName]
		meta.Error = err.Error()
		result.Sources[providerName] = meta
		return result
	}
	return modelsFromIDs(snapshot.Models, SourceHarnessPTY, snapshot.CapturedAt, discoveryIdentity{
		Provider:       providerName,
		ProviderType:   providerType,
		Harness:        providerType,
		EndpointName:   providerName,
		ServerInstance: providerName,
	})
}

func readDiscoveryCache(cache *discoverycache.Cache, src discoverycache.Source, providerName string, via Source, identity discoveryIdentity) providerDiscoveryResult {
	result := providerDiscoveryResult{Sources: map[string]SourceMeta{}}
	read, err := cache.Read(src)
	meta := SourceMeta{Stale: true}
	if read.Data != nil {
		meta.LastRefreshedAt = time.Now().Add(-read.Age).UTC()
		meta.Stale = read.Stale
	}
	if err != nil {
		meta.Error = err.Error()
		result.Sources[src.Name] = meta
		return result
	}
	result.Sources[src.Name] = meta
	refreshFailed := false
	if state, stateErr := cache.RefreshState(src); stateErr == nil && state.Failed {
		refreshFailed = true
		if meta.Error == "" {
			meta.Error = "refresh_failed: " + state.LastError
		}
		if !state.StartedAt.IsZero() {
			meta.LastRefreshedAt = state.StartedAt.UTC()
		}
		meta.Stale = true
		result.Sources[src.Name] = meta
	}
	if read.Data == nil {
		return result
	}
	ids, capturedAt, limits, err := parseDiscoveryData(read.Data, providerName)
	if err != nil {
		meta.Error = err.Error()
		result.Sources[src.Name] = meta
		return result
	}
	if !capturedAt.IsZero() && !refreshFailed {
		meta.LastRefreshedAt = capturedAt.UTC()
		result.Sources[src.Name] = meta
	}
	if via == SourcePropsAPI {
		if limits.ContextWindow > 0 && strings.TrimSpace(limits.ContextWindowSource) == "" {
			limits.ContextWindowSource = limitSourceProviderAPI
		}
		if limits.MaxCompletionTokens > 0 && strings.TrimSpace(limits.MaxCompletionTokensSource) == "" {
			limits.MaxCompletionTokensSource = limitSourceProviderAPI
		}
	}
	result.Models = modelsFromIDsWithLimits(ids, via, discoveredAt(capturedAt, meta.LastRefreshedAt), identity, limits).Models
	return result
}

func parseDiscoveryIDs(data []byte, providerName string) ([]string, time.Time, error) {
	ids, capturedAt, _, err := parseDiscoveryData(data, providerName)
	return ids, capturedAt, err
}

type discoveryLimitEvidence struct {
	ContextWindow             int
	ContextWindowSource       string
	MaxCompletionTokens       int
	MaxCompletionTokensSource string
}

func parseDiscoveryData(data []byte, providerName string) ([]string, time.Time, discoveryLimitEvidence, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, time.Time{}, discoveryLimitEvidence{}, nil
	}
	var list []string
	if err := json.Unmarshal(data, &list); err == nil {
		return uniqueSortedStrings(list), time.Time{}, discoveryLimitEvidence{}, nil
	}
	var payload discoveryPayload
	if err := json.Unmarshal(data, &payload); err == nil && (len(payload.Models) > 0 || !payload.CapturedAt.IsZero()) {
		return uniqueSortedStrings(payload.Models), payload.CapturedAt, discoveryLimitEvidence{
			ContextWindow:             payload.ContextWindow,
			ContextWindowSource:       payload.ContextWindowSource,
			MaxCompletionTokens:       payload.MaxCompletionTokens,
			MaxCompletionTokensSource: payload.MaxCompletionTokensSource,
		}, nil
	}
	var byRef map[string]json.RawMessage
	if err := json.Unmarshal(data, &byRef); err != nil {
		return nil, time.Time{}, discoveryLimitEvidence{}, fmt.Errorf("decode discovery cache: %w", err)
	}
	ids := make([]string, 0, len(byRef))
	var capturedAt time.Time
	for key, raw := range byRef {
		includeValueFields := true
		if ref, err := modelref.Parse(key); err == nil {
			includeValueFields = ref.Provider == providerName
			if ref.Provider == providerName {
				ids = append(ids, ref.ID)
			}
		}
		if !includeValueFields {
			continue
		}
		var item struct {
			ID         string    `json:"id"`
			ModelID    string    `json:"model_id"`
			CapturedAt time.Time `json:"captured_at"`
		}
		if err := json.Unmarshal(raw, &item); err == nil {
			if item.ID != "" {
				ids = append(ids, item.ID)
			}
			if item.ModelID != "" {
				ids = append(ids, item.ModelID)
			}
			if capturedAt.IsZero() && !item.CapturedAt.IsZero() {
				capturedAt = item.CapturedAt
			}
		}
	}
	return uniqueSortedStrings(ids), capturedAt, discoveryLimitEvidence{}, nil
}

type discoveryIdentity struct {
	Provider        string
	ProviderType    string
	Harness         string
	EndpointName    string
	EndpointBaseURL string
	ServerInstance  string
}

func modelsFromIDs(ids []string, via Source, at time.Time, identity discoveryIdentity) providerDiscoveryResult {
	return modelsFromIDsWithLimits(ids, via, at, identity, discoveryLimitEvidence{})
}

func modelsFromIDsWithLimits(ids []string, via Source, at time.Time, identity discoveryIdentity, limits discoveryLimitEvidence) providerDiscoveryResult {
	at = discoveredAt(at, time.Now().UTC())
	out := make([]discoveredModel, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, discoveredModel{
			Provider:                  identity.Provider,
			ProviderType:              identity.ProviderType,
			Harness:                   identity.Harness,
			ID:                        id,
			EndpointName:              identity.EndpointName,
			EndpointBaseURL:           identity.EndpointBaseURL,
			ServerInstance:            identity.ServerInstance,
			Via:                       via,
			DiscoveredAt:              at,
			ContextWindow:             limits.ContextWindow,
			ContextWindowSource:       limits.ContextWindowSource,
			MaxCompletionTokens:       limits.MaxCompletionTokens,
			MaxCompletionTokensSource: limits.MaxCompletionTokensSource,
		})
	}
	sort.Slice(out, func(i, j int) bool { return discoveredModelSortKey(out[i]) < discoveredModelSortKey(out[j]) })
	return providerDiscoveryResult{
		Models:  out,
		Sources: map[string]SourceMeta{identity.Provider: {LastRefreshedAt: at}},
	}
}

func (r *providerDiscoveryResult) merge(other providerDiscoveryResult) {
	if r.Sources == nil {
		r.Sources = map[string]SourceMeta{}
	}
	for k, v := range other.Sources {
		r.Sources[k] = v
	}
	for _, model := range other.Models {
		if model.hasEndpointIdentity() {
			if mergeExactModelEvidence(r.Models, model) {
				continue
			}
			for i := range r.Models {
				if r.Models[i].providerModelKey() == model.providerModelKey() && !r.Models[i].hasEndpointIdentity() {
					mergeMissingLimitEvidence(&model, r.Models[i])
				}
			}
			r.Models = removeGenericModel(r.Models, model.Provider, model.ID)
			r.Models = append(r.Models, model)
			continue
		}
		if mergeProviderModelEvidence(r.Models, model) {
			continue
		}
		r.Models = append(r.Models, model)
	}
	sort.Slice(r.Models, func(i, j int) bool { return discoveredModelSortKey(r.Models[i]) < discoveredModelSortKey(r.Models[j]) })
}

func mergeExactModelEvidence(models []discoveredModel, incoming discoveredModel) bool {
	for i := range models {
		if models[i].identityKey() != incoming.identityKey() {
			continue
		}
		mergeMissingLimitEvidence(&models[i], incoming)
		return true
	}
	return false
}

func mergeProviderModelEvidence(models []discoveredModel, incoming discoveredModel) bool {
	found := false
	for i := range models {
		if models[i].providerModelKey() != incoming.providerModelKey() {
			continue
		}
		mergeMissingLimitEvidence(&models[i], incoming)
		found = true
	}
	return found
}

func mergeMissingLimitEvidence(dst *discoveredModel, src discoveredModel) {
	if dst.ContextWindow <= 0 && src.ContextWindow > 0 {
		dst.ContextWindow = src.ContextWindow
		dst.ContextWindowSource = src.ContextWindowSource
	} else if dst.ContextWindow > 0 && dst.ContextWindowSource == "" && dst.ContextWindow == src.ContextWindow {
		dst.ContextWindowSource = src.ContextWindowSource
	}
	if dst.MaxCompletionTokens <= 0 && src.MaxCompletionTokens > 0 {
		dst.MaxCompletionTokens = src.MaxCompletionTokens
		dst.MaxCompletionTokensSource = src.MaxCompletionTokensSource
	} else if dst.MaxCompletionTokens > 0 && dst.MaxCompletionTokensSource == "" && dst.MaxCompletionTokens == src.MaxCompletionTokens {
		dst.MaxCompletionTokensSource = src.MaxCompletionTokensSource
	}
}

func discoverySource(name string, ttl, deadline time.Duration) discoverycache.Source {
	return discoverycache.Source{Tier: "discovery", Name: name, TTL: ttl, RefreshDeadline: deadline}
}

func discoveryTTLForProvider(pc config.ProviderConfig) time.Duration {
	switch normalizeProviderType(pc.Type) {
	case "ds4", "vidar-ds4", "sindri-llamacpp", "lucebox", "lmstudio", "llama-server", "omlx", "rapid-mlx", "vllm", "ollama":
		return discoveryTTLHTTPLocal
	default:
		return discoveryTTLHTTPRemote
	}
}

func hasPropsDiscovery(providerType string) bool {
	switch normalizeProviderType(providerType) {
	case "ds4", "vidar-ds4", "lucebox", "llama-server", "sindri-llamacpp":
		return true
	default:
		return false
	}
}

func hasDiscoveryEndpoint(pc config.ProviderConfig) bool {
	return firstBaseURL(pc) != ""
}

func providerTypeFromProviderConfig(pc config.ProviderConfig) string {
	return normalizeProviderType(pc.Type)
}

func firstBaseURL(pc config.ProviderConfig) string {
	if strings.TrimSpace(pc.BaseURL) != "" {
		return strings.TrimSpace(pc.BaseURL)
	}
	for _, ep := range pc.Endpoints {
		if strings.TrimSpace(ep.BaseURL) != "" {
			return strings.TrimSpace(ep.BaseURL)
		}
	}
	return ""
}

func firstServerInstance(pc config.ProviderConfig) string {
	if strings.TrimSpace(pc.ServerInstance) != "" {
		return strings.TrimSpace(pc.ServerInstance)
	}
	for _, ep := range pc.Endpoints {
		if strings.TrimSpace(ep.ServerInstance) != "" {
			return strings.TrimSpace(ep.ServerInstance)
		}
	}
	return ""
}

func discoveryEndpoints(providerName string, pc config.ProviderConfig) []modelDiscoveryEndpoint {
	if len(pc.Endpoints) > 0 {
		out := make([]modelDiscoveryEndpoint, 0, len(pc.Endpoints))
		for i, ep := range pc.Endpoints {
			if strings.TrimSpace(ep.BaseURL) == "" {
				continue
			}
			name := strings.TrimSpace(ep.Name)
			if name == "" {
				name = fmt.Sprintf("%s-%d", providerName, i+1)
			}
			out = append(out, modelDiscoveryEndpoint{
				Name:           name,
				BaseURL:        strings.TrimSpace(ep.BaseURL),
				ServerInstance: strings.TrimSpace(ep.ServerInstance),
			})
		}
		return out
	}
	if strings.TrimSpace(pc.BaseURL) == "" {
		return nil
	}
	name := strings.TrimSpace(providerName)
	if name == "" {
		name = endpointNameForConfig(providerName, pc.BaseURL, nil)
	}
	return []modelDiscoveryEndpoint{{
		Name:           name,
		BaseURL:        strings.TrimSpace(pc.BaseURL),
		ServerInstance: firstServerInstance(pc),
	}}
}

func endpointSourceName(providerName, endpointName, baseURL, serverInstance string) string {
	name := strings.TrimSpace(providerName)
	trimmedEndpoint := strings.TrimSpace(endpointName)
	if trimmedEndpoint == "" || trimmedEndpoint == "default" || trimmedEndpoint == name {
		name = sanitizeDiscoveryName(name)
	} else {
		name = sanitizeDiscoveryName(name + "-" + trimmedEndpoint)
	}
	if suffix := discoveryEndpointSuffix(baseURL, serverInstance); suffix != "" {
		name = sanitizeDiscoveryName(name + "-" + suffix)
	}
	return name
}

func discoveryEndpointSuffix(baseURL, serverInstance string) string {
	identity := strings.TrimSpace(baseURL) + "|" + strings.TrimSpace(serverInstance)
	if strings.TrimSpace(identity) == "|" {
		return ""
	}
	sum := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(sum[:4])
}

func endpointNameForConfig(providerName, baseURL string, endpoints []config.ProviderEndpoint) string {
	if len(endpoints) > 0 {
		for _, ep := range endpoints {
			if trimmed := strings.TrimSpace(ep.Name); trimmed != "" {
				return trimmed
			}
		}
	}
	if trimmed := strings.TrimSpace(providerName); trimmed != "" {
		return trimmed
	}
	if trimmed := strings.TrimSpace(baseURL); trimmed != "" {
		return trimmed
	}
	return "default"
}

func sanitizeDiscoveryName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return "discovery"
	}
	var b strings.Builder
	b.Grow(len(name))
	lastDash := false
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
			lastDash = false
		case r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "discovery"
	}
	return out
}

func discoveredModelSortKey(model discoveredModel) string {
	return strings.Join([]string{
		model.Provider,
		model.ProviderType,
		model.Harness,
		model.EndpointName,
		model.EndpointBaseURL,
		model.ServerInstance,
		model.ID,
	}, "\x00")
}

func (m discoveredModel) providerModelKey() string {
	return m.Provider + "\x00" + m.ID
}

func (m discoveredModel) hasEndpointIdentity() bool {
	return strings.TrimSpace(m.Harness) != "" || strings.TrimSpace(m.EndpointName) != "" || strings.TrimSpace(m.EndpointBaseURL) != "" || strings.TrimSpace(m.ServerInstance) != ""
}

func (m discoveredModel) identityKey() string {
	return strings.Join([]string{
		m.Provider,
		m.ProviderType,
		m.Harness,
		m.ID,
		m.EndpointName,
		m.EndpointBaseURL,
		m.ServerInstance,
	}, "\x00")
}

func removeGenericModel(models []discoveredModel, provider, id string) []discoveredModel {
	out := models[:0]
	for _, model := range models {
		if model.Provider == provider && model.ID == id && !model.hasEndpointIdentity() {
			continue
		}
		out = append(out, model)
	}
	return out
}

func discoveredAt(primary, fallback time.Time) time.Time {
	if !primary.IsZero() {
		return primary.UTC()
	}
	if !fallback.IsZero() {
		return fallback.UTC()
	}
	return time.Now().UTC()
}

func uniqueSortedStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func normalizeProviderType(providerType string) string {
	return strings.ToLower(strings.TrimSpace(providerType))
}
