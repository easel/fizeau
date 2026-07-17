package portableruntime

import (
	"bytes"
	"context"
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
	"sort"
	"strings"

	"github.com/easel/fizeau/internal/harnesses"
)

type assetPlan struct {
	asset harnesses.PortableRuntimeAsset
}

type projectionClaim struct {
	directory harnesses.PortableRuntimeGuestPath
	encoded   string
}

type projectionAssetClaim struct {
	projection string
	entry      string
}

type projectionOutputClaim struct {
	path  harnesses.PortableRuntimeGuestPath
	owner projectionAssetClaim
}

type mutableSeedUsage struct {
	projected bool
	owner     string
}

// Prepare collects every required neutral contribution, materializes it into
// one sibling-staged private tree, and atomically commits one runtime child.
func Prepare(ctx context.Context, request Request) (_ *Bundle, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, requestError("preparation canceled")
	}
	if err := harnesses.ValidatePortableRuntimeTarget(request.Target); err != nil {
		return nil, requestError("unsupported target")
	}

	destination, err := openDestination(request.DestinationRoot)
	if err != nil {
		return nil, requestPathError(request.DestinationRoot, "destination validation", err)
	}
	defer func() {
		if destination != nil {
			destination.close()
		}
	}()

	manifest, plans, launcher, err := buildPlan(ctx, request)
	if err != nil {
		return nil, err
	}

	stage, err := destination.createStage()
	if err != nil {
		return nil, requestError("staging creation failed")
	}
	committed := false
	defer func() {
		if !committed {
			if cleanupErr := destination.removeStage(stage); cleanupErr != nil {
				cleanupFailure := fmt.Errorf("%w: staging rollback failed", ErrCleanupIncomplete)
				if err == nil {
					err = cleanupFailure
				} else {
					err = errors.Join(err, cleanupFailure)
				}
			}
		}
	}()

	receipts := make([]sourceReceipt, len(plans))
	for index := range plans {
		if err := checkContext(ctx); err != nil {
			return nil, err
		}
		materializedDigest, receipt, copyErr := materializeAsset(ctx, stage, plans[index].asset)
		if copyErr != nil {
			return nil, fmt.Errorf("%w: asset copy at index %d: %v", ErrClosureIncomplete, index, copyErr)
		}
		manifest.Assets[index].MaterializedSHA256 = materializedDigest
		receipts[index] = receipt
	}
	if launcher != nil {
		launcherDigest, writeErr := writeGeneratedFileWithMode(stage, namespaceLauncherTarget, launcher, 0o700)
		if writeErr != nil || manifest.NamespaceLauncher == nil || launcherDigest != manifest.NamespaceLauncher.ContentSHA256 {
			return nil, closureError("embedded runtime persistence", -1)
		}
	}

	secretBytes, err := marshalProviderSecrets(request.ProviderSecrets)
	if err != nil {
		return nil, closureError("provider snapshot", -1)
	}
	if len(secretBytes) > activationMetadataLimit {
		return nil, closureError("provider snapshot exceeds activation limit", -1)
	}
	secretDigest, err := writeGeneratedFile(stage, providerSecrets, secretBytes)
	if err != nil {
		return nil, closureError("provider snapshot persistence", -1)
	}
	manifest.ProviderSecretsFile = ManifestContentReference{Target: providerSecrets, ContentSHA256: secretDigest}

	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, closureError("manifest encoding", -1)
	}
	manifestBytes = append(manifestBytes, '\n')
	if len(manifestBytes) > activationMetadataLimit {
		return nil, closureError("manifest exceeds activation limit", -1)
	}
	manifestDigest, err := writeGeneratedFile(stage, manifestTarget, manifestBytes)
	if err != nil {
		return nil, closureError("manifest persistence", -1)
	}
	if _, err := writeGeneratedFile(stage, manifestSum, []byte(manifestDigest+"\n")); err != nil {
		return nil, closureError("manifest checksum persistence", -1)
	}

	for index := range receipts {
		if err := verifyAssetSource(ctx, receipts[index]); err != nil {
			return nil, closureError("asset revalidation", index)
		}
	}
	if err := verifyGeneratedFile(stage, manifestTarget, manifestDigest); err != nil {
		return nil, closureError("manifest verification", -1)
	}
	if err := verifyGeneratedFile(stage, providerSecrets, secretDigest); err != nil {
		return nil, closureError("provider snapshot verification", -1)
	}
	if err := verifyStagedBundle(stage, manifest, manifestDigest); err != nil {
		return nil, fmt.Errorf("%w: staged bundle verification: %v", ErrClosureIncomplete, err)
	}
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	if err := destination.revalidateEmpty(); err != nil {
		return nil, requestError("destination changed before commit")
	}
	if err := destination.commit(stage); err != nil {
		return nil, portableRuntimeCommitError(err)
	}
	committed = true

	runtimeRoot := filepath.Join(destination.absolute, "runtime")
	anchor := destination.takeDirectory()
	identity := stage.identity
	bundle := &Bundle{
		runtimeRoot: runtimeRoot,
		mounts:      []Mount{{Source: runtimeRoot, Target: GuestRoot, ReadOnly: true}},
		environment: append([]string(nil), manifest.EnvironmentNames...),
		anchor:      anchor,
	}
	bundle.cleanup = func() error {
		return removeCommittedRuntime(anchor, identity)
	}
	return bundle, nil
}

