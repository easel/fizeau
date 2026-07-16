package gemini

import "fmt"

const (
	geminiPortablePackageVersion      = "0.46.0"
	geminiPortablePackageNodeVersion  = "22.21.1"
	geminiPortableNodeVersion         = "22.22.0"
	geminiPortableHomebrewNodeVersion = "26.5.0"

	geminiPortableLauncherRelative = "bin/gemini"
	geminiPortableLauncherLink     = "../lib/node_modules/@google/gemini-cli/bundle/gemini.js"
	geminiPortablePackageRelative  = "lib/node_modules/@google/gemini-cli"
	geminiPortableEntrypoint       = "bundle/gemini.js"

	geminiPortableNodeArchiveSHA256 = "1bf1eb9ee63ffc4e5d324c0b9b62cf4a289f44332dfef9607cea1a0d9596ba6f"
	geminiPortableBundleChunk       = "bundle/chunk-RCJSF5RP.js"
	geminiPortableKeytarLoader      = "node_modules/@github/keytar/lib/keytar.js"
	geminiPortablePTYLoader         = "node_modules/@lydell/node-pty/requireBinary.js"
	geminiPortablePTYUnixLoader     = "node_modules/@lydell/node-pty/unixTerminal.js"
	geminiPortableKeytarAddon       = "node_modules/@github/keytar/build/Release/keytar.node"
	geminiPortablePTYAddon          = "node_modules/@lydell/node-pty-linux-arm64/pty.node"
	geminiPortablePackageTreeSHA256 = "31adbda660d392d71583f7649dff2fc22e10d080c6701ff5849505ba0ec2a652"
)

type geminiPortableNPMEvidence struct {
	name           string
	version        string
	integrity      string
	shasum         string
	publisher      string
	publisherEmail string
	signatureKeyID string
	signature      string
}

type geminiPortableELFEvidence struct {
	size          int64
	contentSHA256 string
	buildID       string
	interpreter   string
	needed        []string
}

var geminiPortablePackageEvidence = geminiPortableNPMEvidence{
	name:           "@google/gemini-cli",
	version:        geminiPortablePackageVersion,
	integrity:      "sha512-+HZtuuDKsL8mvOUWgK08GkrL1BQM4IplaSzxIjAM262FmJFp3Jo/zlHUq9ulRKcvx0agZ4KAzuj7jG9yIAxFBw==",
	shasum:         "dd5eb69e39327ca4ac0d57ac4a9aea19a356c89f",
	publisher:      "google-wombot",
	publisherEmail: "node-team-npm+wombot@google.com",
	signatureKeyID: "SHA256:DhQ8wR5APBvFHLF/+Tc+AYvPOdTpcIDqOhxsBHRwC7U",
	signature:      "MEUCIQDmDoEwMeo5nRqrZdIJ3MmvoEWONgJ4sWYrn61pXKMjowIgbkhXoxOtyq644RUKA9+lXdZXWVqYL4qvBbaHfW1FJQ8=",
}

var geminiPortableNodeEvidence = geminiPortableELFEvidence{
	size:          120592136,
	contentSHA256: "8eeefcacdf48f58541a651016e604055d14a992e39df98636b76495bc7244395",
	buildID:       "c917b99f70bd51f3f5f37c6fa71bdea3534e192c",
	interpreter:   "/lib/ld-linux-aarch64.so.1",
}

var geminiPortableKeytarPackageEvidence = geminiPortableNPMEvidence{
	name:           "@github/keytar",
	version:        "7.10.6",
	integrity:      "sha512-mRW6cUsSG+nj4jp5gp8e91zPySaT73r+2JM6VyMZfrEgksjPmjSMr+tPGNOK3HUHV+GUU9B1LAiiYy/wmAnIxA==",
	shasum:         "528f2c9f8c55a58e38ca271288cc59a2d7aec269",
	publisher:      "GitHub Actions",
	publisherEmail: "npm-oidc-no-reply@github.com",
	signatureKeyID: "SHA256:DhQ8wR5APBvFHLF/+Tc+AYvPOdTpcIDqOhxsBHRwC7U",
	signature:      "MEYCIQCP64Xw5qxgoqps5j8RE6zfggmMSLK7W6+HbycuEMbEwAIhAMqVPorro4ddiEzGlJ0KJbvZfNmT8bcl9nv6Qwa/XUzw",
}

var geminiPortablePTYPackageEvidence = geminiPortableNPMEvidence{
	name:           "@lydell/node-pty",
	version:        "1.1.0",
	integrity:      "sha512-VDD8LtlMTOrPKWMXUAcB9+LTktzuunqrMwkYR1DMRBkS6LQrCt+0/Ws1o2rMml/n3guePpS7cxhHF7Nm5K4iMw==",
	shasum:         "a04715b19078692e0dabf5d6e4bff9e75826a22b",
	publisher:      "lydell",
	publisherEmail: "simon.lydell@gmail.com",
	signatureKeyID: "SHA256:jl3bwswu80PjjokCgh0o2w5c2U4LhQAE57gj9cz1kzA",
	signature:      "MEQCIFth7pIgC3jem8Gi9rQYvNJKDeMsdHkAoGSI1qYK0k7PAiBDaqZPZcn6durcR+/xYL0QVzLI2DqFEw/UAUBNQRx71w==",
}

var geminiPortablePTYLinuxARM64Evidence = geminiPortableNPMEvidence{
	name:           "@lydell/node-pty-linux-arm64",
	version:        "1.1.0",
	integrity:      "sha512-yyDBmalCfHpLiQMT2zyLcqL2Fay4Xy7rIs8GH4dqKLnEviMvPGOK7LADVkKAsbsyXBSISL3Lt1m1MtxhPH6ckg==",
	shasum:         "a6f8b063d558bc2f4044ee900aef6a6a6bff22f0",
	publisher:      "lydell",
	publisherEmail: "simon.lydell@gmail.com",
	signatureKeyID: "SHA256:jl3bwswu80PjjokCgh0o2w5c2U4LhQAE57gj9cz1kzA",
	signature:      "MEQCID4G+O0r6Bytc2XeG1VGFTux7i0aCwI+awT2LrQYjvjHAiBOD+XBXjp2yuCw23VKqITsX1nKX7DHlKJUO3ko50uFBw==",
}

var geminiPortableKeytarEvidence = geminiPortableELFEvidence{
	size:          134032,
	contentSHA256: "8f0f32c5d576a0987e294b8dc9f1909133504f4fe20b65888bca0dd3bfaec29c",
	buildID:       "27ccb7ef5a2802fa0d22398f6142bae75b0ab34e",
	needed:        []string{"libsecret-1.so.0", "libglib-2.0.so.0", "libstdc++.so.6", "libgcc_s.so.1", "libc.so.6"},
}

var geminiPortablePTYEvidence = geminiPortableELFEvidence{
	size:          69064,
	contentSHA256: "c192e560428e778842fdbbe72f14032824b03373ed49ad3efcdbcf9eb249b75b",
	buildID:       "70823a0341e4d895de4bce9fa49d656b68916cd1",
	needed:        []string{"libstdc++.so.6", "libgcc_s.so.1", "libc.so.6", "ld-linux-aarch64.so.1"},
}

func geminiPortableSelectedAddons(goos, goarch string) ([]string, error) {
	if goos != "linux" || goarch != "arm64" {
		return nil, fmt.Errorf("gemini portable addon evidence is unavailable for %s/%s", goos, goarch)
	}
	return []string{
		geminiPortableKeytarAddon,
		geminiPortablePTYAddon,
	}, nil
}
