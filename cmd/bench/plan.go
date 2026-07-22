// plan.go: `fiz-bench plan --json` helper for the new ./benchmark shell
// driver (ADR-016, plan PR 1d). Loads one or more profile YAMLs plus a
// bench-set YAML, cross-references them against
// scripts/benchmark/concurrency-groups.yaml, and emits a JSON document the
// shell driver consumes to expand the execution matrix.
//
// The helper is pure: no file writes outside stdout, no endpoint probes, no
// benchmark-results/ directory creation. Schema additions require a spec
// amendment so the shell driver and golden fixtures stay in lockstep.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/easel/fizeau/internal/benchmark/profile"
	"gopkg.in/yaml.v3"
)

const (
	defaultBenchSetsDir         = "scripts/benchmark/bench-sets"
	defaultConcurrencyGroupsRel = "scripts/benchmark/concurrency-groups.yaml"
)

// benchSet mirrors the YAML schema authored under scripts/benchmark/bench-sets/.
// Schema is frozen by ADR-016; additive fields require a spec amendment.
type benchSet struct {
	ID          string   `yaml:"id"            json:"id"`
	Framework   string   `yaml:"framework"     json:"framework"`
	Dataset     string   `yaml:"dataset"       json:"dataset"`
	DefaultReps int      `yaml:"default_reps"  json:"default_reps"`
	Description string   `yaml:"description"   json:"description,omitempty"`
	Tasks       []string `yaml:"-"             json:"tasks"`
	// AllTasks signals "tasks: all" — defer task enumeration to the harness
	// dataset rather than encoding it in the bench-set YAML.
	AllTasks bool `yaml:"-"             json:"all_tasks,omitempty"`
}

// rawBenchSet captures the YAML literal before normalizing the `tasks` field,
// which accepts either a list of task IDs or the bare string "all". A
// bench-set may also reference an external task catalog via `tasks_from`
// (path relative to the bench-set file, tasks[].id entries); external tasks
// resolve before inline ones with duplicates removed, mirroring the shell
// driver's resolve_tasks. `task_executor` is executor routing owned by the
// shell driver; the plan helper accepts it but does not interpret it.
type rawBenchSet struct {
	ID           string    `yaml:"id"`
	Framework    string    `yaml:"framework"`
	Dataset      string    `yaml:"dataset"`
	DefaultReps  int       `yaml:"default_reps"`
	Description  string    `yaml:"description"`
	Tasks        yaml.Node `yaml:"tasks"`
	TasksFrom    string    `yaml:"tasks_from"`
	TaskExecutor string    `yaml:"task_executor"`
}

// loadTaskSubset reads a task-subset YAML (scripts/benchmark/task-subset-*.yaml)
// and returns its task IDs in file order. Entries are either scalar IDs or
// mappings with an `id` key; surrounding metadata fields are ignored.
func loadTaskSubset(path string) ([]string, error) {
	raw, err := os.ReadFile(path) // #nosec G304 -- path referenced by operator-supplied bench-set
	if err != nil {
		return nil, fmt.Errorf("read tasks_from %s: %w", path, err)
	}
	var f struct {
		Tasks []yaml.Node `yaml:"tasks"`
	}
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("parse tasks_from %s: %w", path, err)
	}
	out := make([]string, 0, len(f.Tasks))
	for _, n := range f.Tasks {
		switch n.Kind {
		case yaml.ScalarNode:
			if v := strings.TrimSpace(n.Value); v != "" {
				out = append(out, v)
			}
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				if n.Content[i].Value == "id" {
					if v := strings.TrimSpace(n.Content[i+1].Value); v != "" {
						out = append(out, v)
					}
					break
				}
			}
		default:
			return nil, fmt.Errorf("tasks_from %s: tasks entries must be scalars or mappings with an id key", path)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("tasks_from %s: resolved no tasks", path)
	}
	return out, nil
}