func portableRuntimeCommitError(cause error) error {
	requestFailure := requestError("runtime commit failed")
	if errors.Is(cause, ErrCleanupIncomplete) {
		return errors.Join(requestFailure, fmt.Errorf("%w: post-commit rollback failed", ErrCleanupIncomplete))
	}
	return requestFailure
}

func buildPlan(ctx context.Context, request Request) (Manifest, []assetPlan, []byte, error) {
	manifest := Manifest{
		Version:      manifestVersion,
		TargetGOOS:   request.Target.GOOS,
		TargetGOARCH: request.Target.GOARCH,
		GuestRoot:    GuestRoot,
		Providers:    cloneProviderSnapshot(request.Providers),
		Entrypoints:  make(map[string]ManifestEntrypoint),
	}
	if err := validateProviders(request.Providers, request.ProviderSecrets); err != nil {
		return Manifest{}, nil, nil, err
	}

	rows := append([]harnesses.PortableRuntimeSurface(nil), request.Inventory...)
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	seenRows := make(map[string]struct{}, len(rows))
	assets := make(map[string]*assetPlan)
	projectedSeeds := make(map[string]struct{})
	mutableUsage := make(map[string]mutableSeedUsage)
	projectionClaims := make([]projectionClaim, 0)
	projectionAssets := make(map[string]projectionAssetClaim)
	projectionOutputs := make([]projectionOutputClaim, 0)
	requiredAbsent := make([]harnesses.PortableRuntimeGuestPath, 0)
	environmentNames := make(map[string]struct{})
	forbiddenValues := []string{request.DestinationRoot}
	for _, secret := range request.ProviderSecrets {
		forbiddenValues = append(forbiddenValues, secret.apiKey)
		for _, value := range secret.headers {
			forbiddenValues = append(forbiddenValues, value)
		}
	}
	for rowIndex, row := range rows {
		if err := checkContext(ctx); err != nil {
			return Manifest{}, nil, nil, err
		}
		if err := validateSurface(row, rowIndex, seenRows); err != nil {
			return Manifest{}, nil, nil, err
		}
		seenRows[row.Name] = struct{}{}
		manifest.Inventory = append(manifest.Inventory, ManifestSurface{Name: row.Name, Transport: row.Transport, Inclusion: row.Inclusion})
		if row.Inclusion != harnesses.PortableRuntimeInclusionRequired {
			continue
		}
		contributor, ok := row.Instance.(harnesses.PortableRuntimeHarness)
		if !ok {
			return Manifest{}, nil, nil, closureError("required contributor capability", rowIndex)
		}
		contribution, err := contributor.PortableRuntimeAssets(ctx, request.Target)
		if err != nil {
			return Manifest{}, nil, nil, closureError("required contributor discovery", rowIndex)
		}
		normalized, err := harnesses.NormalizePortableRuntimeContribution(request.Target, contribution)
		if err != nil {
			return Manifest{}, nil, nil, closureError("required contributor normalization", rowIndex)
		}
		manifest.Entrypoints[row.Name] = manifestEntrypoint(row.Name, normalized)
		for _, environment := range normalized.Environment {
			value, present := os.LookupEnv(environment.Name)
			if !present {
				return Manifest{}, nil, nil, closureError("required inherited environment", rowIndex)
			}
			environmentNames[environment.Name] = struct{}{}
			forbiddenValues = append(forbiddenValues, value)
		}
		if err := mergeProjectionClaims(&projectionClaims, projectionAssets, &projectionOutputs, &requiredAbsent, normalized); err != nil {
			return Manifest{}, nil, nil, closureError("state projection conflict", rowIndex)
		}
		localProjected := make(map[string]struct{})
		for _, projection := range normalized.StateProjections {
			for _, entry := range projection.Entries {
				localProjected[entry.AssetTarget] = struct{}{}
				projectedSeeds[entry.AssetTarget] = struct{}{}
			}
		}
		for _, asset := range normalized.Assets {
			if mutableAsset(asset.Kind) {
				_, projected := localProjected[asset.Target]
				if previous, exists := mutableUsage[asset.Target]; exists && previous.projected != projected {
					return Manifest{}, nil, nil, closureError("mutable seed ownership conflict", rowIndex)
				}
				mutableUsage[asset.Target] = mutableSeedUsage{projected: projected, owner: row.Name}
			}
			forbiddenValues = append(forbiddenValues, asset.Source)
			if err := mergeAssetPlan(assets, asset); err != nil {
				return Manifest{}, nil, nil, closureError("asset target conflict", rowIndex)
			}
		}
	}

	targets := make([]string, 0, len(assets))
	for target := range assets {
		targets = append(targets, target)
	}
	sort.Strings(targets)
	if err := validateTargetDisjointness(targets); err != nil {
		return Manifest{}, nil, nil, err
	}
	var launcher []byte
	runtimeTargets := append([]string(nil), targets...)
	if len(manifest.Entrypoints) > 0 {
		artifact, artifactErr := namespaceLauncherForTarget(request.Target)
		if artifactErr != nil {
			return Manifest{}, nil, nil, closureError("embedded runtime identity", -1)
		}
		launcher = artifact.bytes
		manifest.NamespaceLauncher = &ManifestContentReference{Target: namespaceLauncherTarget, ContentSHA256: artifact.digest}
		runtimeTargets = append(runtimeTargets, namespaceLauncherTarget)
	}
	if err := validateAbsentRuntimeTargets(requiredAbsent, runtimeTargets); err != nil {
		return Manifest{}, nil, nil, err
	}
	plans := make([]assetPlan, 0, len(targets))
	for _, target := range targets {
		plan := *assets[target]
		plans = append(plans, plan)
		disposition, err := seedDisposition(plan.asset, projectedSeeds)
		if err != nil {
			return Manifest{}, nil, nil, err
		}
		manifest.Assets = append(manifest.Assets, ManifestAsset{
			Kind:            plan.asset.Kind,
			PathKind:        plan.asset.PathKind,
			Target:          plan.asset.Target,
			ContentSHA256:   plan.asset.ContentSHA256,
			Executable:      plan.asset.Executable,
			SeedDisposition: disposition,
		})
	}
	for name := range environmentNames {
		manifest.EnvironmentNames = append(manifest.EnvironmentNames, name)
	}
	sort.Strings(manifest.EnvironmentNames)
	if err := validateActivationGeneratedPaths(manifest); err != nil {
		return Manifest{}, nil, nil, closureError("generated path conflicts with required-absent path", -1)
	}
	if err := validateManifestText(manifest, forbiddenValues); err != nil {
		return Manifest{}, nil, nil, err
	}
	return manifest, plans, launcher, nil
}

