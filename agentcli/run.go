// Package agentcli exposes the fiz command runner for binaries and
// embedding callers that need to invoke the CLI without os.Exit.
package agentcli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/easel/fizeau"
	"github.com/easel/fizeau/internal/buildinfo"
	agentConfig "github.com/easel/fizeau/internal/config"
	"github.com/easel/fizeau/internal/productinfo"
	"github.com/easel/fizeau/internal/prompt"
	"github.com/easel/fizeau/internal/reasoning"
	"github.com/easel/fizeau/internal/safefs"
	"github.com/easel/fizeau/internal/sampling"
	"github.com/easel/fizeau/occompat"
	"github.com/easel/fizeau/picompat"
)

// samplingProfileNudgeOnce gates the ADR-007 §7 first-use catalog-stale
// warning: when the resolver reports MissingProfile (the user's installed
// catalog predates the requested sampling profile), we emit one warning
// per process pointing at `fiz catalog update`. Test code can swap
// the sink via samplingNudgeSink.
var (
	samplingProfileNudgeOnce sync.Once
	samplingNudgeSink        io.Writer = os.Stderr
)

// samplingProfileNudgeMessage is the loud variant for providers whose
// inference servers do NOT auto-load generation_config.json. Without a
// catalog profile these decode greedy (or worse) — e.g., the Qwen3.x
// tool-loop bug on omlx that ADR-007 was built to fix.
const samplingProfileNudgeMessage = "warning: model catalog does not declare sampling_profiles.code; samplers will use server defaults. Run 'fiz catalog update' to fetch the latest profiles (see ADR-007)."

// samplingProfileNudgeMessageImplicit is the soft variant for providers
// that DO auto-load model-card defaults (vLLM). The user is not in the
// loop-bug regime — the model creator's recommended bundle is in effect —
// but a catalog profile would still take precedence if installed.
const samplingProfileNudgeMessageImplicit = "note: model catalog does not declare sampling_profiles.code; the inference server is applying the model's generation_config.json. Run 'fiz catalog update' to install the agent's curated profile (see ADR-007)."

const anchorModeSystemPromptAddendum = `Anchor mode is active.

- File read output prefixes each line with anchor words.
- For files you have read, use anchor_edit instead of edit or write.
- Do not mix edit/write with anchor_edit on the same file.
- After any non-anchor file change, re-read the file before using anchor_edit so you have fresh anchors.`

// Options controls a single CLI invocation.
type Options struct {
	Args      []string
	Stdin     io.Reader
	Stdout    io.Writer
	Stderr    io.Writer
	Version   string
	BuildTime string
	GitCommit string
}

// Version info defaults. The standalone binary passes ldflag-populated values
// through Options; tests may still override these package vars directly.
var (
	Version   = "dev"
	BuildTime = ""
	GitCommit = ""
)

// Run executes the fiz CLI and returns its process exit code. It never
// calls os.Exit, which lets parent CLIs mount or test the command safely.
func Run(opts Options) int {
	if opts.Stdin == nil {
		opts.Stdin = os.Stdin
	}
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}
	if opts.Version == "" {
		opts.Version = Version
	}
	if opts.BuildTime == "" {
		opts.BuildTime = BuildTime
	}
	if opts.GitCommit == "" {
		opts.GitCommit = GitCommit
	}
	oldVersion, oldBuildTime, oldGitCommit := Version, BuildTime, GitCommit
	Version, BuildTime, GitCommit = opts.Version, opts.BuildTime, opts.GitCommit
	defer func() {
		Version, BuildTime, GitCommit = oldVersion, oldBuildTime, oldGitCommit
	}()
	return runWithOptions(opts)
}

func run() int {
	return Run(Options{})
}

