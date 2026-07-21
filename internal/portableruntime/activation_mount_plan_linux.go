//go:build linux

package portableruntime

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/easel/fizeau/internal/harnesses"
)

// The mount-plan vocabulary is deliberately closed.  The namespace launcher
// receives activation-owned descriptors and must not infer a role or operation
// from a descriptor name, its host path, or a caller-controlled string.
type portableNamespaceProjectionRole uint8

const (
	portableProjectionRoleGovernedRoot portableNamespaceProjectionRole = iota + 1
	portableProjectionRoleActivationRoot
	portableProjectionRoleScopeRoot
	portableProjectionRoleProjectionDirectory
	portableProjectionRoleImmutableConfig
	portableProjectionRoleRequiredAbsentParent
)

type portableNamespaceProjectionOperation uint8

const (
	portableProjectionOperationPrivateRoot portableNamespaceProjectionOperation = iota + 1
	portableProjectionOperationAttachDirectory
	portableProjectionOperationReadOnlyBind
	portableProjectionOperationValidateAbsent
)

const maxPortableNamespaceProjectionPlanRecords = 128

// portableNamespaceProjectionPlan is the activation-owned semantic companion
// to the descriptor pins.  It carries no host path, source name, mutable leaf,
// or descriptor number.  Records refer only to the plan-owned descriptor
// position and a guest-root-relative target.  The later protocol seam owns
// serialization and inherited-FD numbering.
type portableNamespaceProjectionPlan struct {
	descriptors []portableNamespaceProjectionDescriptor
	records     []portableNamespaceProjectionRecord
}

type portableNamespaceProjectionDescriptor struct {
	object   activationDescriptorPin
	identity fileIdentity
}

type portableNamespaceProjectionRecord struct {
	descriptorIndex uint8
	role            portableNamespaceProjectionRole
	target          string
	operation       portableNamespaceProjectionOperation
	order           uint16
}

func (p portableNamespaceProjectionPlan) String() string {
	return fmt.Sprintf("{DescriptorCount:%d RecordCount:%d}", len(p.descriptors), len(p.records))
}

func (p portableNamespaceProjectionPlan) GoString() string { return p.String() }

// PortableNamespaceProjectionPlan returns the opaque, activation-owned mount
// semantics for the descriptor pins.  The unexported return type deliberately
// prevents callers from constructing or mutating a plan; package-internal
// lifecycle adapters may carry it to the fixed launcher in a later seam.
func (r ActivationRecipe) PortableNamespaceProjectionPlan() (*portableNamespaceProjectionPlan, error) {
	if r.projection == nil || r.projection.plan == nil || !r.projection.plan.valid() {
		return nil, activationError("projection mount plan")
	}
	return r.projection.plan.clone(), nil
}

func (p *portableNamespaceProjectionPlan) clone() *portableNamespaceProjectionPlan {
	if p == nil {
		return nil
	}
	return &portableNamespaceProjectionPlan{
		descriptors: append([]portableNamespaceProjectionDescriptor(nil), p.descriptors...),
		records:     append([]portableNamespaceProjectionRecord(nil), p.records...),
	}
}

func (p *portableNamespaceProjectionPlan) valid() bool {
	if p == nil || len(p.records) == 0 || len(p.records) > maxPortableNamespaceProjectionPlanRecords || len(p.descriptors) == 0 || len(p.descriptors) > maxPortableNamespaceProjectionPlanRecords {
		return false
	}
	seen := make(map[string]struct{}, len(p.records))
	immutableStarted := false
	for index, record := range p.records {
		if int(record.descriptorIndex) >= len(p.descriptors) || record.order != uint16(index) ||
			!validPortableProjectionTarget(record.target) ||
			!validPortableProjectionRecordSemantics(record.role, record.operation) {
			return false
		}
		if record.role == portableProjectionRoleImmutableConfig {
			immutableStarted = true
		} else if immutableStarted {
			return false
		}
		key := fmt.Sprintf("%d:%d:%s", record.role, record.operation, record.target)
		if _, exists := seen[key]; exists {
			return false
		}
		seen[key] = struct{}{}
	}
	for _, descriptor := range p.descriptors {
		if descriptor.object.object == nil || descriptor.identity == (fileIdentity{}) {
			return false
		}
	}
	return true
}

func validPortableProjectionTarget(target string) bool {
	if target == "." {
		return true
	}
	return target != "" && !strings.HasPrefix(target, "/") && !strings.Contains(target, "\\") &&
		!strings.ContainsRune(target, 0) && path.Clean(target) == target && target != ".." && !strings.HasPrefix(target, "../")
}

func validPortableProjectionRecordSemantics(role portableNamespaceProjectionRole, operation portableNamespaceProjectionOperation) bool {
	switch role {
	case portableProjectionRoleGovernedRoot:
		return operation == portableProjectionOperationPrivateRoot
	case portableProjectionRoleActivationRoot, portableProjectionRoleScopeRoot, portableProjectionRoleProjectionDirectory:
		return operation == portableProjectionOperationAttachDirectory
	case portableProjectionRoleImmutableConfig:
		return operation == portableProjectionOperationReadOnlyBind
	case portableProjectionRoleRequiredAbsentParent:
		return operation == portableProjectionOperationValidateAbsent
	default:
		return false
	}
}

