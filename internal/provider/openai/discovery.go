package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/easel/fizeau/internal/modelmatch"
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

// ProbeProviderFlavorResult holds the result of provider flavor detection.
type ProbeProviderFlavorResult struct {
	// ProviderType is the detected provider type (e.g., "lmstudio", "omlx").
	// Empty if detection failed.
	ProviderType string
	// Confidence is a score from 0 to 1 indicating detection confidence.
	// 1.0 = explicit override, 0.9 = port heuristic match, 0.5 = probe match.
	Confidence float64
}

// probeProviderFlavor attempts to detect the provider type from a base URL using:
// 1. Explicit override (if provided)
// 2. Port-based heuristics (e.g., :1234 → lmstudio, :1235 → omlx)
// 3. Concurrent endpoint probes as fallback
//
// Returns a result with empty ProviderType if detection fails.
func probeProviderFlavor(ctx context.Context, baseURL, explicitType string) ProbeProviderFlavorResult {
	if explicitType != "" {
		return ProbeProviderFlavorResult{ProviderType: explicitType, Confidence: 1.0}
	}

	providerType, ok := detectProviderByPort(baseURL)
	if ok {
		return ProbeProviderFlavorResult{ProviderType: providerType, Confidence: 0.9}
	}

	providerType, ok = probeProviderEndpoints(ctx, baseURL)
	if ok {
		return ProbeProviderFlavorResult{ProviderType: providerType, Confidence: 0.5}
	}

	return ProbeProviderFlavorResult{}
}

// detectProviderByPort infers provider type from the port in a base URL.
// Returns ("", false) if port cannot be determined or doesn't match a known provider.
func detectProviderByPort(baseURL string) (string, bool) {
	u := strings.TrimRight(baseURL, "/")
	if idx := strings.Index(u, "://"); idx >= 0 {
		u = u[idx+3:]
	}
	if idx := strings.Index(u, "/"); idx >= 0 {
		u = u[:idx]
	}

	_, port, err := net.SplitHostPort(u)
	if err != nil {
		return "", false
	}

	switch port {
	case "1234":
		return "lmstudio", true
	case "1235":
		return "omlx", true
	case "11434":
		return "ollama", true
	case "8000":
		return "vllm", true
	}
	return "", false
}

// probeProviderEndpoints concurrently probes known provider endpoints to detect
// the provider type. Returns ("", false) if no provider responds.
func probeProviderEndpoints(ctx context.Context, baseURL string) (string, bool) {
	type probeResult struct {
		providerType string
		ok           bool
	}

	providers := []string{"lmstudio", "omlx", "ollama", "vllm"}
	results := make(chan probeResult, len(providers))
	var wg sync.WaitGroup

	for _, p := range providers {
		wg.Add(1)
		go func(providerType string) {
			defer wg.Done()
			if canProbeProvider(ctx, baseURL, providerType) {
				results <- probeResult{providerType, true}
			}
		}(p)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	for result := range results {
		if result.ok {
			return result.providerType, true
		}
	}
	return "", false
}

// canProbeProvider checks if a provider responds at baseURL by probing a
// provider-specific endpoint.
func canProbeProvider(ctx context.Context, baseURL, providerType string) bool {
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	base := strings.TrimRight(baseURL, "/")
	root := base
	if strings.HasSuffix(base, "/v1") {
		root = base[:len(base)-3]
	}

	var endpoint string
	switch providerType {
	case "lmstudio":
		endpoint = root + "/api/v0/models/test-probe"
	case "omlx":
		endpoint = base + "/models/status"
	case "ollama":
		endpoint = root + "/api/tags"
	case "vllm":
		endpoint = base + "/stats"
	default:
		return false
	}

	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false
	}
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		return false
	}

	return resp.StatusCode == http.StatusOK
}
