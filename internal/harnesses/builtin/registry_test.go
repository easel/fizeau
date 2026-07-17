package builtin

import (
	"os"
	"reflect"
	"testing"

	"github.com/easel/fizeau/internal/harnesses"
	claudeharness "github.com/easel/fizeau/internal/harnesses/claude"
	geminiharness "github.com/easel/fizeau/internal/harnesses/gemini"
	piharness "github.com/easel/fizeau/internal/harnesses/pi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type builtinPortableNamespaceRecipe struct{}

func (builtinPortableNamespaceRecipe) PortableRuntimeNamespaceRecipe() {}

func TestBuiltinNewClaudeHonorsTransport(t *testing.T) {
	t.Run("native", func(t *testing.T) {
		t.Setenv("FIZEAU_CLAUDE_TRANSPORT", "native")

		h := New("claude")
		require.NotNil(t, h)

		runner, ok := h.(*claudeharness.Runner)
		require.True(t, ok, "New(\"claude\") must return *claude.Runner")
		assert.True(t, runner.NativeMode, "native transport must set NativeMode=true")
	})

	t.Run("default subprocess", func(t *testing.T) {
		orig, ok := os.LookupEnv("FIZEAU_CLAUDE_TRANSPORT")
		require.NoError(t, os.Unsetenv("FIZEAU_CLAUDE_TRANSPORT"))
		t.Cleanup(func() {
			if ok {
				_ = os.Setenv("FIZEAU_CLAUDE_TRANSPORT", orig)
				return
			}
			_ = os.Unsetenv("FIZEAU_CLAUDE_TRANSPORT")
		})

		h := New("claude")
		require.NotNil(t, h)

		runner, ok := h.(*claudeharness.Runner)
		require.True(t, ok, "New(\"claude\") must return *claude.Runner")
		assert.False(t, runner.NativeMode, "unset transport must keep NativeMode=false")
	})
}

func TestNewRouteRunnerClonesConfiguredStructuralPrototype(t *testing.T) {
	tests := []struct {
		name      string
		prototype harnesses.Harness
		assert    func(t *testing.T, got harnesses.Harness)
	}{
		{
			name:      "gemini",
			prototype: &geminiharness.Runner{Binary: "/runtime/gemini", BaseArgs: []string{"--configured"}, PromptMode: "stdin", EventBuffer: 17},
			assert: func(t *testing.T, got harnesses.Harness) {
				runner := got.(*geminiharness.Runner)
				if runner.Binary != "/runtime/gemini" || !reflect.DeepEqual(runner.BaseArgs, []string{"--configured"}) || runner.PromptMode != "stdin" || runner.EventBuffer != 17 {
					t.Fatalf("Gemini clone lost configured launch state: %#v", runner)
				}
			},
		},
		{
			name:      "pi",
			prototype: &piharness.Runner{Binary: "/runtime/pi", BaseArgs: []string{"--configured"}, PromptMode: "stdin", EventBuffer: 19},
			assert: func(t *testing.T, got harnesses.Harness) {
				runner := got.(*piharness.Runner)
				if runner.Binary != "/runtime/pi" || !reflect.DeepEqual(runner.BaseArgs, []string{"--configured"}) || runner.PromptMode != "stdin" || runner.EventBuffer != 19 {
					t.Fatalf("Pi clone lost configured launch state: %#v", runner)
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NewRouteRunner(harnesses.RouteRunnerKey{Harness: tc.name, Endpoint: "east"}, tc.prototype)
			require.NoError(t, err)
			require.NotNil(t, got)
			if got == tc.prototype {
				t.Fatal("exact route runner aliases its structural prototype")
			}
			tc.assert(t, got)
		})
	}
}

func TestNewRouteRunnerKeepsClaudeConstructionTimeTransport(t *testing.T) {
	t.Setenv("FIZEAU_CLAUDE_TRANSPORT", "subprocess")
	prototype := New("claude")
	if prototype.(*claudeharness.Runner).NativeMode {
		t.Fatal("subprocess prototype unexpectedly native")
	}

	// Transport identity is a process-lifetime property of the authority.
	// Changing the environment after structural construction must not flip it.
	t.Setenv("FIZEAU_CLAUDE_TRANSPORT", "native")
	t.Setenv(anthropicAPIKeyEnv, "")
	got, err := NewRouteRunner(harnesses.RouteRunnerKey{Harness: "claude", Endpoint: "east"}, prototype)
	require.NoError(t, err)
	if got.(*claudeharness.Runner).NativeMode {
		t.Fatal("exact runner reread transport environment after authority construction")
	}
}

func TestNewRouteRunnerKeepsManifestDeclaredClaudeTransport(t *testing.T) {
	tests := []struct {
		name       string
		transport  harnesses.PortableRuntimeTransport
		mode       harnesses.PortableRuntimeStructuralMode
		wantNative bool
	}{
		{name: "subprocess", transport: harnesses.PortableRuntimeTransportSubprocess, mode: harnesses.PortableRuntimeStructuralUnpinned},
		{name: "native", transport: harnesses.PortableRuntimeTransportNative, mode: harnesses.PortableRuntimeStructuralNonSubprocess, wantNative: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("FIZEAU_CLAUDE_TRANSPORT", map[bool]string{true: "subprocess", false: "native"}[test.wantNative])
			prototype := New("claude").(*claudeharness.Runner)
			input := harnesses.PortableRuntimeRunnerBindingInput{
				Structure: harnesses.PortableRuntimeStructure{
					Name: "claude", Transport: test.transport, Mode: test.mode,
				},
			}
			if test.transport == harnesses.PortableRuntimeTransportSubprocess {
				input.GuestRoot = "/opt/fizeau/runtime"
				input.ClosureClass = harnesses.PortableRuntimeClosureStatic
				input.Launch = harnesses.PortableRuntimeLaunch{EntrypointTarget: "claude/bin/claude"}
				input.Environment = map[string]string{"HOME": "/activation/home", "PATH": "/opt/fizeau/runtime/claude/bin"}
				input.NamespaceRecipe = builtinPortableNamespaceRecipe{}
			}
			binding, err := harnesses.NewPortableRuntimeRunnerBinding(input)
			require.NoError(t, err)
			require.NoError(t, prototype.BindPortableRuntime(binding))

			// Exact-route construction must not reread either transport or native
			// credential state after the manifest has bound the prototype.
			t.Setenv("FIZEAU_CLAUDE_TRANSPORT", map[bool]string{true: "native", false: "subprocess"}[test.wantNative])
			t.Setenv(anthropicAPIKeyEnv, "")
			got, err := NewRouteRunner(harnesses.RouteRunnerKey{Harness: "claude", Endpoint: "east"}, prototype)
			require.NoError(t, err)
			runner := got.(*claudeharness.Runner)
			if runner == prototype || runner.NativeMode != test.wantNative || runner.PortableRuntimeStructure() != input.Structure {
				t.Fatalf("manifest transport clone = %#v, want native=%t structure=%#v", runner, test.wantNative, input.Structure)
			}
			retained, ok := runner.PortableRuntimeBinding()
			if !ok || retained.Structure() != input.Structure {
				t.Fatal("exact Claude runner lost its manifest binding")
			}
			if test.transport == harnesses.PortableRuntimeTransportSubprocess {
				child, err := retained.BuildCommand([]string{"registry"}, []string{"request"})
				require.NoError(t, err)
				if child.Command() != "/opt/fizeau/runtime/claude/bin/claude" || !reflect.DeepEqual(child.Arguments(), []string{"registry", "request"}) {
					t.Fatalf("retained Claude launch = %q %q", child.Command(), child.Arguments())
				}
			}
		})
	}
}

func TestNewRouteRunnerClonesPortableRuntimeBinding(t *testing.T) {
	t.Setenv("FIZEAU_CLAUDE_TRANSPORT", "subprocess")
	for _, name := range []string{"claude", "claude-tui", "codex", "gemini", "opencode", "pi"} {
		t.Run(name, func(t *testing.T) {
			prototype := New(name)
			binder, ok := prototype.(harnesses.PortableRuntimeRunnerBinder)
			if !ok {
				t.Fatalf("%s structural prototype lacks portable binder", name)
			}
			binding, err := harnesses.NewPortableRuntimeRunnerBinding(harnesses.PortableRuntimeRunnerBindingInput{
				Structure: harnesses.PortableRuntimeStructure{
					Name: name, Transport: harnesses.PortableRuntimeTransportSubprocess,
					Mode: harnesses.PortableRuntimeStructuralUnpinned,
				},
				GuestRoot: "/opt/fizeau/runtime", ClosureClass: harnesses.PortableRuntimeClosureStatic,
				Launch:         harnesses.PortableRuntimeLaunch{EntrypointTarget: "harnesses/" + name + "/runner"},
				FixedArguments: []string{"--fixed"}, Environment: map[string]string{"HOME": "/activation/home"},
				NamespaceRecipe: builtinPortableNamespaceRecipe{},
			})
			require.NoError(t, err)
			require.NoError(t, binder.BindPortableRuntime(binding))
			got, err := NewRouteRunner(harnesses.RouteRunnerKey{Harness: name, Endpoint: "east"}, prototype)
			require.NoError(t, err)
			if got == prototype {
				t.Fatal("exact route aliases activated structural prototype")
			}
			retained, ok := got.(harnesses.PortableRuntimeRunnerBinder).PortableRuntimeBinding()
			if !ok || retained.NamespaceRecipe() == nil {
				t.Fatal("exact route lost portable binding state")
			}
			child, err := retained.BuildCommand([]string{"registry"}, []string{"request"})
			require.NoError(t, err)
			if child.Command() != "/opt/fizeau/runtime/harnesses/"+name+"/runner" ||
				!reflect.DeepEqual(child.Arguments(), []string{"--fixed", "registry", "request"}) ||
				!reflect.DeepEqual(child.Environment(), []string{"HOME=/activation/home"}) {
				t.Fatalf("retained portable child = %q %q %q", child.Command(), child.Arguments(), child.Environment())
			}
		})
	}
}