func loadBenchSet(path string) (*benchSet, error) {
	raw, err := os.ReadFile(path) // #nosec G304 -- operator-supplied bench-set path
	if err != nil {
		return nil, fmt.Errorf("read bench-set %s: %w", path, err)
	}
	var r rawBenchSet
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&r); err != nil {
		return nil, fmt.Errorf("parse bench-set %s: %w", path, err)
	}
	bs := &benchSet{
		ID:          r.ID,
		Framework:   r.Framework,
		Dataset:     r.Dataset,
		DefaultReps: r.DefaultReps,
		Description: strings.TrimSpace(r.Description),
	}
	tasksFrom := strings.TrimSpace(r.TasksFrom)
	seen := make(map[string]bool)
	appendTask := func(v string) {
		if v != "" && !seen[v] {
			seen[v] = true
			bs.Tasks = append(bs.Tasks, v)
		}
	}
	switch r.Tasks.Kind {
	case yaml.ScalarNode:
		if strings.TrimSpace(r.Tasks.Value) != "all" {
			return nil, fmt.Errorf("bench-set %s: tasks scalar must be \"all\", got %q", path, r.Tasks.Value)
		}
		if tasksFrom != "" {
			return nil, fmt.Errorf("bench-set %s: tasks: all cannot be combined with tasks_from", path)
		}
		bs.AllTasks = true
	case yaml.SequenceNode:
		// Appended below, after any tasks_from catalog.
	case 0:
		if tasksFrom == "" {
			return nil, fmt.Errorf("bench-set %s: tasks field is required", path)
		}
	default:
		return nil, fmt.Errorf("bench-set %s: unsupported tasks node kind %v", path, r.Tasks.Kind)
	}
	if tasksFrom != "" && !bs.AllTasks {
		external, err := loadTaskSubset(filepath.Join(filepath.Dir(path), tasksFrom))
		if err != nil {
			return nil, fmt.Errorf("bench-set %s: %w", path, err)
		}
		for _, v := range external {
			appendTask(v)
		}
	}
	if r.Tasks.Kind == yaml.SequenceNode {
		for _, child := range r.Tasks.Content {
			if child.Kind != yaml.ScalarNode {
				return nil, fmt.Errorf("bench-set %s: tasks entries must be scalars", path)
			}
			appendTask(strings.TrimSpace(child.Value))
		}
	}
	if err := bs.validate(); err != nil {
		return nil, fmt.Errorf("validate bench-set %s: %w", path, err)
	}
	return bs, nil
}

func (b *benchSet) validate() error {
	if strings.TrimSpace(b.ID) == "" {
		return fmt.Errorf("id is required")
	}
	if strings.TrimSpace(b.Framework) == "" {
		return fmt.Errorf("framework is required")
	}
	if strings.TrimSpace(b.Dataset) == "" {
		return fmt.Errorf("dataset is required")
	}
	if b.DefaultReps <= 0 {
		return fmt.Errorf("default_reps must be > 0")
	}
	if !b.AllTasks && len(b.Tasks) == 0 {
		return fmt.Errorf("tasks: list is empty (use \"all\" to defer to harness)")
	}
	return nil
}

// concurrencyGroup mirrors one entry under groups: in
// scripts/benchmark/concurrency-groups.yaml.
type concurrencyGroup struct {
	ID             string `json:"id"`
	MaxConcurrency int    `json:"max_concurrency"`
	Description    string `json:"description,omitempty"`
}

type concurrencyGroupsFile struct {
	Groups map[string]struct {
		MaxConcurrency int    `yaml:"max_concurrency"`
		Description    string `yaml:"description"`
	} `yaml:"groups"`
}

func loadConcurrencyGroups(path string) (map[string]concurrencyGroup, error) {
	raw, err := os.ReadFile(path) // #nosec G304 -- operator-supplied path
	if err != nil {
		return nil, fmt.Errorf("read concurrency-groups %s: %w", path, err)
	}
	var f concurrencyGroupsFile
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("parse concurrency-groups %s: %w", path, err)
	}
	out := make(map[string]concurrencyGroup, len(f.Groups))
	for id, g := range f.Groups {
		out[id] = concurrencyGroup{
			ID:             id,
			MaxConcurrency: g.MaxConcurrency,
			Description:    strings.TrimSpace(g.Description),
		}
	}
	return out, nil
}

