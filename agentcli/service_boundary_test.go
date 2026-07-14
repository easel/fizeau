package agentcli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const modelRoutesParserDeprecationCycleEnded = true

// TestCLIRoutingProviderHasNoCoreProviderImpl asserts that
// agentcli/routing_provider.go contains no type that implements the
// agent core Provider surface (Chat / ChatStream methods). After
// ADR-005 step 3 the CLI's per-Chat failover wrapper was deleted; only
// route-status display helpers remain in that file.
func TestCLIRoutingProviderHasNoCoreProviderImpl(t *testing.T) {
	root := repoRootForBoundaryTest(t)
	path := filepath.Join(root, "agentcli", "routing_provider.go")
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	for _, decl := range file.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Recv == nil || len(fd.Recv.List) == 0 {
			continue
		}
		switch fd.Name.Name {
		case "Chat", "ChatStream":
			t.Fatalf("agentcli/routing_provider.go must not define a Chat/ChatStream method (ADR-005 removed the per-Chat failover wrapper); found method %q", fd.Name.Name)
		}
	}
	// Also reject the `routeProvider` type and `newRouteProvider`
	// constructor by name — they encode the same intent.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	src := string(data)
	for _, banned := range []string{"type routeProvider struct", "func newRouteProvider("} {
		if strings.Contains(src, banned) {
			t.Fatalf("agentcli/routing_provider.go must not contain %q (ADR-005 step 3)", banned)
		}
	}
}

// TestNoRoutePlansParserAfterDeprecation asserts the deprecated route-table
// parser is gone from internal/config.
func TestNoRoutePlansParserAfterDeprecation(t *testing.T) {
	if !modelRoutesParserDeprecationCycleEnded {
		t.Skip("route-table deprecation cycle still in effect")
	}
	root := repoRootForBoundaryTest(t)
	path := filepath.Join(root, "internal", "config", "legacy_model_routes.go")
	if _, err := os.Stat(path); err == nil {
		t.Fatalf("internal/config/legacy_model_routes.go must be deleted after the deprecation cycle; file still present at %s", path)
	}
}

func TestCLIServiceContractUsesTypedEventDecoder(t *testing.T) {
	root := repoRootForBoundaryTest(t)
	data, err := os.ReadFile(filepath.Join(root, "agentcli", "run.go"))
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	src := string(data)
	if !strings.Contains(src, "fizeau.DecodeServiceEvent(ev)") {
		t.Fatal("CLI execute path must consume typed service events via fizeau.DecodeServiceEvent")
	}
	if strings.Contains(src, "json.Unmarshal(ev.Data") {
		t.Fatal("CLI must not redefine private ServiceEvent payload shapes by unmarshalling ev.Data directly")
	}
}

// @covers US-006-AC3
func TestCLIMainDoesNotImportOrCallInternalCoreRun(t *testing.T) {
	root := repoRootForBoundaryTest(t)
	data, err := os.ReadFile(filepath.Join(root, "cmd", "fiz", "main.go"))
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	src := string(data)
	if strings.Contains(src, "internal/core") {
		t.Fatal("cmd/fiz/main.go must not import internal/core; execution belongs behind the service boundary")
	}
	if strings.Contains(src, "agentcore.Run(") {
		t.Fatal("cmd/fiz/main.go must not call agentcore.Run directly")
	}
}

// approvedProductionInternalImports is the strict allowlist of internal
// packages that production agentcli files (non-test .go files) may import.
// These are packages with no public replacement on the agent root surface;
// every other internal package must be reached through the public agent
// API. Adding to this list is a deliberate boundary widening — first prove
// there is no public re-export that would do.
var approvedProductionInternalImports = []string{
	"github.com/easel/fizeau/internal/buildinfo",
	"github.com/easel/fizeau/internal/config",
	"github.com/easel/fizeau/internal/corpus",
	"github.com/easel/fizeau/internal/discoverycache",
	"github.com/easel/fizeau/internal/modelcatalog",
	"github.com/easel/fizeau/internal/modelregistry",
	"github.com/easel/fizeau/internal/observations",
	"github.com/easel/fizeau/internal/productinfo",
	"github.com/easel/fizeau/internal/prompt",
	"github.com/easel/fizeau/internal/reasoning",
	"github.com/easel/fizeau/internal/runtimesignals",
	"github.com/easel/fizeau/internal/safefs",
	"github.com/easel/fizeau/internal/sampling",
}

// forbiddenCLIInternalImports lists internal packages that must NEVER be
// imported by any agentcli .go file (production or test). Routing,
// per-provider drivers, harness wiring, the agent core loop, the
// session/tool/compaction subsystems — all of those belong behind the
// service boundary and can only be reached through the public agent API
// (for tests, through the public surface or via service Execute events).
var forbiddenCLIInternalImports = []string{
	"github.com/easel/fizeau/internal/core",
	"github.com/easel/fizeau/internal/provider",
	"github.com/easel/fizeau/internal/session",
	"github.com/easel/fizeau/internal/tool",
	"github.com/easel/fizeau/internal/compaction",
	"github.com/easel/fizeau/internal/harnesses",
	"github.com/easel/fizeau/internal/routing",
}