func mutableAsset(kind harnesses.PortableRuntimeAssetKind) bool {
	switch kind {
	case harnesses.PortableRuntimeAssetCredential, harnesses.PortableRuntimeAssetQuota, harnesses.PortableRuntimeAssetCache:
		return true
	default:
		return false
	}
}

func validateAbsentRuntimeTargets(absent []harnesses.PortableRuntimeGuestPath, targets []string) error {
	for absentIndex, required := range absent {
		if required.Scope != harnesses.PortableRuntimeGuestPathRuntime {
			continue
		}
		for _, target := range targets {
			if pathsOverlap(required.Target, target) {
				return closureError("required-absent path overlaps full inventory asset", absentIndex)
			}
		}
	}
	return nil
}

func mergeProjectionClaims(claims *[]projectionClaim, assets map[string]projectionAssetClaim, outputs *[]projectionOutputClaim, absent *[]harnesses.PortableRuntimeGuestPath, contribution harnesses.PortableRuntimeContribution) error {
	for _, projection := range contribution.StateProjections {
		encodedBytes, err := json.Marshal(projection)
		if err != nil {
			return err
		}
		encoded := string(encodedBytes)
		for _, existing := range *claims {
			if !guestPathsOverlap(existing.directory, projection.Directory) {
				continue
			}
			if existing.directory != projection.Directory || existing.encoded != encoded {
				return errors.New("overlapping projection ownership")
			}
			encoded = ""
			break
		}
		if encoded != "" {
			*claims = append(*claims, projectionClaim{directory: projection.Directory, encoded: encoded})
		}
		for _, entry := range projection.Entries {
			entryBytes, err := json.Marshal(entry)
			if err != nil {
				return err
			}
			claim := projectionAssetClaim{projection: string(encodedBytes), entry: string(entryBytes)}
			if previous, exists := assets[entry.AssetTarget]; exists && previous != claim {
				return errors.New("projection asset has multiple owners")
			}
			assets[entry.AssetTarget] = claim
			output := harnesses.PortableRuntimeGuestPath{Scope: projection.Directory.Scope, Target: path.Join(projection.Directory.Target, entry.Target)}
			duplicate := false
			for _, previous := range *outputs {
				if !guestPathsOverlap(previous.path, output) {
					continue
				}
				if previous.path != output || previous.owner != claim {
					return errors.New("projection outputs overlap")
				}
				duplicate = true
			}
			for _, required := range *absent {
				if guestPathsOverlap(required, output) {
					return errors.New("projection output overlaps required-absent path")
				}
			}
			if !duplicate {
				*outputs = append(*outputs, projectionOutputClaim{path: output, owner: claim})
			}
		}
	}
	for _, path := range contribution.ExecutionConstraints.RequiredAbsentPaths {
		for _, output := range *outputs {
			if guestPathsOverlap(path, output.path) {
				return errors.New("required-absent path overlaps projection output")
			}
		}
		for _, previous := range *absent {
			if guestPathsOverlap(path, previous) && path != previous {
				return errors.New("required-absent paths overlap")
			}
		}
		*absent = append(*absent, path)
	}
	return nil
}

