//go:build linux

package portableruntime

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/easel/fizeau/internal/harnesses"
	"golang.org/x/sys/unix"
)

type fileIdentity struct {
	dev       uint64
	ino       uint64
	mode      uint32
	nlink     uint64
	uid       uint32
	gid       uint32
	size      int64
	mtimeSec  int64
	mtimeNsec int64
	ctimeSec  int64
	ctimeNsec int64
}

func descriptorFD(file *os.File) int {
	return int(file.Fd()) // #nosec G115 -- file was constructed from a checked nonnegative Unix descriptor.
}

func newDescriptorFile(fd int, name string) *os.File {
	return os.NewFile(uintptr(fd), name) // #nosec G115 -- callers pass successful nonnegative Unix descriptors.
}

func identityFromStat(stat *unix.Stat_t) fileIdentity {
	return fileIdentity{
		dev:       uint64(stat.Dev),
		ino:       stat.Ino,
		mode:      stat.Mode,
		nlink:     uint64(stat.Nlink),
		uid:       stat.Uid,
		gid:       stat.Gid,
		size:      stat.Size,
		mtimeSec:  stat.Mtim.Sec,
		mtimeNsec: stat.Mtim.Nsec,
		ctimeSec:  stat.Ctim.Sec,
		ctimeNsec: stat.Ctim.Nsec,
	}
}

func identityOfFD(fd int) (fileIdentity, error) {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return fileIdentity{}, err
	}
	return identityFromStat(&stat), nil
}

func sameIdentity(left, right fileIdentity) bool {
	return left == right
}

func sameDirectoryObject(left, right fileIdentity) bool {
	return left.dev == right.dev && left.ino == right.ino &&
		left.mode == right.mode && left.uid == right.uid && left.gid == right.gid &&
		left.mode&unix.S_IFMT == unix.S_IFDIR
}

type stageHandle struct {
	name     string
	path     string
	file     *os.File
	identity fileIdentity
}

type treeSourceIdentity struct {
	path     string
	linkText string
	entry    fileIdentity
	resolved fileIdentity
}

type sourceReceipt struct {
	asset    harnesses.PortableRuntimeAsset
	ancestry []fileIdentity
	tree     []treeSourceIdentity
}

type destinationHandle struct {
	absolute          string
	directory         *os.File
	parent            *os.File
	parentIdentity    fileIdentity
	directoryIdentity fileIdentity
	ancestry          []fileIdentity
	components        []string
}

func openDestination(destination string) (*destinationHandle, error) {
	if destination == "" || strings.ContainsRune(destination, 0) || !filepath.IsAbs(destination) || filepath.Clean(destination) != destination {
		return nil, errors.New("destination path is empty or invalid")
	}
	absolute, err := filepath.Abs(destination)
	if err != nil || filepath.Clean(absolute) != absolute || absolute == string(filepath.Separator) {
		return nil, errors.New("destination path is not a clean directory path")
	}
	components := splitAbsolutePath(absolute)
	if len(components) == 0 {
		return nil, errors.New("destination path has no directory component")
	}

	rootFD, err := unix.Open(string(filepath.Separator), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	current := newDescriptorFile(rootFD, "portable-runtime-destination-root")
	ancestry := make([]fileIdentity, 0, len(components)+1)
	rootIdentity, err := identityOfFD(rootFD)
	if err != nil {
		_ = current.Close()
		return nil, err
	}
	ancestry = append(ancestry, rootIdentity)

	var parent *os.File
	for index, component := range components {
		fd, openErr := unix.Openat(descriptorFD(current), component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if openErr != nil {
			if parent != nil {
				_ = parent.Close()
			}
			_ = current.Close()
			return nil, openErr
		}
		next := newDescriptorFile(fd, "portable-runtime-destination")
		identity, statErr := identityOfFD(fd)
		if statErr != nil || identity.mode&unix.S_IFMT != unix.S_IFDIR {
			_ = next.Close()
			if parent != nil {
				_ = parent.Close()
			}
			_ = current.Close()
			if statErr != nil {
				return nil, statErr
			}
			return nil, errors.New("destination component is not a directory")
		}
		ancestry = append(ancestry, identity)
		if index == len(components)-1 {
			parent = current
			current = next
			break
		}
		_ = current.Close()
		current = next
	}
	if parent == nil {
		_ = current.Close()
		return nil, errors.New("destination parent is unavailable")
	}
	parentIdentity, err := identityOfFD(descriptorFD(parent))
	if err != nil {
		_ = current.Close()
		_ = parent.Close()
		return nil, err
	}
	handle := &destinationHandle{
		absolute:          absolute,
		directory:         current,
		parent:            parent,
		parentIdentity:    parentIdentity,
		directoryIdentity: ancestry[len(ancestry)-1],
		ancestry:          ancestry,
		components:        components,
	}
	if err := handle.revalidateEmpty(); err != nil {
		handle.close()
		return nil, err
	}
	return handle, nil
}

func splitAbsolutePath(value string) []string {
	trimmed := strings.TrimPrefix(filepath.Clean(value), string(filepath.Separator))
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, string(filepath.Separator))
}

func (destination *destinationHandle) close() {
	if destination == nil {
		return
	}
	if destination.directory != nil {
		_ = destination.directory.Close()
		destination.directory = nil
	}
	if destination.parent != nil {
		_ = destination.parent.Close()
		destination.parent = nil
	}
}

func (destination *destinationHandle) takeDirectory() *os.File {
	if destination == nil {
		return nil
	}
	directory := destination.directory
	destination.directory = nil
	return directory
}

func (destination *destinationHandle) revalidateEmpty() error {
	if destination == nil || destination.directory == nil || destination.parent == nil {
		return errors.New("destination handle is closed")
	}
	if err := destination.revalidatePath(); err != nil {
		return err
	}
	if identity, err := identityOfFD(descriptorFD(destination.parent)); err != nil || !sameDirectoryObject(identity, destination.parentIdentity) {
		return errors.New("destination parent identity changed")
	}
	if identity, err := identityOfFD(descriptorFD(destination.directory)); err != nil || !sameDirectoryObject(identity, destination.directoryIdentity) {
		return errors.New("destination identity changed")
	}
	entries, err := readDirectoryNames(descriptorFD(destination.directory))
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return errors.New("destination directory is not empty")
	}
	return nil
}

