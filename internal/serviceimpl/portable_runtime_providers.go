package serviceimpl

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/easel/fizeau/internal/modelcatalog"
	"github.com/easel/fizeau/internal/portableruntime"
)

// PortableRuntimeConfigTreatment describes how a host ServiceConfig path is
// represented in an activated portable runtime. Host path values are never
// retained in the snapshot.
type PortableRuntimeConfigTreatment string

const (
	PortableRuntimeConfigGuestPrivate PortableRuntimeConfigTreatment = portableruntime.ConfigTreatmentGuestPrivate
	PortableRuntimeConfigExcluded     PortableRuntimeConfigTreatment = portableruntime.ConfigTreatmentExcluded

	PortableRuntimeWorkDirRemappedReason       = portableruntime.WorkDirRemappedReason
	PortableRuntimeSessionLogDirExcludedReason = portableruntime.SessionLogExcludedReason
)

// PortableRuntimeConfigField records a named, value-free treatment of one
// ServiceConfig path method.
type PortableRuntimeConfigField struct {
	Field     string
	Treatment PortableRuntimeConfigTreatment
	Reason    string
}

// PortableRuntimeConfiguredProvidersInput is the root facade's API-neutral
// projection input. Providers must contain one entry for each ProviderNames
// element. WorkDir and SessionLogDir are accepted only so their portable
// treatment is explicit; their host values are deliberately not retained.
type PortableRuntimeConfiguredProvidersInput struct {
	ProviderNames       []string
	DefaultProviderName string
	Providers           map[string]ProviderEntry
	HealthCooldown      time.Duration
	WorkDir             string
	SessionLogDir       string
}

func (input PortableRuntimeConfiguredProvidersInput) String() string {
	data, err := json.Marshal(input)
	if err != nil {
		return "{portable configured providers input: unavailable}"
	}
	return string(data)
}

func (input PortableRuntimeConfiguredProvidersInput) GoString() string { return input.String() }

// MarshalJSON keeps the transient root-to-serviceimpl input safe for generic
// diagnostics. Provider entries and host paths remain available only to the
// builder and are represented here by value-free classifications.
func (input PortableRuntimeConfiguredProvidersInput) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ProviderNames       []string                   `json:"provider_names"`
		DefaultProviderName string                     `json:"default_provider_name"`
		ProviderCount       int                        `json:"provider_count"`
		HealthCooldown      time.Duration              `json:"health_cooldown"`
		WorkDir             PortableRuntimeConfigField `json:"work_dir"`
		SessionLogDir       PortableRuntimeConfigField `json:"session_log_dir"`
	}{
		ProviderNames:       append([]string(nil), input.ProviderNames...),
		DefaultProviderName: input.DefaultProviderName,
		ProviderCount:       len(input.Providers),
		HealthCooldown:      input.HealthCooldown,
		WorkDir: PortableRuntimeConfigField{
			Field:     portableruntime.WorkDirField,
			Treatment: PortableRuntimeConfigGuestPrivate,
			Reason:    PortableRuntimeWorkDirRemappedReason,
		},
		SessionLogDir: PortableRuntimeConfigField{
			Field:     portableruntime.SessionLogDirField,
			Treatment: PortableRuntimeConfigExcluded,
			Reason:    PortableRuntimeSessionLogDirExcludedReason,
		},
	})
}

// PortableRuntimeConfiguredProvider is the non-secret structural record used
// to reproduce one configured provider inside an activated runtime. APIKey and
// Headers live only in the paired PortableRuntimeProviderSensitive record.
type PortableRuntimeConfiguredProvider struct {
	Name                      string
	Type                      string
	BaseURL                   string
	ServerInstance            string
	Endpoints                 []ProviderEndpoint
	Model                     string
	Billing                   modelcatalog.BillingModel
	IncludeByDefault          bool
	IncludeByDefaultSet       bool
	ContextWindow             int
	ConfigError               string
	DailyTokenBudget          int
	CreditBalanceThresholdUSD float64
	CreditProbeTTL            time.Duration
}

func (p PortableRuntimeConfiguredProvider) String() string {
	data, err := p.MarshalJSON()
	if err != nil {
		return "{portable configured provider: unavailable}"
	}
	return string(data)
}

func (p PortableRuntimeConfiguredProvider) GoString() string { return p.String() }

// MarshalJSON keeps provider values on the explicit materializer bridge. A
// row's generic representation is value-opaque because ConfigError and custom
// endpoint fields can contain host-derived diagnostics.
func (p PortableRuntimeConfiguredProvider) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Name               string `json:"name"`
		EndpointCount      int    `json:"endpoint_count"`
		ConfigErrorPresent bool   `json:"config_error_present"`
	}{p.Name, len(p.Endpoints), p.ConfigError != ""})
}

// PortableRuntimeProviderSensitive is the internal sensitive record paired
// with one structural provider. Its values are available only to the secure
// materializer through explicit accessors. Generic formatting and JSON always
// redact them.
type PortableRuntimeProviderSensitive struct {
	providerName string
	apiKey       string
	headers      map[string]string
}

func (s PortableRuntimeProviderSensitive) ProviderName() string { return s.providerName }
func (s PortableRuntimeProviderSensitive) APIKey() string       { return s.apiKey }
func (s PortableRuntimeProviderSensitive) Headers() map[string]string {
	return clonePortableRuntimeStringMap(s.headers)
}

