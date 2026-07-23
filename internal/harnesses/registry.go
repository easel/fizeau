package harnesses

import "github.com/easel/fizeau/internal/productinfo"

// PreferenceOrder defines the default harness preference when multiple are available.
var PreferenceOrder = []string{"codex", "claude-tui", "claude", "grok", "opencode", "fiz", "pi", "openrouter", "lmstudio", "omlx", "lucebox", "vllm", "gemini"}

// builtinHarnesses defines known harnesses and how to invoke them.
var builtinHarnesses = map[string]HarnessConfig{
	"codex": {
		Name:     "codex",
		Binary:   "codex",
		BaseArgs: []string{"exec", "--json"},
		PermissionArgs: map[string][]string{
			"safe":         {},
			"supervised":   {},
			"unrestricted": {"--dangerously-bypass-approvals-and-sandbox"},
		},
		PromptMode:          "arg",
		DefaultModel:        "gpt-5.4",
		ReasoningLevels:     []string{"low", "medium", "high", "xhigh", "max"},
		ModelFlag:           "-m",
		WorkDirFlag:         "-C",
		ReasoningFlag:       "-c",
		ReasoningFormat:     "reasoning.effort=%s",
		Surface:             "codex",
		CostClass:           "medium",
		IsLocal:             false,
		IsSubscription:      true,
		AutoRoutingEligible: true,
		ExactPinSupport:     true,
		TUIQuotaCommand:     "/status",
	},
	// #nosec G101 -- not credentials; this is the harness invocation config
	// for the claude CLI (binary name, flags, permission mode strings).
	"claude": {
		Name:   "claude",
		Binary: "claude",
		// stream-json emits one JSON event per stdout line while the agent runs,
		// which lets the service surface real-time progress (tool calls, turn counts,
		// elapsed) instead of blocking until completion. --verbose is required
		// by claude CLI when --output-format=stream-json is combined with --print.
		BaseArgs: []string{"--print", "-p", "--verbose", "--output-format", "stream-json"},
		PermissionArgs: map[string][]string{
			"safe":         {},
			"supervised":   {"--permission-mode", "default"},
			"unrestricted": {"--permission-mode", "bypassPermissions", "--dangerously-skip-permissions"},
		},
		PromptMode:          "arg",
		DefaultModel:        "claude-sonnet-4-6",
		ReasoningLevels:     []string{"low", "medium", "high", "xhigh", "max"},
		ModelFlag:           "--model",
		WorkDirFlag:         "",
		ReasoningFlag:       "--effort",
		TokenPattern:        `(?i)total tokens[:\s]+([0-9,]+)`,
		Surface:             "claude",
		CostClass:           "medium",
		IsLocal:             false,
		IsSubscription:      true,
		AutoRoutingEligible: true,
		ExactPinSupport:     true,
		TUIQuotaCommand:     "/usage",
	},
	"claude-tui": {
		Name:                "claude-tui",
		Binary:              "claude",
		BaseArgs:            nil,
		PermissionArgs:      map[string][]string{"unrestricted": {}},
		PromptMode:          "stdin", // TUI interaction via stdin
		DefaultModel:        "claude-sonnet-4-6",
		ReasoningLevels:     nil,
		ModelFlag:           "",
		WorkDirFlag:         "",
		ReasoningFlag:       "",
		Surface:             "claude",
		CostClass:           "medium",
		IsLocal:             false,
		IsSubscription:      true,
		AutoRoutingEligible: true,
		ExactPinSupport:     false,
	},
	"grok": {
		Name:   "grok",
		Binary: "grok",
		// streaming-json emits one NDJSON event per stdout line (thought/text/end/error),
		// letting the service surface deltas in real time. The grok runner passes the
		// prompt as the value of -p (grok's -p/--single requires a value, unlike claude's
		// bare -p toggle), appended after all flags.
		BaseArgs: []string{"--output-format", "streaming-json"},
		PermissionArgs: map[string][]string{
			"safe":         {},
			"supervised":   {"--permission-mode", "default"},
			"unrestricted": {"--always-approve"},
		},
		PromptMode:          "arg",
		DefaultModel:        "grok-4.5",
		ReasoningLevels:     []string{"low", "medium", "high", "xhigh", "max"},
		ModelFlag:           "-m",
		WorkDirFlag:         "--cwd",
		ReasoningFlag:       "--reasoning-effort",
		Surface:             "grok",
		CostClass:           "medium",
		IsLocal:             false,
		IsSubscription:      true,
		AutoRoutingEligible: true,
		ExactPinSupport:     true,
		TUIQuotaCommand:     "/usage show",
	},
	"gemini": {
		Name:         "gemini",
		Binary:       "gemini",
		BaseArgs:     []string{"--output-format", "stream-json"},
		PromptMode:   "arg",
		DefaultModel: "gemini-2.5-flash",
		PermissionArgs: map[string][]string{
			"safe":         {"--approval-mode", "plan"},
			"supervised":   {"--approval-mode", "default"},
			"unrestricted": {"--approval-mode", "yolo"},
		},
		ModelFlag:           "-m",
		ReasoningLevels:     nil,
		Surface:             "gemini",
		CostClass:           "medium",
		IsLocal:             false,
		IsSubscription:      true,
		AutoRoutingEligible: false,
		ExactPinSupport:     true,
		TUIQuotaCommand:     "/model manage",
	},
	"opencode": {
		Name:     "opencode",
		Binary:   "opencode",
		BaseArgs: []string{"run", "--format", "json"},
		PermissionArgs: map[string][]string{
			// opencode run auto-approves all tool permissions;
			// no separate flags needed for any permission level.
			"safe":         {},
			"supervised":   {},
			"unrestricted": {},
		},
		PromptMode:      "arg",
		DefaultModel:    "opencode/gpt-5.4",
		ReasoningLevels: []string{"minimal", "low", "medium", "high", "max"},
		ModelFlag:       "-m",
		WorkDirFlag:     "--dir",
		ReasoningFlag:   "--variant",
		Surface:         "embedded-openai",
		CostClass:       "medium",
		IsLocal:         false,
		ExactPinSupport: true,
	},
	"fiz": {
		Name:                "fiz",
		Binary:              productinfo.BinaryName, // embedded — runs in-process via the agent library, not as a subprocess
		PermissionArgs:      map[string][]string{"safe": {}, "unrestricted": {}},
		PromptMode:          "arg",
		DefaultModel:        "", // uses agent config or provider default
		ReasoningLevels:     []string{"low", "medium", "high"},
		MaxReasoningTokens:  32768,
		Surface:             "embedded-openai",
		CostClass:           "local",
		IsLocal:             true,
		AutoRoutingEligible: true,
		ExactPinSupport:     true,
	},
	"pi": {
		Name:            "pi",
		Binary:          "pi",
		BaseArgs:        []string{"--mode", "json", "--print"},
		PromptMode:      "arg",
		DefaultModel:    "gemini-2.5-flash",
		ModelFlag:       "--model",
		ReasoningFlag:   "--thinking",
		ReasoningLevels: []string{"minimal", "low", "medium", "high", "xhigh"},
		Surface:         "pi",
		CostClass:       "medium",
		IsLocal:         false,
		ExactPinSupport: true,
	},
	"virtual": {
		Name:         "virtual",
		Binary:       "ddx-virtual-agent", // sentinel — never actually exec'd
		PromptMode:   "arg",
		DefaultModel: "recorded",
		Surface:      "virtual",
		CostClass:    "local",
		IsLocal:      true,
		TestOnly:     true, // test-only replay harness; never selected by production tier routing
	},
	"script": {
		Name:       "script",
		Binary:     "ddx-script-agent", // sentinel — never actually exec'd
		PromptMode: "arg",
		Surface:    "script",
		CostClass:  "local",
		IsLocal:    true,
		TestOnly:   true, // test-only directive interpreter; never selected by production tier routing
	},
	"openrouter": {
		Name:           "openrouter",
		Binary:         "",
		Surface:        "embedded-openai",
		CostClass:      "medium",
		IsHTTPProvider: true,
	},
	"lmstudio": {
		Name:           "lmstudio",
		Binary:         "",
		Surface:        "embedded-openai",
		CostClass:      "local",
		IsHTTPProvider: true,
		IsLocal:        true,
	},
	"omlx": {
		Name:           "omlx",
		Binary:         "",
		Surface:        "embedded-openai",
		CostClass:      "local",
		IsHTTPProvider: true,
		IsLocal:        true,
	},
	"lucebox": {
		Name:           "lucebox",
		Binary:         "",
		Surface:        "embedded-openai",
		CostClass:      "local",
		IsHTTPProvider: true,
		IsLocal:        true,
	},
	"vllm": {
		Name:           "vllm",
		Binary:         "",
		Surface:        "embedded-openai",
		CostClass:      "local",
		IsHTTPProvider: true,
		IsLocal:        true,
	},
}

