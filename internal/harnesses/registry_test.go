package harnesses

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegistryBuiltinHarnesses(t *testing.T) {
	r := NewRegistry()
	for _, name := range []string{"codex", "claude", "gemini", "grok", "opencode", "fiz", "pi"} {
		assert.True(t, r.Has(name), "should have builtin harness: %s", name)
	}
	assert.False(t, r.Has("nonexistent"))
}

func TestRegistryGet(t *testing.T) {
	r := NewRegistry()
	h, ok := r.Get("codex")
	require.True(t, ok)
	assert.Equal(t, "codex", h.Name)
	assert.Equal(t, "codex", h.Binary)
	assert.Equal(t, "arg", h.PromptMode)
	assert.Equal(t, "-m", h.ModelFlag)
	assert.Equal(t, "-C", h.WorkDirFlag)
}

func TestRegistryDefaultBaseArgs(t *testing.T) {
	r := NewRegistry()

	codex, ok := r.Get("codex")
	require.True(t, ok)
	assert.Equal(t, []string{"exec", "--json"}, codex.BaseArgs)

	claude, ok := r.Get("claude")
	require.True(t, ok)
	assert.Equal(t, []string{"--print", "-p", "--verbose", "--output-format", "stream-json"}, claude.BaseArgs)
}

func TestRegistryNamesPreferenceOrder(t *testing.T) {
	r := NewRegistry()
	names := r.Names()
	require.Len(t, names, 15)
	// claude-tui is now in PreferenceOrder (the default for the shared "claude"
	// surface) and ranks just after codex, ahead of claude(--print).
	assert.Equal(t, "codex", names[0])
	assert.Equal(t, "claude-tui", names[1])
	assert.Equal(t, "claude", names[2])
	assert.Equal(t, "grok", names[3])
	assert.Equal(t, "opencode", names[4])
	assert.Equal(t, "lucebox", names[10])
	assert.Equal(t, "vllm", names[11])
	assert.Equal(t, "gemini", names[12])
	assert.Contains(t, names, "virtual")
	assert.Contains(t, names, "claude-tui")
}

func TestRegistryDiscoverEmbeddedAlwaysAvailable(t *testing.T) {
	r := NewRegistry()
	r.LookPath = func(file string) (string, error) {
		return "", fmt.Errorf("not found")
	}
	statuses := r.Discover()
	byName := make(map[string]HarnessStatus)
	for _, s := range statuses {
		byName[s.Name] = s
	}
	assert.True(t, byName["fiz"].Available, "embedded fiz should always be available")
	assert.True(t, byName["virtual"].Available, "virtual harness should always be available")
	assert.True(t, byName["script"].Available, "script harness should always be available")
}

func TestRegistryDiscoverHTTPProviders(t *testing.T) {
	r := NewRegistry()
	r.LookPath = func(file string) (string, error) {
		return "", fmt.Errorf("not found")
	}
	statuses := r.Discover()
	byName := make(map[string]HarnessStatus)
	for _, s := range statuses {
		byName[s.Name] = s
	}
	assert.True(t, byName["openrouter"].Available, "http provider openrouter should be available")
	assert.True(t, byName["lmstudio"].Available, "http provider lmstudio should be available")
	assert.True(t, byName["omlx"].Available, "http provider omlx should be available")
}

func TestRegistryFirstAvailable(t *testing.T) {
	r := NewRegistry()
	// With default LookPath, embedded fiz is always available.
	name, ok := r.FirstAvailable()
	assert.True(t, ok)
	assert.NotEmpty(t, name)
}

func TestRegistryFirstAvailableEmbeddedFallback(t *testing.T) {
	r := NewRegistry()
	r.LookPath = func(file string) (string, error) {
		return "", fmt.Errorf("not found")
	}
	// HTTP providers and embedded harnesses are always available,
	// so FirstAvailable should return the first in preference order.
	name, ok := r.FirstAvailable()
	assert.True(t, ok)
	// openrouter or lmstudio or fiz/virtual/script depending on preference order
	assert.NotEmpty(t, name)
}