func (s PortableRuntimeProviderSensitive) String() string {
	return fmt.Sprintf("{ProviderName:%q APIKey:<redacted> Headers:<redacted>}", s.providerName)
}

func (s PortableRuntimeProviderSensitive) GoString() string { return s.String() }

func (s PortableRuntimeProviderSensitive) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ProviderName string `json:"provider_name"`
		Redacted     bool   `json:"redacted"`
	}{ProviderName: s.providerName, Redacted: true})
}

// PortableRuntimeConfiguredProviders is the complete, route-neutral provider
// snapshot. Sensitive provider values are intentionally unexported so generic
// JSON cannot include them; SensitiveProviders returns a defensive copy for
// the secure materializer.
type PortableRuntimeConfiguredProviders struct {
	ProviderNames       []string
	DefaultProviderName string
	Providers           []PortableRuntimeConfiguredProvider
	HealthCooldown      time.Duration
	WorkDir             PortableRuntimeConfigField
	SessionLogDir       PortableRuntimeConfigField

	sensitiveProviders []PortableRuntimeProviderSensitive
}

func (s PortableRuntimeConfiguredProviders) SensitiveProviders() []PortableRuntimeProviderSensitive {
	out := make([]PortableRuntimeProviderSensitive, len(s.sensitiveProviders))
	for i, record := range s.sensitiveProviders {
		out[i] = PortableRuntimeProviderSensitive{
			providerName: record.providerName,
			apiKey:       record.apiKey,
			headers:      clonePortableRuntimeStringMap(record.headers),
		}
	}
	return out
}

func (s PortableRuntimeConfiguredProviders) String() string {
	data, err := json.Marshal(s)
	if err != nil {
		return "{portable configured providers: unavailable}"
	}
	return string(data)
}

func (s PortableRuntimeConfiguredProviders) GoString() string { return s.String() }

// MarshalJSON keeps the structural snapshot available only to the explicit
// materializer bridge. Generic diagnostics expose counts and names, never
// provider diagnostics, endpoint values, or host-derived text.
func (s PortableRuntimeConfiguredProviders) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ProviderNames       []string `json:"provider_names"`
		DefaultProviderName string   `json:"default_provider_name"`
		ProviderCount       int      `json:"provider_count"`
	}{append([]string(nil), s.ProviderNames...), s.DefaultProviderName, len(s.Providers)})
}

// BuildPortableRuntimeConfiguredProviders produces a deterministic structural
// snapshot without health, quota, network, model-discovery, or route probes.
// ProviderNames order is authoritative and is retained exactly.
func BuildPortableRuntimeConfiguredProviders(input PortableRuntimeConfiguredProvidersInput) (PortableRuntimeConfiguredProviders, error) {
	snapshot := PortableRuntimeConfiguredProviders{
		ProviderNames:       append([]string(nil), input.ProviderNames...),
		DefaultProviderName: input.DefaultProviderName,
		Providers:           make([]PortableRuntimeConfiguredProvider, 0, len(input.ProviderNames)),
		HealthCooldown:      input.HealthCooldown,
		WorkDir: PortableRuntimeConfigField{
			Field:     portableruntime.WorkDirField,
			Treatment: PortableRuntimeConfigGuestPrivate,
			Reason:    PortableRuntimeWorkDirRemappedReason,
		},
		SessionLogDir: PortableRuntimeConfigField{
			Field:     portableruntime.SessionLogDirField,
			Treatment: PortableRuntimeConfigExcluded,
			Reason:    PortableRuntimeSessionLogDirExcludedReason,
		},
		sensitiveProviders: make([]PortableRuntimeProviderSensitive, 0, len(input.ProviderNames)),
	}

	// Do not inspect, normalize, or retain these host paths. Merely accepting
	// them in the input keeps every ServiceConfig method explicitly classified.
	_ = input.WorkDir
	_ = input.SessionLogDir

	for index, name := range input.ProviderNames {
		entry, ok := input.Providers[name]
		if !ok {
			return PortableRuntimeConfiguredProviders{}, fmt.Errorf("portable configured providers: entry missing at index %d", index)
		}
		snapshot.Providers = append(snapshot.Providers, PortableRuntimeConfiguredProvider{
			Name:                      name,
			Type:                      entry.Type,
			BaseURL:                   entry.BaseURL,
			ServerInstance:            entry.ServerInstance,
			Endpoints:                 cloneProviderEndpoints(entry.Endpoints),
			Model:                     entry.Model,
			Billing:                   entry.Billing,
			IncludeByDefault:          entry.IncludeByDefault,
			IncludeByDefaultSet:       entry.IncludeByDefaultSet,
			ContextWindow:             entry.ContextWindow,
			ConfigError:               entry.ConfigError,
			DailyTokenBudget:          entry.DailyTokenBudget,
			CreditBalanceThresholdUSD: entry.CreditBalanceThresholdUSD,
			CreditProbeTTL:            entry.CreditProbeTTL,
		})
		snapshot.sensitiveProviders = append(snapshot.sensitiveProviders, PortableRuntimeProviderSensitive{
			providerName: name,
			apiKey:       entry.APIKey,
			headers:      clonePortableRuntimeStringMap(entry.Headers),
		})
	}
	return snapshot, nil
}

func clonePortableRuntimeStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]string, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}
