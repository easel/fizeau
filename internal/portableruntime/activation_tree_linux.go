//go:build linux

package portableruntime

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"io"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/safefs"
	"golang.org/x/sys/unix"
)

type activationTreeRecord struct {
	kind   byte
	name   string
	owner  uint32
	digest [sha256.Size]byte
}

func activationTreeDigest(root *safefs.NoFollowRoot, target string) (string, error) {
	return activationTreeDigestWithHook(root, target, nil)
}

func activationTreeDigestWithHook(root *safefs.NoFollowRoot, target string, afterRead func()) (string, error) {
	directory, err := root.OpenDirectoryNoFollow(target)
	if err != nil {
		return "", err
	}
	defer directory.Close()
	before, err := activationDirectoryStat(directory)
	if err != nil {
		return "", err
	}
	records := make([]activationTreeRecord, 0)
	if err := walkActivationDirectory(directory, "", true, &records); err != nil {
		return "", err
	}
	if afterRead != nil {
		afterRead()
	}
	var after unix.Stat_t
	if err := unix.Fstat(int(directory.Fd()), &after); err != nil || !sameActivationStat(before, after) { // #nosec G115 -- retained descriptor is nonnegative.
		return "", os.ErrInvalid
	}
	current, err := root.OpenDirectoryNoFollow(target)
	if err != nil {
		return "", err
	}
	currentStat, statErr := activationDirectoryStat(current)
	closeErr := current.Close()
	if statErr != nil || closeErr != nil || !sameActivationStat(after, currentStat) {
		return "", os.ErrInvalid
	}
	sort.Slice(records, func(i, j int) bool { return records[i].name < records[j].name })
	manifest := sha256.New()
	_, _ = io.WriteString(manifest, "fizeau-portable-tree-v1\x00")
	for _, record := range records {
		writeActivationTreeRecord(manifest, record)
	}
	return hex.EncodeToString(manifest.Sum(nil)), nil
}

func validateActivationDeclaredPaths(root *safefs.NoFollowRoot, assets []ManifestAsset) error {
	directory, err := root.OpenDirectoryNoFollow("")
	if err != nil {
		return err
	}
	defer directory.Close()
	if err := requireActivationDirectory(directory); err != nil {
		return err
	}
	records := make([]activationTreeRecord, 0)
	if err := walkActivationDirectory(directory, "", false, &records); err != nil {
		return err
	}
	for _, record := range records {
		if activationPrivatePathAllowed(record) || activationAssetPathAllowed(record, assets) {
			continue
		}
		return os.ErrInvalid
	}
	return nil
}

func activationPrivatePathAllowed(record activationTreeRecord) bool {
	if record.name == ".fizeau" {
		return record.kind == 'd'
	}
	switch record.name {
	case manifestTarget, manifestSum, providerSecrets:
		return record.kind == 'f'
	default:
		return false
	}
}

func activationAssetPathAllowed(record activationTreeRecord, assets []ManifestAsset) bool {
	for _, asset := range assets {
		switch {
		case record.name == asset.Target:
			if asset.PathKind == harnesses.PortableRuntimePathTree {
				return record.kind == 'd'
			}
			return record.kind == 'f'
		case strings.HasPrefix(asset.Target, record.name+"/"):
			return record.kind == 'd'
		case asset.PathKind == harnesses.PortableRuntimePathTree && strings.HasPrefix(record.name, asset.Target+"/"):
			return true
		}
	}
	return false
}

