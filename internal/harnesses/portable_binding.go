package harnesses

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// PortableRuntimeNamespaceRecipe is the opaque activation-owned namespace
// recipe carried to the canonical spawn seam. Harnesses may retain and forward
// it, but cannot inspect its private enforcement details.
type PortableRuntimeNamespaceRecipe interface {
	PortableRuntimeNamespaceRecipe()
}

// PortableRuntimeRunnerBindingInput is the verified, process-free input used
// to bind one manifest surface to an authoritative structural prototype.
type PortableRuntimeRunnerBindingInput struct {
	Structure         PortableRuntimeStructure
	GuestRoot         string
	ClosureClass      PortableRuntimeClosureClass
	Launch            PortableRuntimeLaunch
	FixedArguments    []string
	FixedOptionValues []PortableRuntimeFixedOptionValue
	Environment       map[string]string
	NamespaceRecipe   PortableRuntimeNamespaceRecipe
}

func (i PortableRuntimeRunnerBindingInput) String() string {
	return fmt.Sprintf("{Name:%q Transport:%q LaunchConfigured:%t EnvironmentCount:%d NamespaceRecipeConfigured:%t}",
		i.Structure.Name, i.Structure.Transport, i.GuestRoot != "", len(i.Environment), i.NamespaceRecipe != nil)
}

func (i PortableRuntimeRunnerBindingInput) GoString() string { return i.String() }

func (i PortableRuntimeRunnerBindingInput) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Name                      string                   `json:"name"`
		Transport                 PortableRuntimeTransport `json:"transport"`
		LaunchConfigured          bool                     `json:"launch_configured"`
		EnvironmentCount          int                      `json:"environment_count"`
		NamespaceRecipeConfigured bool                     `json:"namespace_recipe_configured"`
	}{i.Structure.Name, i.Structure.Transport, i.GuestRoot != "", len(i.Environment), i.NamespaceRecipe != nil})
}

type portableRuntimeLaunchBinding struct {
	guestRoot         string
	closureClass      PortableRuntimeClosureClass
	launch            PortableRuntimeLaunch
	fixedArguments    []string
	fixedOptionValues []PortableRuntimeFixedOptionValue
}

// PortableRuntimeRunnerBinding is the immutable manifest authority retained
// by both a structural prototype and every exact-route clone made from it.
// Its diagnostic forms deliberately expose only structural cardinalities.
type PortableRuntimeRunnerBinding struct {
	configured      bool
	structure       PortableRuntimeStructure
	launch          *portableRuntimeLaunchBinding
	environment     map[string]string
	namespaceRecipe PortableRuntimeNamespaceRecipe
}

func (b PortableRuntimeRunnerBinding) String() string {
	return fmt.Sprintf("{Name:%q Transport:%q LaunchConfigured:%t EnvironmentCount:%d NamespaceRecipeConfigured:%t}",
		b.structure.Name, b.structure.Transport, b.launch != nil, len(b.environment), b.namespaceRecipe != nil)
}

func (b PortableRuntimeRunnerBinding) GoString() string { return b.String() }

func (b PortableRuntimeRunnerBinding) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Name                      string                   `json:"name"`
		Transport                 PortableRuntimeTransport `json:"transport"`
		LaunchConfigured          bool                     `json:"launch_configured"`
		EnvironmentCount          int                      `json:"environment_count"`
		NamespaceRecipeConfigured bool                     `json:"namespace_recipe_configured"`
	}{b.structure.Name, b.structure.Transport, b.launch != nil, len(b.environment), b.namespaceRecipe != nil})
}