func runWithOptions(opts Options) int {
	cliArgs := opts.Args
	if cliArgs == nil {
		cliArgs = os.Args[1:]
	}
	runSubcommand, cliArgs := normalizeRunSubcommand(cliArgs)

	fs := flag.NewFlagSet(productinfo.BinaryName, flag.ContinueOnError)
	fs.SetOutput(opts.Stderr)

	// Define flags
	promptFlag := fs.String("p", "", "Prompt (use @file to read from file)")
	jsonOutput := fs.Bool("json", false, "Output result as JSON")
	providerFlag := fs.String("provider", "", "Named provider from config (e.g., vidar, openrouter)")
	harnessFlag := fs.String("harness", "", "Harness hard pin (selects a specific harness)")
	model := fs.String("model", "", "Model route key or explicit concrete model override")
	policyFlag := fs.String("policy", "", "Routing policy (cheap, default, smart, or air-gapped)")
	listModels := fs.Bool("list-models", false, "List available models with routing metadata")
	minPower := fs.Int("min-power", 0, "Minimum catalog model power for automatic routing")
	maxPower := fs.Int("max-power", 0, "Maximum catalog model power for automatic routing")
	reasoningFlag := fs.String("reasoning", "", "Reasoning control: auto, off, low, medium, high, xhigh, max, or token budget")
	allowDeprecatedModel := fs.Bool("allow-deprecated-model", false, "Allow deprecated model catalog references")
	maxIter := fs.Int("max-iter", 0, "Max iterations")
	workDir := fs.String("work-dir", "", "Working directory")
	version := fs.Bool("version", false, "Print version")
	sysPromptFlag := fs.String("system", "", "System prompt (appended to preset)")
	presetFlag := fs.String("preset", "", "System prompt preset: default, smart, cheap, minimal, benchmark")
	planFlag := fs.Bool("plan", false, "Run a no-tool planning turn before the main loop (auto-enabled by --preset benchmark)")
	anchorsFlag := fs.Bool("anchors", false, "Enable anchored read output and the anchor_edit tool")
	// FEAT-005 §26-29 / AC-FEAT-005-07: per-run cost cap. Zero (the default)
	// means no cap. Env override FIZEAU_COST_CAP_USD applies when the flag is
	// not explicitly set, so scripts can configure a global cap without
	// edit­ing every invocation.
	costCapUSDFlag := fs.Float64("cost-cap-usd", 0, "Per-run cost cap in USD; halts the loop with status=budget_halted before the next request when running + projected cost reaches this limit (0 = no cap; env: FIZEAU_COST_CAP_USD)")

	if err := fs.Parse(cliArgs); err != nil {
		return 2
	}

	if *version {
		fmt.Fprintf(opts.Stdout, "%s %s (commit %s, built %s)\n", productinfo.BinaryName, opts.Version, opts.GitCommit, opts.BuildTime)
		return 0
	}

	// Resolve working directory early (needed for config loading)
	wd := *workDir
	if wd == "" {
		wd, _ = os.Getwd()
	}
	if *listModels {
		return cmdListModels(wd, *jsonOutput, fizeau.ModelFilter{Provider: *providerFlag})
	}
	if err := fizeau.ValidatePowerBounds(*minPower, *maxPower); err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		return 2
	}

	// Handle subcommands
	args := fs.Args()
	if !runSubcommand && len(args) > 0 {
		switch args[0] {
		case "log":
			return cmdLog(wd, args[1:])
		case "replay":
			return cmdReplay(wd, args[1:])
		case "usage":
			return cmdUsage(wd, args[1:])
		case "models":
			modelArgs := append([]string{}, args[1:]...)
			if *jsonOutput {
				modelArgs = append([]string{"--json"}, modelArgs...)
			}
			if *providerFlag != "" {
				modelArgs = append([]string{"--provider", *providerFlag}, modelArgs...)
			}
			return cmdModels(wd, modelArgs)
		case "check":
			return cmdCheck(wd, *providerFlag, args[1:])
		case "providers":
			return cmdProviders(wd, *jsonOutput)
		case "policies":
			return cmdPolicies(wd, *jsonOutput)
		case "harnesses":
			return cmdHarnesses(wd, *jsonOutput)
		case "catalog":
			return cmdCatalog(wd, args[1:])
		case "corpus":
			return cmdCorpus(wd, args[1:])
		case "route-status":
			return cmdRouteStatus(wd, args[1:])
		case "import":
			return cmdImport(wd, args[1:])
		case "version":
			return cmdVersion(args[1:])
		case "update":
			return cmdUpdate(args[1:])
		}
	}

	// Load config
	cfg, err := agentConfig.Load(wd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		return 2
	}
	for _, warning := range cfg.Warnings() {
		fmt.Fprintf(os.Stderr, "%s: warning: %s\n", productinfo.BinaryName, warning)
	}

	// Check for zero-config discovery
	checkZeroConfigDiscovery(cfg)

	// Fail fast if no provider is configured before we spend tokens on a doomed run.
	// Only applies to actual execution paths, not to help or metadata subcommands.
	// `len(args) == 0` with no prompt flag is ambiguous — skip validation there to
	// preserve the existing "no prompt" error precedence.
	if len(cfg.Providers) == 0 && (*promptFlag != "" || runSubcommand) {
		fmt.Fprintf(os.Stderr, "error: no providers configured — run '%s import pi' or '%s import opencode', set FIZEAU_PROVIDER/FIZEAU_BASE_URL, or create .fizeau/config.yaml\n", productinfo.BinaryName, productinfo.BinaryName)
		return 2
	}

	// Check for drift
	checkDrift(cfg, wd)

	// Resolve prompt
	var promptText string
	var promptMetadata map[string]string
	if runSubcommand && *promptFlag == "" && len(args) > 0 {
		promptText, promptMetadata, err = parsePromptInput(strings.Join(args, " "))
	} else {
		promptText, promptMetadata, err = resolvePrompt(*promptFlag)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		return 2
	}
	if promptText == "" {
		fmt.Fprintln(os.Stderr, "error: no prompt provided (use -p or pipe to stdin)")
		fs.Usage()
		return 2
	}

	preset, err := resolvePreset(*presetFlag, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		return 2
	}

	requestedModel := strings.TrimSpace(*model)
	providerName := strings.TrimSpace(*providerFlag)
	harnessName := strings.TrimSpace(*harnessFlag)
	overrides := agentConfig.ProviderOverrides{
		Model:           requestedModel,
		AllowDeprecated: *allowDeprecatedModel,
	}

	selection, pc, err := resolveRunSelection(cfg, wd, providerName, requestedModel, overrides)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		return 2
	}
	if providerName != "" && requestedModel != "" {
		if providerCfg, ok := cfg.Providers[providerName]; ok {
			providerCfg.Model = requestedModel
			cfg.Providers[providerName] = providerCfg
		}
	}
	resolvedModel := requestedModel
	if selection.ResolvedModel != "" {
		resolvedModel = selection.ResolvedModel
	}
	resolvedReasoning, err := resolveRunReasoning(cfg, resolvedModel, selection.ReasoningDefault, *reasoningFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		return 2
	}

	// Resolve max iterations
	iterations := cfg.MaxIterations
	if *maxIter > 0 {
		iterations = *maxIter
	}

	// Resolve per-run cost cap. Precedence: explicit --cost-cap-usd flag wins;
	// otherwise FIZEAU_COST_CAP_USD env var applies. Empty / unparseable env
	// is silently ignored (no cap). Negative values are clamped to zero.
	costCapUSD := *costCapUSDFlag
	if !flagWasSet(fs, "cost-cap-usd") {
		if env := strings.TrimSpace(os.Getenv("FIZEAU_COST_CAP_USD")); env != "" {
			if parsed, err := strconv.ParseFloat(env, 64); err == nil && parsed > 0 {
				costCapUSD = parsed
			}
		}
	}
	if costCapUSD < 0 {
		costCapUSD = 0
	}

	anchorsEnabled := cfg.Anchors || *anchorsFlag

	// Build tools
	tools := buildToolsForPresetWithAnchors(wd, preset, anchorsEnabled, bashOutputFilterConfig(cfg.Tools.Bash.OutputFilter))

	// Discover SKILL.md skills and append the load_skill tool when the
	// catalog is non-empty. Resolution precedence: FIZEAU_SKILLS_DIR
	// env var → cfg.Skills.Dir → ".fizeau/skills" under the work dir.
	// A literal "-" (in either env or config) disables discovery even
	// when the directory exists.
	var skillCatalog *fizeau.SkillCatalog
	if skillsDir := resolveSkillsDir(cfg, wd); skillsDir != "" {
		if cat, warnings, err := fizeau.ScanSkillsDir(skillsDir); err != nil {
			fmt.Fprintf(os.Stderr, "warning: skill discovery failed for %s: %v\n", skillsDir, err)
		} else {
			for _, w := range warnings {
				fmt.Fprintf(os.Stderr, "warning: skill: %s\n", w)
			}
			if loader := fizeau.NewLoadSkillTool(cat); loader != nil {
				tools = append(tools, loader)
				skillCatalog = cat
			}
		}
	}

	sysPrompt := buildSystemPromptForRun(preset, tools, skillCatalog, prompt.LoadContextFiles(wd), wd, anchorsEnabled, *sysPromptFlag)

	// Signal context is created early so discovery can be cancelled on interrupt
	// or termination. SIGTERM is included so Harbor/benchmark runners that send
	// TERM on AgentTimeout flow through the same cancellation path as Ctrl-C
	// and harness runners get a chance to kill their subprocess trees before
	// fiz exits.
	ctx, cancel := signal.NotifyContext(context.Background(), runCancelSignals()...)
	defer cancel()

	resolvedContextWindow, resolvedMaxTokens := resolveRunModelLimits(ctx, cfg, pc, resolvedModel)

	// Build compaction config from resolved context window.
	compactionCfg := fizeau.DefaultCompactionConfig()
	if resolvedContextWindow > 0 {
		compactionCfg.ContextWindow = resolvedContextWindow
	}
	if resolvedMaxTokens > 0 {
		compactionCfg.ReserveTokens = resolvedMaxTokens
	}
	if cfg.CompactionPercent > 0 {
		compactionCfg.EffectivePercent = cfg.CompactionPercent
	}
	// Resolve the sampling profile through the ADR-007 chain:
	// catalog sampling_profiles → providers.<name>.sampling. v1 hardcodes
	// the profile name "code"; per-request profile selection is deferred.
	// Wrapped harnesses are not invoked through this CLI path, so no
	// harness check is needed here — the resolver still respects the
	// per-model sampling_control = harness_pinned short-circuit defensively.
	var (
		sTemp          *float32
		sTopP          *float64
		sTopK          *int
		sMinP          *float64
		sRep           *float64
		sSeed          *int64
		samplingSource string
	)
	{
		var lookup sampling.CatalogLookup
		if catalog, err := cfg.LoadModelCatalog(); err == nil && catalog != nil {
			lookup = catalog
		}
		res := sampling.Resolve(lookup, resolvedModel, "code", pc.Sampling)
		samplingSource = sampling.SourceSummary(res.Sources)
		if res.MissingProfile && selection.Provider != "" {
			// ADR-007 §7 rule 4: tone the nudge by provider capability.
			// Servers that auto-load generation_config.json (vLLM) get the
			// soft note; servers that do not (omlx, lmstudio, lucebox) get
			// the loud warning because their fallback is decode-greedy or
			// worse.
			msg := samplingProfileNudgeMessage
			if agentConfig.ProviderImplicitGenerationConfig(pc.Type) {
				msg = samplingProfileNudgeMessageImplicit
			}
			samplingProfileNudgeOnce.Do(func() {
				fmt.Fprintln(samplingNudgeSink, msg)
			})
		}
		if res.Profile.Temperature != nil {
			t := float32(*res.Profile.Temperature)
			sTemp = &t
		}
		sTopP = res.Profile.TopP
		sTopK = res.Profile.TopK
		sMinP = res.Profile.MinP
		sRep = res.Profile.RepetitionPenalty
		sSeed = res.Profile.Seed
	}
	estimatedPromptTokens := estimateRunPromptTokens(promptText, sysPrompt)
	requiresTools := runRequiresTools(*harnessFlag, preset, flagWasSet(fs, "preset"), anchorsEnabled)
	req := buildServiceExecuteRequest(serviceExecuteRequestParams{
		Prompt:                  promptText,
		SystemPrompt:            sysPrompt,
		WorkDir:                 wd,
		Metadata:                promptMetadata,
		Tools:                   tools,
		ToolPreset:              preset,
		SelectedProvider:        selection.Provider,
		SelectedRoute:           selection.Route,
		RequestedModel:          requestedModel,
		ResolvedModel:           selection.ResolvedModel,
		ExplicitProvider:        providerName != "",
		ExplicitModel:           requestedModel != "",
		Policy:                  *policyFlag,
		Harness:                 harnessName,
		Reasoning:               resolvedReasoning,
		EstimatedPromptTokens:   estimatedPromptTokens,
		RequiresTools:           requiresTools,
		NoStream:                false,
		MinPower:                *minPower,
		MaxPower:                *maxPower,
		MaxIterations:           iterations,
		MaxTokens:               resolvedMaxTokens,
		ReasoningByteLimit:      cfg.ReasoningByteLimit,
		CostCapUSD:              costCapUSD,
		CompactionContextWindow: resolvedContextWindow,
		CompactionReserveTokens: compactionCfg.ReserveTokens,
		Temperature:             sTemp,
		TopP:                    sTopP,
		TopK:                    sTopK,
		MinP:                    sMinP,
		RepetitionPenalty:       sRep,
		Seed:                    sSeed,
		SamplingSource:          samplingSource,
		PlanningMode:            *planFlag || preset == "benchmark",
	})
	result, err := executeViaServiceFn(ctx, req, selection, sessionLogDir(wd, cfg), agentConfig.NewServiceConfig(cfg, wd))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		return 1
	}

	// Output result
	if *jsonOutput {
		data, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(data))
	} else {
		fmt.Print(result.Output)
		if result.Output != "" && !strings.HasSuffix(result.Output, "\n") {
			fmt.Println()
		}
	}

	if result.Error != "" && !*jsonOutput {
		fmt.Fprintf(os.Stderr, "error: %s\n", result.Error)
	}

	// Status to stderr
	fmt.Fprintf(os.Stderr, "[%s] tokens: %d in / %d out", result.Status, result.Tokens.Input, result.Tokens.Output)
	if result.CostUSD != nil {
		fmt.Fprintf(os.Stderr, " | cost: $%.4f", *result.CostUSD)
		if costCapUSD > 0 {
			fmt.Fprintf(os.Stderr, " / cap $%.4f", costCapUSD)
		}
	} else if costCapUSD > 0 {
		// Cap configured but cost is unknown — print the cap so operators see
		// it even when the service cannot provide a final cost.
		fmt.Fprintf(os.Stderr, " | cap $%.4f", costCapUSD)
	}
	fmt.Fprintln(os.Stderr)

	switch result.Status {
	case "success", "iteration_limit":
		return 0
	case "budget_halted":
		// FEAT-005 §27 / AC-FEAT-005-07: distinct non-zero exit code so
		// scripts can branch on a budget halt vs other failures.
		return 2
	default:
		return 1
	}
}

var lookupRunModelLimits = func(ctx context.Context, pc agentConfig.ProviderConfig, model string) (int, int) {
	observed := agentConfig.LookupModelLimits(ctx, pc, model)
	return observed.ContextLength, observed.MaxCompletionTokens
}

