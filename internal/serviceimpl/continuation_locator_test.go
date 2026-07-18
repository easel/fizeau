package serviceimpl

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentcore "github.com/easel/fizeau/internal/core"
	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/session"
)

func TestCompletedSessionLocatorResolvesOnlyExactTerminalRoute(t *testing.T) {
	root := t.TempDir()
	store, err := NewContinuationLocatorStore(root)
	if err != nil {
		t.Fatal(err)
	}
	route := harnesses.RouteRunnerKey{Harness: "codex", Provider: "openai", Endpoint: "west", ServerInstance: "one", Model: "gpt-5.6-terra"}
	logPath := writeLocatorLog(t, root, "parent", route, 1)
	if err := store.WritePending("parent", logPath, route); err != nil {
		t.Fatal(err)
	}
	got, err := store.ResolveCompleted("parent")
	if err != nil {
		t.Fatalf("ResolveCompleted: %v", err)
	}
	if !got.Complete || got.SessionLogPath != logPath || got.Route != route {
		t.Fatalf("locator = %#v", got)
	}

	for _, tc := range []struct {
		name  string
		path  string
		route harnesses.RouteRunnerKey
	}{
		{"missing", filepath.Join(root, "missing.jsonl"), route},
		{"duplicate terminal", writeLocatorLog(t, root, "duplicate", route, 2), route},
		{"route mismatch", writeLocatorLog(t, root, "mismatch", route, 1), harnesses.RouteRunnerKey{Harness: "codex", Provider: "openai", Endpoint: "east", ServerInstance: "one", Model: "gpt-5.6-terra"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := store.WritePending(tc.name, tc.path, tc.route); err != nil {
				t.Fatal(err)
			}
			if _, err := store.ResolveCompleted(tc.name); err == nil {
				t.Fatal("ResolveCompleted succeeded")
			}
		})
	}
}

func TestContinuationLocatorRecoversPendingAndHasPrivatePermissions(t *testing.T) {
	root := t.TempDir()
	store, err := NewContinuationLocatorStore(root)
	if err != nil {
		t.Fatal(err)
	}
	route := harnesses.RouteRunnerKey{Harness: "claude-tui", Model: "claude-fable"}
	path := writeLocatorLog(t, root, "pending", route, 1)
	if err := store.WritePending("pending", path, route); err != nil {
		t.Fatal(err)
	}
	locatorPath, err := store.LocatorPath("pending")
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
	got, err := store.ResolveCompleted("pending")
	if err != nil || !got.Complete {
		t.Fatalf("recovery = %#v, %v", got, err)
	}
	raw, err := os.ReadFile(locatorPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"resume", "token", "argv", "policy", "native"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("locator contains %q", forbidden)
		}
	}
}

func TestContinuationLocatorDisabledForEmptyRoot(t *testing.T) {
	store, err := NewContinuationLocatorStore("")
	if err != nil || store.Enabled() {
		t.Fatalf("empty root = %#v, %v", store, err)
	}
	if err := store.WritePending("parent", "/tmp/parent.jsonl", harnesses.RouteRunnerKey{}); err != nil {
		t.Fatal(err)
	}
}

func writeLocatorLog(t *testing.T, dir, id string, route harnesses.RouteRunnerKey, terminals int) string {
	t.Helper()
	path := filepath.Join(dir, id+".jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for i := 0; i < terminals; i++ {
		data, err := json.Marshal(session.SessionEndData{ResolvedHarness: route.Harness, SelectedProvider: route.Provider, SelectedEndpoint: route.Endpoint, SelectedServerInstance: route.ServerInstance, ResolvedModel: route.Model})
		if err != nil {
			t.Fatal(err)
		}
		event, err := json.Marshal(agentcore.Event{SessionID: id, Type: agentcore.EventSessionEnd, Data: data})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(append(event, '\n')); err != nil {
			t.Fatal(err)
		}
	}
	return path
}
