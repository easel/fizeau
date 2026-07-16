package pi

import (
	"debug/elf"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/safefs"
)

const (
	piPortablePackageName = "@mariozechner/pi-coding-agent"
	piPortableVersion     = "0.51.4"
	piPortableGOOS        = "linux"
	piPortableGOARCH      = "arm64"
)

// piPortableRuntimeEvidence binds the one reviewed Pi interpreted-runtime
// layout. Package-manager metadata alone is not sufficient: the installed npm
// tree, separately sourced interpreter, reachable runtime data, and display-
// gated native addon selection are independent trust boundaries.
type piPortableRuntimeEvidenceRecord struct {
	release piPortableReleaseEvidence
	tree    piPortableTreeEvidence
	node    piPortableNodeEvidence
	data    piPortableDataEvidence
}

type piPortableReleaseEvidence struct {
	packageName      string
	version          string
	integrity        string
	shasum           string
	signatureKeyID   string
	packageRelative  string
	launcherRelative string
	launcherLink     string
	binName          string
	binRelative      string
	launcherSize     int64
	launcherSHA256   string
}

type piPortableTreeEvidence struct {
	format  string
	digest  string
	records int
	goos    string
	goarch  string
}

type piPortableNodeEvidence struct {
	version      string
	size         int64
	sha256       string
	buildID      string
	interpreter  string
	rejectedBrew string
}

type piPortableDataEvidence struct {
	photonRelative    string
	photonSize        int64
	photonSHA256      string
	doomRelative      string
	doomSHA256        string
	clipboardRelative string
	clipboardSize     int64
	clipboardSHA256   string
	clipboardClass    elf.Class
	clipboardNeeded   []string
	forbiddenDisplay  []string
}

var piPortableVerifiedRuntime = piPortableRuntimeEvidenceRecord{
	release: piPortableReleaseEvidence{
		packageName:      piPortablePackageName,
		version:          piPortableVersion,
		integrity:        "sha512-agQJ38Hq4vjukzB1AC4Mj2lJ3H3zVBzYz4Fuyu8rvTMRAVkB1zlL+CMHF8FsNZ2+bVkKvMHZusc7nIQ1cPbf4Q==",
		shasum:           "025749df96513e9d328f3c501bdd37ac7e878fe4",
		signatureKeyID:   "SHA256:DhQ8wR5APBvFHLF/+Tc+AYvPOdTpcIDqOhxsBHRwC7U",
		packageRelative:  "lib/node_modules/@mariozechner/pi-coding-agent",
		launcherRelative: "lib/node_modules/@mariozechner/pi-coding-agent/dist/cli.js",
		launcherLink:     "../lib/node_modules/@mariozechner/pi-coding-agent/dist/cli.js",
		binName:          "pi",
		binRelative:      "dist/cli.js",
		launcherSize:     302,
		launcherSHA256:   "34277c76b394762bc1711e859e4b86caf45ac92a85c1b8894671aa584e53a27a",
	},
	tree: piPortableTreeEvidence{
		format:  "fizeau-portable-tree-v1",
		digest:  "e24e2b681a84d3aa44abc3ff565d23f827f668a6e5325070f738e8a420dc4e09",
		records: 17594,
		goos:    piPortableGOOS,
		goarch:  piPortableGOARCH,
	},
	node: piPortableNodeEvidence{
		version:      "22.22.0",
		size:         120592136,
		sha256:       "8eeefcacdf48f58541a651016e604055d14a992e39df98636b76495bc7244395",
		buildID:      "c917b99f70bd51f3f5f37c6fa71bdea3534e192c",
		interpreter:  "/lib/ld-linux-aarch64.so.1",
		rejectedBrew: "26.5.0",
	},
	data: piPortableDataEvidence{
		photonRelative:    "node_modules/@silvia-odwyer/photon-node/photon_rs_bg.wasm",
		photonSize:        1881634,
		photonSHA256:      "10468181565c56004c867f3a4af96f89a0ef5a63a72f2b5fb12c1f1992a3615c",
		doomRelative:      "examples/extensions/doom-overlay/doom/build/doom.wasm",
		doomSHA256:        "571d161956593508cf4ade732ae93753f00484bb526667a8676571cca14dec7d",
		clipboardRelative: "node_modules/@mariozechner/clipboard-linux-arm64-gnu/clipboard.linux-arm64-gnu.node",
		clipboardSize:     2309056,
		clipboardSHA256:   "1c15a004a06c9dc5eda5ba0a7a3535203eb141b97098ca033ca49a1269f84663",
		clipboardClass:    elf.ELFCLASS64,
		clipboardNeeded:   []string{"libgcc_s.so.1", "libpthread.so.0", "libm.so.6", "libdl.so.2", "libc.so.6"},
		forbiddenDisplay:  []string{"DISPLAY", "WAYLAND_DISPLAY"},
	},
}

