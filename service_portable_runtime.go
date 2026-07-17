package fizeau

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/portableruntime"
	"github.com/easel/fizeau/internal/serviceimpl"
)

// PortableRuntimeRequest identifies the caller-owned destination and the
// same-platform Linux target for a route-neutral portable preparation.
type PortableRuntimeRequest struct {
	DestinationRoot string
	TargetGOOS      string
	TargetGOARCH    string
}

func (r PortableRuntimeRequest) String() string {
	return fmt.Sprintf("{TargetGOOS:%q TargetGOARCH:%q DestinationConfigured:%t}", r.TargetGOOS, r.TargetGOARCH, r.DestinationRoot != "")
}

func (r PortableRuntimeRequest) GoString() string { return r.String() }

// MarshalJSON keeps the caller-owned host path off generic diagnostics.
func (r PortableRuntimeRequest) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		TargetGOOS            string `json:"target_goos"`
		TargetGOARCH          string `json:"target_goarch"`
		DestinationConfigured bool   `json:"destination_configured"`
	}{r.TargetGOOS, r.TargetGOARCH, r.DestinationRoot != ""})
}

// PortableRuntimeMount is one generic mount the caller applies before
// constructing a service with NewFromPortableRuntime.
type PortableRuntimeMount struct {
	Source   string
	Target   string
	ReadOnly bool
}

// PortableRuntimeBundle owns the prepared host copy. Its contents remain
// opaque; callers receive only the generic mount and inherited-name plan.
// The zero value is a closed empty bundle.
type PortableRuntimeBundle struct {
	bundle *portableruntime.Bundle
}

var (
	ErrPortableRuntimeRequestInvalid    = errors.New("invalid portable runtime request")
	ErrPortableRuntimeClosureIncomplete = errors.New("portable runtime closure incomplete")
	ErrPortableRuntimeActivationInvalid = errors.New("portable runtime activation invalid")
	ErrPortableRuntimeCleanupIncomplete = errors.New("portable runtime cleanup incomplete")
)

// RuntimeRoot returns the committed host runtime child, or an empty string for
// a zero or successfully closed bundle.
func (b *PortableRuntimeBundle) RuntimeRoot() string {
	if b == nil || b.bundle == nil {
		return ""
	}
	return b.bundle.RuntimeRoot()
}

// Mounts returns a defensive copy of the fixed, generic mount plan.
func (b *PortableRuntimeBundle) Mounts() []PortableRuntimeMount {
	if b == nil || b.bundle == nil {
		return nil
	}
	internal := b.bundle.Mounts()
	mounts := make([]PortableRuntimeMount, len(internal))
	for index, mount := range internal {
		mounts[index] = PortableRuntimeMount{
			Source: mount.Source, Target: mount.Target, ReadOnly: mount.ReadOnly,
		}
	}
	return mounts
}

// EnvironmentNames returns a defensive copy of the sorted inherited names.
// It never returns environment values or name=value assignments.
func (b *PortableRuntimeBundle) EnvironmentNames() []string {
	if b == nil || b.bundle == nil {
		return nil
	}
	return b.bundle.EnvironmentNames()
}

// Close removes only Fizeau's committed host runtime child. Cleanup is
// concurrency-safe, idempotent after success, and retryable after failure.
func (b *PortableRuntimeBundle) Close() error {
	if b == nil || b.bundle == nil {
		return nil
	}
	if err := b.bundle.Close(); err != nil {
		return fmt.Errorf("%w: committed runtime removal failed", ErrPortableRuntimeCleanupIncomplete)
	}
	return nil
}

// PortableRuntimeGuestRoot returns the fixed Linux activation mount root. The
// empty string marks unsupported platforms in v0.15.
func PortableRuntimeGuestRoot() string {
	if runtime.GOOS != "linux" {
		return ""
	}
	return portableruntime.GuestRoot
}

// PreparePortableRuntime materializes this configured service's complete
// structural inventory without selecting a route or starting service work.
func (s *service) PreparePortableRuntime(ctx context.Context, req PortableRuntimeRequest) (*PortableRuntimeBundle, error) {
	if ctx != nil && ctx.Err() != nil {
		return nil, fmt.Errorf("%w: preparation canceled", ErrPortableRuntimeRequestInvalid)
	}
	target := harnesses.PortableRuntimeTarget{GOOS: req.TargetGOOS, GOARCH: req.TargetGOARCH}
	if err := harnesses.ValidatePortableRuntimeTarget(target); err != nil {
		return nil, fmt.Errorf("%w: unsupported target", ErrPortableRuntimeRequestInvalid)
	}
	inventory, err := s.portableRuntimeInventory()
	if err != nil {
		return nil, fmt.Errorf("%w: runtime inventory unavailable", ErrPortableRuntimeClosureIncomplete)
	}
	providers, err := s.portableRuntimeConfiguredProviders()
	if err != nil {
		return nil, fmt.Errorf("%w: configured provider snapshot unavailable", ErrPortableRuntimeClosureIncomplete)
	}

	bundle, err := serviceimpl.PreparePortableRuntime(ctx, serviceimpl.PortableRuntimePrepareInput{
		DestinationRoot:     req.DestinationRoot,
		Target:              target,
		Inventory:           inventory,
		ConfiguredProviders: providers,
	})
	if err != nil {
		return nil, publicPortableRuntimeError(err)
	}
	return &PortableRuntimeBundle{bundle: bundle}, nil
}

// NewFromPortableRuntime is the public activation entrypoint. The successful
// activation path is supplied by the dependent activation bead; until then it
// fails closed and never falls back to ambient host configuration.
func NewFromPortableRuntime(ServiceOptions) (FizeauService, error) {
	return nil, fmt.Errorf("%w: activation is not implemented", ErrPortableRuntimeActivationInvalid)
}

func publicPortableRuntimeError(err error) error {
	classes := make([]error, 0, 3)
	if errors.Is(err, portableruntime.ErrRequestInvalid) {
		classes = append(classes, ErrPortableRuntimeRequestInvalid)
	}
	if errors.Is(err, portableruntime.ErrClosureIncomplete) {
		classes = append(classes, ErrPortableRuntimeClosureIncomplete)
	}
	if errors.Is(err, portableruntime.ErrCleanupIncomplete) {
		classes = append(classes, ErrPortableRuntimeCleanupIncomplete)
	}
	if len(classes) == 0 {
		return fmt.Errorf("%w: runtime preparation failed", ErrPortableRuntimeClosureIncomplete)
	}
	classes = append(classes, err)
	return errors.Join(classes...)
}