func guestPathsOverlap(left, right harnesses.PortableRuntimeGuestPath) bool {
	if left.Scope != right.Scope {
		return false
	}
	return pathsOverlap(left.Target, right.Target)
}

func validateManifestText(manifest Manifest, forbidden []string) error {
	fields := manifestTextFields(manifest)
	for _, value := range forbidden {
		if value == "" {
			continue
		}
		for _, field := range fields {
			if strings.Contains(field, value) {
				return closureError("manifest contains host-derived material", -1)
			}
		}
	}
	for index, provider := range manifest.Providers.Providers {
		if containsHostPath(provider.ConfigError) {
			return closureError("provider configuration diagnostics", index)
		}
	}
	return nil
}

func manifestTextFields(manifest Manifest) []string {
	fields := []string{manifest.TargetGOOS, manifest.TargetGOARCH, manifest.GuestRoot,
		manifest.ProviderSecretsFile.Target, manifest.ProviderSecretsFile.ContentSHA256,
		manifest.Providers.DefaultProviderName, manifest.Providers.WorkDir.Field,
		manifest.Providers.WorkDir.Treatment, manifest.Providers.WorkDir.Reason,
		manifest.Providers.SessionLogDir.Field, manifest.Providers.SessionLogDir.Treatment,
		manifest.Providers.SessionLogDir.Reason,
	}
	if manifest.NamespaceLauncher != nil {
		fields = append(fields, manifest.NamespaceLauncher.Target, manifest.NamespaceLauncher.ContentSHA256)
	}
	fields = append(fields, manifest.EnvironmentNames...)
	fields = append(fields, manifest.Providers.ProviderNames...)
	for _, surface := range manifest.Inventory {
		fields = append(fields, surface.Name, string(surface.Transport), string(surface.Inclusion))
	}
	for name, entrypoint := range manifest.Entrypoints {
		fields = append(fields, name, entrypoint.Name, string(entrypoint.ClosureClass),
			entrypoint.Launch.EntrypointTarget, entrypoint.Launch.EntrypointTreeMember,
			entrypoint.Launch.InterpreterTarget, entrypoint.Launch.LoaderTarget)
		fields = append(fields, entrypoint.Launch.RuntimeArgs...)
		fields = append(fields, entrypoint.Launch.LibraryRootTargets...)
		fields = append(fields, entrypoint.AssetTargets...)
		for _, inherited := range entrypoint.Environment {
			fields = append(fields, inherited.Name)
		}
		constraints := entrypoint.ExecutionConstraints
		fields = append(fields, constraints.FixedArguments...)
		for _, pair := range constraints.FixedOptionValues {
			fields = append(fields, pair.Option, pair.Value)
		}
		for _, environment := range constraints.Environment {
			fields = append(fields, environment.Name, string(environment.Kind), string(environment.GuestPath.Scope), environment.GuestPath.Target)
		}
		for _, guestPath := range append(append([]harnesses.PortableRuntimeGuestPath(nil), constraints.ReadOnlyPaths...), constraints.RequiredAbsentPaths...) {
			fields = append(fields, string(guestPath.Scope), guestPath.Target)
		}
		for _, projection := range entrypoint.StateProjections {
			fields = append(fields, string(projection.Directory.Scope), projection.Directory.Target)
			for _, projectionEntry := range projection.Entries {
				fields = append(fields, projectionEntry.AssetTarget, projectionEntry.Target)
			}
		}
	}
	for _, asset := range manifest.Assets {
		fields = append(fields, string(asset.Kind), string(asset.PathKind), asset.Target,
			asset.ContentSHA256, asset.MaterializedSHA256, string(asset.SeedDisposition))
	}
	for _, provider := range manifest.Providers.Providers {
		fields = append(fields, provider.Name, provider.Type, provider.BaseURL, provider.ServerInstance,
			provider.Model, provider.Billing, provider.ConfigError)
		for _, endpoint := range provider.Endpoints {
			fields = append(fields, endpoint.Name, endpoint.BaseURL, endpoint.ServerInstance)
		}
	}
	return fields
}

