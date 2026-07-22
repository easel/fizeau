package routehealth

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/easel/fizeau/internal/routing"
)

// DefaultCooldown is the process-local route-attempt TTL used when service
// configuration does not provide a health cooldown.
const DefaultCooldown = 30 * time.Second

// Attempt records availability feedback for one routed attempt.
type Attempt struct {
	Harness        string
	Provider       string
	Model          string
	Endpoint       string
	ServerInstance string
	Status         string
	Reason         string
	Error          string
	Duration       time.Duration
	Timestamp      time.Time
}

// RecordOptions controls how a successful attempt clears active failures.
// Failure recording and metric aggregation are identical for every option.
type RecordOptions struct {
	// ExactSuccessClear makes a success clear only the identical normalized
	// route key. Empty endpoint and server-instance fields are intentional
	// identity values, not wildcards, when this option is set.
	ExactSuccessClear bool
}

// Key is the normalized route-attempt identity.
type Key struct {
	Harness        string
	Provider       string
	Model          string
	Endpoint       string
	ServerInstance string
}

// Record is an active route-attempt failure.
type Record struct {
	Key        Key
	Status     string
	Reason     string
	Error      string
	Duration   time.Duration
	RecordedAt time.Time
}

type metricRecord struct {
	attempts      int
	successes     int
	totalDuration time.Duration
	recordedAt    time.Time
}

// Store owns process-local route-attempt failure feedback and reliability
// metrics. It is safe for concurrent use.
type Store struct {
	mu       sync.RWMutex
	failures map[Key]Record
	metrics  map[Key]metricRecord
}

// NewStore returns an empty route-health store.
func NewStore() *Store {
	return &Store{
		failures: make(map[Key]Record),
		metrics:  make(map[Key]metricRecord),
	}
}

// RecordAttempt records availability feedback. Success clears matching active
// failures; non-success records an active failure and updates metrics.
func (s *Store) RecordAttempt(attempt Attempt) error {
	return s.RecordAttemptWithOptions(attempt, RecordOptions{})
}

// RecordAttemptWithOptions records availability feedback with explicit
// success-clearing semantics. ExactSuccessClear is appropriate when the
// caller has authoritative evidence for one dispatched route; the default
// RecordAttempt behavior remains compatible with partial success keys.
func (s *Store) RecordAttemptWithOptions(attempt Attempt, opts RecordOptions) error {
	key := NormalizeKey(attempt)
	if key.Harness == "" && key.Provider == "" {
		return fmt.Errorf("route attempt requires harness or provider")
	}
	status := strings.ToLower(strings.TrimSpace(attempt.Status))
	if status == "" {
		return fmt.Errorf("route attempt status is required")
	}
	recordedAt := attempt.Timestamp
	if recordedAt.IsZero() {
		recordedAt = time.Now()
	}
	recordedAt = recordedAt.UTC()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failures == nil {
		s.failures = make(map[Key]Record)
	}
	if s.metrics == nil {
		s.metrics = make(map[Key]metricRecord)
	}
	s.recordMetricLocked(key, Succeeded(status), attempt.Duration, recordedAt)

	if Succeeded(status) {
		if opts.ExactSuccessClear {
			delete(s.failures, key)
		} else {
			s.clearFailuresLocked(key)
		}
		return nil
	}

	s.failures[key] = Record{
		Key:        key,
		Status:     status,
		Reason:     strings.TrimSpace(attempt.Reason),
		Error:      strings.TrimSpace(attempt.Error),
		Duration:   attempt.Duration,
		RecordedAt: recordedAt,
	}
	return nil
}

func (s *Store) recordMetricLocked(key Key, success bool, duration time.Duration, recordedAt time.Time) {
	record := s.metrics[key]
	record.attempts++
	if success {
		record.successes++
		// Only successful attempts measure endpoint latency. A failed
		// attempt's wall-clock (connection refused, timeout) is a failure
		// signal, not performance evidence; folding it in lets a fast
		// refusal masquerade as a fast endpoint and outscore the cooldown
		// demotion the failure just earned.
		if duration > 0 {
			record.totalDuration += duration
		}
	}
	record.recordedAt = recordedAt
	s.metrics[key] = record
}

// NormalizeKey returns the normalized route-attempt key for attempt.
func NormalizeKey(attempt Attempt) Key {
	return Key{
		Harness:        strings.TrimSpace(attempt.Harness),
		Provider:       strings.TrimSpace(attempt.Provider),
		Model:          strings.TrimSpace(attempt.Model),
		Endpoint:       strings.TrimSpace(attempt.Endpoint),
		ServerInstance: strings.TrimSpace(attempt.ServerInstance),
	}
}

// Succeeded reports whether status is a success spelling.
func Succeeded(status string) bool {
	switch status {
	case "success", "ok", "succeeded":
		return true
	default:
		return false
	}
}

func (s *Store) clearFailuresLocked(key Key) {
	for existing := range s.failures {
		if KeysMatch(existing, key) {
			delete(s.failures, existing)
		}
	}
}

// KeysMatch reports whether existing satisfies every non-empty query field.
func KeysMatch(existing, query Key) bool {
	if query.Harness != "" && existing.Harness != query.Harness {
		return false
	}
	if query.Provider != "" && existing.Provider != query.Provider {
		return false
	}
	if query.Model != "" && existing.Model != query.Model {
		return false
	}
	if query.Endpoint != "" && existing.Endpoint != query.Endpoint {
		return false
	}
	if query.ServerInstance != "" && existing.ServerInstance != query.ServerInstance {
		return false
	}
	return true
}

// ActiveAttempts returns active failure records and removes expired records
// from the store.
func (s *Store) ActiveAttempts(now time.Time, ttl time.Duration) []Record {
	if s == nil {
		return nil
	}
	if ttl <= 0 {
		ttl = DefaultCooldown
	}
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()

	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.failures) == 0 {
		return nil
	}
	out := make([]Record, 0, len(s.failures))
	for key, record := range s.failures {
		if !record.RecordedAt.Add(ttl).After(now) {
			delete(s.failures, key)
			continue
		}
		out = append(out, record)
	}
	return out
}

// MetricSignals returns provider/model success-rate and latency maps suitable
// for routing inputs.
func (s *Store) MetricSignals(now time.Time, ttl time.Duration) (map[string]float64, map[string]float64) {
	if s == nil {
		return nil, nil
	}
	if ttl <= 0 {
		ttl = DefaultCooldown
	}
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()

	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.metrics) == 0 {
		return nil, nil
	}
	successRate := make(map[string]float64, len(s.metrics))
	latencyMS := make(map[string]float64, len(s.metrics))
	for key, record := range s.metrics {
		if record.attempts <= 0 {
			continue
		}
		if !record.recordedAt.IsZero() && !record.recordedAt.Add(ttl).After(now) {
			continue
		}
		metricKey := ProviderModelKey(key)
		successRate[metricKey] = float64(record.successes) / float64(record.attempts)
		if record.totalDuration > 0 && record.successes > 0 {
			latencyMS[metricKey] = float64(record.totalDuration.Milliseconds()) / float64(record.successes)
		}
	}
	return successRate, latencyMS
}

// ProviderModelKey returns the routing provider/model metric key for key.
func ProviderModelKey(key Key) string {
	return routing.ProviderModelKey(key.Provider, key.Endpoint, key.Model)
}
