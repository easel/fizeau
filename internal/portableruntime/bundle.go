package portableruntime

import "fmt"

func (b *Bundle) RuntimeRoot() string {
	if b == nil {
		return ""
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.runtimeRoot
}

func (b *Bundle) Mounts() []Mount {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]Mount(nil), b.mounts...)
}

func (b *Bundle) EnvironmentNames() []string {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.environment...)
}

// Close removes only the committed runtime child. Cleanup is serialized,
// idempotent after success, and remains retryable after failure.
func (b *Bundle) Close() error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cleanup == nil {
		return nil
	}
	if err := b.cleanup(); err != nil {
		return fmt.Errorf("%w: committed runtime removal failed", ErrCleanupIncomplete)
	}
	b.cleanup = nil
	b.runtimeRoot = ""
	b.mounts = nil
	b.environment = nil
	if b.anchor != nil {
		_ = b.anchor.Close()
		b.anchor = nil
	}
	return nil
}