func containsHostPath(value string) bool {
	if strings.Contains(value, "\\") || strings.Contains(value, "/home/") || strings.Contains(value, "/Users/") {
		return true
	}
	for _, token := range strings.FieldsFunc(value, func(r rune) bool {
		switch r {
		case ' ', '\t', '\n', '\r', '"', '\'', '(', ')', '[', ']', '{', '}', ',', ';':
			return true
		default:
			return false
		}
	}) {
		if strings.HasPrefix(token, "/") && !strings.HasPrefix(token, "//") {
			return true
		}
	}
	return false
}

func validateSurface(row harnesses.PortableRuntimeSurface, index int, seen map[string]struct{}) error {
	if row.Name == "" {
		return closureError("inventory identity", index)
	}
	if _, exists := seen[row.Name]; exists {
		return closureError("inventory duplicate", index)
	}
	switch row.Transport {
	case harnesses.PortableRuntimeTransportSubprocess,
		harnesses.PortableRuntimeTransportNative,
		harnesses.PortableRuntimeTransportEmbedded,
		harnesses.PortableRuntimeTransportHTTP:
	default:
		return closureError("inventory transport", index)
	}
	switch row.Inclusion {
	case harnesses.PortableRuntimeInclusionRequired:
		if row.Transport != harnesses.PortableRuntimeTransportSubprocess || row.Instance == nil {
			return closureError("required inventory surface", index)
		}
	case harnesses.PortableRuntimeInclusionExactPinOnly:
		if row.Transport != harnesses.PortableRuntimeTransportSubprocess || row.Instance == nil {
			return closureError("exact-pin inventory surface", index)
		}
	case harnesses.PortableRuntimeInclusionNonSubprocess,
		harnesses.PortableRuntimeInclusionTestOnly:
	default:
		return closureError("inventory inclusion", index)
	}
	return nil
}