// planCell is a single (profile, framework, dataset, task, rep) cell awaiting
// execution. The shell driver iterates these one-to-one.
type planCell struct {
	ProfileID        string `json:"profile_id"`
	Framework        string `json:"framework"`
	Dataset          string `json:"dataset"`
	Task             string `json:"task"`
	Rep              int    `json:"rep"`
	ConcurrencyGroup string `json:"concurrency_group,omitempty"`
}

// planOutput is the top-level JSON document the shell driver consumes.
type planOutput struct {
	BenchSet           *benchSet                   `json:"bench_set"`
	Profiles           []*profile.Profile          `json:"profiles"`
	ConcurrencyGroups  map[string]concurrencyGroup `json:"concurrency_groups"`
	Cells              []planCell                  `json:"cells"`
	UnknownConcurrency []string                    `json:"unknown_concurrency_groups,omitempty"`
}

// resolveProfiles loads each id in the comma-separated list, returning them
// in input order. Profiles are loaded by filename (id.yaml) under profilesDir.
func resolveProfiles(profilesDir string, ids []string) ([]*profile.Profile, error) {
	out := make([]*profile.Profile, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if seen[id] {
			return nil, fmt.Errorf("profile %q listed twice", id)
		}
		seen[id] = true
		path := filepath.Join(profilesDir, id+".yaml")
		p, err := profile.Load(path)
		if err != nil {
			return nil, err
		}
		if p.ID != id {
			return nil, fmt.Errorf("profile %s: id field %q does not match filename", path, p.ID)
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no profiles resolved")
	}
	return out, nil
}

// buildPlan expands the profile × bench-set cross-product into a deterministic
// list of cells. Iteration order: profile (input order), task (bench-set
// order), rep (1..reps).
func buildPlan(profiles []*profile.Profile, bs *benchSet, reps int, groups map[string]concurrencyGroup) (*planOutput, error) {
	if reps <= 0 {
		reps = bs.DefaultReps
	}
	if bs.AllTasks {
		return nil, fmt.Errorf("bench-set %s: tasks: all requires harness-side enumeration not yet supported by plan helper", bs.ID)
	}
	cells := make([]planCell, 0, len(profiles)*len(bs.Tasks)*reps)
	unknown := make(map[string]bool)
	for _, p := range profiles {
		cg := p.ConcurrencyGroup
		if cg != "" {
			if _, ok := groups[cg]; !ok {
				unknown[cg] = true
			}
		}
		for _, task := range bs.Tasks {
			for r := 1; r <= reps; r++ {
				cells = append(cells, planCell{
					ProfileID:        p.ID,
					Framework:        bs.Framework,
					Dataset:          bs.Dataset,
					Task:             task,
					Rep:              r,
					ConcurrencyGroup: cg,
				})
			}
		}
	}
	out := &planOutput{
		BenchSet:          bs,
		Profiles:          profiles,
		ConcurrencyGroups: groups,
		Cells:             cells,
	}
	if len(unknown) > 0 {
		out.UnknownConcurrency = sortedSetKeys(unknown)
	}
	return out, nil
}

func sortedSetKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// cmdPlan implements `fiz-bench plan`.
func cmdPlan(args []string) int {
	fs := flagSet("plan")
	profileList := fs.String("profile", "", "Comma-separated profile IDs (required)")
	benchSetID := fs.String("bench-set", "", "Bench-set ID (required)")
	reps := fs.Int("reps", 0, "Repetitions per (profile, task); 0 = bench-set default_reps")
	jsonOut := fs.Bool("json", false, "Emit JSON (default: human matrix)")
	workDir := fs.String("work-dir", "", "Repository root (default: cwd)")
	profilesDir := fs.String("profiles-dir", "", "Profiles directory (default: scripts/benchmark/profiles)")
	benchSetsDir := fs.String("bench-sets-dir", "", "Bench-sets directory (default: scripts/benchmark/bench-sets)")
	concurrencyPath := fs.String("concurrency-groups", "", "concurrency-groups.yaml path (default: scripts/benchmark/concurrency-groups.yaml)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*profileList) == "" {
		fmt.Fprintf(os.Stderr, "%s plan: --profile is required\n", benchCommandName())
		return 2
	}
	if strings.TrimSpace(*benchSetID) == "" {
		fmt.Fprintf(os.Stderr, "%s plan: --bench-set is required\n", benchCommandName())
		return 2
	}

	root := resolveWorkDir(*workDir)
	pdir := *profilesDir
	if pdir == "" {
		pdir = filepath.Join(root, defaultProfilesDir)
	}
	bdir := *benchSetsDir
	if bdir == "" {
		bdir = filepath.Join(root, defaultBenchSetsDir)
	}
	cgPath := *concurrencyPath
	if cgPath == "" {
		cgPath = filepath.Join(root, defaultConcurrencyGroupsRel)
	}

	bs, err := loadBenchSet(filepath.Join(bdir, *benchSetID+".yaml"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s plan: %v\n", benchCommandName(), err)
		return 1
	}
	if bs.ID != *benchSetID {
		fmt.Fprintf(os.Stderr, "%s plan: bench-set id %q does not match filename %s\n", benchCommandName(), bs.ID, *benchSetID)
		return 1
	}
	groups, err := loadConcurrencyGroups(cgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s plan: %v\n", benchCommandName(), err)
		return 1
	}
	profiles, err := resolveProfiles(pdir, strings.Split(*profileList, ","))
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s plan: %v\n", benchCommandName(), err)
		return 1
	}
	out, err := buildPlan(profiles, bs, *reps, groups)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s plan: %v\n", benchCommandName(), err)
		return 1
	}

	if *jsonOut {
		data, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s plan: marshal: %v\n", benchCommandName(), err)
			return 1
		}
		// Trailing newline to match `gofmt`-style file output and make
		// golden-file diffs friendlier under most editors.
		fmt.Println(string(data))
		return 0
	}

	printPlanMatrix(out)
	if len(out.UnknownConcurrency) > 0 {
		fmt.Fprintf(os.Stderr, "warning: profiles reference unknown concurrency groups: %s\n", strings.Join(out.UnknownConcurrency, ", "))
	}
	return 0
}

