package serviceimpl

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentcore "github.com/easel/fizeau/internal/core"
	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/session"
)

func TestCompletedSessionRouteResolutionRequiresTerminalRoute(t *testing.T) {
	root := t.TempDir()
	store, err := NewContinuationLocatorStore(root)
	if err != nil {
		t.Fatal(err)
	}
	route := locatorRoute()

	valid := writeLocatorLog(t, root, "valid", route, 1)
	if err := store.WritePending("valid", valid, route); err != nil {
		t.Fatal(err)
	}
	if got, err := store.ResolveCompleted("valid"); err != nil || !got.Complete {
		t.Fatalf("valid terminal = %#v, %v", got, err)
	}

	for _, tc := range []struct {
		name    string
		path    string
		session string
		route   harnesses.RouteRunnerKey
	}{
		{name: "absent", path: filepath.Join(root, "absent.jsonl"), session: "absent", route: route},
		// A directory is reliably unreadable as a JSONL file even under a privileged test user.
		{name: "unreadable", path: root, session: "unreadable", route: route},
		{name: "incomplete", path: writeLocatorEvents(t, root, "incomplete", nil), session: "incomplete", route: route},
		{name: "duplicate terminal", path: writeLocatorLog(t, root, "duplicate", route, 2), session: "duplicate", route: route},
		{name: "mismatched session", path: writeLocatorLog(t, root, "other-session", route, 1), session: "mismatched", route: route},
		{name: "route-less", path: writeLocatorLog(t, root, "route-less", harnesses.RouteRunnerKey{}, 1), session: "route-less", route: harnesses.RouteRunnerKey{}},
		{name: "locator log route mismatch", path: writeLocatorLog(t, root, "route-mismatch", route, 1), session: "route-mismatch", route: harnesses.RouteRunnerKey{Harness: route.Harness, Provider: route.Provider, Endpoint: "other", ServerInstance: route.ServerInstance, Model: route.Model}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := store.WritePending(tc.session, tc.path, tc.route); err != nil {
				t.Fatal(err)
			}
			if _, err := store.ResolveCompleted(tc.session); err == nil {
				t.Fatal("ResolveCompleted succeeded for invalid parent")
			}
		})
	}
}

