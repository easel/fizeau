//go:build darwin

package processlifecycle

import (
	"fmt"
	"syscall"
)

func lifecyclePortableChildSysProcAttr(bool) (*syscall.SysProcAttr, error) {
	return nil, fmt.Errorf("portable user namespaces require linux")
}

func validatePortableNamespaceConfig(config *portableNamespaceConfig) error {
	if config == nil {
		return nil
	}
	return fmt.Errorf("portable user namespaces require linux")
}

func writePortableNamespaceMaps(int, portableNamespaceConfig) error {
	return fmt.Errorf("portable user namespaces require linux")
}
