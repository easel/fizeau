package hooks

import (
	"encoding/json"
	"sync"
)

// Hook is a callback interface for handling Claude hook events.
type Hook interface {
	OnToolUse(id string, name string, input json.RawMessage)
	OnToolResult(id string, output string, err string)
}

// Manager arbitrates between user-defined hooks and Fizeau-injected hooks.
// It ensures that hook handlers coexist without duplication.
type Manager struct {
	mu    sync.RWMutex
	hooks map[string]Hook
}

// New creates a new hook manager.
func New() *Manager {
	return &Manager{
		hooks: make(map[string]Hook),
	}
}

// Register adds a hook handler with the given key.
// If a hook with the same key already exists, it is replaced.
func (m *Manager) Register(key string, h Hook) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hooks[key] = h
}

// Unregister removes a hook handler by key.
func (m *Manager) Unregister(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.hooks, key)
}

// OnToolUse calls all registered hooks with the tool use event.
func (m *Manager) OnToolUse(id string, name string, input json.RawMessage) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, h := range m.hooks {
		h.OnToolUse(id, name, input)
	}
}

// OnToolResult calls all registered hooks with the tool result event.
func (m *Manager) OnToolResult(id string, output string, err string) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, h := range m.hooks {
		h.OnToolResult(id, output, err)
	}
}
