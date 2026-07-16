package portableruntime

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
)

const (
	// GuestRoot is the fixed Linux mount target consumed by portable activation.
	GuestRoot = "/opt/fizeau/runtime"

	manifestVersion = 1
	manifestTarget  = ".fizeau/activation.json"
	manifestSum     = ".fizeau/activation.sha256"
	providerSecrets = ".fizeau/provider-secrets.json" // #nosec G101 -- private guest-relative filename, not a credential value.
)

var (
	ErrRequestInvalid    = errors.New("invalid portable runtime request")
	ErrClosureIncomplete = harnesses.ErrPortableRuntimeClosureIncomplete
	ErrCleanupIncomplete = errors.New("portable runtime cleanup incomplete")
)

// Request is the internal, route-neutral materialization input assembled by
// the service bridge. It deliberately has no harness, provider, model, policy,
// power, or capability selector.
type Request struct {
	DestinationRoot string
	Target          harnesses.PortableRuntimeTarget
	Inventory       []harnesses.PortableRuntimeSurface
	Providers       ProviderSnapshot
	ProviderSecrets []ProviderSecret
}

func (r Request) String() string {
	return fmt.Sprintf("{TargetGOOS:%q TargetGOARCH:%q InventoryCount:%d ProviderCount:%d}", r.Target.GOOS, r.Target.GOARCH, len(r.Inventory), len(r.Providers.Providers))
}

func (r Request) GoString() string { return r.String() }

func (r Request) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		TargetGOOS     string `json:"target_goos"`
		TargetGOARCH   string `json:"target_goarch"`
		InventoryCount int    `json:"inventory_count"`
		ProviderCount  int    `json:"provider_count"`
	}{r.Target.GOOS, r.Target.GOARCH, len(r.Inventory), len(r.Providers.Providers)})
}

// ProviderEndpoint is one value-preserving configured endpoint in the private
// structural provider snapshot.
type ProviderEndpoint struct {
	Name           string `json:"name"`
	BaseURL        string `json:"base_url"`
	ServerInstance string `json:"server_instance"`
}

// ConfiguredProvider is the field-exhaustive non-secret provider projection.
type ConfiguredProvider struct {
	Name                      string             `json:"name"`
	Type                      string             `json:"type"`
	BaseURL                   string             `json:"base_url"`
	ServerInstance            string             `json:"server_instance"`
	Endpoints                 []ProviderEndpoint `json:"endpoints"`
	Model                     string             `json:"model"`
	Billing                   string             `json:"billing"`
	IncludeByDefault          bool               `json:"include_by_default"`
	IncludeByDefaultSet       bool               `json:"include_by_default_set"`
	ContextWindow             int                `json:"context_window"`
	ConfigError               string             `json:"config_error"`
	DailyTokenBudget          int                `json:"daily_token_budget"`
	CreditBalanceThresholdUSD float64            `json:"credit_balance_threshold_usd"`
	CreditProbeTTL            time.Duration      `json:"credit_probe_ttl"`
}

type ConfigField struct {
	Field     string `json:"field"`
	Treatment string `json:"treatment"`
	Reason    string `json:"reason"`
}

// ProviderSnapshot is the deterministic effective service configuration. It
// never carries API keys or header values.
type ProviderSnapshot struct {
	ProviderNames       []string             `json:"provider_names"`
	DefaultProviderName string               `json:"default_provider_name"`
	Providers           []ConfiguredProvider `json:"providers"`
	HealthCooldown      time.Duration        `json:"health_cooldown"`
	WorkDir             ConfigField          `json:"work_dir"`
	SessionLogDir       ConfigField          `json:"session_log_dir"`
}

// ProviderSecret holds one provider's sensitive values for private
// persistence. Its representation is always redacted; only the materializer
// can obtain the raw owned copy.
type ProviderSecret struct {
	providerName string
	apiKey       string
	headers      map[string]string
}

func NewProviderSecret(providerName, apiKey string, headers map[string]string) ProviderSecret {
	return ProviderSecret{providerName: providerName, apiKey: apiKey, headers: cloneStrings(headers)}
}

func (s ProviderSecret) String() string {
	return fmt.Sprintf("{ProviderName:%q APIKey:<redacted> Headers:<redacted>}", s.providerName)
}

func (s ProviderSecret) GoString() string { return s.String() }

func (s ProviderSecret) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ProviderName string `json:"provider_name"`
		Redacted     bool   `json:"redacted"`
	}{s.providerName, true})
}

type privateProviderSecret struct {
	ProviderName string            `json:"provider_name"`
	APIKey       string            `json:"api_key"`
	Headers      map[string]string `json:"headers"`
}

func (s ProviderSecret) privateRecord() privateProviderSecret {
	return privateProviderSecret{ProviderName: s.providerName, APIKey: s.apiKey, Headers: cloneStrings(s.headers)}
}

func cloneStrings(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]string, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}

// Mount is the one generic read-only public-plan record projected by the root
// facade in the dependent bead.
type Mount struct {
	Source   string
	Target   string
	ReadOnly bool
}

// Bundle owns one committed runtime child and its retryable cleanup handle.
// Its fields remain private so generic JSON and formatting cannot expose the
// private manifest, source inventory, or provider material.
type Bundle struct {
	mu          sync.Mutex
	runtimeRoot string
	mounts      []Mount
	environment []string
	cleanup     func() error
	anchor      *os.File
}

func (b *Bundle) String() string   { return "{portable runtime bundle}" }
func (b *Bundle) GoString() string { return b.String() }
func (b *Bundle) MarshalJSON() ([]byte, error) {
	return []byte("{}"), nil
}