// NewPortableRuntimeRunnerBinding validates and owns one manifest-derived
// runner binding. Non-subprocess surfaces carry transport authority only.
func NewPortableRuntimeRunnerBinding(input PortableRuntimeRunnerBindingInput) (PortableRuntimeRunnerBinding, error) {
	if input.Structure.Name == "" {
		return PortableRuntimeRunnerBinding{}, closureError("runner binding has no structural name")
	}
	switch input.Structure.Transport {
	case PortableRuntimeTransportSubprocess:
		if input.Structure.Mode != PortableRuntimeStructuralUnpinned && input.Structure.Mode != PortableRuntimeStructuralExactPinOnly {
			return PortableRuntimeRunnerBinding{}, closureError("subprocess runner binding has invalid structural mode")
		}
		if input.GuestRoot == "" || !filepath.IsAbs(input.GuestRoot) || filepath.Clean(input.GuestRoot) != input.GuestRoot {
			return PortableRuntimeRunnerBinding{}, closureError("runner binding has invalid guest root")
		}
		if !validPortableRuntimeClosureClass(input.ClosureClass) || input.NamespaceRecipe == nil {
			return PortableRuntimeRunnerBinding{}, closureError("subprocess runner binding is incomplete")
		}
		contribution := PortableRuntimeContribution{
			ClosureClass: input.ClosureClass,
			Launch:       clonePortableRuntimeLaunch(input.Launch),
			ExecutionConstraints: PortableRuntimeExecutionConstraints{
				FixedArguments:    append([]string(nil), input.FixedArguments...),
				FixedOptionValues: append([]PortableRuntimeFixedOptionValue(nil), input.FixedOptionValues...),
			},
		}
		if err := validatePortableRuntimeBindingShape(contribution); err != nil {
			return PortableRuntimeRunnerBinding{}, err
		}
	case PortableRuntimeTransportNative, PortableRuntimeTransportEmbedded, PortableRuntimeTransportHTTP:
		if input.Structure.Mode != PortableRuntimeStructuralNonSubprocess || input.GuestRoot != "" ||
			input.ClosureClass != "" || !emptyPortableRuntimeLaunch(input.Launch) || len(input.FixedArguments) != 0 ||
			len(input.FixedOptionValues) != 0 || len(input.Environment) != 0 || input.NamespaceRecipe != nil {
			return PortableRuntimeRunnerBinding{}, closureError("non-subprocess runner binding contains launch state")
		}
	default:
		return PortableRuntimeRunnerBinding{}, closureError("runner binding has unknown transport")
	}
	if err := validatePortableRuntimeClosedEnvironment(input.Environment); err != nil {
		return PortableRuntimeRunnerBinding{}, err
	}

	binding := PortableRuntimeRunnerBinding{
		configured:      true,
		structure:       input.Structure,
		environment:     clonePortableRuntimeEnvironmentMap(input.Environment),
		namespaceRecipe: input.NamespaceRecipe,
	}
	if input.Structure.Transport == PortableRuntimeTransportSubprocess {
		binding.launch = &portableRuntimeLaunchBinding{
			guestRoot: input.GuestRoot, closureClass: input.ClosureClass,
			launch:            clonePortableRuntimeLaunch(input.Launch),
			fixedArguments:    append([]string(nil), input.FixedArguments...),
			fixedOptionValues: append([]PortableRuntimeFixedOptionValue(nil), input.FixedOptionValues...),
		}
	}
	return binding, nil
}

func emptyPortableRuntimeLaunch(launch PortableRuntimeLaunch) bool {
	return launch.EntrypointTarget == "" && launch.EntrypointTreeMember == "" && launch.InterpreterTarget == "" &&
		launch.LoaderTarget == "" && len(launch.RuntimeArgs) == 0 && len(launch.LibraryRootTargets) == 0
}

