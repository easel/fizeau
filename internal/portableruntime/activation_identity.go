package portableruntime

import "os"

func currentActivationIdentity() (effectiveUID, primaryGID int, supplementaryGroups []int, err error) {
	groups, err := os.Getgroups()
	if err != nil {
		return 0, 0, nil, err
	}
	return os.Geteuid(), os.Getegid(), groups, nil
}

// validateActivationIdentity checks the only identity shape that can safely
// author the single-ID namespace maps. It performs no filesystem, process, or
// network work, so callers can reject an unsafe activation before any service
// construction or writable-storage mutation.
func validateActivationIdentity(identity activationIdentity, groups []int) error {
	if identity.effectiveUID == 0 || identity.primaryGID == 0 || len(groups) != 0 {
		return activationError("activation identity")
	}
	return nil
}