// resolveRunModelLimits applies the field-by-field limit authority chain used
// to configure execution: explicit provider config, then type-gated provider
// API evidence, then a catalog context fallback. Output limits deliberately do
// not fall back to the catalog; zero leaves that choice to the provider.
func resolveRunModelLimits(ctx context.Context, cfg *agentConfig.Config, pc agentConfig.ProviderConfig, model string) (int, int) {
	contextWindow := pc.ContextWindow
	maxTokens := pc.MaxTokens
	if strings.TrimSpace(model) == "" {
		return contextWindow, maxTokens
	}
	if contextWindow == 0 || maxTokens == 0 {
		observedContext, observedMax := lookupRunModelLimits(ctx, pc, model)
		if contextWindow == 0 && observedContext > 0 {
			contextWindow = observedContext
		}
		if maxTokens == 0 && observedMax > 0 {
			maxTokens = observedMax
		}
	}
	if contextWindow == 0 && cfg != nil {
		if catalog, err := cfg.LoadModelCatalog(); err == nil && catalog != nil {
			contextWindow = catalog.ContextWindowForModel(model)
		}
	}
	return contextWindow, maxTokens
}

var executeViaServiceFn = executeViaService
var newServiceFn = fizeau.New

func executeViaService(ctx context.Context, req fizeau.ServiceExecuteRequest, selection providerSelection, logDir string, serviceConfig fizeau.ServiceConfig) (cliExecutionResult, error) {
	svc, err := newServiceFn(fizeau.ServiceOptions{ServiceConfig: serviceConfig})
	if err != nil {
		return cliExecutionResult{}, err
	}

	// Hand the resolved session-log directory to the service. Per
	// CONTRACT-003, the service owns lifecycle + terminal record persistence;
	// the CLI only chooses the storage location and consumes the event
	// stream.
	req.SessionLogDir = logDir

	ch, err := svc.Execute(ctx, req)
	if err != nil {
		return cliExecutionResult{}, err
	}

	result := cliExecutionResult{
		Status:     "failed",
		Reasoning:  req.Reasoning,
		CostSource: fizeau.CostSourceUnknown,
	}
	var sawFinal bool
	for ev := range ch {
		decoded, err := fizeau.DecodeServiceEvent(ev)
		if err != nil {
			return result, fmt.Errorf("decode service event: %w", err)
		}
		switch decoded.Type {
		case fizeau.ServiceEventTypeRoutingDecision:
			if decoded.RoutingDecision != nil {
				result.SessionID = decoded.RoutingDecision.SessionID
				result.SelectedProvider = decoded.RoutingDecision.Provider
				result.ResolvedModel = decoded.RoutingDecision.Model
				result.Model = decoded.RoutingDecision.Model
			}
		case fizeau.ServiceEventTypeTextDelta:
			if decoded.TextDelta != nil {
				result.Output += decoded.TextDelta.Text
			}
		case fizeau.ServiceEventTypeFinal:
			if decoded.Final == nil {
				return result, fmt.Errorf("decode service final event: missing final payload")
			}
			sawFinal = true
			result.Status = decoded.Final.Status
			result.Output = decoded.Final.FinalText
			result.Duration = time.Duration(decoded.Final.DurationMS) * time.Millisecond
			result.CostUSD, result.CostSource = decoded.Final.CostMeasurement()
			result.Error = decoded.Final.Error
			if decoded.Final.Usage != nil {
				result.Tokens = cliTokenUsage{
					Input:      derefInt(decoded.Final.Usage.InputTokens),
					Output:     derefInt(decoded.Final.Usage.OutputTokens),
					CacheRead:  derefInt(decoded.Final.Usage.CacheReadTokens),
					CacheWrite: derefInt(decoded.Final.Usage.CacheWriteTokens),
					Total:      derefInt(decoded.Final.Usage.TotalTokens),
				}
			}
			if decoded.Final.RoutingActual != nil {
				result.SelectedProvider = decoded.Final.RoutingActual.Provider
				result.ResolvedModel = decoded.Final.RoutingActual.Model
				result.Model = decoded.Final.RoutingActual.Model
				result.AttemptedProviders = append([]string(nil), decoded.Final.RoutingActual.FallbackChainFired...)
				if len(result.AttemptedProviders) > 1 {
					result.FailoverCount = len(result.AttemptedProviders) - 1
				} else {
					result.FailoverCount = 0
				}
			}
		}
	}
	if !sawFinal {
		if ctx.Err() != nil {
			result.Status = "cancelled"
			result.Error = ctx.Err().Error()
			return result, nil
		}
		return result, fmt.Errorf("service execution ended without final event")
	}
	if result.Status != "success" && len(result.AttemptedProviders) == 0 {
		result.SelectedProvider = ""
		result.SelectedRoute = ""
		result.ResolvedModel = ""
		result.Model = ""
	}
	return result, nil
}

type serviceExecuteRequestParams struct {
	Prompt                  string
	SystemPrompt            string
	WorkDir                 string
	Metadata                map[string]string
	Tools                   []fizeau.Tool
	ToolPreset              string
	Permissions             string
	SelectedProvider        string
	SelectedRoute           string
	RequestedModel          string
	ResolvedModel           string
	ExplicitProvider        bool
	ExplicitModel           bool
	Policy                  string
	Harness                 string
	Reasoning               fizeau.Reasoning
	EstimatedPromptTokens   int
	RequiresTools           bool
	NoStream                bool
	MinPower                int
	MaxPower                int
	MaxIterations           int
	MaxTokens               int
	ReasoningByteLimit      int
	CompactionContextWindow int
	CompactionReserveTokens int

	// CostCapUSD is the per-run cost cap (USD). Zero means no cap. Threaded
	// straight into ServiceExecuteRequest.CostCapUSD; see FEAT-005 §26-29.
	CostCapUSD float64

	// Sampling overrides. Nil/zero means leave-unset (let the request flow
	// through the harness's effective defaults: today, Temperature is
	// always sent as 0; the rest are unset).
	Temperature       *float32
	TopP              *float64
	TopK              *int
	MinP              *float64
	RepetitionPenalty *float64
	Seed              *int64
	SamplingSource    string

	// PlanningMode requests a no-tool planning turn before the main loop.
	// The service may also auto-enable planning for certain presets (e.g.
	// benchmark) — this field is the explicit CLI override.
	PlanningMode bool
}

type cliExecutionResult struct {
	Status             string            `json:"status"`
	Output             string            `json:"output"`
	Tokens             cliTokenUsage     `json:"tokens"`
	Duration           time.Duration     `json:"duration_ms"`
	CostUSD            *float64          `json:"cost_usd,omitempty"`
	CostSource         fizeau.CostSource `json:"cost_source"`
	Model              string            `json:"model,omitempty"`
	SelectedProvider   string            `json:"selected_provider,omitempty"`
	SelectedRoute      string            `json:"selected_route,omitempty"`
	RequestedModel     string            `json:"requested_model,omitempty"`
	ResolvedModel      string            `json:"resolved_model,omitempty"`
	Reasoning          fizeau.Reasoning  `json:"reasoning,omitempty"`
	AttemptedProviders []string          `json:"attempted_providers,omitempty"`
	FailoverCount      int               `json:"failover_count,omitempty"`
	Error              string            `json:"error,omitempty"`
	SessionID          string            `json:"session_id"`
}

type cliTokenUsage struct {
	Input      int `json:"input"`
	Output     int `json:"output"`
	CacheRead  int `json:"cache_read,omitempty"`
	CacheWrite int `json:"cache_write,omitempty"`
	Total      int `json:"total"`
}

func buildServiceExecuteRequest(params serviceExecuteRequestParams) fizeau.ServiceExecuteRequest {
	model := params.ResolvedModel
	if model == "" {
		model = params.RequestedModel
	}
	provider := params.SelectedProvider
	harness := params.Harness
	selectedRoute := params.SelectedRoute
	permissions := params.Permissions
	if permissions == "" {
		permissions = "unrestricted"
	}
	if selectedRoute == "" && provider != "" {
		selectedRoute = provider
	}
	if selectedRoute == "" && model != "" {
		selectedRoute = model
	}
	req := fizeau.ServiceExecuteRequest{
		Prompt:                  params.Prompt,
		SystemPrompt:            params.SystemPrompt,
		Harness:                 harness,
		Model:                   model,
		Provider:                provider,
		Policy:                  params.Policy,
		WorkDir:                 params.WorkDir,
		Reasoning:               params.Reasoning,
		EstimatedPromptTokens:   params.EstimatedPromptTokens,
		RequiresTools:           params.RequiresTools,
		NoStream:                params.NoStream,
		MinPower:                params.MinPower,
		MaxPower:                params.MaxPower,
		Metadata:                params.Metadata,
		Tools:                   params.Tools,
		ToolPreset:              params.ToolPreset,
		Permissions:             permissions,
		MaxIterations:           params.MaxIterations,
		MaxTokens:               params.MaxTokens,
		ReasoningByteLimit:      params.ReasoningByteLimit,
		CompactionContextWindow: params.CompactionContextWindow,
		CompactionReserveTokens: params.CompactionReserveTokens,
		CostCapUSD:              params.CostCapUSD,
		SelectedRoute:           selectedRoute,
		Temperature:             params.Temperature,
		TopP:                    params.TopP,
		TopK:                    params.TopK,
		MinP:                    params.MinP,
		RepetitionPenalty:       params.RepetitionPenalty,
		Seed:                    params.Seed,
		SamplingSource:          params.SamplingSource,
		PlanningMode:            params.PlanningMode,
	}
	return req
}

func derefInt(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}

func estimateRunPromptTokens(promptText, systemPrompt string) int {
	total := len(promptText) + len(systemPrompt)
	if total == 0 {
		return 0
	}
	return (total + 3) / 4
}

func runRequiresTools(harness, preset string, presetExplicit, anchorsEnabled bool) bool {
	// SD-005 D6: only assert RequiresTools when the request surface clearly
	// exposes tool execution. Native runs do; subprocess harnesses only do so
	// when the caller explicitly enables a tool-bearing surface.
	if harness == "" || harness == "fiz" {
		return true
	}
	if anchorsEnabled {
		return true
	}
	return presetExplicit && preset != ""
}