// harnessAliases maps convenience names to canonical harness names.
// "local" always routes to the embedded agent; it must never
// fall through to a cloud harness like claude or codex.
var harnessAliases = map[string]string{
	"local":      "fiz",
	"grok-build": "grok",
}

// ResolveHarnessAlias returns the canonical harness name for an alias,
// or the input unchanged if it is not an alias.
func ResolveHarnessAlias(name string) string {
	if canonical, ok := harnessAliases[name]; ok {
		return canonical
	}
	return name
}

// Registry manages known harnesses.
type Registry struct {
	LookPath  LookPathFunc
	harnesses map[string]HarnessConfig
}

// NewRegistry creates a registry with builtin harnesses.
func NewRegistry() *Registry {
	r := &Registry{
		LookPath:  DefaultLookPath,
		harnesses: make(map[string]HarnessConfig),
	}
	for k, v := range builtinHarnesses {
		r.harnesses[k] = v
	}
	return r
}

// NewRegistryForTest creates an isolated registry containing only the named
// built-in harnesses. It lets cross-package composition tests constrain the
// complete candidate trace without exposing registry mutation to product
// callers (the harnesses package is internal).
func NewRegistryForTest(names ...string) *Registry {
	r := &Registry{
		LookPath:  DefaultLookPath,
		harnesses: make(map[string]HarnessConfig, len(names)),
	}
	for _, name := range names {
		cfg, ok := builtinHarnesses[name]
		if !ok {
			panic("harnesses: unknown built-in test harness " + name)
		}
		r.harnesses[name] = cfg
	}
	return r
}

