// SPDX-License-Identifier: MIT
package packaging

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/google/go-containerregistry/pkg/v1/types"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Limits of one package, in line with the installer's defaults.
const (
	maxPackageBytes = 512 << 20
	maxPackageFiles = 10000
)

// secondReact lists markers that only appear in a bundled React runtime. The
// host supplies React through the UI SDK; a package that carries its own
// copy would run two Reacts on one page.
var secondReact = []string{
	"__SECRET_INTERNALS_DO_NOT_USE_OR_YOU_WILL_BE_FIRED",
	"__CLIENT_INTERNALS_DO_NOT_USE_OR_WARN_USERS_THEY_CANNOT_UPGRADE",
	"react-dom.production",
	"react.production",
	"ReactCurrentDispatcher",
	"ReactDOMCurrentDispatcher",
}

// extracted is one validated file of a package.
type extracted struct {
	role string
	name string
	data []byte
	exec bool
}

// Verified is what a successful verification learned about a leg.
type Verified struct {
	Receipt  Receipt
	Manifest contract.Manifest
	Files    []extracted
}

// openLayers decompresses and parses every layer with the installer's own
// path rules: clean relative names, regular files and directories only, no
// duplicate across layers, bounded totals. It also enforces package rules the
// installer does not: no set-id or write bits and execute only where declared.
func (l *leg) openLayers() ([]extracted, error) {
	var out []extracted
	seen := map[string]bool{}
	var total int64
	for i, d := range l.manifest.Layers {
		if d.MediaType != types.OCILayer {
			return nil, fail("E_LAYERS", "layer %d has media type %q, expected %q", i, d.MediaType, types.OCILayer)
		}
		role := d.Annotations[AnnotationLayer]
		blob, err := readBlob(l.dir, d.Digest, d.Size)
		if err != nil {
			return nil, err
		}
		gz, err := gzip.NewReader(bytes.NewReader(blob))
		if err != nil {
			return nil, fail("E_LAYERS", "layer %d is not gzip: %v", i, err)
		}
		t := tar.NewReader(gz)
		for {
			h, err := t.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, fail("E_LAYERS", "layer %d: %v", i, err)
			}
			n := strings.TrimSuffix(h.Name, "/")
			if n == "" || n == "." || path.Clean(n) != n || strings.HasPrefix(n, "/") || n == ".." || strings.HasPrefix(n, "../") || strings.ContainsAny(n, "\\:\x00") || seen[n] {
				return nil, fail("E_PATH", "unsafe or duplicate path %q", h.Name)
			}
			seen[n] = true
			if h.Typeflag == tar.TypeDir {
				continue
			}
			if h.Typeflag != tar.TypeReg {
				return nil, fail("E_PATH", "%q is not a regular file", h.Name)
			}
			if h.Mode&0o7000 != 0 || h.Mode&0o022 != 0 {
				return nil, fail("E_MODE", "%q has mode %04o: set-id, sticky and group or world write bits are not allowed", n, h.Mode)
			}
			total += h.Size
			if h.Size < 0 || total > maxPackageBytes || len(out) >= maxPackageFiles {
				return nil, fail("E_LAYERS", "package exceeds its size or file limit")
			}
			data, err := readAll(t, h.Size)
			if err != nil || int64(len(data)) != h.Size {
				return nil, fail("E_LAYERS", "layer %d: short or oversized file %q", i, n)
			}
			out = append(out, extracted{role: role, name: n, data: data, exec: h.Mode&0o111 != 0})
		}
	}
	return out, nil
}

// verifyLeg applies every package rule to one platform layout.
func verifyLeg(dir, platform, version string, cfg *PlatformsFile) (*Verified, error) {
	l, rec, err := loadLeg(dir)
	if err != nil {
		return nil, err
	}
	return verifyLoaded(l, rec, platform, version, cfg)
}

