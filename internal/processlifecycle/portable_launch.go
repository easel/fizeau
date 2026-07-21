package processlifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"unicode/utf8"
)

// PortableNamespaceLauncher is the activation-verified executable identity
// for the fixed namespace child.  It is intentionally distinct from the
// governed harness target: the latter is data for the launcher protocol, not
// the process lifecycle's direct child.
type PortableNamespaceLauncher struct {
	path   string
	digest [sha256.Size]byte
}

// NewPortableNamespaceLauncher captures an exact, regular, owner-only
// launcher file.  The lifecycle seam repeats this descriptor check before it
// opens the launch gate; callers never get an accessor that could turn this
// into a generic command runner.
func NewPortableNamespaceLauncher(path string) (PortableNamespaceLauncher, error) {
	if !validPortableCommand(path) {
		return PortableNamespaceLauncher{}, fmt.Errorf("%w: portable namespace launcher path is invalid", ErrInvalidRecord)
	}
	// #nosec G304 -- validPortableCommand validates this explicit launcher path before descriptor identity and mode checks.
	file, err := os.Open(path)
	if err != nil {
		return PortableNamespaceLauncher{}, fmt.Errorf("%w: open portable namespace launcher", ErrInvalidRecord)
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil || !validPortableLauncherFile(before) {
		return PortableNamespaceLauncher{}, fmt.Errorf("%w: validate portable namespace launcher", ErrInvalidRecord)
	}
	digest, err := digestPortableLauncher(file)
	if err != nil {
		return PortableNamespaceLauncher{}, fmt.Errorf("%w: read portable namespace launcher", ErrInvalidRecord)
	}
	after, err := os.Stat(path)
	if err != nil || !os.SameFile(before, after) || !validPortableLauncherFile(after) || before.Mode() != after.Mode() {
		return PortableNamespaceLauncher{}, fmt.Errorf("%w: revalidate portable namespace launcher", ErrInvalidRecord)
	}
	return PortableNamespaceLauncher{path: path, digest: digest}, nil
}

func validPortableLauncherFile(info os.FileInfo) bool {
	return info != nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 && info.Mode().Perm()&0o022 == 0
}

func digestPortableLauncher(file *os.File) ([sha256.Size]byte, error) {
	var zero [sha256.Size]byte
	if _, err := file.Seek(0, 0); err != nil {
		return zero, err
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return zero, err
	}
	var digest [sha256.Size]byte
	copy(digest[:], hash.Sum(nil))
	return digest, nil
}

// PortableNamespaceIdentity is the single host identity authorized to back
// the outer portable user namespace.  It deliberately has no String or JSON
// representation: diagnostics must not disclose the activation identity.
type PortableNamespaceIdentity struct {
	uid int
	gid int
}

// NewPortableNamespaceIdentity constructs the only mapping shape supported by
// the portable launcher protocol.  Zero is deliberately rejected because it
// would make the host root identity the mapping authority.
func NewPortableNamespaceIdentity(uid, gid int) (PortableNamespaceIdentity, error) {
	if uid <= 0 || gid <= 0 {
		return PortableNamespaceIdentity{}, fmt.Errorf("%w: portable namespace identity is invalid", ErrInvalidRecord)
	}
	return PortableNamespaceIdentity{uid: uid, gid: gid}, nil
}

// PortableNamespaceLease couples an activation's exact mapping identity to
// its exclusive subprocess lease.  Release is idempotent and is invoked on
// every setup failure before a harness can be executed.
type PortableNamespaceLease struct {
	identity PortableNamespaceIdentity
	release  func()
}

// NewPortableNamespaceLease creates an opaque lease handoff for an activation
// recipe. It is intentionally an internal-package API, not a public runtime
// configuration surface.
func NewPortableNamespaceLease(identity PortableNamespaceIdentity, release func()) (PortableNamespaceLease, error) {
	if identity.uid <= 0 || identity.gid <= 0 || release == nil {
		return PortableNamespaceLease{}, fmt.Errorf("%w: portable namespace lease is invalid", ErrInvalidRecord)
	}
	return PortableNamespaceLease{identity: identity, release: release}, nil
}

// PortableLaunchRecipe is the opaque activation-owned recipe retained until
// the lifecycle-owned spawn seam. Its marker deliberately has no accessors:
// processlifecycle can carry it without learning namespace policy details.
//
// The method matches the portable-runtime recipe marker without importing the
// harness package, which would create a processlifecycle <-> harness cycle.
type PortableLaunchRecipe interface {
	PortableRuntimeNamespaceRecipe()
}

const (
	portableProjectionRecipeVersion = 1
	maxPortableProjectionRecords    = 128
	portableProjectionRecordBytes   = sha256.Size
)

// PortableProjectionRecipe is the sealed, descriptor-only handoff from an
// activation to the lifecycle-owned namespace launcher seam.  It intentionally
// carries no names, host paths, mount instructions, or source content: record
// bytes identify the activation-owned descriptors by position only.  The
// namespace launcher is the sole later consumer of those inherited positions.
//
// Callers construct this value only after descriptor pinning.  The constructor
// owns the record bytes but deliberately does not expose the descriptors or
// records back to callers or diagnostic encoders.
type PortableProjectionRecipe struct {
	version     int
	records     [][]byte
	descriptors []*os.File
}

func (r PortableProjectionRecipe) String() string {
	return fmt.Sprintf("{Version:%d DescriptorCount:%d}", r.version, len(r.descriptors))
}

func (r PortableProjectionRecipe) GoString() string { return r.String() }

func (r PortableProjectionRecipe) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Version         int `json:"version"`
		DescriptorCount int `json:"descriptor_count"`
	}{Version: r.version, DescriptorCount: len(r.descriptors)})
}

