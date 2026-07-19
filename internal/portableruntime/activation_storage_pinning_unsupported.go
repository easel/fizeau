//go:build !linux

package portableruntime

import (
	"errors"

	"github.com/easel/fizeau/internal/safefs"
)

type activationProjectionRecipe struct{}

func pinActivationProjectionRecipe(*safefs.NoFollowRoot, *stageHandle, *destinationHandle, ManifestEntrypoint, map[string]ManifestAsset, ActivationRecipe) (*activationProjectionRecipe, error) {
	return nil, safefs.ErrNoFollowRootUnsupported
}

func (*activationProjectionRecipe) revalidate() error {
	return errors.New("portable projection descriptors require linux")
}
