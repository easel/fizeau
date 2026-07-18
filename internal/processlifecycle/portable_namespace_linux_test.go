//go:build linux

package processlifecycle

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestPortableNamespaceMapProtocolWritesAndRevalidatesExactIdentity(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"setgroups", "uid_map", "gid_map"} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0o600); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}
	config := portableNamespaceConfig{Version: portableNamespaceProtocolVersion, UID: 65531, GID: 65530}
	if err := writePortableNamespaceMapsAt(root, config); err != nil {
		t.Fatalf("writePortableNamespaceMapsAt: %v", err)
	}
	for name, want := range map[string]string{"setgroups": "deny\n", "uid_map": "0 65531 1\n", "gid_map": "0 65530 1\n"} {
		got, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || string(got) != want {
			t.Fatalf("%s = %q, %v; want %q", name, got, err, want)
		}
	}
}

func TestPortableNamespaceProtocolRejectsMalformedIdentity(t *testing.T) {
	if err := validatePortableNamespaceConfig(&portableNamespaceConfig{Version: portableNamespaceProtocolVersion, UID: 0, GID: 1}); err == nil {
		t.Fatal("zero UID protocol was accepted")
	}
}

func TestPortableNamespaceChildUsesAllOuterNamespaceFlags(t *testing.T) {
	attr, err := lifecyclePortableChildSysProcAttr(false)
	if err != nil {
		t.Fatalf("lifecyclePortableChildSysProcAttr: %v", err)
	}
	want := uintptr(syscall.CLONE_NEWUSER | syscall.CLONE_NEWNS | syscall.CLONE_NEWPID)
	if attr.Cloneflags&want != want || !attr.Setpgid {
		t.Fatalf("portable child attributes = %#v, want outer namespace flags and process group", attr)
	}
}