// NewPortableProjectionRecipe seals bounded opaque record IDs to their exact
// descriptor order.  A record is a fixed SHA-256-sized opaque identifier, not
// a pathname or a serialized mount instruction.  Descriptor aliases are
// rejected so one descriptor cannot acquire two projection identities.
func NewPortableProjectionRecipe(records [][]byte, descriptors []*os.File) (*PortableProjectionRecipe, error) {
	if len(records) == 0 || len(records) > maxPortableProjectionRecords || len(records) != len(descriptors) {
		return nil, fmt.Errorf("%w: portable projection recipe is invalid", ErrInvalidRecord)
	}
	owned := make([][]byte, len(records))
	for i, record := range records {
		if len(record) != portableProjectionRecordBytes || allZero(record) || pathShapedPortableProjectionRecord(record) {
			return nil, fmt.Errorf("%w: portable projection record %d is invalid", ErrInvalidRecord, i)
		}
		for prior := 0; prior < i; prior++ {
			if string(record) == string(records[prior]) {
				return nil, fmt.Errorf("%w: portable projection record %d aliases an earlier record", ErrInvalidRecord, i)
			}
		}
		owned[i] = append([]byte(nil), record...)
	}
	for i, descriptor := range descriptors {
		if descriptor == nil {
			return nil, fmt.Errorf("%w: portable projection descriptor %d is invalid", ErrInvalidRecord, i)
		}
		info, err := descriptor.Stat()
		if err != nil || !(info.Mode().IsRegular() || info.IsDir()) {
			return nil, fmt.Errorf("%w: portable projection descriptor %d is invalid", ErrInvalidRecord, i)
		}
		for prior := 0; prior < i; prior++ {
			priorInfo, priorErr := descriptors[prior].Stat()
			if priorErr != nil {
				return nil, fmt.Errorf("%w: portable projection descriptor %d is invalid", ErrInvalidRecord, prior)
			}
			if os.SameFile(info, priorInfo) {
				return nil, fmt.Errorf("%w: portable projection descriptor %d aliases an earlier descriptor", ErrInvalidRecord, i)
			}
		}
	}
	return &PortableProjectionRecipe{version: portableProjectionRecipeVersion, records: owned, descriptors: append([]*os.File(nil), descriptors...)}, nil
}

func allZero(value []byte) bool {
	for _, byteValue := range value {
		if byteValue != 0 {
			return false
		}
	}
	return true
}

func pathShapedPortableProjectionRecord(value []byte) bool {
	if !utf8.Valid(value) {
		return false
	}
	text := string(value)
	return strings.HasPrefix(text, "/") || strings.Contains(text, "../") || strings.Contains(text, "\\")
}

// portableNamespaceProjectionRecipe is deliberately an optional capability of
// an activation-owned recipe.  It keeps descriptor acquisition and projection
// semantics outside processlifecycle while requiring an explicit sealed
// handoff before a namespace child can be released.
type portableNamespaceProjectionRecipe interface {
	PortableNamespaceProjectionRecipe() (*PortableProjectionRecipe, error)
	RevalidatePortableProjectionDescriptors() error
}