func TestCompletedSessionRouteResolutionUsesPerRequestLogOverrideAfterRestart(t *testing.T) {
	hubRoot, overrideRoot := t.TempDir(), t.TempDir()
	route := locatorRoute()
	overridePath := writeLocatorLog(t, overrideRoot, "parent", route, 1)

	store, err := NewContinuationLocatorStore(hubRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.WritePending("parent", overridePath, route); err != nil {
		t.Fatal(err)
	}

	// A valid-looking hub log must not affect recovery: restart uses only the
	// exact per-request path recorded in the locator, never a directory scan.
	_ = writeLocatorLog(t, hubRoot, "parent", harnesses.RouteRunnerKey{Harness: "wrong", Model: "wrong"}, 1)
	store, err = NewContinuationLocatorStore(hubRoot)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.ResolveCompleted("parent")
	if err != nil {
		t.Fatalf("ResolveCompleted after restart: %v", err)
	}
	if got.SessionLogPath != overridePath || got.Route != route {
		t.Fatalf("recovered locator = %#v, want override %q and full route %#v", got, overridePath, route)
	}
}

func TestContinuationRecoversPendingLocatorAfterTerminalCommit(t *testing.T) {
	root := t.TempDir()
	store, err := NewContinuationLocatorStore(root)
	if err != nil {
		t.Fatal(err)
	}
	route := locatorRoute()
	path := writeLocatorEvents(t, root, "pending", nil)
	if err := store.WritePending("pending", path, route); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ResolveCompleted("pending"); err == nil {
		t.Fatal("pending locator promoted before terminal commit")
	}
	if complete := readLocatorComplete(t, store, "pending"); complete {
		t.Fatal("failed recovery promoted pending locator")
	}

	appendLocatorTerminal(t, path, "pending", route)
	got, err := store.ResolveCompleted("pending")
	if err != nil || !got.Complete {
		t.Fatalf("pending recovery = %#v, %v", got, err)
	}
	if !readLocatorComplete(t, store, "pending") {
		t.Fatal("recovered locator was not atomically promoted")
	}
}

func TestContinuationLocatorPermissionsAndConstructionFailure(t *testing.T) {
	root := t.TempDir()
	store, err := NewContinuationLocatorStore(root)
	if err != nil {
		t.Fatal(err)
	}
	route := locatorRoute()
	path := writeLocatorLog(t, root, "permissions", route, 1)
	if err := store.WritePending("permissions", path, route); err != nil {
		t.Fatal(err)
	}
	locatorPath, err := store.LocatorPath("permissions")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Dir(locatorPath), filepath.Dir(filepath.Dir(locatorPath))} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o700 {
			t.Fatalf("%s mode = %o, want 700", p, info.Mode().Perm())
		}
	}
	info, err := os.Stat(locatorPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("locator mode = %o, want 600", info.Mode().Perm())
	}

	fileRoot := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(fileRoot, []byte("not a root"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewContinuationLocatorStore(fileRoot); err == nil {
		t.Fatal("configured locator root backed by a file succeeded")
	}

	// No configured root is intentionally a no-op so ordinary Execute callers
	// do not acquire a persistence requirement.
	disabled, err := NewContinuationLocatorStore("")
	if err != nil || disabled.Enabled() {
		t.Fatalf("empty root = %#v, %v", disabled, err)
	}
	if err := disabled.WritePending("ordinary-execute", filepath.Join(root, "ordinary.jsonl"), route); err != nil {
		t.Fatalf("empty-root ordinary Execute persistence: %v", err)
	}
	hub := NewSessionHub()
	hub.OpenSession("ordinary-execute")
	stream := (ExecuteCoordinator{Hub: hub, Registry: harnesses.NewRegistry()}).RunResolved(context.Background(), ExecuteRequest{
		SessionID: "ordinary-execute", Decision: ExecuteDecision{Harness: "virtual", Model: "test"}, ContinuationLocators: disabled,
	}, ExecutePorts{})
	var final harnesses.Event
	for event := range stream {
		if event.Type == harnesses.EventTypeFinal {
			final = event
		}
	}
	if final.Type != harnesses.EventTypeFinal {
		t.Fatal("empty-root ordinary Execute did not terminalize")
	}
}

func TestContinuationLocatorContainsNoNativeEvidence(t *testing.T) {
	root := t.TempDir()
	store, err := NewContinuationLocatorStore(root)
	if err != nil {
		t.Fatal(err)
	}
	route := locatorRoute()
	path := writeLocatorLog(t, root, "private", route, 1)
	if err := store.WritePending("private", path, route); err != nil {
		t.Fatal(err)
	}
	locatorPath, err := store.LocatorPath("private")
	if err != nil {
		t.Fatal(err)
	}
	locatorBytes, err := os.ReadFile(locatorPath)
	if err != nil {
		t.Fatal(err)
	}

	// The service JSONL contains route identity only. A recognizable native
	// continuation token is intentionally absent from both durable formats.
	serviceLog := OpenSessionLog(SessionLogOptions{
		Dir: root, SessionID: "service", EndBase: session.SessionEndData{
			ResolvedHarness: route.Harness, SelectedProvider: route.Provider,
			SelectedEndpoint: route.Endpoint, SelectedServerInstance: route.ServerInstance, ResolvedModel: route.Model,
		},
	})
	serviceLog.WriteEnd(nil, harnesses.FinalData{Status: "success"})
	serviceLog.Close()
	serviceBytes, err := os.ReadFile(serviceLog.Path())
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range [][]byte{locatorBytes, serviceBytes} {
		if strings.Contains(string(raw), continuationNativeToken) || strings.Contains(string(raw), "resume_token") {
			t.Fatalf("native continuation evidence escaped: %s", raw)
		}
	}
}

func locatorRoute() harnesses.RouteRunnerKey {
	return harnesses.RouteRunnerKey{Harness: "codex", Provider: "openai", Endpoint: "west", ServerInstance: "one", Model: "gpt-5.6-terra"}
}

func readLocatorComplete(t *testing.T, store *ContinuationLocatorStore, sessionID string) bool {
	t.Helper()
	path, err := store.LocatorPath(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var locator ContinuationLocator
	if err := json.Unmarshal(raw, &locator); err != nil {
		t.Fatal(err)
	}
	return locator.Complete
}

func writeLocatorLog(t *testing.T, dir, id string, route harnesses.RouteRunnerKey, terminals int) string {
	t.Helper()
	path := writeLocatorEvents(t, dir, id, nil)
	for i := 0; i < terminals; i++ {
		appendLocatorTerminal(t, path, id, route)
	}
	return path
}

func writeLocatorEvents(t *testing.T, dir, id string, events []agentcore.Event) string {
	t.Helper()
	path := filepath.Join(dir, id+".jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, event := range events {
		raw, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(append(raw, '\n')); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func appendLocatorTerminal(t *testing.T, path, id string, route harnesses.RouteRunnerKey) {
	t.Helper()
	data, err := json.Marshal(session.SessionEndData{ResolvedHarness: route.Harness, SelectedProvider: route.Provider, SelectedEndpoint: route.Endpoint, SelectedServerInstance: route.ServerInstance, ResolvedModel: route.Model})
	if err != nil {
		t.Fatal(err)
	}
	event, err := json.Marshal(agentcore.Event{SessionID: id, Type: agentcore.EventSessionEnd, Data: data})
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write(append(event, '\n')); err != nil {
		t.Fatal(err)
	}
}
