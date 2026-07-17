//go:build !linux

package portableruntime

import (
	"context"
	"errors"
	"os"

	"github.com/easel/fizeau/internal/harnesses"
)

type fileIdentity struct{}

func descriptorFD(*os.File) int { return -1 }

func newDescriptorFile(fd int, name string) *os.File {
	if fd < 0 {
		return nil
	}
	return os.NewFile(uintptr(fd), name)
}

type stageHandle struct {
	path     string
	file     *os.File
	identity fileIdentity
}
type destinationHandle struct {
	absolute string
}

func openDestination(string) (*destinationHandle, error) {
	return nil, errors.New("portable runtime materialization requires linux")
}
func (*destinationHandle) close() {}
func (*destinationHandle) createStage() (*stageHandle, error) {
	return nil, errors.New("portable runtime materialization requires linux")
}
func (*destinationHandle) removeStage(*stageHandle) error { return nil }
func (*destinationHandle) revalidateEmpty() error {
	return errors.New("portable runtime materialization requires linux")
}
func (*destinationHandle) commit(*stageHandle) error {
	return errors.New("portable runtime materialization requires linux")
}
func (*destinationHandle) commitNamed(*stageHandle, string) error {
	return errors.New("portable runtime materialization requires linux")
}
func (*destinationHandle) takeDirectory() *os.File        { return nil }
func removeCommittedRuntime(*os.File, fileIdentity) error { return nil }

type sourceReceipt struct{}

func materializeAsset(context.Context, *stageHandle, harnesses.PortableRuntimeAsset) (string, sourceReceipt, error) {
	return "", sourceReceipt{}, errors.New("portable runtime materialization requires linux")
}
func createTargetParent(int, string) (int, string, error) {
	return -1, "", errors.New("portable runtime materialization requires linux")
}
func closeDescriptor(int) {}
func openExclusiveRegularAt(int, string, uint32) (int, error) {
	return -1, errors.New("portable runtime materialization requires linux")
}
func openTargetRegular(int, string) (*os.File, error) {
	return nil, errors.New("portable runtime materialization requires linux")
}
func verifyRestrictiveMaterialization(*stageHandle) error {
	return errors.New("portable runtime materialization requires linux")
}
func verifyAssetSource(context.Context, sourceReceipt) error {
	return errors.New("portable runtime materialization requires linux")
}