func verifyLoaded(l *leg, rec *Receipt, platform, version string, cfg *PlatformsFile) (*Verified, error) {
	entry, ok := cfg.find(platform)
	if !ok {
		return nil, fail("E_PLATFORM", "platform %s is not in the platform matrix", platform)
	}
	// Platform: descriptor, annotation, config and receipt must all agree.
	osName, arch, _ := strings.Cut(platform, "/")
	p := l.descriptor.Platform
	if p == nil || p.OS != osName || p.Architecture != arch || l.config.OS != osName || l.config.Architecture != arch ||
		l.manifest.Annotations[AnnotationPlatform] != platform || rec.Platform != platform {
		return nil, fail("E_PLATFORM", "layout is not a %s image", platform)
	}
	if l.manifest.ArtifactType != ArtifactType {
		return nil, fail("E_ARTIFACT_TYPE", "artifactType is %q, expected %q", l.manifest.ArtifactType, ArtifactType)
	}
	if l.manifest.Annotations[AnnotationID] != cfg.Addon || l.manifest.Annotations[AnnotationVersion] != version || rec.Version != version || rec.Addon != cfg.Addon {
		return nil, fail("E_VERSION", "annotations or receipt do not name %s %s", cfg.Addon, version)
	}
	if rec.ManifestDigest != l.descriptor.Digest.String() || rec.ConfigDigest != l.manifest.Config.Digest.String() {
		return nil, fail("E_DIGEST", "receipt digests do not match the image")
	}
	if len(rec.Layers) != len(l.manifest.Layers) {
		return nil, fail("E_DIGEST", "receipt lists %d layers, image has %d", len(rec.Layers), len(l.manifest.Layers))
	}
	for i, d := range l.manifest.Layers {
		if rec.Layers[i].Digest != d.Digest.String() || rec.Layers[i].Size != d.Size || rec.Layers[i].MediaType != string(d.MediaType) || rec.Layers[i].Role != d.Annotations[AnnotationLayer] {
			return nil, fail("E_DIGEST", "receipt layer %d does not match the image", i)
		}
	}
	files, err := l.openLayers()
	if err != nil {
		return nil, err
	}
	var manifestBytes []byte
	for _, f := range files {
		if f.name == "manifest.json" {
			manifestBytes = f.data
		}
	}
	if manifestBytes == nil {
		return nil, fail("E_MANIFEST", "manifest.json is missing")
	}
	m, err := contract.ParseManifest(manifestBytes)
	if err != nil {
		return nil, fail("E_MANIFEST", "%v", err)
	}
	if m.OS != osName || m.Arch != arch {
		return nil, fail("E_PLATFORM", "manifest targets %s/%s, image is %s", m.OS, m.Arch, platform)
	}
	if m.Version != version {
		return nil, fail("E_VERSION", "manifest version %q, expected %q", m.Version, version)
	}
	if m.ID != cfg.Addon {
		return nil, fail("E_MANIFEST", "manifest id %q, expected %q", m.ID, cfg.Addon)
	}
	if err := contract.ValidateManifest(m, hostFor(platform)); err != nil {
		return nil, fail("E_MANIFEST", "%v", err)
	}

	// Layer roles: executable, ui when declared, manifest. Nothing else.
	want := []string{roleExecutable}
	if m.Capabilities.UI != nil {
		want = append(want, roleUI)
	}
	want = append(want, roleManifest)
	hasUI := false
	for _, d := range l.manifest.Layers {
		hasUI = hasUI || d.Annotations[AnnotationLayer] == roleUI
	}
	if m.Capabilities.UI != nil && !hasUI {
		return nil, fail("E_UI", "the manifest declares a UI and the image has no ui layer")
	}
	if len(l.manifest.Layers) != len(want) {
		return nil, fail("E_LAYERS", "image has %d layers, expected %d (%s)", len(l.manifest.Layers), len(want), strings.Join(want, ", "))
	}
	for i, d := range l.manifest.Layers {
		if d.Annotations[AnnotationLayer] != want[i] {
			return nil, fail("E_LAYERS", "layer %d has role %q, expected %q", i, d.Annotations[AnnotationLayer], want[i])
		}
	}
	byName := map[string]extracted{}
	for _, f := range files {
		byName[f.name] = f
	}
	// Entrypoint and runtime dependencies.
	exe, ok := byName[m.Executable]
	if !ok || exe.role != roleExecutable {
		return nil, fail("E_ENTRYPOINT", "executable %q is missing from the executable layer", m.Executable)
	}
	if !exe.exec || len(exe.data) == 0 {
		return nil, fail("E_MODE", "executable %q is empty or not executable", m.Executable)
	}
	allowed := map[string]string{m.Executable: roleExecutable, "manifest.json": roleManifest}
	for _, r := range entry.Runtime {
		allowed[r] = roleExecutable
		if f, ok := byName[r]; !ok || f.role != roleExecutable {
			return nil, fail("E_RUNTIME", "runtime file %q is missing", r)
		}
	}
	if u := m.Capabilities.UI; u != nil {
		for _, a := range append(append([]string{u.Entry}, u.CSS...), u.Icon) {
			if a == "" {
				continue
			}
			if f, ok := byName[a]; !ok || f.role != roleUI {
				return nil, fail("E_UI", "declared UI asset %q is missing", a)
			}
		}
		if u.Icon != "" {
			if err := checkIcon(u.Icon, byName[u.Icon].data); err != nil {
				return nil, err
			}
		}
		top := path.Dir(u.Entry) + "/"
		for _, f := range files {
			if f.role == roleUI && !strings.HasPrefix(f.name, top) {
				return nil, fail("E_LAYERS", "UI layer file %q is outside %s", f.name, top)
			}
			if f.role == roleUI && (strings.HasSuffix(f.name, ".js") || strings.HasSuffix(f.name, ".mjs")) {
				for _, marker := range secondReact {
					if bytes.Contains(f.data, []byte(marker)) {
						return nil, fail("E_REACT", "UI file %q bundles its own React runtime (%s); the host provides React", f.name, marker)
					}
				}
			}
		}
	}
	for _, f := range files {
		if r, ok := allowed[f.name]; ok {
			if f.role != r {
				return nil, fail("E_LAYERS", "file %q is in layer %q, expected %q", f.name, f.role, r)
			}
			continue
		}
		if f.role != roleUI {
			return nil, fail("E_LAYERS", "unexpected file %q in the %s layer", f.name, f.role)
		}
		if m.Capabilities.UI == nil {
			return nil, fail("E_LAYERS", "unexpected UI file %q", f.name)
		}
	}
	for _, f := range files {
		if f.exec && f.name != m.Executable {
			return nil, fail("E_MODE", "%q is executable and is not the declared entrypoint", f.name)
		}
	}
	return &Verified{Receipt: *rec, Manifest: m, Files: files}, nil
}