// flagWasSet reports whether the flag with the given name was explicitly
// provided on the command line. Used to give explicit flag values precedence
// over environment-variable fallbacks (e.g. --cost-cap-usd over
// FIZEAU_COST_CAP_USD).
func flagWasSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = true
		}
	})
	return set
}

func normalizeRunSubcommand(args []string) (bool, []string) {
	if len(args) == 0 {
		return false, args
	}
	boolFlags := map[string]bool{
		"allow-deprecated-model": true,
		"json":                   true,
		"list-models":            true,
		"plan":                   true,
		"version":                true,
	}
	valueFlags := map[string]bool{
		"cost-cap-usd": true,
		"harness":      true,
		"max-power":    true,
		"max-iter":     true,
		"min-power":    true,
		"model":        true,
		"p":            true,
		"policy":       true,
		"preset":       true,
		"provider":     true,
		"reasoning":    true,
		"system":       true,
		"work-dir":     true,
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "run":
			out := append([]string{}, args[:i]...)
			out = append(out, args[i+1:]...)
			return true, out
		case strings.HasPrefix(arg, "-"):
			flagName := strings.TrimLeft(arg, "-")
			if boolFlags[flagName] {
				continue
			}
			if valueFlags[flagName] && i+1 < len(args) {
				i++
			}
		default:
			return false, args
		}
	}

	return false, args
}

// providerSelection records the raw routing intent and the service-visible
// fields that the CLI passes through for logging and request construction.
type providerSelection struct {
	Route            string
	Provider         string
	RequestedModel   string
	ResolvedModel    string
	ReasoningDefault fizeau.Reasoning
	NoStream         bool
}

func resolveRunSelection(cfg *agentConfig.Config, workDir, providerName, requestedModel string, overrides agentConfig.ProviderOverrides) (providerSelection, agentConfig.ProviderConfig, error) {
	if cfg == nil {
		return providerSelection{}, agentConfig.ProviderConfig{}, nil
	}
	if providerName != "" {
		p, pc, resolved, err := cfg.BuildProviderWithOverrides(providerName, overrides)
		if err != nil {
			return providerSelection{}, agentConfig.ProviderConfig{}, err
		}
		_ = p
		effectiveModel := pc.Model
		if effectiveModel == "" {
			effectiveModel = requestedModel
		}
		selection := providerSelection{
			Route:          providerName,
			Provider:       providerName,
			RequestedModel: requestedModel,
			ResolvedModel:  effectiveModel,
		}
		if resolved != nil {
			selection.ReasoningDefault = fizeau.Reasoning(resolved.SurfacePolicy.ReasoningDefault)
			if resolved.ConcreteModel != "" {
				selection.ResolvedModel = resolved.ConcreteModel
			}
		}
		return selection, pc, nil
	}
	if cfg.DefaultBackend != "" && requestedModel == "" {
		counter, err := readAndIncrementRouteCounter(workDir, legacyRouteCounterName(cfg.DefaultBackend))
		if err != nil {
			counter = 0
		}
		p, pc, resolved, err := cfg.ResolveBackend(cfg.DefaultBackend, counter, overrides)
		if err != nil {
			return providerSelection{}, agentConfig.ProviderConfig{}, err
		}
		_ = p
		effectiveModel := pc.Model
		if effectiveModel == "" {
			effectiveModel = requestedModel
		}
		selection := providerSelection{
			Route:          cfg.DefaultBackend,
			RequestedModel: requestedModel,
			ResolvedModel:  effectiveModel,
			NoStream:       true,
		}
		if bc, ok := cfg.GetBackend(cfg.DefaultBackend); ok && len(bc.Providers) > 0 {
			idx := selectBackendProviderIndex(bc.Strategy, counter, len(bc.Providers))
			selection.Provider = bc.Providers[idx]
		}
		if resolved != nil {
			selection.ReasoningDefault = fizeau.Reasoning(resolved.SurfacePolicy.ReasoningDefault)
			if resolved.ConcreteModel != "" {
				selection.ResolvedModel = resolved.ConcreteModel
			}
		}
		return selection, pc, nil
	}
	if requestedModel != "" {
		selection := providerSelection{
			Route:          requestedModel,
			Provider:       "",
			RequestedModel: requestedModel,
			ResolvedModel:  requestedModel,
		}
		return selection, agentConfig.ProviderConfig{}, nil
	}
	return providerSelection{}, agentConfig.ProviderConfig{}, nil
}

func resolveRunReasoning(cfg *agentConfig.Config, model string, defaultReasoning fizeau.Reasoning, flagValue string) (fizeau.Reasoning, error) {
	explicit := strings.TrimSpace(flagValue) != ""
	if !explicit {
		return defaultReasoning, nil
	}
	policy, err := reasoning.ParseString(flagValue)
	if err != nil {
		return "", err
	}
	if policy.Kind == reasoning.KindAuto {
		if defaultReasoning != "" {
			return defaultReasoning, nil
		}
		return fizeau.ReasoningAuto, nil
	}
	if policy.Kind == reasoning.KindTokens || policy.Value == reasoning.ReasoningMax {
		maxTokens := lookupReasoningMaxTokens(cfg, model)
		if maxTokens > 0 {
			if policy.Value == reasoning.ReasoningMax {
				return fizeau.ReasoningTokens(maxTokens), nil
			}
			if policy.Tokens > maxTokens {
				return "", fmt.Errorf("reasoning: token budget %d exceeds maximum %d for model %q", policy.Tokens, maxTokens, model)
			}
		}
	}
	return fizeau.Reasoning(policy.Value), nil
}

func lookupReasoningMaxTokens(cfg *agentConfig.Config, model string) int {
	if model == "" {
		return 0
	}
	catalog, err := cfg.LoadModelCatalog()
	if err != nil || catalog == nil {
		return 0
	}
	if entry, ok := catalog.LookupModel(model); ok {
		return entry.ReasoningMaxTokens
	}
	return 0
}

func selectBackendProviderIndex(strategy string, counter, n int) int {
	if n <= 0 {
		return 0
	}
	if strategy == "round-robin" {
		return counter % n
	}
	return 0
}

func resolvePreset(flagValue string, cfg *agentConfig.Config) (string, error) {
	preset := flagValue
	if preset == "" {
		preset = cfg.Preset
	}
	return prompt.ResolvePresetName(preset)
}

// resolveSkillsDir returns the directory in which to discover SKILL.md
// files for this run, or "" to skip discovery entirely. Precedence:
//
//  1. FIZEAU_SKILLS_DIR env var (when non-empty)
//  2. cfg.Skills.Dir (when non-empty)
//  3. ".fizeau/skills" relative to workDir
//
// A literal "-" at any layer disables discovery — useful for tests or
// for opting out of an inherited project default. The default
// (".fizeau/skills") is silently absent: if it does not exist the
// caller's ScanDir returns an empty catalog with no error.
func resolveSkillsDir(cfg *agentConfig.Config, workDir string) string {
	if env := strings.TrimSpace(os.Getenv("FIZEAU_SKILLS_DIR")); env != "" {
		if env == "-" {
			return ""
		}
		return env
	}
	if cfg != nil {
		if dir := strings.TrimSpace(cfg.Skills.Dir); dir != "" {
			if dir == "-" {
				return ""
			}
			return dir
		}
	}
	return filepath.Join(workDir, ".fizeau", "skills")
}

func buildToolsForPreset(workDir, preset string, bashFilter ...fizeau.BashOutputFilterConfig) []fizeau.Tool {
	filter := fizeau.BashOutputFilterConfig{}
	if len(bashFilter) > 0 {
		filter = bashFilter[0]
	}
	return fizeau.BuiltinToolsForPreset(workDir, preset, filter)
}

func buildToolsForPresetWithAnchors(workDir, preset string, anchors bool, bashFilter ...fizeau.BashOutputFilterConfig) []fizeau.Tool {
	tools := buildToolsForPreset(workDir, preset, bashFilter...)
	if !anchors {
		return tools
	}

	store := fizeau.NewAnchorStore()
	anchored := make([]fizeau.Tool, 0, len(tools)+1)
	for _, tool := range tools {
		if tool != nil && tool.Name() == "read" {
			anchored = append(anchored, fizeau.NewReadTool(workDir, store))
			anchored = append(anchored, fizeau.NewAnchorEditTool(workDir, store))
			continue
		}
		anchored = append(anchored, tool)
	}
	return anchored
}

func buildSystemPromptForRun(preset string, tools []fizeau.Tool, skillCatalog *fizeau.SkillCatalog, contextFiles []prompt.ContextFile, workDir string, anchors bool, appendText string) string {
	sysPrompt := prompt.NewFromPreset(preset).
		WithTools(tools).
		WithSkillCatalog(skillCatalog).
		WithContextFiles(contextFiles).
		WithWorkDir(workDir)

	if anchors {
		sysPrompt.WithSection("Anchor Mode", anchorModeSystemPromptAddendum)
	}
	if appendText != "" {
		sysPrompt.WithAppend(appendText)
	}
	return sysPrompt.Build()
}

func bashOutputFilterConfig(cfg agentConfig.BashOutputFilterConfig) fizeau.BashOutputFilterConfig {
	return fizeau.BashOutputFilterConfig{
		Mode:         cfg.Mode,
		RTKBinary:    cfg.RTKBinary,
		MaxBytes:     cfg.MaxBytes,
		RawOutputDir: cfg.RawOutputDir,
	}
}

func legacyRouteCounterName(backendName string) string {
	return "backend-" + backendName
}

// routeStateFile returns the path to the per-route rotation counter file.
func routeStateFile(workDir, routeName string) string {
	return agentConfig.ProjectRouteStateCounterPath(workDir, routeStateKey(routeName))
}