// portableNamespaceLeaseRecipe is deliberately optional. Existing sealed
// launch attachments remain transport-neutral; only an activation-owned
// recipe can request the Linux outer namespace protocol.
type portableNamespaceLeaseRecipe interface {
	AcquirePortableNamespaceLease(context.Context) (uid, gid int, release func(), err error)
}

// portableNamespaceLauncherRecipe is deliberately optional for legacy sealed
// attachments. Activation-owned portable recipes implement it; test and
// transport-only attachments retain their existing no-namespace behavior.
type portableNamespaceLauncherRecipe interface {
	PortableNamespaceLauncherPath() string
}

// portableNamespaceIdentityRevalidationRecipe is implemented by an
// activation-owned recipe that can re-read the host identity it sealed during
// activation.  The lifecycle package deliberately receives only this
// yes-or-error capability: it must prove the identity is still exact without
// learning the identity itself.
type portableNamespaceIdentityRevalidationRecipe interface {
	RevalidatePortableNamespaceIdentity() error
}

// revalidatingPreparedBoundary keeps a prepared child gated until the
// activation identity has been checked at the last possible lifecycle seam.
// Acquire persists the boundary and calls Release exactly once; placing the
// check here prevents a drifted activation from releasing the launcher while
// preserving Acquire's existing abort behavior on a release failure.
type revalidatingPreparedBoundary struct {
	PreparedBoundary
	revalidate   func() error
	releaseLease func()
}

func (p revalidatingPreparedBoundary) Release(ctx context.Context) error {
	if p.revalidate == nil {
		if p.releaseLease != nil {
			p.releaseLease()
		}
		return fmt.Errorf("%w: portable activation identity revalidation is required", ErrInvalidRecord)
	}
	if err := p.revalidate(); err != nil {
		if p.releaseLease != nil {
			p.releaseLease()
		}
		return fmt.Errorf("revalidate portable activation identity: %w", err)
	}
	return p.PreparedBoundary.Release(ctx)
}

// PortableLaunchAttachment seals the exact command, argv, closed environment,
// and opaque recipe selected for a portable runtime route. Its fields are
// private and its diagnostic forms expose only cardinalities, so neither a
// command path, argument, environment value, nor recipe is accidentally
// emitted in lifecycle errors or logs.
type PortableLaunchAttachment struct {
	command     string
	arguments   []string
	environment []string
	recipe      PortableLaunchRecipe
	launcher    *PortableNamespaceLauncher
	projection  *PortableProjectionRecipe
}

// NewPortableLaunchAttachment validates and defensively owns a portable child
// specification. command must already be the exact absolute executable path;
// this constructor never resolves PATH, follows aliases, or reads the host
// filesystem. arguments exclude argv[0]. environment is a closed-world list
// of NAME=value entries and may be empty, but it must be non-nil so inherited
// owner environment is never mistaken for a portable launch.
func NewPortableLaunchAttachment(command string, arguments, environment []string, recipe PortableLaunchRecipe) (*PortableLaunchAttachment, error) {
	if !validPortableCommand(command) {
		return nil, fmt.Errorf("%w: portable command must be an exact absolute path", ErrInvalidRecord)
	}
	if environment == nil {
		return nil, fmt.Errorf("%w: portable environment must be closed", ErrInvalidRecord)
	}
	if recipe == nil || isNilPortableRecipe(recipe) {
		return nil, fmt.Errorf("%w: portable namespace recipe is required", ErrInvalidRecord)
	}
	for i, argument := range arguments {
		if strings.IndexByte(argument, 0) >= 0 {
			return nil, fmt.Errorf("%w: portable argument %d contains NUL", ErrInvalidRecord, i)
		}
	}
	seenNames := make(map[string]struct{}, len(environment))
	for i, entry := range environment {
		name, value, ok := strings.Cut(entry, "=")
		if !ok || !validPortableEnvironmentName(name) || strings.IndexByte(value, 0) >= 0 {
			return nil, fmt.Errorf("%w: portable environment entry %d is malformed", ErrInvalidRecord, i)
		}
		if _, exists := seenNames[name]; exists {
			return nil, fmt.Errorf("%w: portable environment entry %d aliases an earlier name", ErrInvalidRecord, i)
		}
		seenNames[name] = struct{}{}
	}
	attachment := &PortableLaunchAttachment{
		command: command, arguments: append([]string(nil), arguments...),
		environment: append([]string{}, environment...), recipe: recipe,
	}
	if launcherRecipe, ok := recipe.(portableNamespaceLauncherRecipe); ok {
		launcher, err := NewPortableNamespaceLauncher(launcherRecipe.PortableNamespaceLauncherPath())
		if err != nil {
			return nil, fmt.Errorf("%w: portable namespace launcher", ErrInvalidRecord)
		}
		attachment.launcher = &launcher
	}
	if projectionRecipe, ok := recipe.(portableNamespaceProjectionRecipe); ok {
		projection, err := projectionRecipe.PortableNamespaceProjectionRecipe()
		if err != nil || !validPortableProjectionRecipe(projection) {
			return nil, fmt.Errorf("%w: portable projection recipe", ErrInvalidRecord)
		}
		attachment.projection = projection.clone()
	}
	return attachment, nil
}