func cmdVerify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	dir := fs.String("layout", "", "platform OCI layout")
	platform := fs.String("platform", "", "expected os/arch")
	version := fs.String("version", "", "expected version")
	config := fs.String("config", "addons/tables/package/platforms.yaml", "platform matrix file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dir == "" || *platform == "" || *version == "" {
		return errors.New("--layout, --platform and --version are required")
	}
	cfg, err := loadPlatforms(*config)
	if err != nil {
		return err
	}
	v, err := verifyLeg(*dir, *platform, *version, cfg)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "verified=%s %s %s digest=%s files=%d\n", cfg.Addon, *version, *platform, v.Receipt.ManifestDigest, len(v.Files))
	return nil
}

// writeExtracted restores a verified package to dest. The executable gets its
// validated mode back; every other file is 0644.
func writeExtracted(v *Verified, dest string) error {
	for _, f := range v.Files {
		p := filepath.Join(dest, filepath.FromSlash(f.name))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if f.exec {
			mode = 0o755
		}
		if err := os.WriteFile(p, f.data, mode); err != nil {
			return err
		}
		if err := os.Chmod(p, mode); err != nil {
			return err
		}
	}
	return nil
}

func cmdExtract(args []string) error {
	fs := flag.NewFlagSet("extract", flag.ContinueOnError)
	dir := fs.String("layout", "", "platform OCI layout")
	platform := fs.String("platform", "", "expected os/arch")
	version := fs.String("version", "", "expected version")
	config := fs.String("config", "addons/tables/package/platforms.yaml", "platform matrix file")
	dest := fs.String("dest", "", "empty destination directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dest == "" {
		return errors.New("--dest is required")
	}
	if ents, err := os.ReadDir(*dest); err == nil && len(ents) > 0 {
		return errors.New("destination is not empty")
	}
	cfg, err := loadPlatforms(*config)
	if err != nil {
		return err
	}
	v, err := verifyLeg(*dir, *platform, *version, cfg)
	if err != nil {
		return err
	}
	return writeExtracted(v, *dest)
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// checkIcon applies the installer's icon rule at build time, so that a
// package whose icon the host would refuse is never published.
func checkIcon(name string, b []byte) error {
	media := contract.IconTypes[strings.ToLower(path.Ext(name))]
	if media == "" || len(b) == 0 || int64(len(b)) > contract.MaxIconBytes {
		return fail("E_UI", "icon %q has an unsupported type or size", name)
	}
	ok := false
	switch media {
	case "image/png":
		ok = bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n"))
	case "image/jpeg":
		ok = bytes.HasPrefix(b, []byte{0xff, 0xd8, 0xff})
	case "image/webp":
		ok = len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP"
	}
	if !ok {
		return fail("E_UI", "icon %q does not have the signature of its type", name)
	}
	return nil
}