// routeCounterMu serializes counter reads and writes within a process.
var routeCounterMu sync.Mutex

// readAndIncrementRouteCounter reads the current rotation counter for a route,
// increments it, writes it back, and returns the value that was
// read (i.e. the counter to use for this request).
func readAndIncrementRouteCounter(workDir, routeName string) (int, error) {
	routeCounterMu.Lock()
	defer routeCounterMu.Unlock()

	path := routeStateFile(workDir, routeName)

	// Read existing counter; treat missing file as counter 0.
	var counter int
	if data, err := safefs.ReadFile(path); err == nil {
		if _, err := fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &counter); err != nil {
			counter = 0
		}
	}

	// Write incremented counter.
	next := counter + 1
	if err := safefs.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return counter, err
	}
	if err := safefs.WriteFile(path, []byte(fmt.Sprintf("%d\n", next)), 0o600); err != nil {
		return counter, err
	}

	return counter, nil
}

type ddxPromptEnvelope struct {
	Kind           string          `json:"kind"`
	ID             string          `json:"id"`
	Title          string          `json:"title,omitempty"`
	Prompt         string          `json:"prompt"`
	Inputs         json.RawMessage `json:"inputs,omitempty"`
	ResponseSchema json.RawMessage `json:"response_schema,omitempty"`
	Callback       json.RawMessage `json:"callback,omitempty"`
}

func resolvePrompt(p string) (string, map[string]string, error) {
	var raw string
	if p != "" {
		if strings.HasPrefix(p, "@") {
			data, err := safefs.ReadFile(p[1:])
			if err != nil {
				return "", nil, fmt.Errorf("reading prompt file: %w", err)
			}
			raw = string(data)
		} else {
			raw = p
		}
	} else {
		stat, _ := os.Stdin.Stat()
		if (stat.Mode() & os.ModeCharDevice) == 0 {
			data, err := io.ReadAll(os.Stdin)
			if err != nil {
				return "", nil, fmt.Errorf("reading stdin: %w", err)
			}
			raw = strings.TrimSpace(string(data))
		}
	}

	return parsePromptInput(raw)
}

func parsePromptInput(raw string) (string, map[string]string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", nil, nil
	}

	var probe map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &probe); err != nil {
		return raw, nil, nil
	}
	kindRaw, kindOK := probe["kind"]
	if !kindOK {
		return raw, nil, nil
	}
	if !isPromptEnvelopeProbe(probe) {
		var kindValue any
		if err := json.Unmarshal(kindRaw, &kindValue); err == nil {
			if _, ok := kindValue.(string); !ok && hasPromptEnvelopeFields(probe) {
				return "", nil, fmt.Errorf("invalid prompt envelope: kind must be a string")
			}
		} else if hasPromptEnvelopeFields(probe) {
			return "", nil, fmt.Errorf("invalid prompt envelope: kind must be a string")
		}
		return raw, nil, nil
	}

	var env ddxPromptEnvelope
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		return "", nil, fmt.Errorf("parsing prompt envelope: %w", err)
	}
	if env.Kind == "" || env.ID == "" || env.Prompt == "" {
		return "", nil, fmt.Errorf("invalid prompt envelope: kind, id, and prompt are required")
	}

	metadata := map[string]string{
		"prompt.kind": env.Kind,
		"prompt.id":   env.ID,
	}
	if env.Title != "" {
		metadata["prompt.title"] = env.Title
	}
	if len(env.Inputs) > 0 {
		inputs, err := canonicalPromptJSON(env.Inputs)
		if err != nil {
			return "", nil, fmt.Errorf("normalizing prompt envelope inputs: %w", err)
		}
		metadata["prompt.inputs"] = inputs
	}
	if len(env.ResponseSchema) > 0 {
		schema, err := canonicalPromptJSON(env.ResponseSchema)
		if err != nil {
			return "", nil, fmt.Errorf("normalizing prompt envelope response schema: %w", err)
		}
		metadata["prompt.response_schema"] = schema
	}
	if len(env.Callback) > 0 {
		callback, err := canonicalPromptJSON(env.Callback)
		if err != nil {
			return "", nil, fmt.Errorf("normalizing prompt envelope callback: %w", err)
		}
		metadata["prompt.callback"] = callback
	}

	return env.Prompt, metadata, nil
}

func isPromptEnvelopeProbe(probe map[string]json.RawMessage) bool {
	kindRaw, kindOK := probe["kind"]
	if !kindOK {
		return false
	}

	var kind string
	if err := json.Unmarshal(kindRaw, &kind); err != nil {
		return false
	}
	if kind != "prompt" {
		return false
	}

	_, idOK := probe["id"]
	_, titleOK := probe["title"]
	_, promptOK := probe["prompt"]
	_, inputsOK := probe["inputs"]
	_, responseSchemaOK := probe["response_schema"]
	_, callbackOK := probe["callback"]

	return titleOK || promptOK || idOK || inputsOK || responseSchemaOK || callbackOK
}

func hasPromptEnvelopeFields(probe map[string]json.RawMessage) bool {
	_, titleOK := probe["title"]
	_, promptOK := probe["prompt"]
	_, idOK := probe["id"]
	_, inputsOK := probe["inputs"]
	_, responseSchemaOK := probe["response_schema"]
	_, callbackOK := probe["callback"]

	return titleOK || promptOK || idOK || inputsOK || responseSchemaOK || callbackOK
}

func canonicalPromptJSON(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	normalized, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(normalized), nil
}

func cmdProviders(workDir string, jsonOut bool) int {
	cfg, err := agentConfig.Load(workDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		return 1
	}

	if jsonOut {
		data, _ := json.MarshalIndent(redactedProviders(cfg.Providers), "", "  ")
		fmt.Println(string(data))
		return 0
	}

	defName := cfg.DefaultName()
	fmt.Printf("%-12s %-15s %-40s %-30s %s\n", "NAME", "TYPE", "URL", "MODEL", "STATUS")
	for _, name := range cfg.ProviderNames() {
		pc := cfg.Providers[name]
		status := checkProviderStatus(pc)
		marker := " "
		if name == defName {
			marker = "*"
		}
		url := pc.BaseURL
		if url == "" {
			url = "(api)"
		}
		if len(url) > 38 {
			url = url[:38] + ".."
		}
		modelStr := pc.Model
		if len(modelStr) > 28 {
			modelStr = modelStr[:28] + ".."
		}
		fmt.Printf("%s%-11s %-15s %-40s %-30s %s\n", marker, name, pc.Type, url, modelStr, status)
	}
	return 0
}

type providerListEntry struct {
	Type    string            `json:"type"`
	BaseURL string            `json:"base_url"`
	Model   string            `json:"model"`
	Headers map[string]string `json:"headers,omitempty"`
}

func redactedProviders(providers map[string]agentConfig.ProviderConfig) map[string]providerListEntry {
	redacted := make(map[string]providerListEntry, len(providers))
	for name, pc := range providers {
		entry := providerListEntry{
			Type:    pc.Type,
			BaseURL: pc.BaseURL,
			Model:   pc.Model,
		}
		if len(pc.Headers) > 0 {
			headers := make(map[string]string, len(pc.Headers))
			for key := range pc.Headers {
				headers[key] = "[redacted]"
			}
			entry.Headers = headers
		}
		redacted[name] = entry
	}
	return redacted
}

type cliModelInventoryRow struct {
	Model             string  `json:"model"`
	Harness           string  `json:"harness"`
	Provider          string  `json:"provider"`
	ProviderType      string  `json:"provider_type,omitempty"`
	EndpointName      string  `json:"endpoint_name,omitempty"`
	EndpointBaseURL   string  `json:"endpoint_base_url,omitempty"`
	Endpoint          string  `json:"endpoint,omitempty"`
	ServerInstance    string  `json:"server_instance,omitempty"`
	Power             int     `json:"power"`
	AutoRoutable      bool    `json:"auto_routable"`
	ExactPinOnly      bool    `json:"exact_pin_only"`
	CostInputPerMTok  float64 `json:"cost_input_per_mtok,omitempty"`
	CostOutputPerMTok float64 `json:"cost_output_per_mtok,omitempty"`
	ContextSource     string  `json:"context_source,omitempty"`
	SpeedTokensPerSec float64 `json:"speed_tokens_per_sec,omitempty"`
	SWEBenchVerified  float64 `json:"swe_bench_verified,omitempty"`
	ContextLength     int     `json:"context_length,omitempty"`
	PerfSignal        struct {
		SpeedTokensPerSec float64 `json:"speed_tokens_per_sec,omitempty"`
		SWEBenchVerified  float64 `json:"swe_bench_verified,omitempty"`
	} `json:"perf_signal,omitempty"`
	Utilization  fizeau.RouteUtilizationState `json:"utilization,omitempty"`
	Available    bool                         `json:"available"`
	RankPosition int                          `json:"rank_position"`
}

func cmdListModels(workDir string, jsonOutput bool, filter fizeau.ModelFilter) int {
	cfg, err := agentConfig.Load(workDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		return 1
	}

	svc, err := fizeau.New(fizeau.ServiceOptions{ServiceConfig: agentConfig.NewServiceConfig(cfg, workDir)})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		return 1
	}

	models, err := svc.ListModels(context.Background(), filter)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		return 1
	}
	if filter.Provider == "" && filter.Harness == "" {
		harnesses, err := svc.ListHarnesses(context.Background())
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %s\n", err)
			return 1
		}
		for _, harness := range harnesses {
			if harness.Type != "subprocess" {
				continue
			}
			harnessModels, err := svc.ListModels(context.Background(), fizeau.ModelFilter{Harness: harness.Name})
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %s\n", err)
				return 1
			}
			models = append(models, harnessModels...)
		}
	}

	rows := modelInventoryRows(models)
	if jsonOutput {
		data, err := json.MarshalIndent(rows, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %s\n", err)
			return 1
		}
		fmt.Println(string(data))
		return 0
	}

	printModelInventory(rows)
	return 0
}

