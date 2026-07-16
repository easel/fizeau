package safefs

import (
	"errors"
	"os"
)

// ErrNoFollowRootUnsupported reports that descriptor-anchored no-follow
// traversal is unavailable on the current operating system.
var ErrNoFollowRootUnsupported = errors.New("root-anchored no-follow traversal unsupported")

// NoFollowRoot retains an opened directory used as the anchor for relative
// no-follow traversal. It is not safe to close concurrently with Stat or Open.
type NoFollowRoot struct {
	file *os.File
}

// Stat returns identity and metadata for the retained root descriptor.
func (root *NoFollowRoot) Stat() (os.FileInfo, error) {
	if root == nil || root.file == nil {
		return nil, os.ErrInvalid
	}
	return root.file.Stat()
}

// Close releases the retained root descriptor.
func (root *NoFollowRoot) Close() error {
	if root == nil || root.file == nil {
		return nil
	}
	err := root.file.Close()
	root.file = nil
	return err
}
