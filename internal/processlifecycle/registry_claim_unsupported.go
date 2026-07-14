//go:build !linux && !darwin && !windows

package processlifecycle

func claimRecoveryFile(string) (func(), error) {
	return nil, ErrPlatformUnsupported
}
