package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/easel/fizeau/internal/modelmatch"
	"github.com/easel/fizeau/internal/provider/limits"
	"github.com/easel/fizeau/internal/sdk/openaicompat"
)

// ScoredModel is a discovered model with a selection preference score.
// Higher scores are preferred by the auto-selection logic.
type ScoredModel struct {
	// ID is the model identifier returned by the server's /v1/models endpoint.
	ID string
	// CatalogRef is the catalog target ID if this model is recognized in the
	// model catalog for the provider's surface. Empty for unrecognized models.
	CatalogRef string
	// PatternMatch is true when this model matched the configured model_pattern.
	PatternMatch bool
	// Score summarises the selection preference: 3 = catalog-recognized,
	// 2 = pattern-matched, 1 = uncategorized.
	Score int
}

// DiscoverModels queries the generic /v1/models endpoint through the shared
// OpenAI-compatible SDK. It is kept here as a package-level compatibility
// wrapper for existing provider tests and callers inside this package.
func DiscoverModels(ctx context.Context, baseURL, apiKey string) ([]string, error) {
	return openaicompat.DiscoverModels(ctx, baseURL, apiKey)
}

// RankModels scores and sorts a list of discovered model IDs by selection
// preference:
//
//   - Score 3 — catalog-recognized: the model ID appears in knownModels (a map
//     from concrete model ID to catalog target ID, e.g. from
//     Catalog.AllConcreteModels). These are explicitly tracked models; prefer
//     them when auto-selecting.
//   - Score 2 — pattern-matched: the model ID matches the case-insensitive
//     pattern regex (pattern == "" means this tier is skipped).
//   - Score 1 — uncategorized: known to the server but not in the catalog or
//     pattern.
//
// Within each score tier, the original server-returned order is preserved.
// Returns an error only if pattern is non-empty and fails to compile.
func RankModels(candidates []string, knownModels map[string]string, pattern string) ([]ScoredModel, error) {
	var patternRe *regexp.Regexp
	if pattern != "" {
		re, err := regexp.Compile("(?i)" + pattern)
		if err != nil {
			return nil, fmt.Errorf("discovery: invalid model_pattern %q: %w", pattern, err)
		}
		patternRe = re
	}

	scored := make([]ScoredModel, 0, len(candidates))
	for _, id := range candidates {
		sm := ScoredModel{ID: id, Score: 1}
		if ref, ok := knownModels[id]; ok {
			sm.CatalogRef = ref
			sm.Score = 3
		} else if patternRe != nil && patternRe.MatchString(id) {
			sm.PatternMatch = true
			sm.Score = 2
		}
		scored = append(scored, sm)
	}

	// Stable sort: higher score first, original order preserved within tier.
	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})
	return scored, nil
}

// SelectModel picks the preferred model ID from a ranked list. Returns ""
// if the list is empty.
func SelectModel(ranked []ScoredModel) string {
	if len(ranked) == 0 {
		return ""
	}
	return ranked[0].ID
}

// getAndDecode performs a GET request with optional Bearer auth and extra
// headers, decodes the JSON response into out, and returns any error.
func getAndDecode(ctx context.Context, timeout time.Duration, endpoint, apiKey string, headers map[string]string, out any) error {
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// MatchModelIDs returns every catalog entry whose normalized form contains
// the normalized request as a substring. Normalization lowercases, strips a
// single leading vendor namespace, and removes all non-alphanumeric
// separators, so "qwen/qwen3.6" and "Qwen3.6" and "qwen3.6" all match
// "Qwen3.6-35B-A3B-4bit" and "Qwen3.6-35B-A3B-nvfp4".
//
// The returned slice preserves original catalog case and order. An empty slice
// means no match; callers are responsible for deciding whether to pass the
// original request through to the provider unchanged or to escalate.
//
// This is the primary matching primitive since v0.9.2 — it replaces the
// scalar logic previously in NormalizeModelID. NormalizeModelID is retained
// as a backward-compatible wrapper.
func MatchModelIDs(requested string, catalog []string) []string {
	return modelmatch.Match(requested, catalog)
}

// NormalizeModelID resolves a caller-supplied model name against the server's
// canonical model catalog (the IDs returned by GET /v1/models).
//
// Prefer MatchModelIDs for new code; this wrapper is retained for backward
// compatibility with the v0.9.1 call signature. Behaviour:
//   - 0 matches → returns the original requested string, no error
//   - 1 match   → returns the catalog entry, no error
//   - 2+ matches → returns "" and an ambiguity error listing the candidates
func NormalizeModelID(requested string, catalog []string) (string, error) {
	if strings.TrimSpace(requested) == "" {
		return requested, nil
	}
	matches := MatchModelIDs(requested, catalog)
	switch len(matches) {
	case 0:
		return requested, nil
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("ambiguous model %q: matches %v", requested, matches)
	}
}

// ProbeProviderFlavor attempts to detect the provider type from a baseURL by:
// 1. Checking explicit config hints (if non-empty)
// 2. Probing known provider endpoints (per-provider heuristics)
// 3. Concurrent probes to resolve ambiguity
//
// Returns the detected provider type or "" if detection fails.
// Known types: "lmstudio", "omlx", "openrouter", "ollama", "vllm", "rapid-mlx", etc.
func ProbeProviderFlavor(ctx context.Context, baseURL string, explicitType string) string {
	if explicitType != "" {
		return explicitType
	}

	baseURL = strings.TrimRight(baseURL, "/")
	u, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}

	host := u.Host
	if host == "" {
		return ""
	}

	hostname, port, err := net.SplitHostPort(host)
	if err != nil {
		hostname = host
		port = ""
	}

	// Standardize localhost variants
	if hostname == "127.0.0.1" || hostname == "[::1]" {
		hostname = "localhost"
	}

	// Port-based heuristics for local providers
	switch port {
	case "1234":
		return "lmstudio"
	case "1235":
		return "omlx"
	case "11434":
		return "ollama"
	case "8000":
		return "vllm"
	}

	// URL pattern heuristics
	if strings.Contains(baseURL, "openrouter") {
		return "openrouter"
	}
	if strings.Contains(baseURL, "api.anthropic.com") {
		return "anthropic"
	}
	if strings.Contains(baseURL, "api.openai.com") {
		return "openai"
	}

	// Concurrent probes for ambiguous cases (localhost without explicit port)
	results := make(chan string, 3)
	var wg sync.WaitGroup

	probes := []struct {
		providerType string
		probeURL     string
	}{
		{"lmstudio", strings.TrimSuffix(baseURL, "/v1") + "/api/v0/models/probe"},
		{"omlx", baseURL + "/models/status"},
		{"ollama", baseURL + "/api/tags"},
	}

	for _, p := range probes {
		wg.Add(1)
		go func(ptype, purl string) {
			defer wg.Done()
			reqCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			req, _ := http.NewRequestWithContext(reqCtx, http.MethodGet, purl, nil)
			if req != nil {
				resp, err := http.DefaultClient.Do(req)
				if err == nil {
					defer resp.Body.Close()
					if resp.StatusCode == http.StatusOK {
						results <- ptype
					}
				}
			}
		}(p.providerType, p.probeURL)
	}

	wg.Wait()
	close(results)

	// Return first detected provider type
	if len(results) > 0 {
		return <-results
	}
	return ""
}