func mergeAssetPlan(plans map[string]*assetPlan, asset harnesses.PortableRuntimeAsset) error {
	existing, ok := plans[asset.Target]
	if !ok {
		plans[asset.Target] = &assetPlan{asset: asset}
		return nil
	}
	if existing.asset.Source != asset.Source || existing.asset.Kind != asset.Kind || existing.asset.PathKind != asset.PathKind ||
		existing.asset.ContentSHA256 != asset.ContentSHA256 || existing.asset.Executable != asset.Executable {
		return errors.New("conflicting asset identity")
	}
	return nil
}

func validateTargetDisjointness(targets []string) error {
	reserved := []string{".fizeau", manifestTarget, manifestSum, providerSecrets, namespaceLauncherTarget}
	for index, target := range targets {
		for _, privateTarget := range reserved {
			if pathsOverlap(target, privateTarget) {
				return closureError("reserved asset target", index)
			}
		}
		if index > 0 && pathsOverlap(targets[index-1], target) {
			return closureError("overlapping asset targets", index)
		}
	}
	return nil
}

func pathsOverlap(left, right string) bool {
	return left == right || strings.HasPrefix(left, right+"/") || strings.HasPrefix(right, left+"/")
}

func seedDisposition(asset harnesses.PortableRuntimeAsset, projected map[string]struct{}) (SeedDisposition, error) {
	switch asset.Kind {
	case harnesses.PortableRuntimeAssetCredential, harnesses.PortableRuntimeAssetQuota, harnesses.PortableRuntimeAssetCache:
		if _, ok := projected[asset.Target]; ok {
			return SeedProjectionConsumed, nil
		}
		prefix, suffix, found := strings.Cut(asset.Target, "/")
		if !found || suffix == "" {
			return SeedNone, closureError("mutable seed prefix", -1)
		}
		if prefix != "data" && prefix != "state" && prefix != "cache" {
			return SeedNone, closureError("mutable seed prefix", -1)
		}
		return SeedPrefixPreserving, nil
	default:
		return SeedNone, nil
	}
}

func manifestEntrypoint(name string, contribution harnesses.PortableRuntimeContribution) ManifestEntrypoint {
	assetTargets := make([]string, len(contribution.Assets))
	for index := range contribution.Assets {
		assetTargets[index] = contribution.Assets[index].Target
	}
	return ManifestEntrypoint{
		Name:                 name,
		ClosureClass:         contribution.ClosureClass,
		Launch:               contribution.Launch,
		AssetTargets:         assetTargets,
		Environment:          append([]harnesses.PortableRuntimeEnvironment(nil), contribution.Environment...),
		ExecutionConstraints: contribution.ExecutionConstraints,
		StateProjections:     contribution.StateProjections,
	}
}

