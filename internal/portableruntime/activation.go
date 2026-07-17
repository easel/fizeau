package portableruntime

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/safefs"
)

const activationMetadataLimit = 16 << 20

// ActivationPlan is the verified, route-neutral input to later activation
// phases. It owns every returned value and deliberately exposes no host
// configuration source or service/process handle.
type ActivationPlan struct {
	manifest             Manifest
	providerSecrets      []ProviderSecret
	inheritedEnvironment map[string]string
	backingRoot          string
	entrypoints          map[string]activationEntrypoint
	workDir              string
	sessionLogDir        string
}

func (p ActivationPlan) String() string {
	return fmt.Sprintf("{InventoryCount:%d ProviderCount:%d EnvironmentCount:%d}", len(p.manifest.Inventory), len(p.manifest.Providers.Providers), len(p.inheritedEnvironment))
}

func (p ActivationPlan) GoString() string { return p.String() }

func (p ActivationPlan) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		InventoryCount   int `json:"inventory_count"`
		ProviderCount    int `json:"provider_count"`
		EnvironmentCount int `json:"environment_count"`
	}{len(p.manifest.Inventory), len(p.manifest.Providers.Providers), len(p.inheritedEnvironment)})
}

// Manifest returns an owned copy of the verified private manifest.
func (p ActivationPlan) Manifest() Manifest { return cloneManifest(p.manifest) }

// ProviderSnapshot returns an owned copy of the verified provider structure.
func (p ActivationPlan) ProviderSnapshot() ProviderSnapshot {
	return cloneProviderSnapshot(p.manifest.Providers)
}

// ProviderSecrets returns owned sensitive records for the explicit service
// activation bridge. Their diagnostic representations remain redacted.
func (p ActivationPlan) ProviderSecrets() []ProviderSecret {
	out := make([]ProviderSecret, len(p.providerSecrets))
	for i, secret := range p.providerSecrets {
		out[i] = NewProviderSecret(secret.providerName, secret.apiKey, secret.headers)
	}
	return out
}

// InheritedEnvironment returns exactly the declared inherited names. A
// present empty value is retained and remains distinct from an absent name.
func (p ActivationPlan) InheritedEnvironment() map[string]string {
	out := make(map[string]string, len(p.inheritedEnvironment))
	for name, value := range p.inheritedEnvironment {
		out[name] = value
	}
	return out
}

// GuestPath maps one private manifest target into the fixed logical guest
// root. The physical root used by package tests is never retained or exposed.
func (p ActivationPlan) GuestPath(target string) (string, error) {
	if !cleanTarget(target) {
		return "", activationError("guest target")
	}
	return path.Join(GuestRoot, target), nil
}

// LoadActivation verifies one mounted portable runtime without constructing
// service state, resolving a route, contacting a provider, or starting a
// process. runtimeRoot is internal-only testability plumbing; the public
// entrypoint always supplies GuestRoot.
func LoadActivation(runtimeRoot string, lookupEnv func(string) (string, bool)) (ActivationPlan, error) {
	root, err := openActivationRoot(runtimeRoot, lookupEnv)
	if err != nil {
		return ActivationPlan{}, err
	}
	defer root.Close()
	return loadActivationFromRoot(runtimeRoot, root, lookupEnv)
}

func openActivationRoot(runtimeRoot string, lookupEnv func(string) (string, bool)) (*safefs.NoFollowRoot, error) {
	if runtimeRoot == "" || !filepath.IsAbs(runtimeRoot) || filepath.Clean(runtimeRoot) != runtimeRoot || lookupEnv == nil {
		return nil, activationError("activation input")
	}
	root, err := safefs.OpenNoFollowRoot(runtimeRoot)
	if err != nil {
		return nil, activationError("runtime root")
	}
	rootInfo, err := root.Stat()
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode().Perm() != 0o700 {
		_ = root.Close()
		return nil, activationError("runtime root mode")
	}
	return root, nil
}