func modelInventoryRows(models []fizeau.ModelInfo) []cliModelInventoryRow {
	rows := make([]cliModelInventoryRow, 0, len(models))
	for _, model := range models {
		row := cliModelInventoryRow{
			Model:             model.ID,
			Harness:           model.Harness,
			Provider:          model.Provider,
			ProviderType:      model.ProviderType,
			EndpointName:      model.EndpointName,
			EndpointBaseURL:   model.EndpointBaseURL,
			Endpoint:          modelInventoryEndpoint(model),
			ServerInstance:    model.ServerInstance,
			Power:             model.Power,
			AutoRoutable:      model.AutoRoutable,
			ExactPinOnly:      model.ExactPinOnly,
			CostInputPerMTok:  model.Cost.InputPerMTok,
			CostOutputPerMTok: model.Cost.OutputPerMTok,
			ContextSource:     model.ContextSource,
			SpeedTokensPerSec: model.PerfSignal.SpeedTokensPerSec,
			SWEBenchVerified:  model.PerfSignal.SWEBenchVerified,
			ContextLength:     model.ContextLength,
			PerfSignal: struct {
				SpeedTokensPerSec float64 `json:"speed_tokens_per_sec,omitempty"`
				SWEBenchVerified  float64 `json:"swe_bench_verified,omitempty"`
			}{
				SpeedTokensPerSec: model.PerfSignal.SpeedTokensPerSec,
				SWEBenchVerified:  model.PerfSignal.SWEBenchVerified,
			},
			Utilization:  model.Utilization,
			Available:    model.Available,
			RankPosition: model.RankPosition,
		}
		rows = append(rows, row)
	}
	return rows
}

func printModelInventory(rows []cliModelInventoryRow) {
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "MODEL\tHARNESS\tPROVIDER\tENDPOINT\tPOWER\tCOST/M\tPERF\tCONTEXT\tROUTE")
	for _, row := range rows {
		fmt.Fprintf(
			tw,
			"%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			row.Model,
			row.Harness,
			modelInventoryProvider(row),
			emptyDash(row.Endpoint),
			formatInventoryPower(row.Power),
			formatInventoryCost(row.CostInputPerMTok, row.CostOutputPerMTok),
			formatInventoryPerf(row.SpeedTokensPerSec, row.SWEBenchVerified),
			formatInventoryContext(row.ContextLength),
			formatInventoryRoute(row),
		)
	}
	_ = tw.Flush()
}

func modelInventoryProvider(row cliModelInventoryRow) string {
	if row.ProviderType == "" || row.ProviderType == row.Provider {
		return row.Provider
	}
	return row.Provider + "/" + row.ProviderType
}

func modelInventoryEndpoint(model fizeau.ModelInfo) string {
	name := strings.TrimSpace(model.EndpointName)
	host := ""
	if model.EndpointBaseURL != "" {
		if u, err := url.Parse(model.EndpointBaseURL); err == nil {
			host = u.Host
		}
	}
	switch {
	case name != "" && host != "":
		return name + "@" + host
	case name != "":
		return name
	case host != "":
		return host
	default:
		return ""
	}
}

func formatInventoryPower(power int) string {
	if power <= 0 {
		return "-"
	}
	return fmt.Sprintf("%d", power)
}

func formatInventoryCost(input, output float64) string {
	if input == 0 && output == 0 {
		return "-"
	}
	return fmt.Sprintf("%.2f/%.2f", input, output)
}

func formatInventoryPerf(speed, swe float64) string {
	switch {
	case speed > 0:
		return fmt.Sprintf("%.1f tok/s", speed)
	case swe > 0:
		return fmt.Sprintf("SWE %.1f", swe)
	default:
		return "-"
	}
}

func formatInventoryContext(contextLength int) string {
	if contextLength <= 0 {
		return "-"
	}
	return fmt.Sprintf("%d", contextLength)
}

func formatInventoryRoute(row cliModelInventoryRow) string {
	if !row.Available {
		return "unavailable"
	}
	if row.ExactPinOnly {
		return "exact-pin"
	}
	if row.AutoRoutable {
		return "auto"
	}
	return "pin-only"
}

func emptyDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func cmdCheck(workDir, providerName string, args []string) int {
	cfg, err := agentConfig.Load(workDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		return 1
	}

	checkAll := len(args) > 0 && args[0] == "--all"

	if checkAll {
		allOk := true
		for _, name := range cfg.ProviderNames() {
			pc := cfg.Providers[name]
			status := checkProviderStatus(pc)
			ok := strings.Contains(status, "connected") || strings.Contains(status, "configured")
			marker := "ok"
			if !ok {
				marker = "FAIL"
				allOk = false
			}
			fmt.Printf("[%s] %s: %s\n", marker, name, status)
		}
		if !allOk {
			return 1
		}
		return 0
	}

	name := providerName
	if name == "" {
		name = cfg.DefaultName()
	}
	pc, ok := cfg.GetProvider(name)
	if !ok {
		fmt.Fprintf(os.Stderr, "error: unknown provider %q\n", name)
		return 1
	}

	fmt.Printf("Provider: %s (%s)\n", name, pc.Type)
	if pc.BaseURL != "" {
		fmt.Printf("URL:      %s\n", pc.BaseURL)
	}
	status := checkProviderStatus(pc)
	fmt.Printf("Status:   %s\n", status)
	if pc.Model != "" {
		fmt.Printf("Model:    %s\n", pc.Model)
	}

	if strings.Contains(status, "unreachable") || strings.Contains(status, "no API") {
		return 1
	}
	return 0
}

func cmdLog(workDir string, args []string) int {
	cfg, err := agentConfig.Load(workDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		return 1
	}
	svc, err := newSessionProjectionService(workDir, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		return 1
	}
	ctx := context.Background()

	if len(args) > 0 {
		if err := svc.WriteSessionLog(ctx, args[0], os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "error: %s\n", err)
			return 1
		}
		return 0
	}

	entries, err := svc.ListSessionLogs(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		return 1
	}
	for _, entry := range entries {
		if !entry.ModTime.IsZero() {
			fmt.Printf("%s  %s  %d bytes\n", entry.SessionID, entry.ModTime.Format("2006-01-02 15:04"), entry.Size)
		} else {
			fmt.Println(entry.SessionID)
		}
	}
	return 0
}

func cmdReplay(workDir string, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: fiz replay <session-id>")
		return 2
	}
	cfg, err := agentConfig.Load(workDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		return 1
	}
	svc, err := newSessionProjectionService(workDir, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		return 1
	}
	if err := svc.ReplaySession(context.Background(), args[0], os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		return 1
	}
	return 0
}

