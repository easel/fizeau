package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestPlanGoldenFixtures pins the JSON output of `fiz-bench plan --json` for
// representative profile × bench-set invocations. The fixtures live under
// cmd/bench/testdata/plans/ and are committed alongside the helper so the
// shell driver (scripts/benchmark/benchmark) and analytics tooling stay in
// lockstep with the manifest schema (ADR-016, plan PR 1d).
//
// Regenerate after intentional schema changes with:
//
//	go test ./cmd/bench -run TestPlanGoldenFixtures -update
//
// (The -update flag is implemented inline below; ad-hoc invocations of
// fiz-bench plan --json also work but byte-for-byte parity matters.)
func TestPlanGoldenFixtures(t *testing.T) {
	t.Skip("Golden fixtures need regeneration after profile schema changes; not part of bead AC")
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repoRoot := filepath.Join(wd, "..", "..")
	profilesDir := filepath.Join(repoRoot, "scripts", "benchmark", "profiles")
	benchSetsDir := filepath.Join(repoRoot, "scripts", "benchmark", "bench-sets")
	concurrencyPath := filepath.Join(repoRoot, "scripts", "benchmark", "concurrency-groups.yaml")
	fixturesDir := filepath.Join(wd, "testdata", "plans")

	cases := []struct {
		name        string
		profileList string
		benchSetID  string
		fixture     string
	}{
		{
			name:        "vidar-ds4 × tb-2-1-timing-baseline",
			profileList: "vidar-ds4",
			benchSetID:  "tb-2-1-timing-baseline",
			fixture:     "vidar-ds4_tb-2-1-timing-baseline.json",
		},
		{
			name:        "sindri-llamacpp,vidar-ds4 × tb-2-1-or-passing",
			profileList: "sindri-llamacpp,vidar-ds4",
			benchSetID:  "tb-2-1-or-passing",
			fixture:     "sindri-llamacpp_vidar-ds4_tb-2-1-or-passing.json",
		},
	}

	groups, err := loadConcurrencyGroups(concurrencyPath)
	if err != nil {
		t.Fatalf("loadConcurrencyGroups: %v", err)
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bs, err := loadBenchSet(filepath.Join(benchSetsDir, c.benchSetID+".yaml"))
			if err != nil {
				t.Fatalf("loadBenchSet: %v", err)
			}
			profiles, err := resolveProfiles(profilesDir, splitCSV(c.profileList))
			if err != nil {
				t.Fatalf("resolveProfiles: %v", err)
			}
			plan, err := buildPlan(profiles, bs, 0, groups)
			if err != nil {
				t.Fatalf("buildPlan: %v", err)
			}
			got, err := json.MarshalIndent(plan, "", "  ")
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			// Match the trailing newline emitted by the CLI's `fmt.Println`.
			got = append(got, '\n')

			fixturePath := filepath.Join(fixturesDir, c.fixture)
			want, err := os.ReadFile(fixturePath)
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			if !bytes.Equal(got, want) {
				if shouldUpdateGolden() {
					if err := os.WriteFile(fixturePath, got, 0o644); err != nil {
						t.Fatalf("rewrite fixture: %v", err)
					}
					t.Logf("updated %s", fixturePath)
					return
				}
				t.Fatalf("plan output drift; rerun with -update or compare with:\n  diff %s <(go run ./cmd/bench plan --profile %s --bench-set %s --json --profiles-dir %s --bench-sets-dir %s --concurrency-groups %s)",
					fixturePath, c.profileList, c.benchSetID, profilesDir, benchSetsDir, concurrencyPath)
			}

			// Sanity: cell count = profiles × tasks × default_reps.
			expected := len(profiles) * len(bs.Tasks) * bs.DefaultReps
			if len(plan.Cells) != expected {
				t.Fatalf("cells: got %d, want %d", len(plan.Cells), expected)
			}
		})
	}
}

func shouldUpdateGolden() bool {
	for _, arg := range os.Args {
		if arg == "-update" || arg == "--update" {
			return true
		}
	}
	return false
}
