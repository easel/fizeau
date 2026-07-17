//go:build linux

package portableruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/safefs"
	"golang.org/x/sys/unix"
)

func ensureActivationStageDirectory(stage *stageHandle, target string) error {
	if stage == nil || stage.file == nil || !cleanTarget(target) {
		return os.ErrInvalid
	}
	fd, err := unix.Openat(descriptorFD(stage.file), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	for _, component := range strings.Split(target, "/") {
		if err := unix.Mkdirat(fd, component, 0o700); err != nil && !errors.Is(err, unix.EEXIST) {
			return err
		}
		next, err := unix.Openat(fd, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return err
		}
		if err := unix.Fchmod(next, 0o700); err != nil {
			_ = unix.Close(next)
			return err
		}
		_ = unix.Close(fd)
		fd = next
	}
	return nil
}

func activationAssetIdentity(root *safefs.NoFollowRoot, asset ManifestAsset) (fileIdentity, error) {
	var reference *os.File
	var err error
	if asset.PathKind == harnesses.PortableRuntimePathTree {
		reference, err = root.OpenDirectoryNoFollow(asset.Target)
	} else {
		reference, err = root.OpenReadNoFollow(asset.Target)
	}
	if err != nil {
		return fileIdentity{}, err
	}
	defer reference.Close()
	before, err := identityOfFD(descriptorFD(reference))
	if err != nil {
		return fileIdentity{}, err
	}
	if err := verifyActivationAsset(root, asset); err != nil {
		return fileIdentity{}, err
	}
	after, err := identityOfFD(descriptorFD(reference))
	if err != nil || !sameIdentity(before, after) {
		return fileIdentity{}, os.ErrInvalid
	}
	var current *os.File
	if asset.PathKind == harnesses.PortableRuntimePathTree {
		current, err = root.OpenDirectoryNoFollow(asset.Target)
	} else {
		current, err = root.OpenReadNoFollow(asset.Target)
	}
	if err != nil {
		return fileIdentity{}, err
	}
	currentIdentity, statErr := identityOfFD(descriptorFD(current))
	closeErr := current.Close()
	if statErr != nil || closeErr != nil || !sameIdentity(after, currentIdentity) {
		return fileIdentity{}, os.ErrInvalid
	}
	return after, nil
}

func copyActivationAsset(ctx context.Context, root *safefs.NoFollowRoot, stage *stageHandle, asset ManifestAsset, output string) error {
	if stage == nil || stage.file == nil || !cleanTarget(output) {
		return os.ErrInvalid
	}
	parentFD, leaf, err := createTargetParent(descriptorFD(stage.file), output)
	if err != nil {
		return err
	}
	defer unix.Close(parentFD)
	if asset.PathKind == harnesses.PortableRuntimePathTree {
		return copyActivationTreeAsset(ctx, root, parentFD, leaf, asset)
	}
	return copyActivationFileAsset(ctx, root, parentFD, leaf, asset)
}

func copyActivationFileAsset(ctx context.Context, root *safefs.NoFollowRoot, parentFD int, leaf string, asset ManifestAsset) error {
	source, err := root.OpenReadNoFollow(asset.Target)
	if err != nil {
		return err
	}
	defer source.Close()
	before, err := source.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm()&0o077 != 0 {
		return os.ErrInvalid
	}
	destinationFD, err := unix.Openat(parentFD, leaf, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return err
	}
	if err := unix.Fchmod(destinationFD, 0o600); err != nil {
		_ = unix.Close(destinationFD)
		return err
	}
	digest, err := copyAndHash(ctx, source, destinationFD)
	if syncErr := unix.Fsync(destinationFD); err == nil && syncErr != nil {
		err = syncErr
	}
	if closeErr := unix.Close(destinationFD); err == nil && closeErr != nil {
		err = closeErr
	}
	if err != nil || digest != asset.MaterializedSHA256 {
		return os.ErrInvalid
	}
	after, err := source.Stat()
	if err != nil || !sameActivationFileInfo(before, after) {
		return os.ErrInvalid
	}
	current, err := root.OpenReadNoFollow(asset.Target)
	if err != nil {
		return err
	}
	currentInfo, statErr := current.Stat()
	closeErr := current.Close()
	if statErr != nil || closeErr != nil || !sameActivationFileInfo(after, currentInfo) {
		return os.ErrInvalid
	}
	return nil
}

func copyActivationTreeAsset(ctx context.Context, root *safefs.NoFollowRoot, parentFD int, leaf string, asset ManifestAsset) error {
	source, err := root.OpenDirectoryNoFollow(asset.Target)
	if err != nil {
		return err
	}
	defer source.Close()
	before, err := activationDirectoryStat(source)
	if err != nil {
		return err
	}
	if err := unix.Mkdirat(parentFD, leaf, 0o700); err != nil {
		return err
	}
	destinationFD, err := unix.Openat(parentFD, leaf, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	if err := unix.Fchmod(destinationFD, 0o700); err != nil {
		_ = unix.Close(destinationFD)
		return err
	}
	records := make([]activationTreeRecord, 0)
	err = copyActivationTreeDirectory(ctx, descriptorFD(source), destinationFD, "", &records)
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
	if err := unix.Fstat(descriptorFD(source), &after); err != nil || !sameActivationStat(before, after) {
		return os.ErrInvalid
	}
	current, err := root.OpenDirectoryNoFollow(asset.Target)
	if err != nil {
		return err
	}
	currentStat, statErr := activationDirectoryStat(current)
	closeErr := current.Close()
	if statErr != nil || closeErr != nil || !sameActivationStat(after, currentStat) {
		return os.ErrInvalid
	}
	sort.Slice(records, func(i, j int) bool { return records[i].name < records[j].name })
	hasher := sha256.New()
	_, _ = io.WriteString(hasher, "fizeau-portable-tree-v1\x00")
	for _, record := range records {
		writeActivationTreeRecord(hasher, record)
	}
	if hex.EncodeToString(hasher.Sum(nil)) != asset.MaterializedSHA256 {
		return os.ErrInvalid
	}
	return nil
}

func copyActivationTreeDirectory(ctx context.Context, sourceFD, destinationFD int, prefix string, records *[]activationTreeRecord) error {
	names, err := readDirectoryNames(sourceFD)
	if err != nil {
		return err
	}
	for _, name := range names {
		if err := activationContext(ctx); err != nil {
			return err
		}
		var before unix.Stat_t
		if err := unix.Fstatat(sourceFD, name, &before, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return err
		}
		relative := name
		if prefix != "" {
			relative = prefix + "/" + name
		}
		switch before.Mode & unix.S_IFMT {
		case unix.S_IFDIR:
			if before.Mode&0o777 != 0o700 || unix.Mkdirat(destinationFD, name, 0o700) != nil {
				return os.ErrInvalid
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
			opened, err := identityOfFD(sourceChild)
			if err != nil || !sameIdentity(identityFromStat(&before), opened) {
				_ = unix.Close(sourceChild)
				_ = unix.Close(destinationChild)
				return os.ErrInvalid
			}
			*records = append(*records, activationTreeRecord{kind: 'd', name: relative, owner: uint32(before.Mode & 0o700)})
			err = copyActivationTreeDirectory(ctx, sourceChild, destinationChild, relative, records)
			after, statErr := identityOfFD(sourceChild)
			_ = unix.Close(sourceChild)
			_ = unix.Close(destinationChild)
			if err != nil || statErr != nil || !sameIdentity(opened, after) {
				return os.ErrInvalid
			}
		case unix.S_IFREG:
			if before.Mode&0o077 != 0 || before.Nlink != 1 {
				return os.ErrPermission
			}
			sourceChild, err := unix.Openat(sourceFD, name, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			if err != nil {
				return err
			}
			source := newDescriptorFile(sourceChild, "portable-runtime-activation-source")
			opened, err := identityOfFD(sourceChild)
			if err != nil || !sameIdentity(identityFromStat(&before), opened) {
				_ = source.Close()
				return os.ErrInvalid
			}
			destinationChild, err := unix.Openat(destinationFD, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
			if err != nil {
				_ = source.Close()
				return err
			}
			if err := unix.Fchmod(destinationChild, 0o600); err != nil {
				_ = source.Close()
				_ = unix.Close(destinationChild)
				return err
			}
			digest, err := copyAndHash(ctx, source, destinationChild)
			if syncErr := unix.Fsync(destinationChild); err == nil && syncErr != nil {
				err = syncErr
			}
			if closeErr := unix.Close(destinationChild); err == nil && closeErr != nil {
				err = closeErr
			}
			after, statErr := identityOfFD(sourceChild)
			closeErr := source.Close()
			if err != nil || statErr != nil || closeErr != nil || !sameIdentity(opened, after) {
				return os.ErrInvalid
			}
			decoded, err := hex.DecodeString(digest)
			if err != nil || len(decoded) != sha256.Size {
				return os.ErrInvalid
			}
			record := activationTreeRecord{kind: 'f', name: relative, owner: uint32(before.Mode & 0o700)}
			copy(record.digest[:], decoded)
			*records = append(*records, record)
		default:
			return os.ErrInvalid
		}
	}
	return nil
}
