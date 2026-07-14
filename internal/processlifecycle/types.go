// Package processlifecycle owns durable process-boundary leases shared by all
// subprocess transports. Platform-specific containment and signalling live in
// backend implementations; this package owns ordering and recovery evidence.
package processlifecycle

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const (
	// RecordSchemaID identifies the durable lifecycle-record schema.
	RecordSchemaID       = "fizeau.process-lifecycle/v1"
	LegacyRecordSchemaID = "fizeau.harness-session/legacy-v0"
)

var (
	ErrAbortIndeterminate    = errors.New("launch abort could not prove the boundary empty")
	ErrBoundaryAlreadyEmpty  = errors.New("process lifecycle boundary was already empty")
	ErrBoundaryIndeterminate = errors.New("process lifecycle boundary state is indeterminate")
	ErrBoundaryNotEmpty      = errors.New("process lifecycle boundary is not empty")
	ErrBoundaryOwned         = errors.New("process lifecycle boundary is still owned by its original process")
	ErrIdentityMismatch      = errors.New("process lifecycle identity does not match durable record")
	ErrInvalidRecord         = errors.New("invalid process lifecycle record")
	ErrRecordExists          = errors.New("process lifecycle record already exists")
	ErrRevisionConflict      = errors.New("process lifecycle record revision conflict")
)

// BoundaryType identifies the platform containment primitive.
type BoundaryType string

const (
	BoundaryTypeUnixProcessGroup BoundaryType = "unix_process_group"
	BoundaryTypeWindowsJob       BoundaryType = "windows_job"
	BoundaryTypeTest             BoundaryType = "test"
)

// State is the durable lifecycle state of one owned boundary.
type State string

const (
	StateOwned           State = "owned"
	StateCleaning        State = "cleaning"
	StateCleanupFailed   State = "cleanup_failed"
	StateRecoveryBlocked State = "recovery_blocked"
	StateCompleted       State = "completed"
)

// ProcessIdentity combines a process ID with an OS-derived birth token. The
// token is what makes recovery safe when a PID has been reused.
type ProcessIdentity struct {
	PID              int    `json:"pid"`
	BirthTokenScheme string `json:"birth_token_scheme"`
	BirthToken       string `json:"birth_token"`
}

// OwnerObservation describes what happened to the Fizeau process or trusted
// supervisor identity that created the boundary. A gone owner is expected
// during crash recovery; a reused or indeterminate owner identity is evidence
// that must be retained.
type OwnerObservation string

const (
	OwnerMatching      OwnerObservation = "matching"
	OwnerGone          OwnerObservation = "gone"
	OwnerMismatch      OwnerObservation = "mismatch"
	OwnerIndeterminate OwnerObservation = "indeterminate"
)

// BoundaryObservationStatus is the backend's typed answer about a recorded
// containment boundary.
type BoundaryObservationStatus string

const (
	BoundaryEmpty         BoundaryObservationStatus = "empty"
	BoundaryMatching      BoundaryObservationStatus = "matching"
	BoundaryMismatch      BoundaryObservationStatus = "mismatch"
	BoundaryIndeterminate BoundaryObservationStatus = "indeterminate"
)

// BoundaryObservation carries enough observed identity to prevent a backend
// from labelling a reused PID or a different boundary as matching. Matching
// observations permit OwnerMatching or OwnerGone; owner mismatch and
// indeterminate states are retained as unresolved recovery evidence.
type BoundaryObservation struct {
	Status                  BoundaryObservationStatus
	BoundaryIdentity        string
	BoundaryProcessIdentity ProcessIdentity
	OwnerStatus             OwnerObservation
	OwnerIdentity           ProcessIdentity
	Detail                  string
}

// AbortStatus describes whether a failed launch was proven contained and
// empty. Anything other than AbortEmpty is returned as indeterminate evidence.
type AbortStatus string

const (
	AbortEmpty         AbortStatus = "empty"
	AbortIndeterminate AbortStatus = "indeterminate"
)

type AbortResult struct {
	Status AbortStatus
	Detail string
}

func (i ProcessIdentity) valid() bool {
	return i.PID > 0 && i.BirthTokenScheme != "" && i.BirthToken != ""
}

func (i ProcessIdentity) matches(other ProcessIdentity) bool {
	return i.PID == other.PID && i.BirthTokenScheme != "" && i.BirthTokenScheme == other.BirthTokenScheme && i.BirthToken != "" && i.BirthToken == other.BirthToken
}

// EscapeEvidence preserves evidence that a descendant may have escaped or
// that containment membership could not be determined.
type EscapeEvidence struct {
	Kind       string    `json:"kind"`
	Detail     string    `json:"detail"`
	ObservedAt time.Time `json:"observed_at"`
}

// Timestamps records creation and the latest lifecycle transition. Optional
// cleanup timestamps are populated as the lease advances.
type Timestamps struct {
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
	CleanupStartedAt   time.Time `json:"cleanup_started_at,omitempty"`
	CleanupCompletedAt time.Time `json:"cleanup_completed_at,omitempty"`
}