func TestResolveHarnessAlias(t *testing.T) {
	assert.Equal(t, "fiz", ResolveHarnessAlias("local"))
	assert.Equal(t, "fiz", ResolveHarnessAlias("fiz"))
	assert.Equal(t, "grok", ResolveHarnessAlias("grok-build"))
	assert.Equal(t, "grok", ResolveHarnessAlias("grok"))
	assert.Equal(t, "agent", ResolveHarnessAlias("agent"))
	assert.Equal(t, "claude", ResolveHarnessAlias("claude"))
	assert.Equal(t, "unknown", ResolveHarnessAlias("unknown"))
}

func TestRegistryDoesNotRegisterAgentAsNativeHarness(t *testing.T) {
	r := NewRegistry()
	assert.True(t, r.Has("fiz"))
	assert.False(t, r.Has("agent"))
}

func TestBuiltinHarnessesPermissionArgs(t *testing.T) {
	r := NewRegistry()

	codex, ok := r.Get("codex")
	require.True(t, ok)
	assert.Equal(t, []string{"--dangerously-bypass-approvals-and-sandbox"}, codex.PermissionArgs["unrestricted"])

	claude, ok := r.Get("claude")
	require.True(t, ok)
	assert.Contains(t, claude.PermissionArgs["unrestricted"], "--dangerously-skip-permissions")

	claudeTUI, ok := r.Get("claude-tui")
	require.True(t, ok)
	assert.Empty(t, claudeTUI.PermissionArgs["unrestricted"])
	assert.Len(t, claudeTUI.PermissionArgs, 1)
}

// TestRegistryDiscover_LookupTable drives Discover() through a table of
// PATH-presence scenarios loaded from testdata/lookup_cases.json. Each case
// fakes exec.LookPath success for a chosen subset of binaries and asserts
// the resulting Available set. Embedded harnesses (fiz, virtual, script)
// and HTTP-only providers must always show as available regardless of PATH.
func TestRegistryDiscover_LookupTable(t *testing.T) {
	type caseDef struct {
		Name              string   `json:"name"`
		Installed         []string `json:"installed"`
		ExpectAvailable   []string `json:"expect_available"`
		ExpectUnavailable []string `json:"expect_unavailable"`
	}

	raw, err := os.ReadFile(filepath.Join("testdata", "lookup_cases.json"))
	require.NoError(t, err, "read testdata/lookup_cases.json")

	var cases []caseDef
	require.NoError(t, json.Unmarshal(raw, &cases), "decode lookup_cases.json")
	require.NotEmpty(t, cases, "lookup_cases.json must contain at least one case")

	for _, c := range cases {
		c := c
		t.Run(c.Name, func(t *testing.T) {
			installed := make(map[string]bool, len(c.Installed))
			for _, b := range c.Installed {
				installed[b] = true
			}

			r := NewRegistry()
			r.LookPath = func(file string) (string, error) {
				if installed[file] {
					return "/usr/local/bin/" + file, nil
				}
				return "", fmt.Errorf("exec: %q: not found in $PATH", file)
			}

			statuses := r.Discover()
			gotAvailable := make([]string, 0, len(statuses))
			gotUnavailable := make([]string, 0, len(statuses))
			for _, s := range statuses {
				if s.Available {
					gotAvailable = append(gotAvailable, s.Name)
				} else {
					gotUnavailable = append(gotUnavailable, s.Name)
				}
			}

			sort.Strings(gotAvailable)
			sort.Strings(gotUnavailable)
			expectAvail := append([]string{}, c.ExpectAvailable...)
			expectUnavail := append([]string{}, c.ExpectUnavailable...)
			sort.Strings(expectAvail)
			sort.Strings(expectUnavail)

			assert.ElementsMatch(t, expectAvail, gotAvailable, "available set")
			assert.ElementsMatch(t, expectUnavail, gotUnavailable, "unavailable set")

			// Subprocess harnesses that resolved must report a non-empty Path
			// pointing at the faked binary location.
			for _, s := range statuses {
				if !s.Available || s.Path == "(embedded)" || s.Path == "(http)" {
					continue
				}
				assert.Equal(t, "/usr/local/bin/"+s.Binary, s.Path,
					"resolved path for %s should be the faked LookPath result", s.Name)
				assert.Empty(t, s.Error, "available harness %s should have no error", s.Name)
			}

			// Unavailable subprocess harnesses must surface a "binary not
			// found" error so callers can render a helpful message.
			for _, s := range statuses {
				if s.Available {
					continue
				}
				assert.Equal(t, "binary not found", s.Error,
					"unavailable harness %s should report 'binary not found'", s.Name)
			}
		})
	}
}