func loadActivationFromRoot(runtimeRoot string, root *safefs.NoFollowRoot, lookupEnv func(string) (string, bool)) (ActivationPlan, error) {
	manifestBytes, err := readActivationMetadata(root, manifestTarget)
	if err != nil {
		return ActivationPlan{}, activationError("manifest read")
	}
	sumBytes, err := readActivationMetadata(root, manifestSum)
	if err != nil {
		return ActivationPlan{}, activationError("manifest checksum read")
	}
	digest := sha256.Sum256(manifestBytes)
	if string(sumBytes) != hex.EncodeToString(digest[:])+"\n" {
		return ActivationPlan{}, activationError("manifest checksum")
	}
	manifest, err := decodeActivationManifest(manifestBytes)
	if err != nil {
		return ActivationPlan{}, err
	}
	if err := validateActivationManifest(runtimeRoot, root, manifest); err != nil {
		return ActivationPlan{}, err
	}

	secretBytes, err := readActivationMetadata(root, manifest.ProviderSecretsFile.Target)
	if err != nil {
		return ActivationPlan{}, activationError("provider secrets read")
	}
	secretDigest := sha256.Sum256(secretBytes)
	if hex.EncodeToString(secretDigest[:]) != manifest.ProviderSecretsFile.ContentSHA256 {
		return ActivationPlan{}, activationError("provider secrets identity")
	}
	secrets, err := decodeActivationSecrets(secretBytes)
	if err != nil || validateProviders(manifest.Providers, secrets) != nil {
		return ActivationPlan{}, activationError("provider parity")
	}

	inherited := make(map[string]string, len(manifest.EnvironmentNames))
	for _, name := range manifest.EnvironmentNames {
		value, present := lookupEnv(name)
		if !present {
			return ActivationPlan{}, activationError("required environment")
		}
		inherited[name] = value
	}
	return ActivationPlan{
		manifest:             cloneManifest(manifest),
		providerSecrets:      cloneProviderSecrets(secrets),
		inheritedEnvironment: inherited,
	}, nil
}

func decodeActivationManifest(data []byte) (Manifest, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, activationError("manifest schema")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Manifest{}, activationError("manifest trailing content")
	}
	return manifest, nil
}

func decodeActivationSecrets(data []byte) ([]ProviderSecret, error) {
	var document privateProviderSecretsDocument
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return nil, activationError("provider secrets schema")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) || document.Version != manifestVersion {
		return nil, activationError("provider secrets version")
	}
	secrets := make([]ProviderSecret, len(document.Providers))
	for i, record := range document.Providers {
		secrets[i] = NewProviderSecret(record.ProviderName, record.APIKey, record.Headers)
	}
	return secrets, nil
}

func validateActivationManifest(runtimeRoot string, root *safefs.NoFollowRoot, manifest Manifest) error {
	target := harnesses.PortableRuntimeTarget{GOOS: manifest.TargetGOOS, GOARCH: manifest.TargetGOARCH}
	if manifest.Version != manifestVersion || manifest.GuestRoot != GuestRoot ||
		manifest.TargetGOOS != "linux" || manifest.TargetGOARCH != runtime.GOARCH ||
		harnesses.ValidatePortableRuntimeTarget(target) != nil {
		return activationError("manifest target")
	}
	if manifest.ProviderSecretsFile.Target != providerSecrets || !validDigest(manifest.ProviderSecretsFile.ContentSHA256) {
		return activationError("provider secrets reference")
	}
	if err := validateActivationProviderSnapshot(manifest.Providers); err != nil {
		return err
	}
	if err := validateActivationInventory(manifest); err != nil {
		return err
	}
	if err := validateActivationAssets(runtimeRoot, root, manifest); err != nil {
		return err
	}
	if err := validateManifestText(manifest, nil); err != nil {
		return activationError("manifest text")
	}
	return nil
}

func validateActivationProviderSnapshot(snapshot ProviderSnapshot) error {
	if snapshot.WorkDir != (ConfigField{Field: WorkDirField, Treatment: ConfigTreatmentGuestPrivate, Reason: WorkDirRemappedReason}) ||
		snapshot.SessionLogDir != (ConfigField{Field: SessionLogDirField, Treatment: ConfigTreatmentExcluded, Reason: SessionLogExcludedReason}) {
		return activationError("provider path treatments")
	}
	if len(snapshot.ProviderNames) != len(snapshot.Providers) {
		return activationError("provider cardinality")
	}
	seen := make(map[string]struct{}, len(snapshot.ProviderNames))
	for i, name := range snapshot.ProviderNames {
		if name == "" || snapshot.Providers[i].Name != name {
			return activationError("provider identity")
		}
		if _, exists := seen[name]; exists {
			return activationError("provider duplicate")
		}
		seen[name] = struct{}{}
	}
	if snapshot.DefaultProviderName != "" {
		if _, exists := seen[snapshot.DefaultProviderName]; !exists {
			return activationError("default provider identity")
		}
	}
	return nil
}