func validatePiPortableReleaseEvidence(observed piPortableReleaseEvidence) error {
	if observed != piPortableVerifiedRuntime.release {
		return piPortableEvidenceError("release")
	}
	return nil
}

func validatePiPortableTreeEvidence(observed piPortableTreeEvidence) error {
	if observed != piPortableVerifiedRuntime.tree {
		return piPortableEvidenceError("package tree")
	}
	return nil
}

func validatePiPortableNodeEvidence(observed piPortableNodeEvidence) error {
	if observed != piPortableVerifiedRuntime.node {
		return piPortableEvidenceError("interpreter")
	}
	return nil
}

func validatePiPortableDataEvidence(observed piPortableDataEvidence) error {
	want := piPortableVerifiedRuntime.data
	if observed.photonRelative != want.photonRelative || observed.photonSize != want.photonSize ||
		observed.photonSHA256 != want.photonSHA256 || observed.doomRelative != want.doomRelative ||
		observed.doomSHA256 != want.doomSHA256 || observed.clipboardRelative != want.clipboardRelative ||
		observed.clipboardSize != want.clipboardSize || observed.clipboardSHA256 != want.clipboardSHA256 ||
		observed.clipboardClass != want.clipboardClass ||
		!slices.Equal(observed.clipboardNeeded, want.clipboardNeeded) ||
		!slices.Equal(observed.forbiddenDisplay, want.forbiddenDisplay) {
		return piPortableEvidenceError("runtime data")
	}
	return nil
}

func piPortableEvidenceError(class string) error {
	return fmt.Errorf("%w: Pi portable %s evidence mismatch", harnesses.ErrPortableRuntimeClosureIncomplete, class)
}

type piPortablePackageMetadata struct {
	Name    string            `json:"name"`
	Version string            `json:"version"`
	Bin     map[string]string `json:"bin"`
}

// inspectPiPortableInstalledRelease recognizes only npm's exact global layout
// beneath one prefix: <prefix>/bin/pi is the reviewed relative symlink into the
// exact package root. All failures are typed and value-opaque so callers can
// fail closed without exposing host installation paths or observed metadata.
func inspectPiPortableInstalledRelease(launcher string, want piPortableReleaseEvidence) (string, error) {
	if !filepath.IsAbs(launcher) || filepath.Clean(launcher) != launcher || filepath.Base(launcher) != want.binName || filepath.Base(filepath.Dir(launcher)) != "bin" {
		return "", piPortableEvidenceError("installed layout")
	}
	launcherInfo, err := os.Lstat(launcher)
	if err != nil || launcherInfo.Mode()&os.ModeSymlink == 0 {
		return "", piPortableEvidenceError("installed layout")
	}
	link, err := os.Readlink(launcher)
	if err != nil || filepath.ToSlash(link) != want.launcherLink {
		return "", piPortableEvidenceError("installed layout")
	}
	prefix := filepath.Dir(filepath.Dir(launcher))
	packageRoot := filepath.Join(prefix, filepath.FromSlash(want.packageRelative))
	packageInfo, err := os.Lstat(packageRoot)
	if err != nil || !packageInfo.IsDir() || packageInfo.Mode()&os.ModeSymlink != 0 {
		return "", piPortableEvidenceError("installed layout")
	}
	launcherPath := filepath.Join(prefix, filepath.FromSlash(want.launcherRelative))
	resolved, err := filepath.EvalSymlinks(launcher)
	if err != nil || resolved != launcherPath || filepath.Join(packageRoot, filepath.FromSlash(want.binRelative)) != launcherPath {
		return "", piPortableEvidenceError("installed layout")
	}
	metadataBytes, err := safefs.ReadFile(filepath.Join(packageRoot, "package.json"))
	if err != nil {
		return "", piPortableEvidenceError("installed layout")
	}
	var metadata piPortablePackageMetadata
	if json.Unmarshal(metadataBytes, &metadata) != nil || metadata.Name != want.packageName || metadata.Version != want.version ||
		!reflect.DeepEqual(metadata.Bin, map[string]string{want.binName: want.binRelative}) {
		return "", piPortableEvidenceError("installed layout")
	}
	info, err := os.Lstat(launcherPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o100 == 0 {
		return "", piPortableEvidenceError("installed layout")
	}
	digest, err := harnesses.PortableRuntimeFileDigest(launcherPath)
	if err != nil {
		return "", piPortableEvidenceError("installed layout")
	}
	observed := want
	observed.packageName = metadata.Name
	observed.version = metadata.Version
	observed.launcherLink = filepath.ToSlash(link)
	observed.binName = want.binName
	observed.binRelative = metadata.Bin[want.binName]
	observed.launcherSize = info.Size()
	observed.launcherSHA256 = digest
	if observed != want {
		return "", piPortableEvidenceError("installed layout")
	}
	return packageRoot, nil
}
