package portableruntime

import "github.com/easel/fizeau/internal/harnesses"

type SeedDisposition string

const (
	SeedNone               SeedDisposition = ""
	SeedPrefixPreserving   SeedDisposition = "prefix_preserving"
	SeedProjectionConsumed SeedDisposition = "projection_consumed"
)

// Manifest is private activation metadata. It is internal API for the
// dependent activation bead, but its JSON schema is deliberately explicit and
// contains no host source paths or environment values.
type Manifest struct {
	Version             int                           `json:"version"`
	TargetGOOS          string                        `json:"target_goos"`
	TargetGOARCH        string                        `json:"target_goarch"`
	GuestRoot           string                        `json:"guest_root"`
	Inventory           []ManifestSurface             `json:"inventory"`
	Entrypoints         map[string]ManifestEntrypoint `json:"entrypoints"`
	Assets              []ManifestAsset               `json:"assets"`
	EnvironmentNames    []string                      `json:"environment_names"`
	Providers           ProviderSnapshot              `json:"providers"`
	ProviderSecretsFile ManifestContentReference      `json:"provider_secrets_file"`
	NamespaceLauncher   *ManifestContentReference     `json:"namespace_launcher,omitempty"`
}

type ManifestContentReference struct {
	Target        string `json:"target"`
	ContentSHA256 string `json:"content_sha256"`
}

type ManifestSurface struct {
	Name      string                             `json:"name"`
	Transport harnesses.PortableRuntimeTransport `json:"transport"`
	Inclusion harnesses.PortableRuntimeInclusion `json:"inclusion"`
}

type ManifestEntrypoint struct {
	Name                 string                                        `json:"name"`
	ClosureClass         harnesses.PortableRuntimeClosureClass         `json:"closure_class"`
	Launch               harnesses.PortableRuntimeLaunch               `json:"launch"`
	AssetTargets         []string                                      `json:"asset_targets"`
	Environment          []harnesses.PortableRuntimeEnvironment        `json:"environment"`
	ExecutionConstraints harnesses.PortableRuntimeExecutionConstraints `json:"execution_constraints"`
	StateProjections     []harnesses.PortableRuntimeStateProjection    `json:"state_projections"`
}

type ManifestAsset struct {
	Kind               harnesses.PortableRuntimeAssetKind `json:"kind"`
	PathKind           harnesses.PortableRuntimePathKind  `json:"path_kind"`
	Target             string                             `json:"target"`
	ContentSHA256      string                             `json:"content_sha256"`
	MaterializedSHA256 string                             `json:"materialized_sha256"`
	Executable         bool                               `json:"executable"`
	SeedDisposition    SeedDisposition                    `json:"seed_disposition,omitempty"`
}
