package codex

import "testing"

func TestCodexPromptReady(t *testing.T) {
	cases := map[string]struct {
		screen string
		want   bool
	}{
		"prompt only, header not drawn": {"› Ask Codex to do anything\n", false},
		"header still loading": {
			"│ model:     loading   /model to change      │\n› Ask Codex\n", false},
		"header resolved": {
			"│ model:     gpt-5.6-sol medium   /model to change      │\n› Ask Codex\n", true},
	}
	for name, tc := range cases {
		if got := codexPromptReady(tc.screen); got != tc.want {
			t.Errorf("%s: codexPromptReady = %v, want %v", name, got, tc.want)
		}
	}
}