// TestRegistryDiscover_FirstAvailableHonorsPreferenceOrder verifies that
// FirstAvailable returns the highest-preference installed CLI even when
// multiple are present, matching the documented preference order.
func TestRegistryDiscover_FirstAvailableHonorsPreferenceOrder(t *testing.T) {
	cases := []struct {
		installed []string
		// expected may be any of these values — tests only that it is one
		// of the installed CLIs and matches the preference order.
		wantFirst string
	}{
		{installed: []string{"claude", "codex", "opencode", "pi"}, wantFirst: "codex"},
		// claude-tui shares the "claude" binary and now ranks ahead of
		// claude(--print), so with the claude binary installed it is the first
		// available claude-surface harness.
		{installed: []string{"claude", "opencode", "pi"}, wantFirst: "claude-tui"},
		{installed: []string{"opencode", "pi"}, wantFirst: "opencode"},
		{installed: []string{"pi"}, wantFirst: "fiz"}, // fiz beats pi in preference
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("installed=%v", c.installed), func(t *testing.T) {
			set := make(map[string]bool, len(c.installed))
			for _, b := range c.installed {
				set[b] = true
			}
			r := NewRegistry()
			r.LookPath = func(file string) (string, error) {
				if set[file] {
					return "/usr/local/bin/" + file, nil
				}
				return "", fmt.Errorf("not found")
			}
			got, ok := r.FirstAvailable()
			require.True(t, ok)
			assert.Equal(t, c.wantFirst, got)
		})
	}
}

func TestBuiltinHarnessesMetadata(t *testing.T) {
	r := NewRegistry()

	codex, _ := r.Get("codex")
	assert.True(t, codex.IsSubscription)
	assert.False(t, codex.IsLocal)
	assert.True(t, codex.AutoRoutingEligible)
	assert.True(t, codex.ExactPinSupport)

	fiz, _ := r.Get("fiz")
	assert.True(t, fiz.IsLocal)
	assert.Equal(t, "local", fiz.CostClass)
	assert.True(t, fiz.AutoRoutingEligible)

	claude, _ := r.Get("claude")
	assert.True(t, claude.AutoRoutingEligible)

	gemini, _ := r.Get("gemini")
	assert.False(t, gemini.AutoRoutingEligible)
	assert.True(t, gemini.IsSubscription)
	assert.Equal(t, "medium", gemini.CostClass)

	opencode, _ := r.Get("opencode")
	assert.False(t, opencode.AutoRoutingEligible)
	assert.Equal(t, "opencode/gpt-5.4", opencode.DefaultModel)

	pi, _ := r.Get("pi")
	assert.False(t, pi.AutoRoutingEligible)
	assert.Equal(t, "gemini-2.5-flash", pi.DefaultModel)

	virtual, _ := r.Get("virtual")
	assert.True(t, virtual.TestOnly)
	assert.False(t, virtual.AutoRoutingEligible)

	script, _ := r.Get("script")
	assert.True(t, script.TestOnly)
	assert.False(t, script.AutoRoutingEligible)

	openrouter, _ := r.Get("openrouter")
	assert.True(t, openrouter.IsHTTPProvider)
	assert.False(t, openrouter.AutoRoutingEligible)

	lmstudio, _ := r.Get("lmstudio")
	assert.True(t, lmstudio.IsHTTPProvider)
	assert.True(t, lmstudio.IsLocal)
	assert.False(t, lmstudio.AutoRoutingEligible)

	omlx, _ := r.Get("omlx")
	assert.True(t, omlx.IsHTTPProvider)
	assert.True(t, omlx.IsLocal)
	assert.Equal(t, "local", omlx.CostClass)
	assert.False(t, omlx.AutoRoutingEligible)
}
