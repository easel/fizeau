//go:build linux

package portableruntime

import (
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

// activationProjectionRecipe is deliberately descriptor-only.  Names are
// single directory entries used with their retained parent descriptors; no
// absolute host path (or mount recipe) is retained.
type activationProjectionRecipe struct {
	governed    activationDescriptorPin
	activation  activationDescriptorPin
	directories []activationDescriptorPin
	sources     []activationSourcePin
	absent      []activationAbsentPin
	plan        *portableNamespaceProjectionPlan
}

type activationDescriptorPin struct {
	object         *os.File
	identity       fileIdentity
	parent         *os.File
	parentIdentity fileIdentity
	name           string
	directory      bool
}

type activationSourcePin struct {
	activationDescriptorPin
	digest string
	tree   bool
}

type activationAbsentPin struct {
	parent         *os.File
	parentIdentity fileIdentity
	names          []string
}

func pinActivationProjectionRecipe(runtime *safefs.NoFollowRoot, stage *stageHandle, destination *destinationHandle, entrypoint ManifestEntrypoint, assets map[string]ManifestAsset, recipe ActivationRecipe) (*activationProjectionRecipe, error) {
	if runtime == nil || stage == nil || stage.file == nil || destination == nil || destination.directory == nil {
		return nil, os.ErrInvalid
	}
	pinned := &activationProjectionRecipe{}
	var err error
	if pinned.governed, err = pinDescriptor(destination.directory, nil, ""); err != nil {
		return nil, err
	}
	if pinned.activation, err = pinDescriptor(stage.file, destination.directory, activationChild); err != nil {
		return nil, err
	}
	// Every activation scope is a separately pinned mountpoint candidate.
	for _, scope := range []harnesses.PortableRuntimeGuestPathScope{
		harnesses.PortableRuntimeGuestPathHome, harnesses.PortableRuntimeGuestPathConfig,
		harnesses.PortableRuntimeGuestPathData, harnesses.PortableRuntimeGuestPathCache,
		harnesses.PortableRuntimeGuestPathState, harnesses.PortableRuntimeGuestPathTmp,
	} {
		pin, pinErr := pinRelativeDirectory(stage.file, string(scope))
		if pinErr != nil {
			return nil, pinErr
		}
		pinned.directories = append(pinned.directories, pin)
	}
	for _, projection := range entrypoint.StateProjections {
		pin, pinErr := pinRelativeDirectory(stage.file, activationRelativeGuestPath(projection.Directory))
		if pinErr != nil {
			return nil, pinErr
		}
		pinned.directories = append(pinned.directories, pin)
	}
	for _, binding := range recipe.immutableBindings {
		target := strings.TrimPrefix(binding.runtimeGuestTarget, GuestRoot+"/")
		asset, exists := assets[target]
		if !exists || asset.PathKind != harnesses.PortableRuntimePathFile && asset.PathKind != harnesses.PortableRuntimePathTree {
			return nil, os.ErrInvalid
		}
		root, openErr := runtime.OpenDirectoryNoFollow("")
		if openErr != nil {
			return nil, openErr
		}
		tree := asset.PathKind == harnesses.PortableRuntimePathTree
		pin, pinErr := pinRelativeObject(root, target, tree)
		_ = root.Close()
		if pinErr != nil {
			return nil, pinErr
		}
		if !sameIdentity(pin.identity, binding.identity) {
			return nil, os.ErrInvalid
		}
		pinned.sources = append(pinned.sources, activationSourcePin{activationDescriptorPin: pin, digest: binding.contentSHA256, tree: tree})
	}
	for _, absent := range recipe.requiredAbsent {
		parent, names, openErr := openExistingProjectionParent(stage.file, activationRelativeGuestPath(absent))
		if openErr != nil {
			return nil, openErr
		}
		parentCopy, copyErr := duplicateDescriptor(parent)
		identity, identityErr := identityOfFD(descriptorFD(parent))
		_ = parent.Close()
		if copyErr != nil || identityErr != nil {
			if parentCopy != nil {
				_ = parentCopy.Close()
			}
			return nil, os.ErrInvalid
		}
		pinned.absent = append(pinned.absent, activationAbsentPin{parent: parentCopy, parentIdentity: identity, names: names})
	}
	plan, planErr := compilePortableNamespaceProjectionPlan(pinned, entrypoint, recipe)
	if planErr != nil {
		return nil, planErr
	}
	pinned.plan = plan
	return pinned, nil
}

func (p *activationProjectionRecipe) revalidate() error {
	if p == nil || p.governed.revalidate() != nil || p.activation.revalidate() != nil {
		return os.ErrInvalid
	}
	for _, pin := range p.directories {
		if pin.revalidate() != nil {
			return os.ErrInvalid
		}
	}
	for _, source := range p.sources {
		digest := descriptorSHA256(source.object)
		if source.tree {
			digest = descriptorTreeSHA256(source.object)
		}
		if source.revalidate() != nil || digest != source.digest {
			return os.ErrInvalid
		}
	}
	for _, absent := range p.absent {
		identity, err := identityOfFD(descriptorFD(absent.parent))
		if err != nil || !sameDirectoryObject(identity, absent.parentIdentity) {
			return os.ErrInvalid
		}
		if revalidateAbsentPath(absent) != nil {
			return os.ErrInvalid
		}
	}
	return nil
}

func (p activationDescriptorPin) revalidate() error {
	if p.object == nil {
		return os.ErrInvalid
	}
	identity, err := identityOfFD(descriptorFD(p.object))
	if err != nil || !samePinnedObject(p, identity) {
		return os.ErrInvalid
	}
	if p.parent == nil {
		return nil
	}
	parent, err := identityOfFD(descriptorFD(p.parent))
	if err != nil || !sameDirectoryObject(parent, p.parentIdentity) {
		return os.ErrInvalid
	}
	var stat unix.Stat_t
	if err := unix.Fstatat(descriptorFD(p.parent), p.name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil || !samePinnedObject(p, identityFromStat(&stat)) {
		return os.ErrInvalid
	}
	return nil
}

func samePinnedObject(pin activationDescriptorPin, actual fileIdentity) bool {
	if pin.directory {
		return sameDirectoryObject(pin.identity, actual)
	}
	return sameIdentity(pin.identity, actual)
}

func pinDescriptor(object, parent *os.File, name string) (activationDescriptorPin, error) {
	copyObject, err := duplicateDescriptor(object)
	if err != nil {
		return activationDescriptorPin{}, err
	}
	identity, err := identityOfFD(descriptorFD(copyObject))
	if err != nil {
		_ = copyObject.Close()
		return activationDescriptorPin{}, err
	}
	pin := activationDescriptorPin{object: copyObject, identity: identity, name: name, directory: identity.mode&unix.S_IFMT == unix.S_IFDIR}
	if parent != nil {
		pin.parent, err = duplicateDescriptor(parent)
		if err != nil {
			_ = copyObject.Close()
			return activationDescriptorPin{}, err
		}
		pin.parentIdentity, err = identityOfFD(descriptorFD(pin.parent))
		if err != nil {
			_ = copyObject.Close()
			_ = pin.parent.Close()
			return activationDescriptorPin{}, err
		}
	}
	return pin, nil
}

func duplicateDescriptor(file *os.File) (*os.File, error) {
	if file == nil {
		return nil, os.ErrInvalid
	}
	// #nosec G115 -- descriptorFD returns the non-negative Unix descriptor owned by file.
	fd, err := unix.FcntlInt(uintptr(descriptorFD(file)), unix.F_DUPFD_CLOEXEC, 3)
	if err != nil {
		return nil, err
	}
	copy := newDescriptorFile(fd, "portable-runtime-projection-pin")
	if copy == nil {
		_ = unix.Close(fd)
		return nil, os.ErrInvalid
	}
	return copy, nil
}

func pinRelativeDirectory(root *os.File, relative string) (activationDescriptorPin, error) {
	parent, leaf, err := openRelativeParent(root, relative)
	if err != nil {
		return activationDescriptorPin{}, err
	}
	defer parent.Close()
	fd, err := unix.Openat(descriptorFD(parent), leaf, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return activationDescriptorPin{}, err
	}
	object := newDescriptorFile(fd, "portable-runtime-projection-directory")
	defer object.Close()
	return pinDescriptor(object, parent, leaf)
}

func pinRelativeObject(root *os.File, relative string, directory bool) (activationDescriptorPin, error) {
	parent, leaf, err := openRelativeParent(root, relative)
	if err != nil {
		return activationDescriptorPin{}, err
	}
	defer parent.Close()
	flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC
	if directory {
		flags |= unix.O_DIRECTORY
	} else {
		flags |= unix.O_NONBLOCK
	}
	fd, err := unix.Openat(descriptorFD(parent), leaf, flags, 0)
	if err != nil {
		return activationDescriptorPin{}, err
	}
	object := newDescriptorFile(fd, "portable-runtime-projection-source")
	defer object.Close()
	return pinDescriptor(object, parent, leaf)
}

func openRelativeParent(root *os.File, relative string) (*os.File, string, error) {
	if root == nil || !cleanTarget(relative) {
		return nil, "", os.ErrInvalid
	}
	parts := strings.Split(relative, "/")
	current, err := duplicateDescriptor(root)
	if err != nil {
		return nil, "", err
	}
	for _, component := range parts[:len(parts)-1] {
		fd, openErr := unix.Openat(descriptorFD(current), component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if openErr != nil {
			_ = current.Close()
			return nil, "", openErr
		}
		next := newDescriptorFile(fd, "portable-runtime-projection-parent")
		_ = current.Close()
		current = next
	}
	return current, parts[len(parts)-1], nil
}

// openExistingProjectionParent retains the nearest extant no-follow parent.
// Required-absent paths are allowed to have absent intermediate components;
// those components remain intentionally mutable, while their governing
// ancestor is descriptor-pinned.
func openExistingProjectionParent(root *os.File, relative string) (*os.File, []string, error) {
	if root == nil || !cleanTarget(relative) {
		return nil, nil, os.ErrInvalid
	}
	parts := strings.Split(relative, "/")
	current, err := duplicateDescriptor(root)
	if err != nil {
		return nil, nil, err
	}
	for index, component := range parts {
		fd, openErr := unix.Openat(descriptorFD(current), component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if errors.Is(openErr, unix.ENOENT) {
			return current, append([]string(nil), parts[index:]...), nil
		}
		if openErr != nil {
			_ = current.Close()
			return nil, nil, openErr
		}
		next := newDescriptorFile(fd, "portable-runtime-projection-absent-parent")
		_ = current.Close()
		current = next
	}
	_ = current.Close()
	return nil, nil, os.ErrInvalid
}

func revalidateAbsentPath(pin activationAbsentPin) error {
	if len(pin.names) == 0 {
		return os.ErrInvalid
	}
	current, err := duplicateDescriptor(pin.parent)
	if err != nil {
		return err
	}
	defer current.Close()
	for index, component := range pin.names {
		var stat unix.Stat_t
		err := unix.Fstatat(descriptorFD(current), component, &stat, unix.AT_SYMLINK_NOFOLLOW)
		if errors.Is(err, unix.ENOENT) {
			return nil
		}
		if err != nil || stat.Mode&unix.S_IFMT != unix.S_IFDIR || index == len(pin.names)-1 {
			return os.ErrInvalid
		}
		fd, openErr := unix.Openat(descriptorFD(current), component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if openErr != nil {
			return openErr
		}
		next := newDescriptorFile(fd, "portable-runtime-projection-absent-check")
		_ = current.Close()
		current = next
	}
	return os.ErrInvalid
}

func descriptorSHA256(file *os.File) string {
	if file == nil {
		return ""
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return ""
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return ""
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return ""
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func descriptorTreeSHA256(file *os.File) string {
	copy, err := duplicateDescriptor(file)
	if err != nil {
		return ""
	}
	defer copy.Close()
	records := make([]activationTreeRecord, 0)
	if err := walkActivationDirectory(copy, "", true, &records); err != nil {
		return ""
	}
	sort.Slice(records, func(i, j int) bool { return records[i].name < records[j].name })
	hash := sha256.New()
	_, _ = io.WriteString(hash, "fizeau-portable-tree-v1\x00")
	for _, record := range records {
		writeActivationTreeRecord(hash, record)
	}
	return hex.EncodeToString(hash.Sum(nil))
}
