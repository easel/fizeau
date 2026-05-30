package routing

import (
	"testing"
)

func TestIsUnpinnedRequest(t *testing.T) {
	tests := []struct {
		req  Request
		want bool
		name string
	}{
		{Request{}, true, "empty request is unpinned"},
		{Request{Model: "gpt-4"}, false, "model pin makes it pinned"},
		{Request{Provider: "openrouter"}, false, "provider pin makes it pinned"},
		{Request{Harness: "fiz"}, false, "harness pin makes it pinned"},
		{Request{Policy: "smart"}, true, "policy alone doesn't pin"},
		{Request{Policy: "smart", Model: "gpt-4"}, false, "policy + model is pinned"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isUnpinnedRequest(tt.req)
			if got != tt.want {
				t.Fatalf("isUnpinnedRequest(%#v)=%v, want %v", tt.req, got, tt.want)
			}
		})
	}
}

func TestNextPolicyInLadder(t *testing.T) {
	tests := []struct {
		current string
		want    string
		name    string
	}{
		{"cheap", "default", "cheap -> default"},
		{"default", "smart", "default -> smart"},
		{"smart", "", "smart is last"},
		{"air-gapped", "", "air-gapped not in ladder"},
		{"", "", "empty not in ladder"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := nextPolicyInLadder(tt.current)
			if got != tt.want {
				t.Fatalf("nextPolicyInLadder(%q)=%q, want %q", tt.current, got, tt.want)
			}
		})
	}
}
