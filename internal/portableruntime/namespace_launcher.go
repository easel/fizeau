package portableruntime

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/easel/fizeau/internal/harnesses"
)

const (
	namespaceLauncherZigVersion    = "0.16.0"
	namespaceLauncherSourceVersion = 2
)

var (
	//go:embed nslauncher/main.zig
	namespaceLauncherSource []byte
	//go:embed nslauncher/main.zig.sha256
	namespaceLauncherSourceDigestRecord string
	//go:embed nslauncher/artifacts/namespace-launcher-linux-amd64
	namespaceLauncherLinuxAMD64 []byte
	//go:embed nslauncher/artifacts/namespace-launcher-linux-amd64.sha256
	namespaceLauncherLinuxAMD64DigestRecord string
	//go:embed nslauncher/artifacts/namespace-launcher-linux-arm64
	namespaceLauncherLinuxARM64 []byte
	//go:embed nslauncher/artifacts/namespace-launcher-linux-arm64.sha256
	namespaceLauncherLinuxARM64DigestRecord string
)

type namespaceLauncherArtifact struct {
	target harnesses.PortableRuntimeTarget
	bytes  []byte
	digest string
}

func namespaceLauncherForTarget(target harnesses.PortableRuntimeTarget) (namespaceLauncherArtifact, error) {
	if !validateNamespaceLauncherSourceIdentity() {
		return namespaceLauncherArtifact{}, errors.New("embedded runtime source identity is invalid")
	}
	var data []byte
	var digestRecord string
	switch {
	case target.GOOS == "linux" && target.GOARCH == "amd64":
		data = namespaceLauncherLinuxAMD64
		digestRecord = namespaceLauncherLinuxAMD64DigestRecord
	case target.GOOS == "linux" && target.GOARCH == "arm64":
		data = namespaceLauncherLinuxARM64
		digestRecord = namespaceLauncherLinuxARM64DigestRecord
	default:
		return namespaceLauncherArtifact{}, errors.New("embedded runtime target is unsupported")
	}
	digest := strings.TrimSpace(digestRecord)
	actual := sha256.Sum256(data)
	if !validDigest(digest) || hex.EncodeToString(actual[:]) != digest {
		return namespaceLauncherArtifact{}, errors.New("embedded runtime identity is invalid")
	}
	return namespaceLauncherArtifact{
		target: target,
		bytes:  append([]byte(nil), data...),
		digest: digest,
	}, nil
}

func validateNamespaceLauncherSourceIdentity() bool {
	digest := sha256.Sum256(namespaceLauncherSource)
	return validDigest(strings.TrimSpace(namespaceLauncherSourceDigestRecord)) &&
		hex.EncodeToString(digest[:]) == strings.TrimSpace(namespaceLauncherSourceDigestRecord)
}
