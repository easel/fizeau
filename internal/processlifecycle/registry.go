package processlifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// FileRegistry stores one atomically replaced JSON file per lifecycle record.
type FileRegistry struct {
	dir string
	mu  sync.Mutex
}

func NewFileRegistry(dir string) *FileRegistry { return &FileRegistry{dir: dir} }

func (r *FileRegistry) Create(ctx context.Context, record Record) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := record.Validate(); err != nil {
		return err
	}
	if record.Revision != 1 {
		return fmt.Errorf("%w: new record revision is %d, want 1", ErrRevisionConflict, record.Revision)
	}
	path, err := r.path(record.RecordID)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := os.Stat(path); err == nil {
		return ErrRecordExists
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return writeRecordAtomic(path, record)
}

func (r *FileRegistry) Get(ctx context.Context, recordID string) (Record, error) {
	if err := ctx.Err(); err != nil {
		return Record{}, err
	}
	path, err := r.path(recordID)
	if err != nil {
		return Record{}, err
	}
	data, err := os.ReadFile(path) // #nosec G304 -- path is constrained to the private registry directory
	if err != nil {
		return Record{}, err
	}
	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		return Record{}, err
	}
	if err := record.Validate(); err != nil {
		return Record{}, err
	}
	return record, nil
}

// List returns v1 records only. Explicit legacy records are left for the
// transitional stale reaper and are never adopted as identity-safe v1 leases.
func (r *FileRegistry) List(ctx context.Context) ([]Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []Record{}, nil
		}
		return nil, err
	}
	records := make([]Record, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		data, err := os.ReadFile(filepath.Join(r.dir, entry.Name())) // #nosec G304 -- entry comes from the private registry directory
		if err != nil {
			return nil, err
		}
		var header struct {
			SchemaID string `json:"schema_id"`
		}
		if err := json.Unmarshal(data, &header); err != nil {
			return nil, fmt.Errorf("decode lifecycle registry entry %s: %w", entry.Name(), err)
		}
		if header.SchemaID == LegacyRecordSchemaID || (header.SchemaID == "" && isLegacyRecord(data)) {
			continue
		}
		var record Record
		if err := json.Unmarshal(data, &record); err != nil {
			return nil, fmt.Errorf("decode lifecycle record %s: %w", entry.Name(), err)
		}
		if err := record.Validate(); err != nil {
			return nil, fmt.Errorf("validate lifecycle record %s: %w", entry.Name(), err)
		}
		records = append(records, cloneRecord(record))
	}
	sort.Slice(records, func(i, j int) bool { return records[i].RecordID < records[j].RecordID })
	return records, nil
}

// Update uses optimistic revision checking. FileRegistry serializes operations
// through one registry instance; a later recovery bead must add an OS-backed
// cross-process adoption claim before concurrent startup recovery is wired.
func (r *FileRegistry) Update(ctx context.Context, record Record, expectedRevision uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := record.Validate(); err != nil {
		return err
	}
	if record.Revision != expectedRevision+1 {
		return fmt.Errorf("%w: update revision is %d, want %d", ErrRevisionConflict, record.Revision, expectedRevision+1)
	}
	path, err := r.path(record.RecordID)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	current, err := readRecord(path)
	if err != nil {
		return err
	}
	if current.Revision != expectedRevision {
		return fmt.Errorf("%w: current revision is %d, expected %d", ErrRevisionConflict, current.Revision, expectedRevision)
	}
	return writeRecordAtomic(path, record)
}