func validateActivationInventory(manifest Manifest) error {
	seen := make(map[string]struct{}, len(manifest.Inventory))
	required := make(map[string]struct{})
	previous := ""
	for i, surface := range manifest.Inventory {
		if surface.Name == "" || (i > 0 && surface.Name <= previous) {
			return activationError("inventory order")
		}
		previous = surface.Name
		if _, exists := seen[surface.Name]; exists {
			return activationError("inventory duplicate")
		}
		seen[surface.Name] = struct{}{}
		switch surface.Transport {
		case harnesses.PortableRuntimeTransportSubprocess, harnesses.PortableRuntimeTransportNative,
			harnesses.PortableRuntimeTransportEmbedded, harnesses.PortableRuntimeTransportHTTP:
		default:
			return activationError("inventory transport")
		}
		switch surface.Inclusion {
		case harnesses.PortableRuntimeInclusionRequired:
			if surface.Transport != harnesses.PortableRuntimeTransportSubprocess {
				return activationError("required inventory surface")
			}
			required[surface.Name] = struct{}{}
		case harnesses.PortableRuntimeInclusionExactPinOnly:
			if surface.Transport != harnesses.PortableRuntimeTransportSubprocess {
				return activationError("exact-pin inventory surface")
			}
		case harnesses.PortableRuntimeInclusionNonSubprocess:
			if surface.Transport == harnesses.PortableRuntimeTransportSubprocess {
				return activationError("non-subprocess inventory surface")
			}
		case harnesses.PortableRuntimeInclusionTestOnly:
			if surface.Transport != harnesses.PortableRuntimeTransportEmbedded {
				return activationError("test-only inventory surface")
			}
		default:
			return activationError("inventory inclusion")
		}
	}
	if len(required) != len(manifest.Entrypoints) {
		return activationError("entrypoint cardinality")
	}
	for name, entrypoint := range manifest.Entrypoints {
		if _, exists := required[name]; !exists || entrypoint.Name != name {
			return activationError("entrypoint identity")
		}
	}
	return nil
}