// LookupModelLimits queries the appropriate provider endpoint for model limits.
// Dispatches to the right provider based on the explicit providerType or
// probes the baseURL to detect the provider flavor.
//
// Returns ModelLimits with zero values if limits cannot be determined.
func LookupModelLimits(ctx context.Context, baseURL, providerType, apiKey string, headers map[string]string, model string) limits.ModelLimits {
	if providerType == "" {
		providerType = ProbeProviderFlavor(ctx, baseURL, "")
	}

	baseURL = strings.TrimRight(baseURL, "/")

	switch providerType {
	case "lmstudio":
		return lookupLMStudioLimits(ctx, baseURL, model)
	case "omlx":
		return lookupOMLXLimits(ctx, baseURL, model)
	case "openrouter":
		return lookupOpenRouterLimits(ctx, baseURL, apiKey, headers, model)
	default:
		return limits.ModelLimits{}
	}
}

func lookupLMStudioLimits(ctx context.Context, baseURL, model string) limits.ModelLimits {
	root := strings.TrimSuffix(strings.TrimRight(baseURL, "/"), "/v1")
	endpoint := root + "/api/v0/models/" + url.QueryEscape(model)

	var info struct {
		LoadedContextLength int `json:"loaded_context_length"`
		MaxContextLength    int `json:"max_context_length"`
	}
	if err := getAndDecode(ctx, 5*time.Second, endpoint, "", nil, &info); err != nil {
		return limits.ModelLimits{}
	}

	contextLen := info.LoadedContextLength
	if contextLen == 0 {
		contextLen = info.MaxContextLength
	}
	return limits.ModelLimits{ContextLength: contextLen}
}

func lookupOMLXLimits(ctx context.Context, baseURL, model string) limits.ModelLimits {
	base := strings.TrimRight(baseURL, "/")
	endpoint := base + "/models/status"

	var status struct {
		Models []struct {
			ID               string `json:"id"`
			MaxContextWindow int    `json:"max_context_window"`
			MaxTokens        int    `json:"max_tokens"`
		} `json:"models"`
	}
	if err := getAndDecode(ctx, 5*time.Second, endpoint, "", nil, &status); err != nil {
		return limits.ModelLimits{}
	}

	for _, entry := range status.Models {
		if strings.EqualFold(entry.ID, model) {
			return limits.ModelLimits{
				ContextLength:       entry.MaxContextWindow,
				MaxCompletionTokens: entry.MaxTokens,
			}
		}
	}
	return limits.ModelLimits{}
}

func lookupOpenRouterLimits(ctx context.Context, baseURL, apiKey string, headers map[string]string, model string) limits.ModelLimits {
	var list struct {
		Data []struct {
			ID            string `json:"id"`
			ContextLength int    `json:"context_length"`
			TopProvider   struct {
				MaxCompletionTokens int `json:"max_completion_tokens"`
			} `json:"top_provider"`
		} `json:"data"`
	}
	endpoint := strings.TrimRight(baseURL, "/") + "/models"
	if err := getAndDecode(ctx, 10*time.Second, endpoint, apiKey, headers, &list); err != nil {
		return limits.ModelLimits{}
	}

	normalizeID := func(s string) string {
		return strings.ToLower(strings.ReplaceAll(s, "-", "."))
	}
	normModel := normalizeID(model)
	for _, m := range list.Data {
		if strings.EqualFold(m.ID, model) || normalizeID(m.ID) == normModel {
			return limits.ModelLimits{
				ContextLength:       m.ContextLength,
				MaxCompletionTokens: m.TopProvider.MaxCompletionTokens,
			}
		}
	}
	return limits.ModelLimits{}
}
