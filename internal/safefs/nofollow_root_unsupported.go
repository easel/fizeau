//go:build !linux

package safefs

import "os"

// OpenNoFollowRoot is unavailable off Linux because the portable-runtime
// closure contract requires Linux openat no-follow semantics.
func OpenNoFollowRoot(string) (*NoFollowRoot, error) {
	return nil, ErrNoFollowRootUnsupported
}

// OpenReadNoFollow is unavailable off Linux.
func (root *NoFollowRoot) OpenReadNoFollow(string) (*os.File, error) {
	return nil, ErrNoFollowRootUnsupported
}

func (root *NoFollowRoot) OpenDirectoryNoFollow(string) (*os.File, error) {
	return nil, ErrNoFollowRootUnsupported
}
