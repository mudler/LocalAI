// SPDX-License-Identifier: MIT

// Package tablespkg builds the installable Tables packages of the acceptance
// suites with the real packaging entry point. It runs scripts/addons/package.sh
// to make the OCI layout of one platform, checks every layer against its
// digest and joins the layers into the plain archive a local catalog serves.
// Nothing is signed, pushed or published.
package tablespkg

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Release is one packaged version of Tables.
type Release struct {
	Version string
	// Archive is the plain tar the catalog serves: the layers of the layout
	// of package.sh in one archive.
	Archive []byte
	// ArchiveDigest is the catalog digest of Archive.
	ArchiveDigest string
	// ImageDigest is the digest of the platform image in the layout.
	ImageDigest string
}

// Recorder receives every command the builder runs.
type Recorder func(argv ...string)

func run(root string, rec Recorder, env []string, name string, args ...string) error {
	if rec != nil {
		rec(append([]string{name}, args...)...)
	}
	cmd := exec.Command(name, args...) // #nosec G204 -- test harness starts the binaries it built
	cmd.Dir = root
	cmd.Env = append(cmd.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, out)
	}
	return nil
}

// BuildExecutable builds the Tables executable of this platform.
func BuildExecutable(root, out string, rec Recorder) error {
	return BuildExecutableFrom(root, out, "./addons/tables/cmd/tables", rec)
}

// BuildExecutableFrom builds another main package of the Tables module. The
// fault suites use it for the test-only executable; nothing else does.
func BuildExecutableFrom(root, out, pkg string, rec Recorder) error {
	return run(root, rec, []string{"CGO_ENABLED=1"}, "go", "build", "-p", "2", "-trimpath", "-o", out, pkg)
}

// Build packages one executable under each version and returns the releases.
// scratch must be an empty directory the caller removes.
func Build(root, scratch, executable string, rec Recorder, versions ...string) (map[string]Release, error) {
	if _, err := os.Stat(filepath.Join(root, "addons/tables/ui/dist/ui/tables.js")); err != nil {
		if err := run(root, rec, nil, "node", "addons/tables/ui/build.mjs"); err != nil {
			return nil, err
		}
	}
	out := map[string]Release{}
	for _, version := range versions {
		layout := filepath.Join(scratch, "layout-"+version)
		if err := run(root, rec, nil, "sh", "scripts/addons/package.sh", "--layout", layout, "--version", version,
			"--platform", runtime.GOOS+"/"+runtime.GOARCH, "--executable", executable, "--local-check"); err != nil {
			return nil, err
		}
		archive, image, err := mergeLayers(layout)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(archive)
		out[version] = Release{Version: version, Archive: archive, ArchiveDigest: "sha256:" + hex.EncodeToString(sum[:]), ImageDigest: image}
	}
	return out, nil
}

// mergeLayers turns the layers of an OCI layout into one plain tar, in the
// order the receipt of package.sh lists them.
func mergeLayers(layout string) ([]byte, string, error) {
	raw, err := os.ReadFile(filepath.Join(layout, "receipt.json")) // #nosec G304 -- test harness reads files it created in its own work directory
	if err != nil {
		return nil, "", err
	}
	var receipt struct {
		ManifestDigest string `json:"manifestDigest"`
		Publishable    bool   `json:"publishable"`
		Layers         []struct {
			Digest string `json:"digest"`
		} `json:"layers"`
	}
	if err := json.Unmarshal(raw, &receipt); err != nil {
		return nil, "", err
	}
	if receipt.Publishable {
		return nil, "", fmt.Errorf("a local acceptance package must not be publishable")
	}
	var out bytes.Buffer
	tw := tar.NewWriter(&out)
	seen := map[string]bool{}
	for _, l := range receipt.Layers {
		hexDigest := strings.TrimPrefix(l.Digest, "sha256:")
		blob, err := os.ReadFile(filepath.Join(layout, "blobs", "sha256", hexDigest)) // #nosec G304 -- test harness reads files it created in its own work directory
		if err != nil {
			return nil, "", err
		}
		sum := sha256.Sum256(blob)
		if hex.EncodeToString(sum[:]) != hexDigest {
			return nil, "", fmt.Errorf("layer %s does not match its digest", l.Digest)
		}
		zr, err := gzip.NewReader(bytes.NewReader(blob))
		if err != nil {
			return nil, "", err
		}
		tr := tar.NewReader(zr)
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, "", err
			}
			if seen[h.Name] {
				return nil, "", fmt.Errorf("duplicate path %s in the layers", h.Name)
			}
			seen[h.Name] = true
			if err := tw.WriteHeader(&tar.Header{Name: h.Name, Mode: h.Mode, Size: h.Size, Typeflag: h.Typeflag}); err != nil {
				return nil, "", err
			}
			if _, err := io.CopyN(tw, tr, h.Size); err != nil {
				return nil, "", err
			}
		}
	}
	if err := tw.Close(); err != nil {
		return nil, "", err
	}
	if !seen["manifest.json"] || !seen["tables"] {
		return nil, "", fmt.Errorf("the package lacks its manifest or executable")
	}
	return out.Bytes(), receipt.ManifestDigest, nil
}