func validateActivationAssets(runtimeRoot string, root *safefs.NoFollowRoot, manifest Manifest) error {
	assets := make(map[string]ManifestAsset, len(manifest.Assets))
	targets := make([]string, len(manifest.Assets))
	for i, asset := range manifest.Assets {
		if !cleanTarget(asset.Target) || !validDigest(asset.ContentSHA256) || !validDigest(asset.MaterializedSHA256) ||
			(i > 0 && asset.Target <= manifest.Assets[i-1].Target) {
			return activationError("asset identity")
		}
		switch asset.PathKind {
		case harnesses.PortableRuntimePathFile, harnesses.PortableRuntimePathTree:
		default:
			return activationError("asset path kind")
		}
		assets[asset.Target] = asset
		targets[i] = asset.Target
	}
	if err := validateTargetDisjointness(targets); err != nil {
		return activationError("asset target overlap")
	}
	if err := validateActivationDeclaredPaths(root, manifest.Assets); err != nil {
		return activationError("undeclared runtime content")
	}

	projected := make(map[string]struct{})
	referenced := make(map[string]struct{})
	environmentNames := make(map[string]struct{})
	projectionClaims := make([]projectionClaim, 0)
	projectionAssets := make(map[string]projectionAssetClaim)
	projectionOutputs := make([]projectionOutputClaim, 0)
	requiredAbsent := make([]harnesses.PortableRuntimeGuestPath, 0)
	mutableUsage := make(map[string]mutableSeedUsage)
	for name, entrypoint := range manifest.Entrypoints {
		contribution := harnesses.PortableRuntimeContribution{
			ClosureClass:         entrypoint.ClosureClass,
			Launch:               entrypoint.Launch,
			Environment:          append([]harnesses.PortableRuntimeEnvironment(nil), entrypoint.Environment...),
			ExecutionConstraints: entrypoint.ExecutionConstraints,
			StateProjections:     entrypoint.StateProjections,
		}
		previous := ""
		for i, target := range entrypoint.AssetTargets {
			asset, exists := assets[target]
			if !exists || (i > 0 && target <= previous) {
				return activationError("entrypoint asset identity")
			}
			previous = target
			referenced[target] = struct{}{}
			contribution.Assets = append(contribution.Assets, harnesses.PortableRuntimeAsset{
				Kind: asset.Kind, PathKind: asset.PathKind,
				Source: filepath.Join(runtimeRoot, filepath.FromSlash(asset.Target)),
				Target: asset.Target, ContentSHA256: asset.ContentSHA256, Executable: asset.Executable,
			})
		}
		normalized, err := harnesses.NormalizePortableRuntimeContribution(
			harnesses.PortableRuntimeTarget{GOOS: manifest.TargetGOOS, GOARCH: manifest.TargetGOARCH}, contribution,
		)
		if err != nil || !reflect.DeepEqual(normalized.ClosureClass, entrypoint.ClosureClass) ||
			!reflect.DeepEqual(normalized.Launch, entrypoint.Launch) ||
			!reflect.DeepEqual(normalized.Environment, entrypoint.Environment) ||
			!reflect.DeepEqual(normalized.ExecutionConstraints, entrypoint.ExecutionConstraints) ||
			!reflect.DeepEqual(normalized.StateProjections, entrypoint.StateProjections) {
			return activationError("entrypoint structure")
		}
		if err := mergeProjectionClaims(&projectionClaims, projectionAssets, &projectionOutputs, &requiredAbsent, normalized); err != nil {
			return activationError("cross-entrypoint projection")
		}
		locallyProjected := make(map[string]struct{})
		for _, environment := range entrypoint.Environment {
			environmentNames[environment.Name] = struct{}{}
		}
		for _, projection := range entrypoint.StateProjections {
			for _, entry := range projection.Entries {
				projected[entry.AssetTarget] = struct{}{}
				locallyProjected[entry.AssetTarget] = struct{}{}
			}
		}
		for _, asset := range normalized.Assets {
			if !mutableAsset(asset.Kind) {
				continue
			}
			_, isProjected := locallyProjected[asset.Target]
			if previous, exists := mutableUsage[asset.Target]; exists && previous.projected != isProjected {
				return activationError("mutable seed ownership")
			}
			mutableUsage[asset.Target] = mutableSeedUsage{projected: isProjected, owner: name}
		}
	}
	if len(referenced) != len(assets) {
		return activationError("unreferenced asset")
	}
	if !sort.StringsAreSorted(manifest.EnvironmentNames) || len(environmentNames) != len(manifest.EnvironmentNames) {
		return activationError("environment identity")
	}
	for i, name := range manifest.EnvironmentNames {
		if name == "" || (i > 0 && name == manifest.EnvironmentNames[i-1]) {
			return activationError("environment order")
		}
		if _, exists := environmentNames[name]; !exists {
			return activationError("environment parity")
		}
	}
	for _, asset := range manifest.Assets {
		expectedDisposition, err := seedDisposition(harnesses.PortableRuntimeAsset{Kind: asset.Kind, Target: asset.Target}, projected)
		if err != nil || expectedDisposition != asset.SeedDisposition {
			return activationError("seed disposition")
		}
		if err := verifyActivationAsset(root, asset); err != nil {
			return err
		}
	}
	if err := validateAbsentRuntimeTargets(requiredAbsent, targets); err != nil {
		return activationError("required-absent runtime path")
	}
	return nil
}

func verifyActivationAsset(root *safefs.NoFollowRoot, asset ManifestAsset) error {
	return verifyActivationAssetWithHook(root, asset, nil)
}