// Record is the durable ownership fact used by current cleanup and later
// recovery.
type Record struct {
	SchemaID                string           `json:"schema_id"`
	RecordID                string           `json:"record_id"`
	Revision                uint64           `json:"revision"`
	OperationID             string           `json:"operation_id"`
	Harness                 string           `json:"harness"`
	OwnerIdentity           ProcessIdentity  `json:"owner_identity"`
	BoundaryIdentity        string           `json:"boundary_identity"`
	BoundaryType            BoundaryType     `json:"boundary_type"`
	BoundaryProcessIdentity ProcessIdentity  `json:"boundary_process_identity"`
	State                   State            `json:"state"`
	Timestamps              Timestamps       `json:"timestamps"`
	EscapeEvidence          []EscapeEvidence `json:"escape_evidence"`
}

// LegacyHarnessSessionRecord is the explicitly transitional flat shape read
// by the pre-v1 startup reaper. It deliberately does not masquerade as a v1
// Record: PID/PGID evidence alone is unsafe for identity-checked recovery.
type LegacyHarnessSessionRecord struct {
	SchemaID  string    `json:"schema_id,omitempty"`
	SessionID string    `json:"session_id"`
	Harness   string    `json:"harness"`
	Command   string    `json:"command"`
	PID       int       `json:"pid"`
	PGID      int       `json:"pgid"`
	StartedAt time.Time `json:"started_at"`
}

// Validate rejects records that cannot safely identify an owned boundary.
func (r Record) Validate() error {
	if r.SchemaID != RecordSchemaID {
		return fmt.Errorf("%w: schema ID %q", ErrInvalidRecord, r.SchemaID)
	}
	if r.RecordID == "" || r.Revision == 0 || r.OperationID == "" || r.Harness == "" {
		return fmt.Errorf("%w: record, operation, and harness IDs are required", ErrInvalidRecord)
	}
	if !r.OwnerIdentity.valid() || !r.BoundaryProcessIdentity.valid() {
		return fmt.Errorf("%w: owner and boundary process birth identities are required", ErrInvalidRecord)
	}
	if r.BoundaryIdentity == "" || r.BoundaryType == "" || r.State == "" {
		return fmt.Errorf("%w: boundary identity, boundary type, and state are required", ErrInvalidRecord)
	}
	switch r.BoundaryType {
	case BoundaryTypeUnixProcessGroup, BoundaryTypeWindowsJob, BoundaryTypeTest:
	default:
		return fmt.Errorf("%w: unknown boundary type %q", ErrInvalidRecord, r.BoundaryType)
	}
	switch r.State {
	case StateOwned, StateCleaning, StateCleanupFailed, StateRecoveryBlocked, StateCompleted:
	default:
		return fmt.Errorf("%w: unknown state %q", ErrInvalidRecord, r.State)
	}
	if r.Timestamps.CreatedAt.IsZero() || r.Timestamps.UpdatedAt.IsZero() {
		return fmt.Errorf("%w: lifecycle timestamps are required", ErrInvalidRecord)
	}
	if r.EscapeEvidence == nil {
		return fmt.Errorf("%w: escape evidence must be present (an empty list is valid)", ErrInvalidRecord)
	}
	return nil
}

// Registry persists lifecycle ownership records atomically.
type Registry interface {
	Create(context.Context, Record) error
	Get(context.Context, string) (Record, error)
	List(context.Context) ([]Record, error)
	Update(context.Context, Record, uint64) error
	Delete(context.Context, string, uint64) error
}

// PlatformBackend observes the exact durable record during cleanup/recovery.
// It deliberately does not prescribe signals or platform syscalls.
type PlatformBackend interface {
	ObserveBoundary(context.Context, Record) (BoundaryObservation, error)
}

// BoundaryDescriptor identifies the exact prepared containment boundary whose
// launch gate will be released. Descriptor and gate live on one value so a
// caller cannot persist group A and accidentally release group B.
type BoundaryDescriptor struct {
	OwnerIdentity           ProcessIdentity
	BoundaryProcessIdentity ProcessIdentity
	BoundaryID              string
	BoundaryType            BoundaryType
}

// PreparedBoundary keeps untrusted code non-runnable until durable ownership
// exists and binds that gate to the boundary identity being recorded.
type PreparedBoundary interface {
	Descriptor() BoundaryDescriptor
	Release(context.Context) error
	Abort(context.Context) (AbortResult, error)
}

// Clock permits deterministic lifecycle timestamps.
type Clock interface {
	Now() time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }

// Options identify one lifecycle lease.
type Options struct {
	RecordID    string
	OperationID string
	Harness     string
	Clock       Clock
}

// CleanupResult says whether ownership evidence was safely removed.
type CleanupResult struct {
	State          State
	BoundaryEmpty  bool
	RecordRetained bool
}
