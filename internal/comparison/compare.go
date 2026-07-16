package comparison

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/easel/fizeau"
)

// RunCompare dispatches the same prompt to multiple harnesses,
// optionally in isolated worktrees, and returns a ComparisonRecord.
func RunCompare(run RunFunc, opts CompareOptions) (*ComparisonRecord, error) {
	if len(opts.Harnesses) == 0 {
		return nil, fmt.Errorf("comparison: RunCompare requires at least one harness")
	}

	id := genCompareID()
	record := &ComparisonRecord{
		ID:        id,
		Timestamp: time.Now().UTC(),
		Prompt:    opts.Prompt,
		Arms:      make([]ComparisonArm, len(opts.Harnesses)),
	}

	// Resolve base working directory.
	baseDir := opts.WorkDir
	if baseDir == "" {
		baseDir, _ = os.Getwd()
	}

	// Create worktrees sequentially (git worktree add takes a lock)
	// then run agent arms in parallel.
	worktrees := make([]string, len(opts.Harnesses))
	if opts.Sandbox {
		for i, harness := range opts.Harnesses {
			label := harness
			if l, ok := opts.ArmLabels[i]; ok {
				label = l
			}
			wt, err := createCompareWorktree(baseDir, id, label)
			if err != nil {
				record.Arms[i] = ComparisonArm{
					Harness:    label,
					CostSource: fizeau.CostSourceUnknown,
					ExitCode:   1,
					Error:      fmt.Sprintf("worktree: %s", err),
				}
				continue
			}
			worktrees[i] = wt
		}
	}

	var wg sync.WaitGroup
	for i, harness := range opts.Harnesses {
		// Skip arms that failed worktree creation.
		if opts.Sandbox && worktrees[i] == "" && record.Arms[i].Error != "" {
			continue
		}
		wg.Add(1)
		go func(idx int, harnessName string) {
			defer wg.Done()
			record.Arms[idx] = runCompareArm(run, opts, idx, harnessName, baseDir, id, opts.Prompt, worktrees[idx])
		}(i, harness)
	}
	wg.Wait()

	// Cleanup worktrees unless KeepSandbox.
	if opts.Sandbox && !opts.KeepSandbox {
		cleanupCompareWorktrees(baseDir, id)
	}

	return record, nil
}

// runCompareArm executes one harness arm, optionally in a pre-created worktree.
func runCompareArm(run RunFunc, opts CompareOptions, armIdx int, harnessName, baseDir, compareID, prompt, worktreePath string) ComparisonArm {
	label := harnessName
	if l, ok := opts.ArmLabels[armIdx]; ok {
		label = l
	}
	arm := ComparisonArm{Harness: label, CostSource: fizeau.CostSourceUnknown}

	// Determine working directory.
	workDir := baseDir
	if worktreePath != "" {
		workDir = worktreePath
	}

	// Resolve per-arm model override.
	model := ""
	if m, ok := opts.ArmModels[armIdx]; ok {
		model = m
	}

	result := run(harnessName, model, prompt)

	arm.Model = result.Model
	arm.Output = result.Output
	arm.ToolCalls = result.ToolCalls
	arm.Tokens = result.Tokens
	arm.InputTokens = result.InputTokens
	arm.OutputTokens = result.OutputTokens
	arm.CostUSD, arm.CostSource = normalizeRunResultCost(result)
	arm.DurationMS = result.DurationMS
	arm.ExitCode = result.ExitCode
	arm.Error = result.Error

	// Capture git diff if we're in a worktree.
	if worktreePath != "" {
		arm.Diff = captureGitDiff(worktreePath)
	}

	// Run post-run command if specified.
	if opts.PostRun != "" && workDir != "" {
		out, ok := runPostCommand(workDir, opts.PostRun)
		arm.PostRunOut = out
		arm.PostRunOK = &ok
	}

	return arm
}