func verifyActivationAssetWithHook(root *safefs.NoFollowRoot, asset ManifestAsset, afterRead func()) error {
	if asset.PathKind == harnesses.PortableRuntimePathTree {
		digest, err := activationTreeDigestWithHook(root, asset.Target, afterRead)
		if err != nil || digest != asset.MaterializedSHA256 {
			return activationError("asset tree content")
		}
		return nil
	}
	file, err := root.OpenReadNoFollow(asset.Target)
	if err != nil {
		return activationError("asset file read")
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm() != expectedDirectFileMode(asset) {
		return activationError("asset file mode")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return activationError("asset file content")
	}
	if afterRead != nil {
		afterRead()
	}
	after, err := file.Stat()
	if err != nil || !sameActivationFileInfo(before, after) {
		return activationError("asset file changed")
	}
	current, err := root.OpenReadNoFollow(asset.Target)
	if err != nil {
		return activationError("asset file revalidation")
	}
	currentInfo, statErr := current.Stat()
	closeErr := current.Close()
	if statErr != nil || closeErr != nil || !sameActivationFileInfo(after, currentInfo) {
		return activationError("asset file path changed")
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if digest != asset.MaterializedSHA256 || digest != asset.ContentSHA256 {
		return activationError("asset file identity")
	}
	return nil
}

func sameActivationFileInfo(left, right os.FileInfo) bool {
	return left != nil && right != nil && os.SameFile(left, right) && left.Mode() == right.Mode() &&
		left.Size() == right.Size() && left.ModTime().Equal(right.ModTime())
}

func readActivationMetadata(root *safefs.NoFollowRoot, target string) ([]byte, error) {
	file, err := root.OpenReadNoFollow(target)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return nil, os.ErrInvalid
	}
	data, err := io.ReadAll(io.LimitReader(file, activationMetadataLimit+1))
	if err != nil || len(data) > activationMetadataLimit {
		return nil, os.ErrInvalid
	}
	return data, nil
}

func cloneProviderSecrets(src []ProviderSecret) []ProviderSecret {
	out := make([]ProviderSecret, len(src))
	for i, secret := range src {
		out[i] = NewProviderSecret(secret.providerName, secret.apiKey, secret.headers)
	}
	return out
}

func cloneManifest(src Manifest) Manifest {
	out := src
	out.Inventory = append([]ManifestSurface(nil), src.Inventory...)
	out.Assets = append([]ManifestAsset(nil), src.Assets...)
	out.EnvironmentNames = append([]string(nil), src.EnvironmentNames...)
	out.Providers = cloneProviderSnapshot(src.Providers)
	out.Entrypoints = make(map[string]ManifestEntrypoint, len(src.Entrypoints))
	for name, entrypoint := range src.Entrypoints {
		entrypoint.Launch.RuntimeArgs = append([]string(nil), entrypoint.Launch.RuntimeArgs...)
		entrypoint.Launch.LibraryRootTargets = append([]string(nil), entrypoint.Launch.LibraryRootTargets...)
		entrypoint.AssetTargets = append([]string(nil), entrypoint.AssetTargets...)
		entrypoint.Environment = append([]harnesses.PortableRuntimeEnvironment(nil), entrypoint.Environment...)
		entrypoint.ExecutionConstraints.Environment = append([]harnesses.PortableRuntimeEnvironmentConstraint(nil), entrypoint.ExecutionConstraints.Environment...)
		entrypoint.ExecutionConstraints.ReadOnlyPaths = append([]harnesses.PortableRuntimeGuestPath(nil), entrypoint.ExecutionConstraints.ReadOnlyPaths...)
		entrypoint.ExecutionConstraints.RequiredAbsentPaths = append([]harnesses.PortableRuntimeGuestPath(nil), entrypoint.ExecutionConstraints.RequiredAbsentPaths...)
		entrypoint.ExecutionConstraints.FixedArguments = append([]string(nil), entrypoint.ExecutionConstraints.FixedArguments...)
		entrypoint.ExecutionConstraints.FixedOptionValues = append([]harnesses.PortableRuntimeFixedOptionValue(nil), entrypoint.ExecutionConstraints.FixedOptionValues...)
		entrypoint.StateProjections = append([]harnesses.PortableRuntimeStateProjection(nil), entrypoint.StateProjections...)
		for i := range entrypoint.StateProjections {
			entrypoint.StateProjections[i].Entries = append([]harnesses.PortableRuntimeStateProjectionEntry(nil), entrypoint.StateProjections[i].Entries...)
		}
		out.Entrypoints[name] = entrypoint
	}
	return out
}

func activationError(operation string) error {
	return fmt.Errorf("%w: %s", ErrActivationInvalid, operation)
}