// printPlanMatrix renders a human-readable summary of the resolved matrix to
// stdout. Side-effect-free; safe under `--plan`.
func printPlanMatrix(out *planOutput) {
	fmt.Printf("bench-set: %s  framework=%s  dataset=%s  default_reps=%d  tasks=%d\n",
		out.BenchSet.ID, out.BenchSet.Framework, out.BenchSet.Dataset, out.BenchSet.DefaultReps, len(out.BenchSet.Tasks))
	for _, p := range out.Profiles {
		cg := p.ConcurrencyGroup
		cap := ""
		if g, ok := out.ConcurrencyGroups[cg]; ok {
			cap = fmt.Sprintf("max_concurrency=%d", g.MaxConcurrency)
		} else if cg != "" {
			cap = "max_concurrency=?"
		}
		fmt.Printf("profile: %-28s  provider=%-14s  model=%-32s  cg=%-32s  %s\n",
			p.ID, string(p.Provider.Type), p.Provider.Model, cg, cap)
	}
	fmt.Printf("cells: %d (profiles=%d × tasks=%d × reps=%d)\n",
		len(out.Cells), len(out.Profiles), len(out.BenchSet.Tasks), cellsPerProfileTask(out.Cells, out.Profiles, out.BenchSet))
}

func cellsPerProfileTask(cells []planCell, profiles []*profile.Profile, bs *benchSet) int {
	if len(profiles) == 0 || len(bs.Tasks) == 0 {
		return 0
	}
	return len(cells) / (len(profiles) * len(bs.Tasks))
}