func walkActivationDirectory(directory *os.File, prefix string, hashFiles bool, records *[]activationTreeRecord) error {
	entries, err := directory.ReadDir(-1)
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		name := entry.Name()
		relative := name
		if prefix != "" {
			relative = path.Join(prefix, name)
		}
		if !cleanTarget(relative) {
			return os.ErrInvalid
		}
		var before unix.Stat_t
		if err := unix.Fstatat(int(directory.Fd()), name, &before, unix.AT_SYMLINK_NOFOLLOW); err != nil { // #nosec G115 -- retained descriptor is nonnegative.
			return err
		}
		switch before.Mode & unix.S_IFMT {
		case unix.S_IFDIR:
			if before.Mode&0o777 != 0o700 {
				return os.ErrPermission
			}
			fd, err := unix.Openat(int(directory.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0) // #nosec G115 -- retained descriptor is nonnegative.
			if err != nil {
				return err
			}
			child := os.NewFile(uintptr(fd), "portable-runtime-tree-directory") // #nosec G115 -- unix.Openat returned a nonnegative descriptor.
			if child == nil {
				_ = unix.Close(fd)
				return os.ErrInvalid
			}
			var opened unix.Stat_t
			if err := unix.Fstat(fd, &opened); err != nil || !sameActivationStat(before, opened) {
				_ = child.Close()
				return os.ErrInvalid
			}
			*records = append(*records, activationTreeRecord{kind: 'd', name: relative, owner: uint32(before.Mode & 0o700)})
			if err := walkActivationDirectory(child, relative, hashFiles, records); err != nil {
				_ = child.Close()
				return err
			}
			var after unix.Stat_t
			statErr := unix.Fstat(fd, &after)
			closeErr := child.Close()
			if statErr != nil || closeErr != nil || !sameActivationStat(opened, after) {
				return os.ErrInvalid
			}
		case unix.S_IFREG:
			if before.Mode&0o077 != 0 || before.Nlink != 1 {
				return os.ErrPermission
			}
			fd, err := unix.Openat(int(directory.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0) // #nosec G115 -- retained descriptor is nonnegative.
			if err != nil {
				return err
			}
			file := os.NewFile(uintptr(fd), "portable-runtime-tree-file") // #nosec G115 -- unix.Openat returned a nonnegative descriptor.
			if file == nil {
				_ = unix.Close(fd)
				return os.ErrInvalid
			}
			var opened unix.Stat_t
			if err := unix.Fstat(fd, &opened); err != nil || !sameActivationStat(before, opened) {
				_ = file.Close()
				return os.ErrInvalid
			}
			record := activationTreeRecord{kind: 'f', name: relative, owner: uint32(before.Mode & 0o700)}
			if hashFiles {
				hasher := sha256.New()
				if _, err := io.Copy(hasher, file); err != nil {
					_ = file.Close()
					return err
				}
				copy(record.digest[:], hasher.Sum(nil))
			}
			var after unix.Stat_t
			statErr := unix.Fstat(fd, &after)
			closeErr := file.Close()
			if statErr != nil || closeErr != nil || !sameActivationStat(opened, after) {
				return os.ErrInvalid
			}
			*records = append(*records, record)
		default:
			return os.ErrInvalid
		}
	}
	return nil
}

func requireActivationDirectory(directory *os.File) error {
	_, err := activationDirectoryStat(directory)
	return err
}

func activationDirectoryStat(directory *os.File) (unix.Stat_t, error) {
	var stat unix.Stat_t
	if directory == nil || unix.Fstat(int(directory.Fd()), &stat) != nil || stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Mode&0o777 != 0o700 { // #nosec G115 -- retained descriptor is nonnegative.
		return unix.Stat_t{}, os.ErrInvalid
	}
	return stat, nil
}

func sameActivationStat(left, right unix.Stat_t) bool {
	return left.Dev == right.Dev && left.Ino == right.Ino && left.Mode == right.Mode && left.Nlink == right.Nlink &&
		left.Uid == right.Uid && left.Gid == right.Gid && left.Size == right.Size &&
		left.Mtim == right.Mtim && left.Ctim == right.Ctim
}

func writeActivationTreeRecord(writer hash.Hash, record activationTreeRecord) {
	_, _ = writer.Write([]byte{record.kind})
	var encoded [4]byte
	binary.BigEndian.PutUint32(encoded[:], uint32(len(record.name))) // #nosec G115 -- Linux path lengths are bounded below uint32.
	_, _ = writer.Write(encoded[:])
	_, _ = io.WriteString(writer, record.name)
	binary.BigEndian.PutUint32(encoded[:], record.owner)
	_, _ = writer.Write(encoded[:])
	if record.kind == 'f' {
		_, _ = writer.Write(record.digest[:])
	}
}