func validPortableProjectionRecipe(recipe *PortableProjectionRecipe) bool {
	if recipe == nil || recipe.version != portableProjectionRecipeVersion || len(recipe.records) == 0 || len(recipe.records) > maxPortableProjectionRecords || len(recipe.records) != len(recipe.descriptors) {
		return false
	}
	for _, record := range recipe.records {
		if len(record) != portableProjectionRecordBytes || allZero(record) || pathShapedPortableProjectionRecord(record) {
			return false
		}
	}
	return true
}

func (r *PortableProjectionRecipe) clone() *PortableProjectionRecipe {
	if r == nil {
		return nil
	}
	copyRecipe := &PortableProjectionRecipe{version: r.version, records: make([][]byte, len(r.records)), descriptors: append([]*os.File(nil), r.descriptors...)}
	for i := range r.records {
		copyRecipe.records[i] = append([]byte(nil), r.records[i]...)
	}
	return copyRecipe
}

func validPortableCommand(command string) bool {
	return command != "" && filepath.IsAbs(command) && filepath.Clean(command) == command && strings.IndexByte(command, 0) < 0
}

func validPortableEnvironmentName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '_' || (i > 0 && r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}

func isNilPortableRecipe(recipe PortableLaunchRecipe) bool {
	v := reflect.ValueOf(recipe)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

func (a PortableLaunchAttachment) String() string {
	return fmt.Sprintf("{ArgumentCount:%d EnvironmentCount:%d NamespaceRecipeConfigured:%t NamespaceLauncherConfigured:%t ProjectionRecipeConfigured:%t}", len(a.arguments), len(a.environment), a.recipe != nil, a.launcher != nil, a.projection != nil)
}

func (a PortableLaunchAttachment) GoString() string { return a.String() }

func (a PortableLaunchAttachment) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ArgumentCount               int  `json:"argument_count"`
		EnvironmentCount            int  `json:"environment_count"`
		NamespaceRecipeConfigured   bool `json:"namespace_recipe_configured"`
		NamespaceLauncherConfigured bool `json:"namespace_launcher_configured"`
		ProjectionRecipeConfigured  bool `json:"projection_recipe_configured"`
	}{len(a.arguments), len(a.environment), a.recipe != nil, a.launcher != nil, a.projection != nil})
}

func (a *PortableLaunchAttachment) clone() *PortableLaunchAttachment {
	if a == nil {
		return nil
	}
	return &PortableLaunchAttachment{
		command: a.command, arguments: append([]string(nil), a.arguments...),
		environment: append([]string{}, a.environment...), recipe: a.recipe,
		launcher: a.launcher, projection: a.projection.clone(),
	}
}

// validatePortableLaunchTarget proves that the caller did not alter the
// attachment while converting it to exec.Cmd. It runs before any lifecycle
// supervisor or child process is created.
func validatePortableLaunchTarget(target *exec.Cmd, attachment *PortableLaunchAttachment) error {
	if attachment == nil {
		return nil
	}
	if target == nil || target.Path != attachment.command || !equalStrings(target.Args, append([]string{attachment.command}, attachment.arguments...)) || !equalStrings(target.Env, attachment.environment) {
		return fmt.Errorf("%w: portable launch target differs from sealed attachment", ErrInvalidRecord)
	}
	return nil
}

func (a *PortableLaunchAttachment) commandForPTY(command string, arguments, environment []string) (*exec.Cmd, error) {
	if a == nil || command != a.command || !equalStrings(arguments, a.arguments) || !equalStrings(environment, a.environment) {
		return nil, fmt.Errorf("%w: portable PTY launch differs from sealed attachment", ErrInvalidRecord)
	}
	// #nosec G204 -- this command was validated as an exact portable path and
	// is immediately verified again at the lifecycle-owned spawn boundary.
	target := exec.Command(a.command, a.arguments...)
	target.Env = append([]string{}, a.environment...)
	return target, nil
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