func compilePortableNamespaceProjectionPlan(pinned *activationProjectionRecipe, entrypoint ManifestEntrypoint, recipe ActivationRecipe) (*portableNamespaceProjectionPlan, error) {
	if pinned == nil || len(pinned.directories) < 6 || len(pinned.sources) != len(recipe.immutableBindings) || len(pinned.absent) != len(recipe.requiredAbsent) {
		return nil, activationError("projection mount plan")
	}

	plan := &portableNamespaceProjectionPlan{}
	add := func(pin activationDescriptorPin, role portableNamespaceProjectionRole, target string, operation portableNamespaceProjectionOperation) error {
		if !validPortableProjectionTarget(target) || !validPortableProjectionRecordSemantics(role, operation) || pin.object == nil || pin.identity == (fileIdentity{}) {
			return activationError("projection mount plan")
		}
		for index, descriptor := range plan.descriptors {
			if samePinnedObject(descriptor.object, pin.identity) {
				// #nosec G115 -- both indices are bounded by maxPortableNamespaceProjectionPlanRecords (64).
				plan.records = append(plan.records, portableNamespaceProjectionRecord{descriptorIndex: uint8(index), role: role, target: target, operation: operation, order: uint16(len(plan.records))})
				return nil
			}
		}
		if len(plan.descriptors) >= maxPortableNamespaceProjectionPlanRecords || len(plan.records) >= maxPortableNamespaceProjectionPlanRecords {
			return activationError("projection mount plan")
		}
		plan.descriptors = append(plan.descriptors, portableNamespaceProjectionDescriptor{object: pin, identity: pin.identity})
		// #nosec G115 -- both indices are bounded by maxPortableNamespaceProjectionPlanRecords (64).
		plan.records = append(plan.records, portableNamespaceProjectionRecord{descriptorIndex: uint8(len(plan.descriptors) - 1), role: role, target: target, operation: operation, order: uint16(len(plan.records))})
		return nil
	}

	if err := add(pinned.governed, portableProjectionRoleGovernedRoot, ".", portableProjectionOperationPrivateRoot); err != nil {
		return nil, err
	}
	if err := add(pinned.activation, portableProjectionRoleActivationRoot, ".", portableProjectionOperationAttachDirectory); err != nil {
		return nil, err
	}

	// The first six directory pins are the fixed activation scopes.  They are
	// emitted in the closed scope order used during pinning.  Remaining pins are
	// normalized state-projection directories and are sorted by their declared
	// guest target, never by a host filesystem spelling.
	scopes := []harnesses.PortableRuntimeGuestPathScope{
		harnesses.PortableRuntimeGuestPathHome, harnesses.PortableRuntimeGuestPathConfig,
		harnesses.PortableRuntimeGuestPathData, harnesses.PortableRuntimeGuestPathCache,
		harnesses.PortableRuntimeGuestPathState, harnesses.PortableRuntimeGuestPathTmp,
	}
	for index, scope := range scopes {
		if err := add(pinned.directories[index], portableProjectionRoleScopeRoot, string(scope), portableProjectionOperationAttachDirectory); err != nil {
			return nil, err
		}
	}
	type projectionDirectory struct {
		pin    activationDescriptorPin
		target string
	}
	directories := make([]projectionDirectory, 0, len(entrypoint.StateProjections))
	for index, projection := range entrypoint.StateProjections {
		if index+len(scopes) >= len(pinned.directories) {
			return nil, activationError("projection mount plan")
		}
		directories = append(directories, projectionDirectory{pin: pinned.directories[index+len(scopes)], target: activationRelativeGuestPath(projection.Directory)})
	}
	sort.Slice(directories, func(i, j int) bool { return directories[i].target < directories[j].target })
	for _, directory := range directories {
		if err := add(directory.pin, portableProjectionRoleProjectionDirectory, directory.target, portableProjectionOperationAttachDirectory); err != nil {
			return nil, err
		}
	}

	// Required-absent parents are descriptor-pinned now, but their final check
	// remains the later PID-1 responsibility.  They precede immutable binds so
	// every actual mount operation still ends with the immutable config binds.
	type absentParent struct {
		pin    activationDescriptorPin
		target string
	}
	absentParents := make([]absentParent, 0, len(recipe.requiredAbsent))
	for index, absent := range recipe.requiredAbsent {
		if index >= len(pinned.absent) {
			return nil, activationError("projection mount plan")
		}
		pin := activationDescriptorPin{object: pinned.absent[index].parent, identity: pinned.absent[index].parentIdentity, directory: true}
		absentParents = append(absentParents, absentParent{pin: pin, target: activationRelativeGuestPath(absent)})
	}
	sort.Slice(absentParents, func(i, j int) bool { return absentParents[i].target < absentParents[j].target })
	for _, absent := range absentParents {
		if err := add(absent.pin, portableProjectionRoleRequiredAbsentParent, absent.target, portableProjectionOperationValidateAbsent); err != nil {
			return nil, err
		}
	}

	type immutableSource struct {
		pin    activationDescriptorPin
		target string
	}
	immutable := make([]immutableSource, 0, len(recipe.immutableBindings))
	for index, binding := range recipe.immutableBindings {
		if index >= len(pinned.sources) || pinned.sources[index].tree {
			return nil, activationError("projection mount plan")
		}
		immutable = append(immutable, immutableSource{pin: pinned.sources[index].activationDescriptorPin, target: activationRelativeGuestPath(binding.output)})
	}
	sort.Slice(immutable, func(i, j int) bool { return immutable[i].target < immutable[j].target })
	for _, binding := range immutable {
		if err := add(binding.pin, portableProjectionRoleImmutableConfig, binding.target, portableProjectionOperationReadOnlyBind); err != nil {
			return nil, err
		}
	}

	if !plan.valid() {
		return nil, activationError("projection mount plan")
	}
	return plan, nil
}