func validatePortableRuntimeBindingShape(contribution PortableRuntimeContribution) error {
	launch := contribution.Launch
	if !validPortableRuntimeTargetPath(launch.EntrypointTarget) {
		return closureError("runner binding has invalid entrypoint target")
	}
	for i, argument := range launch.RuntimeArgs {
		if !validPortableRuntimeArgument(argument) {
			return closureErrorAt("runtime argument", i, "is not a fixed non-secret argument")
		}
	}
	seenRoots := make(map[string]struct{}, len(launch.LibraryRootTargets))
	for i, root := range launch.LibraryRootTargets {
		if !validPortableRuntimeTargetPath(root) || strings.ContainsRune(root, ':') {
			return closureErrorAt("library root", i, "has invalid target path")
		}
		if _, exists := seenRoots[root]; exists {
			return closureErrorAt("library root", i, "duplicates an earlier target")
		}
		seenRoots[root] = struct{}{}
	}
	switch contribution.ClosureClass {
	case PortableRuntimeClosureStatic:
		if launch.InterpreterTarget != "" || launch.LoaderTarget != "" || len(launch.RuntimeArgs) != 0 || len(launch.LibraryRootTargets) != 0 {
			return closureError("static runner binding contains interpreter or loader state")
		}
	case PortableRuntimeClosureDynamic:
		if !validPortableRuntimeTargetPath(launch.LoaderTarget) || launch.InterpreterTarget != "" || len(launch.RuntimeArgs) != 0 || len(launch.LibraryRootTargets) == 0 {
			return closureError("dynamic runner binding has an invalid loader recipe")
		}
	case PortableRuntimeClosureInterpreted:
		if !validPortableRuntimeTargetPath(launch.InterpreterTarget) {
			return closureError("interpreted runner binding has no interpreter")
		}
		if launch.LoaderTarget == "" && len(launch.LibraryRootTargets) != 0 ||
			launch.LoaderTarget != "" && (!validPortableRuntimeTargetPath(launch.LoaderTarget) || len(launch.LibraryRootTargets) == 0) {
			return closureError("interpreted runner binding has an invalid loader recipe")
		}
	}
	return validatePortableRuntimeFixedPrefix(contribution.ExecutionConstraints)
}

func validatePortableRuntimeFixedPrefix(constraints PortableRuntimeExecutionConstraints) error {
	seen := make(map[string]struct{}, len(constraints.FixedArguments)+len(constraints.FixedOptionValues))
	for i, argument := range constraints.FixedArguments {
		if !validPortableRuntimeFixedArgument(argument) || !validPortableRuntimeArgument(argument) {
			return closureErrorAt("fixed argument", i, "is not a fixed non-secret argument")
		}
		key := portableRuntimeFixedOptionKey(argument)
		if _, exists := seen[key]; exists {
			return closureErrorAt("fixed argument", i, "duplicates an earlier option")
		}
		seen[key] = struct{}{}
	}
	for i, pair := range constraints.FixedOptionValues {
		if !validPortableRuntimeFixedOption(pair.Option) || !validPortableRuntimeArgument(pair.Option) || !validPortableRuntimeFixedOptionLiteral(pair.Value) {
			return closureErrorAt("fixed option/value", i, "is not a fixed non-secret pair")
		}
		key := portableRuntimeFixedOptionKey(pair.Option)
		if _, exists := seen[key]; exists {
			return closureErrorAt("fixed option/value", i, "duplicates an earlier option")
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validatePortableRuntimeClosedEnvironment(environment map[string]string) error {
	for name, value := range environment {
		if !validPortableRuntimeEnvironmentName(name) || strings.ContainsRune(value, 0) {
			return closureError("runner binding has invalid closed environment")
		}
	}
	return nil
}

func clonePortableRuntimeLaunch(input PortableRuntimeLaunch) PortableRuntimeLaunch {
	input.RuntimeArgs = append([]string(nil), input.RuntimeArgs...)
	input.LibraryRootTargets = append([]string(nil), input.LibraryRootTargets...)
	return input
}

func clonePortableRuntimeEnvironmentMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	output := make(map[string]string, len(input))
	for name, value := range input {
		output[name] = value
	}
	return output
}

func (b PortableRuntimeRunnerBinding) clone() PortableRuntimeRunnerBinding {
	clone := b
	clone.environment = clonePortableRuntimeEnvironmentMap(b.environment)
	if b.launch != nil {
		launch := *b.launch
		launch.launch = clonePortableRuntimeLaunch(b.launch.launch)
		launch.fixedArguments = append([]string(nil), b.launch.fixedArguments...)
		launch.fixedOptionValues = append([]PortableRuntimeFixedOptionValue(nil), b.launch.fixedOptionValues...)
		clone.launch = &launch
	}
	return clone
}

// Structure returns the manifest-declared structural identity.
func (b PortableRuntimeRunnerBinding) Structure() PortableRuntimeStructure { return b.structure }

// Environment returns an owned copy of the activation's closed child environment.
func (b PortableRuntimeRunnerBinding) Environment() map[string]string {
	return clonePortableRuntimeEnvironmentMap(b.environment)
}

// NamespaceRecipe returns the opaque activation-owned recipe.
func (b PortableRuntimeRunnerBinding) NamespaceRecipe() PortableRuntimeNamespaceRecipe {
	return b.namespaceRecipe
}

// PortableRuntimeChildCommand is a pure spawn specification. It owns the
// exact executable, argv, closed environment, and opaque namespace recipe.
type PortableRuntimeChildCommand struct {
	command         string
	arguments       []string
	environment     []string
	namespaceRecipe PortableRuntimeNamespaceRecipe
}

func (c PortableRuntimeChildCommand) String() string {
	return fmt.Sprintf("{ArgumentCount:%d EnvironmentCount:%d NamespaceRecipeConfigured:%t}",
		len(c.arguments), len(c.environment), c.namespaceRecipe != nil)
}

func (c PortableRuntimeChildCommand) GoString() string { return c.String() }

func (c PortableRuntimeChildCommand) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ArgumentCount             int  `json:"argument_count"`
		EnvironmentCount          int  `json:"environment_count"`
		NamespaceRecipeConfigured bool `json:"namespace_recipe_configured"`
	}{len(c.arguments), len(c.environment), c.namespaceRecipe != nil})
}

