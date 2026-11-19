// SPDX-License-Identifier: MIT
package packaging

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Constants of the package format. Anything that reads a package compares
// against these, never against a value it found in the package.
const (
	// ArtifactType marks an OCI manifest as a LocalAI add-on package.
	ArtifactType = "application/vnd.localai.addon.package.v1"
	// IndexArtifactType marks the image index that joins the platform images.
	IndexArtifactType = "application/vnd.localai.addon.package.index.v1"

	AnnotationID       = "io.localai.addon.id"
	AnnotationVersion  = "io.localai.addon.version"
	AnnotationPlatform = "io.localai.addon.platform"
	AnnotationLayer    = "io.localai.addon.layer"
	AnnotationRevision = "org.opencontainers.image.revision"
	AnnotationOCIVer   = "org.opencontainers.image.version"

	// SignerWorkflow is the workflow file whose OIDC identity signs packages.
	// It is deliberately not the backend merge workflow.
	SignerWorkflow = "addons-package.yml"
	OIDCIssuer     = "https://token.actions.githubusercontent.com"
)

var (
	versionRE  = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z]+(\.[0-9A-Za-z-]+)*)?$`)
	platformRE = regexp.MustCompile(`^((linux|darwin)/(amd64|arm64)|windows/amd64)$`)
	addonRE    = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	shaRE      = regexp.MustCompile(`^[0-9a-f]{40}$`)
	digestRE   = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// ExecutableFor is the file name of the executable of a package for
// platform. A Windows package names it <executable>.exe: Windows has no
// execute bit, and only the .exe extension makes a file a program there. The
// platform matrix and the manifest template keep the plain name.
func ExecutableFor(executable, platform string) string {
	if strings.HasPrefix(platform, "windows/") {
		return executable + ".exe"
	}
	return executable
}

// PlatformEntry is one row of the release platform matrix. A platform ships
// only while Enabled is true, and it is enabled only after its native runtime
// check passes on a runner of that platform.
type PlatformEntry struct {
	Platform    string   `yaml:"platform"`
	Enabled     bool     `yaml:"enabled"`
	Runner      string   `yaml:"runner"`
	NativeCheck string   `yaml:"native_check"`
	Runtime     []string `yaml:"runtime"`
	// Execution is empty for a native package. "wsl2" (only on
	// windows/amd64) makes a Windows package whose executable and runtime
	// files are those of linux/amd64: they run in WSL 2. It is build
	// configuration; the package manifest gets it as
	// osSettings.windows.execution (see ManifestOSSettings).
	Execution string `yaml:"execution"`
}

// ManifestOSSettings is the osSettings block that the manifest of the row's
// package must carry: nil for a native row.
func (e PlatformEntry) ManifestOSSettings() *contract.OSSettings {
	if e.Execution == "" {
		return nil
	}
	return &contract.OSSettings{Windows: &contract.WindowsSettings{Execution: e.Execution}}
}

// BuildPlatform is the os/arch the executable and the runtime files of the
// row are built for: linux/amd64 for a wsl2 row, the platform otherwise.
func (e PlatformEntry) BuildPlatform() string {
	if e.Execution == contract.ExecutionWSL2 {
		return "linux/amd64"
	}
	return e.Platform
}

// ExecutableName is the file name of the executable in the package of the
// row: the plain name of a Linux program for a wsl2 row, ExecutableFor
// otherwise.
func (e PlatformEntry) ExecutableName(executable string) string {
	if e.Execution == contract.ExecutionWSL2 {
		return executable
	}
	return ExecutableFor(executable, e.Platform)
}

// PlatformsFile is addons/<id>/package/platforms.yaml.
type PlatformsFile struct {
	Schema     int    `yaml:"schema"`
	Addon      string `yaml:"addon"`
	Executable string `yaml:"executable"`
	Manifest   string `yaml:"manifest"`
	UI         string `yaml:"ui"`
	// Skills is the directory laid at the package root as skills/: one folder
	// per skill the manifest declares. It is the same on every platform.
	Skills    string          `yaml:"skills"`
	Platforms []PlatformEntry `yaml:"platforms"`
}

// NativeCheckKind is the only native check that exists: the packaged
// executable is extracted on the native runner and started.
const NativeCheckKind = "executable-start"

var errNoPlatforms = errors.New("the release platform matrix has no enabled platform; enable a platform after its native check passes")

func loadPlatforms(file string) (*PlatformsFile, error) {
	if file == "" {
		return nil, errors.New("platform matrix file is required")
	}
	b, err := os.ReadFile(file) // #nosec G304 -- the packaging tool reads paths the operator passes on its command line
	if err != nil {
		return nil, fmt.Errorf("platform matrix: %w", err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	var p PlatformsFile
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("platform matrix: %w", err)
	}
	if p.Schema != 1 {
		return nil, fmt.Errorf("platform matrix: unsupported schema %d", p.Schema)
	}
	if !addonRE.MatchString(p.Addon) {
		return nil, fmt.Errorf("platform matrix: invalid addon id %q", p.Addon)
	}
	for _, f := range []string{p.Executable, p.Manifest} {
		if f == "" || !localPath(f) {
			return nil, fmt.Errorf("platform matrix: executable and manifest need a local path, got %q", f)
		}
	}
	if p.UI != "" && !localPath(p.UI) {
		return nil, fmt.Errorf("platform matrix: ui must be a local path, got %q", p.UI)
	}
	if p.Skills != "" && !localPath(p.Skills) {
		return nil, fmt.Errorf("platform matrix: skills must be a local path, got %q", p.Skills)
	}
	if len(p.Platforms) == 0 {
		return nil, errNoPlatforms
	}
	seen := map[string]bool{}
	for _, e := range p.Platforms {
		if !platformRE.MatchString(e.Platform) {
			return nil, fmt.Errorf("platform matrix: unsupported platform %q", e.Platform)
		}
		if seen[e.Platform] {
			return nil, fmt.Errorf("platform matrix: duplicate platform %q", e.Platform)
		}
		seen[e.Platform] = true
		if e.Execution != "" && (e.Execution != contract.ExecutionWSL2 || e.Platform != "windows/amd64") {
			return nil, fmt.Errorf("platform matrix: execution %q is only valid as %q on windows/amd64", e.Execution, contract.ExecutionWSL2)
		}
		if e.Enabled && (e.Runner == "" || e.NativeCheck != NativeCheckKind) {
			return nil, fmt.Errorf("platform matrix: enabled platform %q needs a runner and native_check %q", e.Platform, NativeCheckKind)
		}
		for _, r := range e.Runtime {
			if !localPath(r) || r == "manifest.json" || r == p.Executable || r == e.ExecutableName(p.Executable) {
				return nil, fmt.Errorf("platform matrix: invalid runtime file %q", r)
			}
		}
	}
	return &p, nil
}

func localPath(p string) bool {
	return p != "" && !path.IsAbs(p) && path.Clean(p) == p && p != "." && p != ".." && !strings.HasPrefix(p, "../") && !strings.ContainsAny(p, "\\:\x00")
}

func (p *PlatformsFile) enabled() ([]PlatformEntry, error) {
	var out []PlatformEntry
	for _, e := range p.Platforms {
		if e.Enabled {
			out = append(out, e)
		}
	}
	if len(out) == 0 {
		return nil, errNoPlatforms
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Platform < out[j].Platform })
	return out, nil
}

func (p *PlatformsFile) find(platform string) (PlatformEntry, bool) {
	for _, e := range p.Platforms {
		if e.Platform == platform {
			return e, true
		}
	}
	return PlatformEntry{}, false
}