func marshalProviderSecrets(secrets []ProviderSecret) ([]byte, error) {
	records := make([]privateProviderSecret, len(secrets))
	for index := range secrets {
		records[index] = secrets[index].privateRecord()
	}
	data, err := json.MarshalIndent(privateProviderSecretsDocument{Version: manifestVersion, Providers: records}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func writeGeneratedFile(stage *stageHandle, target string, data []byte) (string, error) {
	return writeGeneratedFileWithMode(stage, target, data, 0o600)
}

func writeGeneratedFileWithMode(stage *stageHandle, target string, data []byte, mode uint32) (string, error) {
	if stage == nil || stage.file == nil {
		return "", errors.New("staging handle is unavailable")
	}
	parentFD, leaf, err := createTargetParent(descriptorFD(stage.file), target)
	if err != nil {
		return "", err
	}
	defer closeDescriptor(parentFD)
	fd, err := openExclusiveRegularAt(parentFD, leaf, mode)
	if err != nil {
		return "", err
	}
	file := newDescriptorFile(fd, "portable-runtime-generated")
	hash := sha256.New()
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return "", err
	}
	_, _ = hash.Write(data)
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func verifyGeneratedFile(stage *stageHandle, target, expected string) error {
	if stage == nil || stage.file == nil {
		return errors.New("staging handle is unavailable")
	}
	file, err := openTargetRegular(descriptorFD(stage.file), target)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}
	if hex.EncodeToString(hash.Sum(nil)) != expected {
		return errors.New("generated content changed")
	}
	return nil
}

func verifyStagedBundle(stage *stageHandle, expected Manifest, expectedDigest string) error {
	manifestBytes, err := readTargetRegular(stage, manifestTarget)
	if err != nil {
		return err
	}
	sumBytes, err := readTargetRegular(stage, manifestSum)
	if err != nil {
		return err
	}
	actual, err := decodeManifest(manifestBytes, sumBytes, expected)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(manifestBytes)
	if hex.EncodeToString(digest[:]) != expectedDigest {
		return errors.New("manifest checksum does not match generated identity")
	}
	for _, asset := range actual.Assets {
		var materialized string
		if asset.PathKind == harnesses.PortableRuntimePathTree {
			materialized, err = harnesses.PortableRuntimeTreeDigest(filepath.Join(stage.path, filepath.FromSlash(asset.Target)))
		} else {
			file, openErr := openTargetRegular(descriptorFD(stage.file), asset.Target)
			if openErr != nil {
				return openErr
			}
			info, statErr := file.Stat()
			if statErr != nil || info.Mode().Perm() != expectedDirectFileMode(asset) {
				_ = file.Close()
				return errors.New("materialized asset mode changed")
			}
			hash := sha256.New()
			_, err = io.Copy(hash, file)
			closeErr := file.Close()
			if err == nil {
				err = closeErr
			}
			materialized = hex.EncodeToString(hash.Sum(nil))
		}
		if err != nil || materialized != asset.MaterializedSHA256 {
			return errors.New("materialized asset content changed")
		}
	}
	if err := verifyStagedNamespaceLauncher(stage, actual); err != nil {
		return err
	}
	for _, target := range []string{manifestTarget, manifestSum, providerSecrets} {
		file, err := openTargetRegular(descriptorFD(stage.file), target)
		if err != nil {
			return err
		}
		info, statErr := file.Stat()
		closeErr := file.Close()
		if statErr != nil || closeErr != nil || info.Mode().Perm() != 0o600 {
			return errors.New("generated file mode changed")
		}
	}
	return verifyRestrictiveTree(stage)
}

func verifyStagedNamespaceLauncher(stage *stageHandle, manifest Manifest) error {
	if manifest.NamespaceLauncher == nil {
		return nil
	}
	artifact, err := namespaceLauncherForTarget(harnesses.PortableRuntimeTarget{GOOS: manifest.TargetGOOS, GOARCH: manifest.TargetGOARCH})
	if err != nil || manifest.NamespaceLauncher.Target != namespaceLauncherTarget || manifest.NamespaceLauncher.ContentSHA256 != artifact.digest {
		return errors.New("embedded runtime identity changed")
	}
	file, err := openTargetRegular(descriptorFD(stage.file), namespaceLauncherTarget)
	if err != nil {
		return err
	}
	info, statErr := file.Stat()
	data, readErr := io.ReadAll(file)
	closeErr := file.Close()
	if statErr != nil || readErr != nil || closeErr != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o700 || !bytes.Equal(data, artifact.bytes) {
		return errors.New("embedded runtime content changed")
	}
	return nil
}

func decodeManifest(manifestBytes, sumBytes []byte, expected Manifest) (Manifest, error) {
	digest := sha256.Sum256(manifestBytes)
	encodedDigest := hex.EncodeToString(digest[:])
	if string(sumBytes) != encodedDigest+"\n" {
		return Manifest{}, errors.New("manifest checksum record is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(manifestBytes))
	decoder.DisallowUnknownFields()
	var actual Manifest
	if err := decoder.Decode(&actual); err != nil {
		return Manifest{}, errors.New("manifest cannot be decoded strictly")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Manifest{}, errors.New("manifest has trailing content")
	}
	if actual.Version != manifestVersion || actual.GuestRoot != GuestRoot || actual.TargetGOOS != expected.TargetGOOS || actual.TargetGOARCH != expected.TargetGOARCH ||
		!reflect.DeepEqual(actual, expected) {
		return Manifest{}, errors.New("manifest round trip changed its structural content")
	}
	return actual, nil
}

func expectedDirectFileMode(asset ManifestAsset) os.FileMode {
	if asset.Executable {
		return 0o700
	}
	return 0o600
}

func readTargetRegular(stage *stageHandle, target string) ([]byte, error) {
	if stage == nil || stage.file == nil {
		return nil, errors.New("staging handle is unavailable")
	}
	file, err := openTargetRegular(descriptorFD(stage.file), target)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(file)
}

func verifyRestrictiveTree(stage *stageHandle) error {
	return verifyRestrictiveMaterialization(stage)
}

func validateProviders(snapshot ProviderSnapshot, secrets []ProviderSecret) error {
	if len(snapshot.ProviderNames) != len(snapshot.Providers) || len(snapshot.ProviderNames) != len(secrets) {
		return closureError("provider snapshot cardinality", -1)
	}
	seen := make(map[string]struct{}, len(snapshot.ProviderNames))
	for index, name := range snapshot.ProviderNames {
		if name == "" || snapshot.Providers[index].Name != name || secrets[index].providerName != name {
			return closureError("provider snapshot identity", index)
		}
		if _, exists := seen[name]; exists {
			return closureError("provider snapshot duplicate", index)
		}
		seen[name] = struct{}{}
	}
	if snapshot.DefaultProviderName != "" {
		if _, ok := seen[snapshot.DefaultProviderName]; !ok {
			return closureError("default provider identity", -1)
		}
	}
	return nil
}

func cloneProviderSnapshot(snapshot ProviderSnapshot) ProviderSnapshot {
	out := snapshot
	out.ProviderNames = append([]string(nil), snapshot.ProviderNames...)
	out.Providers = append([]ConfiguredProvider(nil), snapshot.Providers...)
	for index := range out.Providers {
		out.Providers[index].Endpoints = append([]ProviderEndpoint(nil), snapshot.Providers[index].Endpoints...)
	}
	return out
}

func checkContext(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return fmt.Errorf("%w: preparation canceled", ErrClosureIncomplete)
	default:
		return nil
	}
}

func requestError(operation string) error {
	return fmt.Errorf("%w: %s", ErrRequestInvalid, operation)
}

func requestPathError(destination, operation string, cause error) error {
	return fmt.Errorf("%w: %s %q: %v", ErrRequestInvalid, operation, destination, cause)
}

func closureError(operation string, index int) error {
	if index >= 0 {
		return fmt.Errorf("%w: %s at index %d", ErrClosureIncomplete, operation, index)
	}
	return fmt.Errorf("%w: %s", ErrClosureIncomplete, operation)
}

func validDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && value == strings.ToLower(value)
}

func cleanTarget(target string) bool {
	if target == "" || target != path.Clean(target) || path.IsAbs(target) || strings.Contains(target, "\\") || strings.ContainsRune(target, 0) {
		return false
	}
	for _, component := range strings.Split(target, "/") {
		if component == "" || component == "." || component == ".." {
			return false
		}
	}
	return true
}