func (c PortableRuntimeChildCommand) Command() string { return c.command }
func (c PortableRuntimeChildCommand) Arguments() []string {
	return append([]string(nil), c.arguments...)
}
func (c PortableRuntimeChildCommand) Environment() []string {
	return append([]string(nil), c.environment...)
}
func (c PortableRuntimeChildCommand) NamespaceRecipe() PortableRuntimeNamespaceRecipe {
	return c.namespaceRecipe
}

// BuildCommand appends registry and request arguments at their distinct
// governed boundaries without consulting the host process or filesystem.
func (b PortableRuntimeRunnerBinding) BuildCommand(registryArgv, requestArgv []string) (PortableRuntimeChildCommand, error) {
	if !b.configured || b.launch == nil {
		return PortableRuntimeChildCommand{}, closureError("runner binding has no subprocess launch")
	}
	command, arguments, err := buildPortableRuntimeLaunchCommand(
		b.launch.guestRoot, b.launch.closureClass, b.launch.launch,
		b.launch.fixedArguments, b.launch.fixedOptionValues, registryArgv, requestArgv,
	)
	if err != nil {
		return PortableRuntimeChildCommand{}, err
	}
	names := make([]string, 0, len(b.environment))
	for name := range b.environment {
		names = append(names, name)
	}
	sort.Strings(names)
	environment := make([]string, len(names))
	for i, name := range names {
		environment[i] = name + "=" + b.environment[name]
	}
	return PortableRuntimeChildCommand{
		command: command, arguments: arguments, environment: environment,
		namespaceRecipe: b.namespaceRecipe,
	}, nil
}

// PortableRuntimeRunnerBinder is the one generic activation-facing contract
// implemented by authoritative structural subprocess prototypes.
type PortableRuntimeRunnerBinder interface {
	BindPortableRuntime(PortableRuntimeRunnerBinding) error
	PortableRuntimeBinding() (PortableRuntimeRunnerBinding, bool)
}

// PortableRuntimeRunnerState supplies the generic binder implementation for
// concrete runner structs. Exact-route factories clone it with Clone.
type PortableRuntimeRunnerState struct {
	binding PortableRuntimeRunnerBinding
}

func (s PortableRuntimeRunnerState) String() string {
	return fmt.Sprintf("{Configured:%t}", s.binding.configured)
}

func (s PortableRuntimeRunnerState) GoString() string { return s.String() }

func (s PortableRuntimeRunnerState) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Configured bool `json:"configured"`
	}{s.binding.configured})
}

func (s *PortableRuntimeRunnerState) BindPortableRuntime(binding PortableRuntimeRunnerBinding) error {
	if s == nil || !binding.configured {
		return ErrRouteRunnerUnavailable
	}
	s.binding = binding.clone()
	return nil
}

func (s *PortableRuntimeRunnerState) PortableRuntimeBinding() (PortableRuntimeRunnerBinding, bool) {
	if s == nil || !s.binding.configured {
		return PortableRuntimeRunnerBinding{}, false
	}
	return s.binding.clone(), true
}

func (s PortableRuntimeRunnerState) Clone() PortableRuntimeRunnerState {
	return PortableRuntimeRunnerState{binding: s.binding.clone()}
}
