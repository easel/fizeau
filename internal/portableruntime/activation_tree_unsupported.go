//go:build !linux

package portableruntime

import (
	"github.com/easel/fizeau/internal/safefs"
)

func activationTreeDigest(*safefs.NoFollowRoot, string) (string, error) {
	return "", safefs.ErrNoFollowRootUnsupported
}

func activationTreeDigestWithHook(*safefs.NoFollowRoot, string, func()) (string, error) {
	return "", safefs.ErrNoFollowRootUnsupported
}

func validateActivationDeclaredPaths(*safefs.NoFollowRoot, []ManifestAsset, bool) error {
	return safefs.ErrNoFollowRootUnsupported
}
