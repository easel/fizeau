package anthropic

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/easel/fizeau/internal/safefs"
)

// EnsureWorkspaceTrusted records that dir is a Claude Code workspace the
// operator already asked Fizeau to execute in. Current Claude Code highlights
// "No, exit" on the folder-trust dialog, and a bare Enter therefore quits
// before the harness can type. Persisting hasTrustDialogAccepted skips that
// dialog. Existing project fields are preserved. A directory that is already
// trusted is left untouched.
func EnsureWorkspaceTrusted(dir string) error {
	if dir == "" {
		return nil
	}
	// go test binaries must opt in. Otherwise every harness unit test that
	// launches Claude would rewrite the developer's ~/.claude.json.
	if strings.HasSuffix(os.Args[0], ".test") && os.Getenv("FIZEAU_RECORD_CLAUDE_TRUST") != "1" {
		return nil
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	path := filepath.Join(home, ".claude.json")
	raw, err := safefs.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			raw = []byte(`{"projects":{}}`)
		} else {
			return err
		}
	}
	var doc map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &doc); err != nil {
			return err
		}
	}
	if doc == nil {
		doc = map[string]any{}
	}
	projects, _ := doc["projects"].(map[string]any)
	if projects == nil {
		projects = map[string]any{}
		doc["projects"] = projects
	}
	entry, _ := projects[abs].(map[string]any)
	if entry == nil {
		entry = map[string]any{}
		projects[abs] = entry
	}
	if accepted, _ := entry["hasTrustDialogAccepted"].(bool); accepted {
		return nil
	}
	entry["hasTrustDialogAccepted"] = true
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	tmp := path + ".fizeau-trust"
	if err := safefs.WriteFile(tmp, out, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
