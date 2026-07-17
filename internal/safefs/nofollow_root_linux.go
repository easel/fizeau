//go:build linux

package safefs

import (
	"os"
	"path"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// OpenNoFollowRoot opens one clean absolute directory without following any
// path component.
// #nosec G304 -- callers intentionally anchor traversal at a selected root.
func OpenNoFollowRoot(name string) (*NoFollowRoot, error) {
	if name == "" || !filepath.IsAbs(name) || filepath.Clean(name) != name || strings.ContainsRune(name, 0) {
		return nil, os.ErrInvalid
	}
	fd, err := unix.Open(string(filepath.Separator), unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	components := strings.Split(strings.TrimPrefix(name, string(filepath.Separator)), string(filepath.Separator))
	if name == string(filepath.Separator) {
		components = nil
	}
	for _, component := range components {
		next, openErr := unix.Openat(fd, component, unix.O_PATH|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		_ = unix.Close(fd)
		if openErr != nil {
			return nil, openErr
		}
		fd = next
	}
	file := os.NewFile(uintptr(fd), "portable-runtime-root") // #nosec G115 -- unix.Open returns a nonnegative OS descriptor.
	if file == nil {
		_ = unix.Close(fd)
		return nil, os.ErrInvalid
	}
	return &NoFollowRoot{file: file}, nil
}

// OpenReadNoFollow opens a slash-relative regular-file candidate beneath the
// retained root without following any intermediate or final symlink. The
// caller owns the returned descriptor and decides which file types are valid.
func (root *NoFollowRoot) OpenReadNoFollow(relative string) (*os.File, error) {
	if root == nil || root.file == nil || !validNoFollowRelativePath(relative) {
		return nil, os.ErrInvalid
	}
	components := strings.Split(relative, "/")
	current := int(root.file.Fd()) // #nosec G115 -- the file was constructed from a nonnegative unix descriptor above.
	owned := -1
	defer func() {
		if owned >= 0 {
			_ = unix.Close(owned)
		}
	}()
	for _, component := range components[:len(components)-1] {
		next, err := unix.Openat(current, component, unix.O_PATH|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return nil, err
		}
		if owned >= 0 {
			_ = unix.Close(owned)
		}
		owned = next
		current = next
	}
	fd, err := unix.Openat(current, components[len(components)-1], unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "portable-runtime-member") // #nosec G115 -- unix.Openat returns a nonnegative OS descriptor.
	if file == nil {
		_ = unix.Close(fd)
		return nil, os.ErrInvalid
	}
	return file, nil
}

// OpenDirectoryNoFollow opens a slash-relative directory below the retained
// root without following any path component. An empty relative path returns
// a new descriptor for the retained root itself.
func (root *NoFollowRoot) OpenDirectoryNoFollow(relative string) (*os.File, error) {
	if root == nil || root.file == nil || (relative != "" && !validNoFollowRelativePath(relative)) {
		return nil, os.ErrInvalid
	}
	components := []string{"."}
	if relative != "" {
		components = strings.Split(relative, "/")
	}
	current := int(root.file.Fd()) // #nosec G115 -- retained from a successful nonnegative descriptor.
	owned := -1
	for _, component := range components {
		next, err := unix.Openat(current, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			if owned >= 0 {
				_ = unix.Close(owned)
			}
			return nil, err
		}
		if owned >= 0 {
			_ = unix.Close(owned)
		}
		owned = next
		current = next
	}
	file := os.NewFile(uintptr(owned), "portable-runtime-directory") // #nosec G115 -- unix.Openat returns a nonnegative descriptor.
	if file == nil {
		_ = unix.Close(owned)
		return nil, os.ErrInvalid
	}
	return file, nil
}

func validNoFollowRelativePath(relative string) bool {
	if relative == "" || relative != path.Clean(relative) || path.IsAbs(relative) || strings.Contains(relative, "\\") {
		return false
	}
	for _, component := range strings.Split(relative, "/") {
		if component == "" || component == "." || component == ".." {
			return false
		}
	}
	return true
}
