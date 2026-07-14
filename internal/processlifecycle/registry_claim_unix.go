//go:build linux || darwin

package processlifecycle

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

func claimRecoveryFile(path string) (func(), error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) // #nosec G304 -- FileRegistry.path constrains the record ID and supplies this private registry path.
	if err != nil {
		return nil, err
	}
	fd := file.Fd()
	if fd > uintptr(^uint(0)>>1) {
		_ = file.Close()
		return nil, fmt.Errorf("lifecycle recovery lock descriptor %d exceeds int range", fd)
	}
	lockFD := int(fd) // #nosec G115 -- fd is checked against the platform int maximum immediately above.
	if err := syscall.Flock(lockFD, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, ErrRecoveryClaimed
		}
		return nil, err
	}
	return func() {
		_ = syscall.Flock(lockFD, syscall.LOCK_UN)
		_ = file.Close()
	}, nil
}