// Get returns a harness config by name.
func (r *Registry) Get(name string) (HarnessConfig, bool) {
	h, ok := r.harnesses[name]
	return h, ok
}

// Has returns true if the harness is registered.
func (r *Registry) Has(name string) bool {
	_, ok := r.harnesses[name]
	return ok
}

// Names returns all registered harness names in preference order.
func (r *Registry) Names() []string {
	var names []string
	// First add preferred harnesses that exist in registry
	for _, name := range PreferenceOrder {
		if _, ok := r.harnesses[name]; ok {
			names = append(names, name)
		}
	}
	// Then add any extras not in preference list
	for name := range r.harnesses {
		found := false
		for _, pref := range PreferenceOrder {
			if name == pref {
				found = true
				break
			}
		}
		if !found {
			names = append(names, name)
		}
	}
	return names
}

// Discover checks which harnesses are available on the system.
func (r *Registry) Discover() []HarnessStatus {
	var statuses []HarnessStatus
	lookPath := r.LookPath
	if lookPath == nil {
		lookPath = DefaultLookPath
	}
	for _, name := range r.Names() {
		h := r.harnesses[name]
		status := HarnessStatus{
			Name:   name,
			Binary: h.Binary,
		}
		// Embedded harnesses are always available — no binary lookup needed.
		if name == "virtual" || name == "fiz" || name == "script" {
			status.Available = true
			status.Path = "(embedded)"
		} else if h.IsHTTPProvider {
			// HTTP-only providers: availability determined by probe, not binary.
			status.Available = true
			status.Path = "(http)"
		} else if path, err := lookPath(h.Binary); err != nil {
			status.Available = false
			status.Error = "binary not found"
		} else {
			status.Available = true
			status.Path = path
		}
		statuses = append(statuses, status)
	}
	return statuses
}

// FirstAvailable returns the first available harness in preference order.
func (r *Registry) FirstAvailable() (string, bool) {
	for _, s := range r.Discover() {
		if s.Available {
			return s.Name, true
		}
	}
	return "", false
}