func (destination *destinationHandle) revalidatePath() error {
	rootFD, err := unix.Open(string(filepath.Separator), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	current := newDescriptorFile(rootFD, "portable-runtime-path-revalidation")
	defer func() { _ = current.Close() }()
	identity, err := identityOfFD(rootFD)
	if err != nil || !sameDirectoryObject(identity, destination.ancestry[0]) {
		return errors.New("destination root identity changed")
	}
	for index, component := range destination.components {
		fd, openErr := unix.Openat(descriptorFD(current), component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if openErr != nil {
			return errors.New("destination ancestry changed")
		}
		next := newDescriptorFile(fd, "portable-runtime-path-revalidation")
		_ = current.Close()
		current = next
		identity, err = identityOfFD(fd)
		if err != nil || !sameDirectoryObject(identity, destination.ancestry[index+1]) {
			return errors.New("destination ancestry changed")
		}
	}
	return nil
}

func (destination *destinationHandle) createStage() (*stageHandle, error) {
	if err := destination.revalidateEmpty(); err != nil {
		return nil, err
	}
	for attempt := 0; attempt < 32; attempt++ {
		var nonce [12]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return nil, err
		}
		name := ".fizeau-runtime-" + hex.EncodeToString(nonce[:])
		if err := unix.Mkdirat(descriptorFD(destination.parent), name, 0o700); err != nil {
			if errors.Is(err, unix.EEXIST) {
				continue
			}
			return nil, err
		}
		fd, err := unix.Openat(descriptorFD(destination.parent), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			_ = unix.Unlinkat(descriptorFD(destination.parent), name, unix.AT_REMOVEDIR)
			return nil, err
		}
		if err := unix.Fchmod(fd, 0o700); err != nil {
			_ = unix.Close(fd)
			_ = unix.Unlinkat(descriptorFD(destination.parent), name, unix.AT_REMOVEDIR)
			return nil, err
		}
		identity, err := identityOfFD(fd)
		if err != nil {
			_ = unix.Close(fd)
			_ = unix.Unlinkat(descriptorFD(destination.parent), name, unix.AT_REMOVEDIR)
			return nil, err
		}
		file := newDescriptorFile(fd, "portable-runtime-stage")
		return &stageHandle{name: name, path: fmt.Sprintf("/proc/self/fd/%d", fd), file: file, identity: identity}, nil
	}
	return nil, errors.New("could not allocate a private staging directory")
}

func (destination *destinationHandle) removeStage(stage *stageHandle) error {
	if destination == nil || destination.parent == nil || stage == nil {
		return nil
	}
	defer func() {
		if stage.file != nil {
			_ = stage.file.Close()
			stage.file = nil
		}
	}()
	return removeOwnedDirectoryAt(descriptorFD(destination.parent), stage.name, stage.identity)
}

func (destination *destinationHandle) commit(stage *stageHandle) error {
	if destination == nil || destination.directory == nil || destination.parent == nil || stage == nil || stage.file == nil {
		return errors.New("commit handles are incomplete")
	}
	if err := destination.revalidateEmpty(); err != nil {
		return err
	}
	if identity, err := identityOfFD(descriptorFD(stage.file)); err != nil || !sameDirectoryObject(identity, stage.identity) {
		return errors.New("staging identity changed")
	}
	if err := unix.Renameat2(descriptorFD(destination.parent), stage.name, descriptorFD(destination.directory), "runtime", unix.RENAME_NOREPLACE); err != nil {
		return err
	}
	stage.name = ""
	return destination.validateCommittedStage(stage)
}

func (destination *destinationHandle) validateCommittedStage(stage *stageHandle) error {
	rollback := func(cause error) error {
		if cleanupErr := removeOwnedDirectoryAt(descriptorFD(destination.directory), "runtime", stage.identity); cleanupErr != nil {
			return fmt.Errorf("%w: post-commit rollback failed: %v", ErrCleanupIncomplete, cause)
		}
		return cause
	}
	runtimeFD, err := unix.Openat(descriptorFD(destination.directory), "runtime", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return rollback(errors.New("committed runtime cannot be reopened"))
	}
	runtimeIdentity, statErr := identityOfFD(runtimeFD)
	_ = unix.Close(runtimeFD)
	if statErr != nil || !sameDirectoryObject(runtimeIdentity, stage.identity) {
		return rollback(errors.New("committed runtime identity changed"))
	}
	if err := destination.revalidatePath(); err != nil {
		return rollback(err)
	}
	entries, err := readDirectoryNames(descriptorFD(destination.directory))
	if err != nil || len(entries) != 1 || entries[0] != "runtime" {
		return rollback(errors.New("destination changed during commit"))
	}
	_ = stage.file.Close()
	stage.file = nil
	return nil
}

func removeCommittedRuntime(anchor *os.File, identity fileIdentity) error {
	if anchor == nil {
		return nil
	}
	return removeOwnedDirectoryAt(descriptorFD(anchor), "runtime", identity)
}

func removeOwnedDirectoryAt(parentFD int, name string, expected fileIdentity) error {
	if name == "" {
		return nil
	}
	var stat unix.Stat_t
	if err := unix.Fstatat(parentFD, name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		if errors.Is(err, unix.ENOENT) {
			return errors.New("owned directory is missing")
		}
		return err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR || !sameDirectoryObject(identityFromStat(&stat), expected) {
		return errors.New("owned directory identity changed")
	}
	fd, err := unix.Openat(parentFD, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	if identity, statErr := identityOfFD(fd); statErr != nil || !sameDirectoryObject(identity, expected) {
		_ = unix.Close(fd)
		return errors.New("owned directory identity changed during open")
	}
	if err := removeDirectoryContents(fd); err != nil {
		_ = unix.Close(fd)
		return err
	}
	if err := unix.Close(fd); err != nil {
		return err
	}
	var final unix.Stat_t
	if err := unix.Fstatat(parentFD, name, &final, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		if errors.Is(err, unix.ENOENT) {
			return nil
		}
		return err
	}
	if !sameDirectoryObject(identityFromStat(&final), expected) {
		return errors.New("owned directory identity changed before removal")
	}
	if err := unix.Unlinkat(parentFD, name, unix.AT_REMOVEDIR); err != nil && !errors.Is(err, unix.ENOENT) {
		return err
	}
	return nil
}

func removeDirectoryContents(directoryFD int) error {
	names, err := readDirectoryNames(directoryFD)
	if err != nil {
		return err
	}
	for _, name := range names {
		var stat unix.Stat_t
		if err := unix.Fstatat(directoryFD, name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			if errors.Is(err, unix.ENOENT) {
				continue
			}
			return err
		}
		if stat.Mode&unix.S_IFMT == unix.S_IFDIR {
			childFD, err := unix.Openat(directoryFD, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			if err != nil {
				return err
			}
			before := identityFromStat(&stat)
			after, statErr := identityOfFD(childFD)
			if statErr != nil || !sameDirectoryObject(before, after) {
				_ = unix.Close(childFD)
				return errors.New("cleanup child identity changed")
			}
			if err := removeDirectoryContents(childFD); err != nil {
				_ = unix.Close(childFD)
				return err
			}
			if err := unix.Close(childFD); err != nil {
				return err
			}
			var final unix.Stat_t
			if err := unix.Fstatat(directoryFD, name, &final, unix.AT_SYMLINK_NOFOLLOW); err != nil {
				if errors.Is(err, unix.ENOENT) {
					continue
				}
				return err
			}
			if !sameDirectoryObject(identityFromStat(&final), before) {
				return errors.New("cleanup child identity changed before removal")
			}
			if err := unix.Unlinkat(directoryFD, name, unix.AT_REMOVEDIR); err != nil && !errors.Is(err, unix.ENOENT) {
				return err
			}
			continue
		}
		if err := unix.Unlinkat(directoryFD, name, 0); err != nil && !errors.Is(err, unix.ENOENT) {
			return err
		}
	}
	return nil
}

func readDirectoryNames(directoryFD int) ([]string, error) {
	fd, err := unix.Openat(directoryFD, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	file := newDescriptorFile(fd, "portable-runtime-directory-read")
	names, readErr := file.Readdirnames(-1)
	closeErr := file.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	sort.Strings(names)
	return names, nil
}

func verifyRestrictiveMaterialization(stage *stageHandle) error {
	if stage == nil || stage.file == nil {
		return errors.New("staging handle is unavailable")
	}
	root, err := identityOfFD(descriptorFD(stage.file))
	if err != nil || root.mode&0o777 != 0o700 {
		return errors.New("staging root has a permissive mode")
	}
	return verifyRestrictiveDirectory(descriptorFD(stage.file))
}

func verifyRestrictiveDirectory(directoryFD int) error {
	names, err := readDirectoryNames(directoryFD)
	if err != nil {
		return err
	}
	for _, name := range names {
		var stat unix.Stat_t
		if err := unix.Fstatat(directoryFD, name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return err
		}
		switch stat.Mode & unix.S_IFMT {
		case unix.S_IFREG:
			if stat.Mode&0o077 != 0 {
				return errors.New("staging regular file has group or world permissions")
			}
		case unix.S_IFDIR:
			if stat.Mode&0o777 != 0o700 {
				return errors.New("staging directory has a permissive mode")
			}
			childFD, err := unix.Openat(directoryFD, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			if err != nil {
				return err
			}
			err = verifyRestrictiveDirectory(childFD)
			_ = unix.Close(childFD)
			if err != nil {
				return err
			}
		default:
			return errors.New("staging tree contains an unsupported file type")
		}
	}
	return nil
}

func materializeAsset(ctx context.Context, stage *stageHandle, asset harnesses.PortableRuntimeAsset) (string, sourceReceipt, error) {
	if stage == nil || stage.file == nil {
		return "", sourceReceipt{}, errors.New("staging handle is unavailable")
	}
	receipt, err := captureSourceReceipt(asset)
	if err != nil {
		return "", sourceReceipt{}, err
	}
	parentFD, leaf, err := createTargetParent(descriptorFD(stage.file), asset.Target)
	if err != nil {
		return "", sourceReceipt{}, err
	}
	defer unix.Close(parentFD)

	switch asset.PathKind {
	case harnesses.PortableRuntimePathFile:
		source, ancestry, err := openAbsoluteNoFollow(asset.Source, false)
		if err != nil {
			return "", sourceReceipt{}, err
		}
		defer source.Close()
		before, err := identityOfFD(descriptorFD(source))
		if err != nil || before.mode&unix.S_IFMT != unix.S_IFREG {
			return "", sourceReceipt{}, errors.New("asset source is not a regular file")
		}
		destinationFD, err := unix.Openat(parentFD, leaf, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
		if err != nil {
			return "", sourceReceipt{}, err
		}
		mode := restrictiveDirectFileMode(asset)
		if err := unix.Fchmod(destinationFD, mode); err != nil {
			_ = unix.Close(destinationFD)
			return "", sourceReceipt{}, err
		}
		digest, err := copyAndHash(ctx, source, destinationFD)
		if syncErr := unix.Fsync(destinationFD); err == nil && syncErr != nil {
			err = syncErr
		}
		if closeErr := unix.Close(destinationFD); err == nil && closeErr != nil {
			err = closeErr
		}
		if err != nil {
			return "", sourceReceipt{}, err
		}
		after, err := identityOfFD(descriptorFD(source))
		if err != nil || !sameIdentity(before, after) || digest != asset.ContentSHA256 {
			return "", sourceReceipt{}, errors.New("asset source changed during copy")
		}
		if err := revalidateAbsoluteNoFollow(asset.Source, ancestry); err != nil {
			return "", sourceReceipt{}, err
		}
		if err := verifyAssetSource(ctx, receipt); err != nil {
			return "", sourceReceipt{}, err
		}
		return digest, receipt, nil
	case harnesses.PortableRuntimePathTree:
		if declared, err := harnesses.PortableRuntimeTreeDigest(asset.Source); err != nil || declared != asset.ContentSHA256 {
			return "", sourceReceipt{}, errors.New("asset tree does not match its declared identity")
		}
		source, ancestry, err := openAbsoluteNoFollow(asset.Source, true)
		if err != nil {
			return "", sourceReceipt{}, err
		}
		defer source.Close()
		before, err := identityOfFD(descriptorFD(source))
		if err != nil || before.mode&unix.S_IFMT != unix.S_IFDIR {
			return "", sourceReceipt{}, errors.New("asset source is not a directory")
		}
		if err := unix.Mkdirat(parentFD, leaf, 0o700); err != nil {
			return "", sourceReceipt{}, err
		}
		destinationFD, err := unix.Openat(parentFD, leaf, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return "", sourceReceipt{}, err
		}
		if err := unix.Fchmod(destinationFD, 0o700); err != nil {
			_ = unix.Close(destinationFD)
			return "", sourceReceipt{}, err
		}
		err = copyTree(ctx, descriptorFD(source), descriptorFD(source), destinationFD, "", asset)
		if syncErr := unix.Fsync(destinationFD); err == nil && syncErr != nil {
			err = syncErr
		}
		if closeErr := unix.Close(destinationFD); err == nil && closeErr != nil {
			err = closeErr
		}
		if err != nil {
			return "", sourceReceipt{}, err
		}
		after, err := identityOfFD(descriptorFD(source))
		if err != nil || !sameIdentity(before, after) {
			return "", sourceReceipt{}, errors.New("asset tree changed during copy")
		}
		if err := revalidateAbsoluteNoFollow(asset.Source, ancestry); err != nil {
			return "", sourceReceipt{}, err
		}
		if declared, err := harnesses.PortableRuntimeTreeDigest(asset.Source); err != nil || declared != asset.ContentSHA256 {
			return "", sourceReceipt{}, errors.New("asset tree changed after copy")
		}
		if err := verifyAssetSource(ctx, receipt); err != nil {
			return "", sourceReceipt{}, err
		}
		materialized, err := harnesses.PortableRuntimeTreeDigest(filepath.Join(stage.path, filepath.FromSlash(asset.Target)))
		if err != nil {
			return "", sourceReceipt{}, err
		}
		return materialized, receipt, nil
	default:
		return "", sourceReceipt{}, errors.New("asset has an unknown path kind")
	}
}

func createTargetParent(rootFD int, target string) (int, string, error) {
	parts := strings.Split(target, "/")
	if len(parts) == 0 || parts[len(parts)-1] == "" {
		return -1, "", errors.New("asset target is invalid")
	}
	fd, err := unix.Openat(rootFD, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, "", err
	}
	for _, component := range parts[:len(parts)-1] {
		if err := unix.Mkdirat(fd, component, 0o700); err != nil && !errors.Is(err, unix.EEXIST) {
			_ = unix.Close(fd)
			return -1, "", err
		}
		next, err := unix.Openat(fd, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			_ = unix.Close(fd)
			return -1, "", err
		}
		if err := unix.Fchmod(next, 0o700); err != nil {
			_ = unix.Close(next)
			_ = unix.Close(fd)
			return -1, "", err
		}
		_ = unix.Close(fd)
		fd = next
	}
	return fd, parts[len(parts)-1], nil
}

func closeDescriptor(fd int) {
	if fd >= 0 {
		_ = unix.Close(fd)
	}
}

func openExclusiveRegularAt(parentFD int, leaf string, mode uint32) (int, error) {
	fd, err := unix.Openat(parentFD, leaf, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, mode)
	if err != nil {
		return -1, err
	}
	if err := unix.Fchmod(fd, mode); err != nil {
		_ = unix.Close(fd)
		return -1, err
	}
	return fd, nil
}

func openTargetRegular(rootFD int, target string) (*os.File, error) {
	parts := strings.Split(target, "/")
	if len(parts) == 0 || parts[len(parts)-1] == "" {
		return nil, errors.New("generated target is invalid")
	}
	current, err := unix.Openat(rootFD, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	for _, component := range parts[:len(parts)-1] {
		next, openErr := unix.Openat(current, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		_ = unix.Close(current)
		if openErr != nil {
			return nil, openErr
		}
		current = next
	}
	fd, err := unix.Openat(current, parts[len(parts)-1], unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	_ = unix.Close(current)
	if err != nil {
		return nil, err
	}
	identity, err := identityOfFD(fd)
	if err != nil || identity.mode&unix.S_IFMT != unix.S_IFREG {
		_ = unix.Close(fd)
		if err != nil {
			return nil, err
		}
		return nil, errors.New("generated target is not a regular file")
	}
	return newDescriptorFile(fd, "portable-runtime-generated-verification"), nil
}

func openAbsoluteNoFollow(source string, directory bool) (*os.File, []fileIdentity, error) {
	if source == "" || !filepath.IsAbs(source) || filepath.Clean(source) != source || strings.ContainsRune(source, 0) {
		return nil, nil, errors.New("asset source path is invalid")
	}
	components := splitAbsolutePath(source)
	if len(components) == 0 {
		return nil, nil, errors.New("asset source path is invalid")
	}
	rootFD, err := unix.Open(string(filepath.Separator), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}
	current := newDescriptorFile(rootFD, "portable-runtime-source")
	ancestry := make([]fileIdentity, 0, len(components)+1)
	rootIdentity, err := identityOfFD(rootFD)
	if err != nil {
		_ = current.Close()
		return nil, nil, err
	}
	ancestry = append(ancestry, rootIdentity)
	for index, component := range components {
		flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC
		if index < len(components)-1 || directory {
			flags |= unix.O_DIRECTORY
		}
		fd, openErr := unix.Openat(descriptorFD(current), component, flags, 0)
		if openErr != nil {
			_ = current.Close()
			return nil, nil, openErr
		}
		next := newDescriptorFile(fd, "portable-runtime-source")
		_ = current.Close()
		current = next
		identity, statErr := identityOfFD(fd)
		if statErr != nil {
			_ = current.Close()
			return nil, nil, statErr
		}
		ancestry = append(ancestry, identity)
	}
	return current, ancestry, nil
}

func revalidateAbsoluteNoFollow(source string, expected []fileIdentity) error {
	directory := len(expected) > 0 && expected[len(expected)-1].mode&unix.S_IFMT == unix.S_IFDIR
	file, actual, err := openAbsoluteNoFollow(source, directory)
	if err != nil {
		return errors.New("asset source ancestry changed")
	}
	_ = file.Close()
	if len(actual) != len(expected) {
		return errors.New("asset source ancestry changed")
	}
	for index := range actual {
		finalRegular := index == len(actual)-1 && actual[index].mode&unix.S_IFMT == unix.S_IFREG
		if (finalRegular && !sameIdentity(actual[index], expected[index])) || (!finalRegular && !sameDirectoryObject(actual[index], expected[index])) {
			return errors.New("asset source ancestry changed")
		}
	}
	return nil
}

func copyAndHash(ctx context.Context, source *os.File, destinationFD int) (string, error) {
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	hasher := sha256.New()
	buffer := make([]byte, 128*1024)
	for {
		if err := checkContext(ctx); err != nil {
			return "", err
		}
		read, readErr := source.Read(buffer)
		if read > 0 {
			chunk := buffer[:read]
			if _, err := hasher.Write(chunk); err != nil {
				return "", err
			}
			for len(chunk) > 0 {
				written, writeErr := unix.Write(destinationFD, chunk)
				if writeErr != nil {
					return "", writeErr
				}
				if written == 0 {
					return "", io.ErrShortWrite
				}
				chunk = chunk[written:]
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return "", readErr
		}
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func restrictiveDirectFileMode(asset harnesses.PortableRuntimeAsset) uint32 {
	mode := uint32(0o600)
	if asset.Executable {
		mode |= 0o100
	}
	return mode
}

func restrictiveTreeFileMode(asset harnesses.PortableRuntimeAsset, sourceMode uint32) uint32 {
	mode := uint32(0o600)
	allowExecute := asset.Kind == harnesses.PortableRuntimeAssetExecutable ||
		asset.Kind == harnesses.PortableRuntimeAssetSupport ||
		asset.Kind == harnesses.PortableRuntimeAssetInstallTree
	if allowExecute && sourceMode&0o100 != 0 {
		mode |= 0o100
	}
	return mode
}

func copyTree(ctx context.Context, rootSourceFD, sourceFD, destinationFD int, relative string, asset harnesses.PortableRuntimeAsset) error {
	names, err := readDirectoryNames(sourceFD)
	if err != nil {
		return err
	}
	for _, name := range names {
		if err := checkContext(ctx); err != nil {
			return err
		}
		var before unix.Stat_t
		if err := unix.Fstatat(sourceFD, name, &before, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return err
		}
		childRelative := name
		if relative != "" {
			childRelative = relative + "/" + name
		}
		switch before.Mode & unix.S_IFMT {
		case unix.S_IFDIR:
			if err := unix.Mkdirat(destinationFD, name, 0o700); err != nil {
				return err
			}
			sourceChild, err := unix.Openat(sourceFD, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			if err != nil {
				return err
			}
			destinationChild, err := unix.Openat(destinationFD, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			if err != nil {
				_ = unix.Close(sourceChild)
				return err
			}
			if err := unix.Fchmod(destinationChild, 0o700); err != nil {
				_ = unix.Close(sourceChild)
				_ = unix.Close(destinationChild)
				return err
			}
			err = copyTree(ctx, rootSourceFD, sourceChild, destinationChild, childRelative, asset)
			sourceAfter, sourceStatErr := identityOfFD(sourceChild)
			_ = unix.Close(sourceChild)
			_ = unix.Close(destinationChild)
			if err != nil {
				return err
			}
			if sourceStatErr != nil || !sameIdentity(identityFromStat(&before), sourceAfter) {
				return errors.New("asset tree directory changed during copy")
			}
		case unix.S_IFREG:
			if err := copyTreeRegular(ctx, sourceFD, name, destinationFD, name, identityFromStat(&before), asset); err != nil {
				return err
			}
		case unix.S_IFLNK:
			if err := copyTreeSymlink(ctx, rootSourceFD, sourceFD, name, destinationFD, childRelative, identityFromStat(&before), asset); err != nil {
				return err
			}
		default:
			return errors.New("asset tree contains a special file")
		}
	}
	return nil
}

func copyTreeRegular(ctx context.Context, sourceParentFD int, sourceName string, destinationParentFD int, destinationName string, before fileIdentity, asset harnesses.PortableRuntimeAsset) error {
	sourceFD, err := unix.Openat(sourceParentFD, sourceName, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	source := newDescriptorFile(sourceFD, "portable-runtime-tree-source")
	defer source.Close()
	opened, err := identityOfFD(sourceFD)
	if err != nil || !sameIdentity(before, opened) {
		return errors.New("asset tree file changed during open")
	}
	destinationFD, err := unix.Openat(destinationParentFD, destinationName, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return err
	}
	if err := unix.Fchmod(destinationFD, restrictiveTreeFileMode(asset, before.mode)); err != nil {
		_ = unix.Close(destinationFD)
		return err
	}
	_, err = copyAndHash(ctx, source, destinationFD)
	if syncErr := unix.Fsync(destinationFD); err == nil && syncErr != nil {
		err = syncErr
	}
	if closeErr := unix.Close(destinationFD); err == nil && closeErr != nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	after, err := identityOfFD(sourceFD)
	if err != nil || !sameIdentity(before, after) {
		return errors.New("asset tree file changed during copy")
	}
	return nil
}

func copyTreeSymlink(ctx context.Context, rootSourceFD, sourceParentFD int, name string, destinationParentFD int, relative string, before fileIdentity, asset harnesses.PortableRuntimeAsset) error {
	linkBefore, err := readlinkAt(sourceParentFD, name)
	if err != nil {
		return err
	}
	resolvedFD, err := openSafeTreeSymlink(rootSourceFD, asset.Source, relative)
	if err != nil {
		return errors.New("asset tree symlink is not a safe in-tree file")
	}
	resolved := newDescriptorFile(resolvedFD, "portable-runtime-tree-symlink-source")
	defer resolved.Close()
	resolvedIdentity, err := identityOfFD(resolvedFD)
	if err != nil || resolvedIdentity.mode&unix.S_IFMT != unix.S_IFREG {
		return errors.New("asset tree symlink does not resolve to a regular file")
	}
	destinationFD, err := unix.Openat(destinationParentFD, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return err
	}
	if err := unix.Fchmod(destinationFD, restrictiveTreeFileMode(asset, resolvedIdentity.mode)); err != nil {
		_ = unix.Close(destinationFD)
		return err
	}
	_, err = copyAndHash(ctx, resolved, destinationFD)
	if syncErr := unix.Fsync(destinationFD); err == nil && syncErr != nil {
		err = syncErr
	}
	if closeErr := unix.Close(destinationFD); err == nil && closeErr != nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	var after unix.Stat_t
	linkAfter, linkErr := readlinkAt(sourceParentFD, name)
	statErr := unix.Fstatat(sourceParentFD, name, &after, unix.AT_SYMLINK_NOFOLLOW)
	resolvedAfter, resolvedStatErr := identityOfFD(resolvedFD)
	if linkErr != nil || statErr != nil || resolvedStatErr != nil || linkBefore != linkAfter ||
		!sameIdentity(before, identityFromStat(&after)) || !sameIdentity(resolvedIdentity, resolvedAfter) {
		return errors.New("asset tree symlink changed during copy")
	}
	return nil
}

func readlinkAt(directoryFD int, name string) (string, error) {
	buffer := make([]byte, 256)
	for len(buffer) <= 64*1024 {
		length, err := unix.Readlinkat(directoryFD, name, buffer)
		if err != nil {
			return "", err
		}
		if length < len(buffer) {
			return string(buffer[:length]), nil
		}
		buffer = make([]byte, len(buffer)*2)
	}
	return "", syscall.ENAMETOOLONG
}

func captureSourceReceipt(asset harnesses.PortableRuntimeAsset) (sourceReceipt, error) {
	directory := asset.PathKind == harnesses.PortableRuntimePathTree
	source, ancestry, err := openAbsoluteNoFollow(asset.Source, directory)
	if err != nil {
		return sourceReceipt{}, err
	}
	defer source.Close()
	receipt := sourceReceipt{asset: asset, ancestry: ancestry}
	if directory {
		if err := captureTreeSourceIdentities(descriptorFD(source), asset.Source, descriptorFD(source), "", &receipt.tree); err != nil {
			return sourceReceipt{}, err
		}
	}
	return receipt, nil
}

func captureTreeSourceIdentities(rootFD int, rootPath string, directoryFD int, relative string, identities *[]treeSourceIdentity) error {
	names, err := readDirectoryNames(directoryFD)
	if err != nil {
		return err
	}
	for _, name := range names {
		childRelative := name
		if relative != "" {
			childRelative = relative + "/" + name
		}
		var stat unix.Stat_t
		if err := unix.Fstatat(directoryFD, name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return err
		}
		entry := treeSourceIdentity{path: childRelative, entry: identityFromStat(&stat)}
		switch stat.Mode & unix.S_IFMT {
		case unix.S_IFDIR:
			childFD, err := unix.Openat(directoryFD, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			if err != nil {
				return err
			}
			opened, statErr := identityOfFD(childFD)
			if statErr != nil || !sameIdentity(entry.entry, opened) {
				_ = unix.Close(childFD)
				return errors.New("asset tree changed during identity capture")
			}
			*identities = append(*identities, entry)
			err = captureTreeSourceIdentities(rootFD, rootPath, childFD, childRelative, identities)
			_ = unix.Close(childFD)
			if err != nil {
				return err
			}
		case unix.S_IFREG:
			childFD, err := unix.Openat(directoryFD, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			if err != nil {
				return err
			}
			opened, statErr := identityOfFD(childFD)
			_ = unix.Close(childFD)
			if statErr != nil || !sameIdentity(entry.entry, opened) {
				return errors.New("asset tree changed during identity capture")
			}
			*identities = append(*identities, entry)
		case unix.S_IFLNK:
			entry.linkText, err = readlinkAt(directoryFD, name)
			if err != nil {
				return err
			}
			resolvedFD, err := openSafeTreeSymlink(rootFD, rootPath, childRelative)
			if err != nil {
				return errors.New("asset tree symlink is not a safe in-tree file")
			}
			entry.resolved, err = identityOfFD(resolvedFD)
			_ = unix.Close(resolvedFD)
			if err != nil || entry.resolved.mode&unix.S_IFMT != unix.S_IFREG {
				return errors.New("asset tree symlink does not resolve to a regular file")
			}
			*identities = append(*identities, entry)
		default:
			return errors.New("asset tree contains a special file")
		}
	}
	return nil
}

func openSafeTreeSymlink(rootFD int, rootPath, relative string) (int, error) {
	fd, err := unix.Openat2(rootFD, relative, &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_MAGICLINKS,
	})
	if err == nil {
		return fd, nil
	}
	resolved, resolveErr := filepath.EvalSymlinks(filepath.Join(rootPath, filepath.FromSlash(relative)))
	if resolveErr != nil {
		return -1, errors.New("asset tree symlink cannot be resolved")
	}
	resolved = filepath.Clean(resolved)
	relativeResolved, relErr := filepath.Rel(rootPath, resolved)
	if relErr != nil || relativeResolved == "." || relativeResolved == ".." || strings.HasPrefix(relativeResolved, ".."+string(filepath.Separator)) {
		return -1, errors.New("asset tree symlink escapes its source root")
	}
	return unix.Openat2(rootFD, filepath.ToSlash(relativeResolved), &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_MAGICLINKS | unix.RESOLVE_NO_SYMLINKS,
	})
}

func sameSourceReceipt(left, right sourceReceipt) bool {
	if left.asset != right.asset || len(left.ancestry) != len(right.ancestry) || len(left.tree) != len(right.tree) {
		return false
	}
	for index := range left.ancestry {
		finalRegular := index == len(left.ancestry)-1 && left.ancestry[index].mode&unix.S_IFMT == unix.S_IFREG
		if (finalRegular && !sameIdentity(left.ancestry[index], right.ancestry[index])) ||
			(!finalRegular && !sameDirectoryObject(left.ancestry[index], right.ancestry[index])) {
			return false
		}
	}
	for index := range left.tree {
		if left.tree[index] != right.tree[index] {
			return false
		}
	}
	return true
}

func verifyAssetSource(ctx context.Context, receipt sourceReceipt) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	current, err := captureSourceReceipt(receipt.asset)
	if err != nil || !sameSourceReceipt(receipt, current) {
		return errors.New("asset source identity changed")
	}
	var digest string
	if receipt.asset.PathKind == harnesses.PortableRuntimePathTree {
		digest, err = harnesses.PortableRuntimeTreeDigest(receipt.asset.Source)
	} else {
		digest, err = harnesses.PortableRuntimeFileDigest(receipt.asset.Source)
	}
	if err != nil || digest != receipt.asset.ContentSHA256 {
		return errors.New("asset source no longer matches its declared identity")
	}
	return nil
}
