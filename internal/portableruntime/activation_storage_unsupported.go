//go:build !linux

package portableruntime

import (
	"context"

	"github.com/easel/fizeau/internal/safefs"
)

func ensureActivationStageDirectory(*stageHandle, string) error {
	return safefs.ErrNoFollowRootUnsupported
}

func activationAssetIdentity(*safefs.NoFollowRoot, ManifestAsset) (fileIdentity, error) {
	return fileIdentity{}, safefs.ErrNoFollowRootUnsupported
}

func copyActivationAsset(context.Context, *safefs.NoFollowRoot, *stageHandle, ManifestAsset, string) error {
	return safefs.ErrNoFollowRootUnsupported
}
