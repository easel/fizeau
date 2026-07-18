package serviceimpl

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	agentcore "github.com/easel/fizeau/internal/core"
	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/safefs"
	"github.com/easel/fizeau/internal/session"
)

// ContinuationLocatorStore is the service-private durable index for completed
// Fizeau sessions. It intentionally contains only a Fizeau identity, an exact
// log path, completion state, and the five-part route identity.
type ContinuationLocatorStore struct{ dir string }

// ContinuationLocator is the deliberately small, native-evidence-free record.
type ContinuationLocator struct {
	SessionID      string                   `json:"session_id"`
	SessionLogPath string                   `json:"session_log_path"`
	Complete       bool                     `json:"complete"`
	Route          harnesses.RouteRunnerKey `json:"route"`
}

// NewContinuationLocatorStore opens the private locator root. An empty root
// deliberately disables persistence; a configured root must be usable.
func NewContinuationLocatorStore(root string) (*ContinuationLocatorStore, error) {
	if root == "" {
		return &ContinuationLocatorStore{}, nil
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("continuation locator root: %w", err)
	}
	dir := filepath.Join(filepath.Clean(abs), ".fizeau-state", "continuation")
	if err := safefs.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("continuation locator root: %w", err)
	}
	if err := safefs.Chmod(filepath.Join(filepath.Clean(abs), ".fizeau-state"), 0o700); err != nil {
		return nil, fmt.Errorf("continuation locator state permissions: %w", err)
	}
	if err := safefs.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("continuation locator permissions: %w", err)
	}
	return &ContinuationLocatorStore{dir: dir}, nil
}

// Enabled reports whether durable locator persistence is configured.
func (s *ContinuationLocatorStore) Enabled() bool { return s != nil && s.dir != "" }

// WritePending records the exact path actually opened for a parent before
// route execution. sessionID is encoded, never used as a filesystem path.
func (s *ContinuationLocatorStore) WritePending(sessionID, logPath string, route harnesses.RouteRunnerKey) error {
	if !s.Enabled() {
		return nil
	}
	locator, err := newContinuationLocator(sessionID, logPath, route, false)
	if err != nil {
		return err
	}
	return s.write(locator)
}

// MarkComplete atomically promotes an already-pending locator. It does not
// inspect or copy harness-native state.
func (s *ContinuationLocatorStore) MarkComplete(sessionID string) error {
	if !s.Enabled() {
		return nil
	}
	locator, err := s.read(sessionID)
	if err != nil {
		return err
	}
	locator.Complete = true
	return s.write(locator)
}

// ResolveCompleted reads one recorded locator and validates only its exact log
// path. It never scans session-log directories. A valid terminal can recover a
// pending locator left by a crash and is atomically promoted before return.
func (s *ContinuationLocatorStore) ResolveCompleted(sessionID string) (ContinuationLocator, error) {
	locator, err := s.read(sessionID)
	if err != nil {
		return ContinuationLocator{}, err
	}
	if err := validateLocatorTerminal(locator); err != nil {
		return ContinuationLocator{}, err
	}
	if !locator.Complete {
		locator.Complete = true
		if err := s.write(locator); err != nil {
			return ContinuationLocator{}, err
		}
	}
	return locator, nil
}

func newContinuationLocator(sessionID, logPath string, route harnesses.RouteRunnerKey, complete bool) (ContinuationLocator, error) {
	if sessionID == "" || strings.ContainsRune(sessionID, filepath.Separator) || strings.ContainsRune(sessionID, 0) {
		return ContinuationLocator{}, fmt.Errorf("invalid continuation session id")
	}
	abs, err := filepath.Abs(logPath)
	if err != nil || logPath == "" {
		return ContinuationLocator{}, fmt.Errorf("invalid continuation session log path")
	}
	abs = filepath.Clean(abs)
	return ContinuationLocator{SessionID: sessionID, SessionLogPath: abs, Complete: complete, Route: route}, nil
}

func (s *ContinuationLocatorStore) locatorPath(sessionID string) (string, error) {
	if !s.Enabled() {
		return "", fmt.Errorf("continuation locator persistence is disabled")
	}
	if sessionID == "" || strings.ContainsRune(sessionID, filepath.Separator) || strings.ContainsRune(sessionID, 0) {
		return "", fmt.Errorf("invalid continuation session id")
	}
	return filepath.Join(s.dir, sessionID+".json"), nil
}

func (s *ContinuationLocatorStore) write(locator ContinuationLocator) error {
	path, err := s.locatorPath(locator.SessionID)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(locator)
	if err != nil {
		return fmt.Errorf("encode continuation locator: %w", err)
	}
	if err := safefs.WriteFileAtomic(path, raw, 0o600); err != nil {
		return fmt.Errorf("write continuation locator: %w", err)
	}
	return safefs.Chmod(path, 0o600)
}

func (s *ContinuationLocatorStore) read(sessionID string) (ContinuationLocator, error) {
	path, err := s.locatorPath(sessionID)
	if err != nil {
		return ContinuationLocator{}, err
	}
	raw, err := safefs.ReadFile(path)
	if err != nil {
		return ContinuationLocator{}, fmt.Errorf("read continuation locator: %w", err)
	}
	var locator ContinuationLocator
	if err := json.Unmarshal(raw, &locator); err != nil {
		return ContinuationLocator{}, fmt.Errorf("decode continuation locator: %w", err)
	}
	if locator.SessionID != sessionID || !filepath.IsAbs(locator.SessionLogPath) || filepath.Clean(locator.SessionLogPath) != locator.SessionLogPath {
		return ContinuationLocator{}, fmt.Errorf("invalid continuation locator")
	}
	return locator, nil
}

func validateLocatorTerminal(locator ContinuationLocator) error {
	events, err := session.ReadEvents(locator.SessionLogPath)
	if err != nil {
		return fmt.Errorf("read continuation parent log: %w", err)
	}
	var terminal *session.SessionEndData
	for _, event := range events {
		if event.SessionID != locator.SessionID {
			return fmt.Errorf("continuation parent session mismatch")
		}
		if event.Type != agentcore.EventSessionEnd {
			continue
		}
		if terminal != nil {
			return fmt.Errorf("continuation parent has duplicate terminal")
		}
		end := new(session.SessionEndData)
		if err := json.Unmarshal(event.Data, end); err != nil {
			return fmt.Errorf("decode continuation terminal: %w", err)
		}
		terminal = end
	}
	if terminal == nil {
		return fmt.Errorf("continuation parent is incomplete")
	}
	route := harnesses.RouteRunnerKey{Harness: terminal.ResolvedHarness, Provider: terminal.SelectedProvider, Endpoint: terminal.SelectedEndpoint, ServerInstance: terminal.SelectedServerInstance, Model: terminal.ResolvedModel}
	if route != locator.Route {
		return fmt.Errorf("continuation terminal route mismatch")
	}
	return nil
}

// LocatorPath is available to package tests and diagnostics without exposing
// locator contents through any public service projection.
func (s *ContinuationLocatorStore) LocatorPath(sessionID string) (string, error) {
	return s.locatorPath(sessionID)
}
