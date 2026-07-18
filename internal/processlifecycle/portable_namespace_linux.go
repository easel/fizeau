//go:build linux

package processlifecycle

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// lifecyclePortableChildSysProcAttr creates the one gated direct child as PID
// 1 of its new PID namespace. The child remains the lifecycle process-group
// leader; no wrapper, shell, or PATH lookup is introduced.
func lifecyclePortableChildSysProcAttr(terminal bool) (*syscall.SysProcAttr, error) {
	attr := lifecycleChildSysProcAttr(terminal)
	attr.Cloneflags |= syscall.CLONE_NEWUSER | syscall.CLONE_NEWNS | syscall.CLONE_NEWPID
	return attr, nil
}

func validatePortableNamespaceConfig(config *portableNamespaceConfig) error {
	if config == nil {
		return nil
	}
	if config.Version != portableNamespaceProtocolVersion || config.UID <= 0 || config.GID <= 0 {
		return fmt.Errorf("invalid portable namespace protocol")
	}
	return nil
}

// writePortableNamespaceMaps is deliberately parent-owned: Linux requires
// the creator to deny setgroups before it writes gid_map. Every write is read
// back before the gate can ever be released to the child.
func writePortableNamespaceMaps(pid int, config portableNamespaceConfig) error {
	if err := validatePortableNamespaceConfig(&config); err != nil {
		return err
	}
	return writePortableNamespaceMapsAt(filepath.Join("/proc", fmt.Sprintf("%d", pid)), config)
}

func writePortableNamespaceMapsAt(root string, config portableNamespaceConfig) error {
	if err := validatePortableNamespaceConfig(&config); err != nil {
		return err
	}
	writeAndVerify := func(name, want string) error {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte(want), 0); err != nil {
			return fmt.Errorf("write portable namespace %s: %w", name, err)
		}
		got, err := os.ReadFile(path)
		if err != nil || strings.Join(strings.Fields(string(got)), " ") != strings.Join(strings.Fields(want), " ") {
			if err != nil {
				return fmt.Errorf("verify portable namespace %s: %w", name, err)
			}
			return fmt.Errorf("verify portable namespace %s", name)
		}
		return nil
	}
	if err := writeAndVerify("setgroups", "deny\n"); err != nil {
		return err
	}
	if err := writeAndVerify("uid_map", fmt.Sprintf("0 %d 1\n", config.UID)); err != nil {
		return err
	}
	return writeAndVerify("gid_map", fmt.Sprintf("0 %d 1\n", config.GID))
}