// createCompareWorktree creates a git worktree for a comparison arm.
func createCompareWorktree(workDir, compareID, harnessName string) (string, error) {
	gitRoot, err := resolveGitRoot(workDir)
	if err != nil {
		return "", fmt.Errorf("resolving git root: %w", err)
	}

	wtDir := filepath.Join(gitRoot, ".worktrees", fmt.Sprintf("%s-%s", compareID, harnessName))

	cmd := runGit(gitRoot, "worktree", "add", "--detach", wtDir, "HEAD")
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git worktree add: %s\n%s", err, string(out))
	}
	return wtDir, nil
}

// runGit prepares an *exec.Cmd that invokes the git binary in dir with a
// scrubbed environment. git is a fixed binary; args originate from the
// comparison driver and operator-supplied benchmark suite YAML — no raw
// network input flows here. This helper localizes the gosec G204 annotation
// rather than scattering it across each callsite.
func runGit(dir string, args ...string) *exec.Cmd {
	// #nosec G204 -- "git" is a fixed binary; args come from operator-supplied
	// benchmark config and internally derived paths (worktree dir, gitRoot).
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = cleanGitEnv()
	return cmd
}

// resolveGitRoot finds the git repository root from any directory within it.
func resolveGitRoot(dir string) (string, error) {
	out, err := runGit(dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("not a git repository: %s", dir)
	}
	return strings.TrimSpace(string(out)), nil
}

// captureGitDiff captures the unified diff of all changes in a worktree.
func captureGitDiff(worktreePath string) string {
	out, err := runGit(worktreePath, "diff", "HEAD").Output()
	if err != nil {
		return ""
	}
	diff := string(out)

	// Also capture untracked files as a diff-like listing.
	untrackedOut, _ := runGit(worktreePath, "ls-files", "--others", "--exclude-standard").Output()
	untracked := strings.TrimSpace(string(untrackedOut))
	if untracked != "" {
		for _, f := range strings.Split(untracked, "\n") {
			f = strings.TrimSpace(f)
			if f == "" {
				continue
			}
			// #nosec G304 -- f is a worktree-relative path emitted by `git
			// ls-files --others`; worktreePath is the comparison driver's
			// own sandbox root, not external input.
			content, err := os.ReadFile(filepath.Join(worktreePath, f))
			if err != nil {
				continue
			}
			diff += fmt.Sprintf("\n--- /dev/null\n+++ b/%s\n@@ -0,0 +1 @@\n", f)
			for _, line := range strings.Split(string(content), "\n") {
				if line != "" || len(content) > 0 {
					diff += "+" + line + "\n"
				}
			}
		}
	}

	return strings.TrimSpace(diff)
}

// cleanGitEnv returns the current environment with git hook-specific vars removed.
func cleanGitEnv() []string {
	blocked := map[string]bool{
		"GIT_DIR":                          true,
		"GIT_INDEX_FILE":                   true,
		"GIT_WORK_TREE":                    true,
		"GIT_OBJECT_DIRECTORY":             true,
		"GIT_ALTERNATE_OBJECT_DIRECTORIES": true,
	}
	env := os.Environ()
	out := make([]string, 0, len(env))
	for _, e := range env {
		key := e
		if i := strings.Index(e, "="); i >= 0 {
			key = e[:i]
		}
		if !blocked[key] {
			out = append(out, e)
		}
	}
	return out
}

// runPostCommand runs a shell command in the given directory.
func runPostCommand(dir, command string) (string, bool) {
	// #nosec G204 -- post-command is operator-supplied benchmark suite YAML;
	// the shell-out is intentional so suite authors can chain arbitrary
	// validation commands. This is the documented contract of PostRun.
	cmd := exec.Command("sh", "-c", command)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err == nil
}

// cleanupCompareWorktrees removes worktrees created for a comparison.
func cleanupCompareWorktrees(repoDir, compareID string) {
	if root, err := resolveGitRoot(repoDir); err == nil {
		repoDir = root
	}
	wtBase := filepath.Join(repoDir, ".worktrees")
	entries, err := os.ReadDir(wtBase)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), compareID) {
			wtPath := filepath.Join(wtBase, e.Name())
			_ = runGit(repoDir, "worktree", "remove", "--force", wtPath).Run()
		}
	}
	_ = runGit(repoDir, "worktree", "prune").Run()
}

func genCompareID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return "cmp-" + hex.EncodeToString(b)
}