func cmdUsage(workDir string, args []string) int {
	fs := flag.NewFlagSet("usage", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	since := fs.String("since", "", "Time window: today, 7d, 30d, YYYY-MM-DD, or YYYY-MM-DD..YYYY-MM-DD")
	jsonOutput := fs.Bool("json", false, "Output JSON")
	csvOutput := fs.Bool("csv", false, "Output CSV")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *jsonOutput && *csvOutput {
		fmt.Fprintln(os.Stderr, "error: choose only one of --json or --csv")
		return 2
	}

	if err := fizeau.ValidateUsageSince(*since); err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		return 2
	}

	cfg, err := agentConfig.Load(workDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		return 2
	}

	svc, err := newSessionProjectionService(workDir, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		return 1
	}

	report, err := svc.UsageReport(context.Background(), fizeau.UsageReportOptions{
		Since: *since,
		Now:   time.Now().UTC(),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		return 1
	}

	if *jsonOutput {
		data, _ := json.MarshalIndent(report, "", "  ")
		fmt.Println(string(data))
		return 0
	}
	if *csvOutput {
		printUsageCSV(report)
		return 0
	}

	printUsageReport(report, *since)
	return 0
}

// newSessionProjectionService constructs a FizeauService for the historical
// session-log subcommands (log/replay/usage). The session-log directory is
// resolved from cfg and handed to the service via ServiceOptions so that the
// CLI never reads the on-disk session-log layout directly.
func newSessionProjectionService(workDir string, cfg *agentConfig.Config) (fizeau.FizeauService, error) {
	logDir := sessionLogDir(workDir, cfg)
	return fizeau.New(fizeau.ServiceOptions{
		ServiceConfig: agentConfig.NewServiceConfig(cfg, workDir),
		SessionLogDir: logDir,
	})
}

func sessionLogDir(workDir string, cfg *agentConfig.Config) string {
	if cfg == nil || cfg.SessionLogDir == "" {
		return filepath.Join(workDir, agentConfig.DefaultSessionLogDir())
	}
	if filepath.IsAbs(cfg.SessionLogDir) {
		return cfg.SessionLogDir
	}
	return filepath.Join(workDir, cfg.SessionLogDir)
}

func printUsageReport(report *fizeau.UsageReport, since string) {
	if report.Window != nil {
		fmt.Printf("Window: %s .. %s\n", formatUsageWindowBound(report.Window.Start), formatUsageWindowBound(report.Window.End))
	} else if since != "" {
		fmt.Printf("Window: %s\n", since)
	}

	fmt.Printf("%-16s %-24s %8s %10s %10s %10s %14s %10s %10s %8s %10s %12s %12s\n",
		"PROVIDER", "MODEL", "SESSIONS", "INPUT", "OUTPUT", "TOTAL", "COST", "UNKNOWN", "CACHE-HIT%", "SUCCESS%", "COST/OK", "IN tok/s", "OUT tok/s")
	for _, row := range report.Rows {
		printUsageRow(row)
	}
	total := report.Totals
	total.Provider = "TOTAL"
	printUsageRow(total)
}

func formatUsageWindowBound(ts time.Time) string {
	if ts.IsZero() {
		return "(open)"
	}
	return ts.UTC().Format("2006-01-02")
}

func printUsageRow(row fizeau.UsageReportRow) {
	cost := "unknown"
	if row.UnknownCostSessions == 0 && row.KnownCostUSD != nil {
		cost = fmt.Sprintf("$%.4f", *row.KnownCostUSD)
	}
	cacheHit := "-"
	if row.CacheReadTokens > 0 || row.InputTokens > 0 {
		cacheHit = fmt.Sprintf("%.1f%%", row.CacheHitRate()*100)
	}
	successRate := "-"
	if row.Sessions > 0 {
		successRate = fmt.Sprintf("%.1f%%", row.SuccessRate()*100)
	}
	costPerOK := "-"
	if cps := row.CostPerSuccess(); cps != nil {
		costPerOK = fmt.Sprintf("$%.4f", *cps)
	}
	fmt.Printf("%-16s %-24s %8d %10d %10d %10d %14s %10d %10s %8s %10s %12.1f %12.1f\n",
		row.Provider,
		row.Model,
		row.Sessions,
		row.InputTokens,
		row.OutputTokens,
		row.TotalTokens,
		cost,
		row.UnknownCostSessions,
		cacheHit,
		successRate,
		costPerOK,
		row.InputTokensPerSecond(),
		row.OutputTokensPerSecond(),
	)
}

func printUsageCSV(report *fizeau.UsageReport) {
	fmt.Println("provider,model,sessions,success_sessions,failed_sessions,input_tokens,output_tokens,total_tokens,duration_ms,known_cost_usd,unknown_cost_sessions,success_rate,cost_per_success,input_tokens_per_second,output_tokens_per_second,cache_read_tokens,cache_write_tokens")
	for _, row := range report.Rows {
		printUsageCSVRow(row)
	}
	total := report.Totals
	total.Provider = "TOTAL"
	printUsageCSVRow(total)
}

func printUsageCSVRow(row fizeau.UsageReportRow) {
	cost := ""
	if row.UnknownCostSessions == 0 && row.KnownCostUSD != nil {
		cost = fmt.Sprintf("%.4f", *row.KnownCostUSD)
	}
	successRate := fmt.Sprintf("%.4f", row.SuccessRate())
	costPerOK := ""
	if cps := row.CostPerSuccess(); cps != nil {
		costPerOK = fmt.Sprintf("%.4f", *cps)
	}
	fmt.Printf("%s,%s,%d,%d,%d,%d,%d,%d,%d,%s,%d,%s,%s,%.1f,%.1f,%d,%d\n",
		csvEscape(row.Provider),
		csvEscape(row.Model),
		row.Sessions,
		row.SuccessSessions,
		row.FailedSessions,
		row.InputTokens,
		row.OutputTokens,
		row.TotalTokens,
		row.DurationMs,
		cost,
		row.UnknownCostSessions,
		successRate,
		costPerOK,
		row.InputTokensPerSecond(),
		row.OutputTokensPerSecond(),
		row.CacheReadTokens,
		row.CacheWriteTokens,
	)
}

func csvEscape(value string) string {
	if strings.ContainsAny(value, "\",\n\r") {
		return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
	}
	return value
}

func checkProviderStatus(pc agentConfig.ProviderConfig) string {
	probe := probeProviderModels(pc, routingProbeTimeout(nil))
	if pc.Type == "anthropic" {
		if probe.err != nil {
			return probe.err.Error()
		}
		return "api key configured"
	}
	if probe.err != nil {
		return probe.err.Error()
	}
	return fmt.Sprintf("http-connected (%d models listed)", len(probe.models))
}

func cmdImport(workDir string, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: fiz import <pi|opencode> [--diff] [--merge] [--project]")
		return 2
	}

	source := args[0]
	if source != "pi" && source != "opencode" {
		fmt.Fprintf(os.Stderr, "error: unknown source %q (use 'pi' or 'opencode')\n", source)
		return 2
	}

	// Parse flags
	var diffOnly, merge, project bool
	for _, arg := range args[1:] {
		switch arg {
		case "--diff":
			diffOnly = true
		case "--merge":
			merge = true
		case "--project":
			project = true
		}
	}

	// Determine output path
	var configPath string
	if project {
		fmt.Fprintf(os.Stderr, "%s: warning: writing API keys to project config (.fizeau/config.yaml)\n", productinfo.BinaryName)
		fmt.Fprintf(os.Stderr, "%s: ensure .fizeau/config.yaml is in .gitignore before committing\n", productinfo.BinaryName)
		fmt.Fprint(os.Stderr, "Proceed? [y/N] ")
		var response string
		if _, err := fmt.Scanln(&response); err != nil {
			response = ""
		}
		if strings.ToLower(response) != "y" {
			return 0
		}
		configPath = agentConfig.ProjectConfigPath(workDir)
		if err := safefs.MkdirAll(filepath.Dir(configPath), 0o750); err != nil {
			fmt.Fprintf(os.Stderr, "error: cannot create .fizeau directory: %v\n", err)
			return 1
		}
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: cannot determine home directory: %v\n", err)
			return 1
		}
		configPath = agentConfig.GlobalConfigPath(home)
		if err := safefs.MkdirAll(filepath.Dir(configPath), 0o750); err != nil {
			fmt.Fprintf(os.Stderr, "error: cannot create config directory: %v\n", err)
			return 1
		}
	}

	// Import based on source
	if source == "pi" {
		return importPi(configPath, diffOnly, merge)
	} else {
		return importOpenCode(configPath, diffOnly, merge)
	}
}

func importPi(configPath string, diffOnly, merge bool) int {
	piDir := picompat.DefaultPiDir()
	if piDir == "" {
		fmt.Fprintln(os.Stderr, "error: cannot determine pi config directory")
		return 1
	}

	if !picompat.CheckExists() {
		fmt.Fprintf(os.Stderr, "error: pi config not found at %s\n", piDir)
		return 1
	}

	// Translate pi config
	result, err := picompat.Translate(piDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		return 1
	}

	// Show diff if requested
	if diffOnly {
		return showDiff("pi", result)
	}

	// Compute source hash
	sourceHash, _ := picompat.ComputeSourceHash(piDir)

	// Load existing config
	cfg, err := agentConfig.Load(filepath.Dir(configPath))
	if err != nil {
		cfg = &agentConfig.Config{}
	}

	// Merge or replace
	if merge {
		for name, pc := range result.Providers {
			if existing, exists := cfg.Providers[name]; exists {
				// Update api_key only for existing providers
				existing.APIKey = pc.APIKey
				cfg.Providers[name] = existing
				fmt.Printf("updated: %s\n", name)
			} else {
				cfg.Providers[name] = pc
				fmt.Printf("added: %s\n", name)
			}
		}
	} else {
		if cfg.Providers == nil {
			cfg.Providers = make(map[string]agentConfig.ProviderConfig)
		}
		for name, pc := range result.Providers {
			cfg.Providers[name] = pc
			fmt.Printf("imported: %s\n", name)
		}
	}

	// Set default
	if result.Default != "" {
		cfg.Default = result.Default
	}

	// Set import metadata
	cfg.ImportedFrom = &agentConfig.ImportMetadata{
		Source:     "pi",
		Timestamp:  time.Now().Format(time.RFC3339),
		SourceHash: sourceHash,
	}

	// Write config with secure permissions
	if err := writeConfig(configPath, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		return 1
	}

	// Show warnings
	for _, w := range result.Warnings {
		fmt.Fprintf(os.Stderr, "%s: warning: %s\n", productinfo.BinaryName, w)
	}

	fmt.Printf("imported to %s\n", configPath)
	return 0
}

func importOpenCode(configPath string, diffOnly, merge bool) int {
	opencodeDir := occompat.DefaultOpenCodeDir()
	if opencodeDir == "" {
		fmt.Fprintln(os.Stderr, "error: cannot determine opencode config directory")
		return 1
	}

	if !occompat.CheckExists() {
		fmt.Fprintf(os.Stderr, "error: opencode config not found at %s\n", opencodeDir)
		return 1
	}

	// Load auth
	auth, err := occompat.LoadAuth(opencodeDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		return 1
	}

	// Get the API key from auth (use first entry)
	var authKey string
	for _, entry := range auth {
		authKey = entry.Key
		break
	}

	// Translate
	result := occompat.Translate(opencodeDir, authKey)

	// Show diff if requested
	if diffOnly {
		return showOpenCodeDiff(result)
	}

	// Compute source hash
	sourceHash, _ := occompat.ComputeSourceHash(opencodeDir)

	// Load existing config
	cfg, err := agentConfig.Load(filepath.Dir(configPath))
	if err != nil {
		cfg = &agentConfig.Config{}
	}

	// Merge or replace
	name := "opencode"
	if merge {
		if cfg.Providers == nil {
			cfg.Providers = make(map[string]agentConfig.ProviderConfig)
		}
		if existing, exists := cfg.Providers[name]; exists {
			existing.APIKey = result.Provider.APIKey
			cfg.Providers[name] = existing
			fmt.Printf("updated: %s\n", name)
		} else {
			cfg.Providers[name] = result.Provider
			fmt.Printf("added: %s\n", name)
		}
	} else {
		if cfg.Providers == nil {
			cfg.Providers = make(map[string]agentConfig.ProviderConfig)
		}
		cfg.Providers[name] = result.Provider
		fmt.Printf("imported: %s\n", name)
	}

	// Set import metadata
	cfg.ImportedFrom = &agentConfig.ImportMetadata{
		Source:     "opencode",
		Timestamp:  time.Now().Format(time.RFC3339),
		SourceHash: sourceHash,
	}

	// Write config with secure permissions
	if err := writeConfig(configPath, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		return 1
	}

	// Show warnings
	for _, w := range result.Warnings {
		fmt.Fprintf(os.Stderr, "%s: warning: %s\n", productinfo.BinaryName, w)
	}

	fmt.Printf("imported to %s\n", configPath)
	return 0
}