func (r *FileRegistry) Delete(ctx context.Context, recordID string, expectedRevision uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := r.path(recordID)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	current, err := readRecord(path)
	if err != nil {
		return err
	}
	if current.Revision != expectedRevision {
		return fmt.Errorf("%w: current revision is %d, expected %d", ErrRevisionConflict, current.Revision, expectedRevision)
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return syncDirectory(r.dir)
}

func (r *FileRegistry) path(recordID string) (string, error) {
	if r == nil || r.dir == "" {
		return "", fmt.Errorf("%w: registry directory is required", ErrInvalidRecord)
	}
	if recordID == "" || recordID != filepath.Base(recordID) || strings.ContainsAny(recordID, `/\\`) {
		return "", fmt.Errorf("%w: unsafe record ID %q", ErrInvalidRecord, recordID)
	}
	return filepath.Join(r.dir, recordID+".json"), nil
}

func readRecord(path string) (Record, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- caller supplies a constrained registry path
	if err != nil {
		return Record{}, err
	}
	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		return Record{}, err
	}
	if err := record.Validate(); err != nil {
		return Record{}, err
	}
	return record, nil
}

func writeRecordAtomic(path string, record Record) error {
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, append(data, '\n'))
}

func isLegacyRecord(data []byte) bool {
	var record LegacyHarnessSessionRecord
	return json.Unmarshal(data, &record) == nil && record.Harness != "" && record.PID > 0 && record.PGID > 0 && !record.StartedAt.IsZero()
}

func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".lifecycle-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	removeTemp = false
	return syncDirectory(dir)
}

func syncDirectory(dir string) error {
	f, err := os.Open(dir) // #nosec G304 -- directory is selected by the caller for lifecycle state
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return f.Sync()
}

// MemoryRegistry is a concurrency-safe test backend. It does not satisfy the
// crash-durability requirement for production launch gates.
type MemoryRegistry struct {
	mu      sync.Mutex
	records map[string]Record
	PutErr  error
	GetErr  error
	DelErr  error
}

func NewMemoryRegistry() *MemoryRegistry {
	return &MemoryRegistry{records: make(map[string]Record)}
}

func (r *MemoryRegistry) Put(ctx context.Context, record Record) error {
	return r.Create(ctx, record)
}

func (r *MemoryRegistry) Create(ctx context.Context, record Record) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := record.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.PutErr != nil {
		return r.PutErr
	}
	if _, exists := r.records[record.RecordID]; exists {
		return ErrRecordExists
	}
	if record.Revision != 1 {
		return ErrRevisionConflict
	}
	r.records[record.RecordID] = cloneRecord(record)
	return nil
}

func (r *MemoryRegistry) Get(ctx context.Context, recordID string) (Record, error) {
	if err := ctx.Err(); err != nil {
		return Record{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.GetErr != nil {
		return Record{}, r.GetErr
	}
	record, ok := r.records[recordID]
	if !ok {
		return Record{}, fs.ErrNotExist
	}
	return cloneRecord(record), nil
}

func (r *MemoryRegistry) List(ctx context.Context) ([]Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	records := make([]Record, 0, len(r.records))
	for _, record := range r.records {
		records = append(records, cloneRecord(record))
	}
	sort.Slice(records, func(i, j int) bool { return records[i].RecordID < records[j].RecordID })
	return records, nil
}

func (r *MemoryRegistry) Update(ctx context.Context, record Record, expectedRevision uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := record.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.PutErr != nil {
		return r.PutErr
	}
	current, exists := r.records[record.RecordID]
	if !exists {
		return fs.ErrNotExist
	}
	if current.Revision != expectedRevision || record.Revision != expectedRevision+1 {
		return ErrRevisionConflict
	}
	r.records[record.RecordID] = cloneRecord(record)
	return nil
}

func (r *MemoryRegistry) Delete(ctx context.Context, recordID string, expectedRevision uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.DelErr != nil {
		return r.DelErr
	}
	current, exists := r.records[recordID]
	if !exists {
		return fs.ErrNotExist
	}
	if current.Revision != expectedRevision {
		return ErrRevisionConflict
	}
	delete(r.records, recordID)
	return nil
}

func cloneRecord(record Record) Record {
	if record.EscapeEvidence != nil {
		evidence := make([]EscapeEvidence, len(record.EscapeEvidence))
		copy(evidence, record.EscapeEvidence)
		record.EscapeEvidence = evidence
	}
	return record
}
