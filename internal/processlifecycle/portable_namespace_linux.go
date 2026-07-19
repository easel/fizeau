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
	// Mapping helpers use this config before a launcher has been selected. The
	// lifecycle supervisor requires the full sealed launcher protocol below.
	if config.Launcher.Path == "" && config.Target.Path == "" && len(config.Target.Args) == 0 && config.Target.Env == nil {
		return nil
	}
	if !validPortableCommand(config.Launcher.Path) || config.Target.Path == "" || len(config.Target.Args) == 0 || config.Target.Args[0] != config.Target.Path || config.Target.Env == nil || !validPortableNamespaceProjectionConfig(config.Projection) {
		return fmt.Errorf("invalid portable namespace protocol")
	}
	for _, argument := range config.Target.Args {
		if strings.IndexByte(argument, 0) >= 0 {
			return fmt.Errorf("invalid portable namespace protocol")
		}
	}
	for _, entry := range config.Target.Env {
		name, value, ok := strings.Cut(entry, "=")
		if !ok || !validPortableEnvironmentName(name) || strings.IndexByte(value, 0) >= 0 {
			return fmt.Errorf("invalid portable namespace protocol")
		}
	}
	if _, err := config.protocolEnvironment(); err != nil {
		return err
	}
	return nil
}

func validPortableNamespaceProjectionConfig(projection portableNamespaceProjectionConfig) bool {
	if projection.Version != portableProjectionRecipeVersion || projection.DescriptorBase < 3 || len(projection.Records) == 0 || len(projection.Records) > maxPortableProjectionRecords || projection.DescriptorBase > maxLifecycleInheritedFD-len(projection.Records) {
		return false
	}
	for i, record := range projection.Records {
		if len(record) != portableProjectionRecordBytes || allZero(record) || pathShapedPortableProjectionRecord(record) {
			return false
		}
		for prior := 0; prior < i; prior++ {
			if string(record) == string(projection.Records[prior]) {
				return false
			}
		}
	}
	return true
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