// forbiddenCLISymbols rejects callers that re-grow the boundary by
// referencing core execution symbols even via aliases or unusual import
// paths. The list is small and targeted — each entry is a symbol that has
// a public replacement and whose presence in a cmd/fiz file means the
// boundary is leaking again.
var forbiddenCLISymbols = []string{
	"agentcore.Run(",
	"agentcore.NewLoop(",
	"compaction.NewCompactor(",
	"tool.BuiltinToolsForPreset(",
	"tool.BashOutputFilterConfig{",
	"session.ReadEvents(",
	"session.NewLogger(",
	"session.NewEvent(",
	"oaiProvider.DiscoverModels(",
	"oaiProvider.RankModels(",
	"oaiProvider.NormalizeModelID(",
}

// TestCLIInternalImportBoundaryAllowlist enforces the strict production
// allowlist: production agentcli files may only import internal packages
// from approvedProductionInternalImports, and may never import packages
// from forbiddenCLIInternalImports.
func TestCLIInternalImportBoundaryAllowlist(t *testing.T) {
	root := repoRootForBoundaryTest(t)
	entries, err := filepath.Glob(filepath.Join(root, "agentcli", "*.go"))
	if err != nil {
		t.Fatalf("glob cmd files: %v", err)
	}
	for _, path := range entries {
		base := filepath.Base(path)
		if base == "service_boundary_test.go" {
			continue
		}
		if strings.HasSuffix(base, "_test.go") {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		src := string(data)
		if !strings.Contains(src, "/internal/") {
			continue
		}
		for _, line := range strings.Split(src, "\n") {
			if !strings.Contains(line, "github.com/easel/fizeau/internal/") {
				continue
			}
			ok := false
			for _, prefix := range approvedProductionInternalImports {
				if strings.Contains(line, prefix) {
					ok = true
					break
				}
			}
			if !ok {
				t.Fatalf("unapproved internal import in %s: %s\n(allowlist is approvedProductionInternalImports; reach other internals through the public agent API)", path, strings.TrimSpace(line))
			}
		}
	}
}

// TestCLIBoundaryForbiddenInternalImports rejects forbidden internal
// imports in every agentcli .go file (production AND test). Test files
// are slightly more permissive about the allowlist above (they may
// integration-test against unlisted packages such as session for log
// fixtures), but the forbidden list is absolute: nothing in agentcli
// should reach into routing, the agent core loop, or per-provider
// drivers.
func TestCLIBoundaryForbiddenInternalImports(t *testing.T) {
	root := repoRootForBoundaryTest(t)
	entries, err := filepath.Glob(filepath.Join(root, "agentcli", "*.go"))
	if err != nil {
		t.Fatalf("glob cmd files: %v", err)
	}
	for _, path := range entries {
		if filepath.Base(path) == "service_boundary_test.go" {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, data, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, imp := range file.Imports {
			pathLit := strings.Trim(imp.Path.Value, "\"")
			for _, banned := range forbiddenCLIInternalImports {
				if pathLit == banned || strings.HasPrefix(pathLit, banned+"/") {
					t.Fatalf("forbidden internal import in %s: %q (banned prefix %q — reach this via the public agent API)", path, pathLit, banned)
				}
			}
		}
	}
}

// TestCLIBoundaryForbiddenSymbols rejects callers that re-grow the
// boundary by calling forbidden internal symbols. The symbol list is
// curated to packages with a public replacement — adding a re-export to
// the public agent API is the path forward, not a new entry here.
func TestCLIBoundaryForbiddenSymbols(t *testing.T) {
	root := repoRootForBoundaryTest(t)
	entries, err := filepath.Glob(filepath.Join(root, "agentcli", "*.go"))
	if err != nil {
		t.Fatalf("glob cmd files: %v", err)
	}
	for _, path := range entries {
		base := filepath.Base(path)
		if base == "service_boundary_test.go" {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		src := string(data)
		for _, sym := range forbiddenCLISymbols {
			if strings.Contains(src, sym) {
				t.Fatalf("forbidden symbol %q used in %s — call the public agent API instead", sym, path)
			}
		}
	}
}

// TestCLIBoundaryDocsListsApprovedAllowlist is a small structural check
// that the approved list is non-empty and free of duplicates. It exists
// so an accidental wholesale removal of the allowlist (which would
// silently make the production check trivially pass) is caught.
func TestCLIBoundaryDocsListsApprovedAllowlist(t *testing.T) {
	if len(approvedProductionInternalImports) == 0 {
		t.Fatal("approvedProductionInternalImports must not be empty")
	}
	seen := map[string]bool{}
	for _, pkg := range approvedProductionInternalImports {
		if seen[pkg] {
			t.Fatalf("duplicate entry in approvedProductionInternalImports: %q", pkg)
		}
		seen[pkg] = true
	}
	if len(forbiddenCLIInternalImports) == 0 {
		t.Fatal("forbiddenCLIInternalImports must not be empty")
	}
	for _, banned := range forbiddenCLIInternalImports {
		for _, allowed := range approvedProductionInternalImports {
			if allowed == banned || strings.HasPrefix(allowed, banned+"/") {
				t.Fatalf("approvedProductionInternalImports entry %q overlaps forbidden prefix %q", allowed, banned)
			}
		}
	}
}

func repoRootForBoundaryTest(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), ".."))
}