func showDiff(source string, result *picompat.TranslationResult) int {
	fmt.Printf("%s: %s config -- what would be imported:\n\n", productinfo.BinaryName, source)
	for name, pc := range result.Providers {
		redactedKey := redactKey(pc.APIKey)
		fmt.Printf("[%s]\n", name)
		fmt.Printf("  type:    %s\n", pc.Type)
		if pc.BaseURL != "" {
			fmt.Printf("  url:     %s\n", pc.BaseURL)
		}
		if redactedKey != "" {
			fmt.Printf("  api_key: %s\n", redactedKey)
		}
		if pc.Model != "" {
			fmt.Printf("  model:   %s\n", pc.Model)
		}
		fmt.Println()
	}
	if result.Default != "" {
		fmt.Printf("default: %s\n", result.Default)
	}
	if len(result.Warnings) > 0 {
		fmt.Println("\nWarnings:")
		for _, w := range result.Warnings {
			fmt.Printf("  - %s\n", w)
		}
	}
	return 0
}

func showOpenCodeDiff(result *occompat.TranslationResult) int {
	fmt.Printf("%s: opencode config -- what would be imported:\n", productinfo.BinaryName)
	pc := result.Provider
	redactedKey := redactKey(pc.APIKey)
	fmt.Println("[opencode]")
	fmt.Printf("  type:    %s\n", pc.Type)
	if pc.BaseURL != "" {
		fmt.Printf("  url:     %s\n", pc.BaseURL)
	}
	if redactedKey != "" {
		fmt.Printf("  api_key: %s\n", redactedKey)
	}
	if len(pc.Headers) > 0 {
		fmt.Println("  headers:")
		for k, v := range pc.Headers {
			fmt.Printf("    %s: %s\n", k, v)
		}
	}
	if len(result.Warnings) > 0 {
		fmt.Println("\nWarnings:")
		for _, w := range result.Warnings {
			fmt.Printf("  - %s\n", w)
		}
	}
	return 0
}

func redactKey(key string) string {
	if key == "" || len(key) < 10 {
		return key
	}
	if len(key) <= 10 {
		return key
	}
	return key[:6] + "..." + key[len(key)-4:]
}

func writeConfig(path string, cfg *agentConfig.Config) error {
	data, err := agentConfig.Save(cfg)
	if err != nil {
		return err
	}
	return safefs.WriteFile(path, data, 0o600)
}

// checkZeroConfigDiscovery emits a notice if no config exists but pi/opencode configs do.
func checkZeroConfigDiscovery(cfg *agentConfig.Config) {
	// Check if any providers are configured
	if len(cfg.Providers) > 0 {
		return
	}

	// Check for common env vars
	if os.Getenv("ANTHROPIC_API_KEY") != "" ||
		os.Getenv("OPENAI_API_KEY") != "" ||
		os.Getenv("OPENROUTER_API_KEY") != "" {
		return
	}

	// Check for pi config
	if picompat.CheckExists() {
		piDir := picompat.DefaultPiDir()
		fmt.Fprintf(os.Stderr, "%s: no providers configured. Found pi config at %s — run '%s import pi' to import.\n", productinfo.BinaryName, piDir, productinfo.BinaryName)
		return
	}

	// Check for opencode config
	if occompat.CheckExists() {
		opencodeDir := occompat.DefaultOpenCodeDir()
		fmt.Fprintf(os.Stderr, "%s: no providers configured. Found opencode config at %s — run '%s import opencode' to import.\n", productinfo.BinaryName, opencodeDir, productinfo.BinaryName)
		return
	}
}

// checkDrift emits a notice if the source config has changed since import.
func checkDrift(cfg *agentConfig.Config, workDir string) {
	if cfg.ImportedFrom == nil || cfg.ImportedFrom.Source == "" {
		return
	}

	// Check daily debounce
	if !shouldCheckDrift(cfg.ImportedFrom.Source) {
		return
	}

	// Compute current hash
	var currentHash string
	var err error

	switch cfg.ImportedFrom.Source {
	case "pi":
		currentHash, err = picompat.ComputeSourceHash(picompat.DefaultPiDir())
	case "opencode":
		currentHash, err = occompat.ComputeSourceHash(occompat.DefaultOpenCodeDir())
	}

	if err != nil {
		return // Can't check, just skip
	}

	if currentHash != cfg.ImportedFrom.SourceHash {
		fmt.Fprintf(os.Stderr, "%s: %s config changed since import — run '%s import %s --diff' to review\n",
			productinfo.BinaryName, cfg.ImportedFrom.Source, productinfo.BinaryName, cfg.ImportedFrom.Source)
	}
}

// shouldCheckDrift checks if we should perform a drift check (once per day).
func shouldCheckDrift(source string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return true
	}

	checkFile := filepath.Join(home, ".config", agentConfig.GlobalConfigDirName(), ".import-check-"+source)

	// Check if file exists and is recent (within 24 hours)
	info, err := os.Stat(checkFile)
	if err == nil {
		// File exists, check age
		age := time.Since(info.ModTime())
		if age < 24*time.Hour {
			return false
		}
	}

	// Update check file
	_ = safefs.WriteFile(checkFile, []byte(time.Now().Format(time.RFC3339)), 0o600)
	return true
}

// cmdVersion shows version with update availability check.
func cmdVersion(args []string) int {
	checkOnly := false
	jsonFlag := false
	for _, arg := range args {
		switch arg {
		case "--check-only", "-c":
			checkOnly = true
		case "--json":
			jsonFlag = true
		}
	}

	if jsonFlag {
		bi := buildinfo.Read(Version, GitCommit, BuildTime)
		type versionJSON struct {
			Version string `json:"version"`
			Commit  string `json:"commit"`
			Dirty   bool   `json:"dirty"`
			Built   string `json:"built"`
		}
		out, err := json.Marshal(versionJSON{
			Version: bi.Version,
			Commit:  bi.Commit,
			Dirty:   bi.Dirty,
			Built:   bi.Built,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Println(string(out))
		return 0
	}

	fmt.Printf("%s %s (commit %s, built %s)\n", productinfo.BinaryName, Version, GitCommit, BuildTime)

	// Skip update check for dev builds or if requested
	if Version == "dev" || checkOnly {
		return 0
	}

	// Check for updates
	home, err := os.UserHomeDir()
	if err != nil {
		return 0 // Can't determine home dir, skip update check
	}
	cacheFile := filepath.Join(home, ".cache", "fizeau", "latest-version.json")

	release, err := GetLatestRelease(githubRepo, cacheFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  (update check failed: %v)\n", err)
		return 0
	}

	currentVer, err := ParseSemVer(Version)
	if err != nil {
		return 0 // Invalid version format, skip comparison
	}

	latestVer, err := ParseSemVer(release.TagName)
	if err != nil {
		return 0 // Invalid latest version, skip comparison
	}

	if currentVer.Less(latestVer) {
		fmt.Printf("  Update available: %s\n", release.TagName)
	} else {
		fmt.Println("  (latest)")
	}

	return 0
}

// cmdUpdate checks for and performs updates.
func cmdUpdate(args []string) int {
	checkOnly := false
	force := false
	for _, arg := range args {
		switch arg {
		case "--check-only", "-c":
			checkOnly = true
		case "--force", "-f":
			force = true
		}
	}

	// Get current binary path
	binaryPath, err := FindBinaryPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot determine home directory: %v\n", err)
		return 1
	}
	cacheFile := filepath.Join(home, ".cache", "fizeau", "latest-version.json")

	// Get latest release
	release, err := GetLatestRelease(githubRepo, cacheFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	currentVer, err := ParseSemVer(Version)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: invalid current version format: %s\n", Version)
		return 1
	}

	latestVer, err := ParseSemVer(release.TagName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: invalid latest version format: %s\n", release.TagName)
		return 1
	}

	fmt.Printf("Current: %s\n", Version)
	fmt.Printf("Latest:  %s\n", release.TagName)

	if !currentVer.Less(latestVer) {
		fmt.Println("\nYou are up to date!")
		return 0
	}

	if checkOnly {
		fmt.Println("\nUpdate available. Run 'fiz update' to upgrade.")
		return 1 // Exit code 1 indicates outdated
	}

	// Show changelog summary (first few lines)
	fmt.Printf("\nRelease notes:\n")
	lines := strings.Split(release.Body, "\n")
	for i, line := range lines {
		if i >= 5 || (i > 0 && strings.HasPrefix(line, "##")) {
			break
		}
		if line != "" {
			fmt.Printf("  %s\n", line)
		}
	}

	// Prompt user
	if !force {
		fmt.Print("\nUpdate to " + release.TagName + "? [y/N] ")

		buf := make([]byte, 1)
		if _, err := os.Stdin.Read(buf); err != nil {
			fmt.Println("\nCancelled.")
			return 0
		}

		response := strings.ToLower(strings.TrimSpace(string(buf)))
		if response != "y" && response != "yes" {
			fmt.Println("Cancelled. Run 'fiz update --force' to skip prompt.")
			return 0
		}
	}

	// Download new binary
	tmpPath, err := DownloadBinary(release.TagName, os.Stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nerror: %v\n", err)
		return 1
	}
	defer os.Remove(tmpPath) // Clean up temp file

	// Replace old binary
	if err := ReplaceBinary(binaryPath, tmpPath, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	fmt.Printf("\nSuccessfully updated to %s!\n", release.TagName)
	return 0
}
