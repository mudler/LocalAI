// SPDX-License-Identifier: MIT
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/chv"
)

// kvmPins holds the sha256 of each KVM host program the package carries, per
// architecture. package/kvm.json describes the download; a test keeps the two
// equal. virtiofsd has no pin for arm64: upstream publishes no static build
// for it, so the backend uses one that the administrator installs.
var kvmPins = map[string]map[string]string{
	"amd64": {
		"cloud-hypervisor": "4f4f98a3d661a40f3e5272627cc895a1b2e525f2f6bedf5f895f9cd21ba5c65b",
		"virtiofsd":        "15b2e72a78cc08a9bd8a6943e89fb69c88cb3cbeb63069efceade835342ac7d4",
	},
	"arm64": {
		"cloud-hypervisor": "e8a6d9786ea678e739f1a386682846e93742dcb9c7b6e3e0c62e3bda6cff0ef3",
	},
}

// installKVMTools copies the KVM host programs that sit next to the add-on
// executable (srcDir) into dstDir as executable files, when each has the
// checksum pinned for arch. The package does not keep the executable bit. A
// program that the package does not carry is left out of the result, and the
// backend then looks for it in the system folders.
func installKVMTools(srcDir, dstDir, arch string, pins map[string]map[string]string) (chv.Bins, error) {
	var bins chv.Bins
	for _, tool := range []struct {
		name string
		path *string
	}{
		{"cloud-hypervisor", &bins.CloudHypervisor},
		{"virtiofsd", &bins.Virtiofsd},
	} {
		src := filepath.Join(srcDir, tool.name)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		want := strings.ToLower(pins[arch][tool.name])
		if want == "" {
			return chv.Bins{}, unsupported("this release pins no %s for %s", tool.name, arch)
		}
		got, err := fileSHA256(src)
		if err != nil || got != want {
			return chv.Bins{}, unsupported("%s does not match the checksum this release pins for %s", src, tool.name)
		}
		dst := filepath.Join(dstDir, tool.name)
		if have, err := fileSHA256(dst); err != nil || have != want {
			if err := os.MkdirAll(dstDir, 0o700); err != nil {
				return chv.Bins{}, fmt.Errorf("making the KVM program folder: %w", err)
			}
			if err := copyExecutable(src, dst); err != nil {
				return chv.Bins{}, fmt.Errorf("copying %s: %w", tool.name, err)
			}
		}
		*tool.path = dst
	}
	return bins, nil
}

// containerMarkers are files that a container runtime creates.
var containerMarkers = []string{"/.dockerenv", "/run/.containerenv"}

// inContainer says whether one of the marker files exists.
func inContainer(markers ...string) bool {
	for _, m := range markers {
		if _, err := os.Stat(m); err == nil {
			return true
		}
	}
	return false
}

// noBwrap says whether the KVM backend runs virtiofsd and debugfs without
// bubblewrap. SANDBOX_NO_BWRAP decides when it is set to a value; otherwise a
// container does not use bubblewrap, because it cannot make namespaces there
// and the container is already the isolation. The supervisor starts the
// add-on with a fixed environment, so the container check is what a packaged
// add-on relies on.
func noBwrap(lookup func(string) (string, bool), container bool) bool {
	if v, ok := lookup("SANDBOX_NO_BWRAP"); ok && v != "" {
		switch strings.ToLower(v) {
		case "1", "true", "yes":
			return true
		default:
			return false
		}
	}
	return container
}
