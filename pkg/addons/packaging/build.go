// SPDX-License-Identifier: MIT
package packaging

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// hostFor is the compatibility the package is validated against. The numbers
// are the ones the host runtime uses today; a package whose manifest range
// does not contain them is not installable and must not be published.
func hostFor(platform string) contract.HostCompatibility {
	os_, arch, _ := strings.Cut(platform, "/")
	return contract.HostCompatibility{Host: 1, API: 1, Protocol: 1, SDK: contract.HostSDKVersion, DataFormat: 1, OS: os_, Arch: arch}
}

type fileEntry struct {
	name string
	data []byte
	mode int64
}

// tarLayer writes entries in sorted order with fixed metadata so the same
// inputs always give the same digest.
func tarLayer(entries []fileEntry, role string) (v1.Layer, error) {
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
	var b bytes.Buffer
	w := tar.NewWriter(&b)
	for _, e := range entries {
		h := &tar.Header{Typeflag: tar.TypeReg, Name: e.name, Mode: e.mode, Size: int64(len(e.data)), ModTime: time.Unix(0, 0), Format: tar.FormatPAX}
		if err := w.WriteHeader(h); err != nil {
			return nil, err
		}
		if _, err := w.Write(e.data); err != nil {
			return nil, err
		}
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	data := b.Bytes()
	return tarball.LayerFromOpener(func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(data)), nil }, tarball.WithMediaType(types.OCILayer))
}

// rawImage replaces the manifest bytes of an image, which the mutate package
// cannot do for artifactType.
type rawImage struct {
	v1.Image
	raw []byte
}

func (r rawImage) RawManifest() ([]byte, error) { return r.raw, nil }
func (r rawImage) Manifest() (*v1.Manifest, error) {
	return v1.ParseManifest(bytes.NewReader(r.raw))
}
func (r rawImage) Digest() (v1.Hash, error) {
	h, _, err := v1.SHA256(bytes.NewReader(r.raw))
	return h, err
}
func (r rawImage) Size() (int64, error) { return int64(len(r.raw)), nil }

func stampManifest(template []byte, version, platform string) ([]byte, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(template, &m); err != nil {
		return nil, fmt.Errorf("manifest template: %w", err)
	}
	os_, arch, _ := strings.Cut(platform, "/")
	for k, v := range map[string]string{"version": version, "os": os_, "arch": arch} {
		b, _ := json.Marshal(v)
		m[k] = b
	}
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

func readRegular(p string) ([]byte, error) {
	info, err := os.Lstat(p)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", p)
	}
	return os.ReadFile(p) // #nosec G304 -- the packaging tool reads paths the operator passes on its command line
}

func cmdBuild(args []string) error {
	fs_ := flag.NewFlagSet("build", flag.ContinueOnError)
	out := fs_.String("layout", "", "output OCI layout directory (must not exist or be empty)")
	version := fs_.String("version", "", "release version, semver without the v prefix")
	platform := fs_.String("platform", "", "os/arch")
	config := fs_.String("config", "addons/tables/package/platforms.yaml", "platform matrix file")
	exe := fs_.String("executable", "", "built executable of the platform")
	runtimeDir := fs_.String("runtime-dir", "", "directory that holds the runtime files of the platform")
	revision := fs_.String("revision", "", "source commit, 40 hex characters")
	local := fs_.Bool("local-check", false, "allow a platform that is not enabled; the layout is marked unpublishable")
	root := fs_.String("root", ".", "repository root that the matrix paths are relative to")
	if err := fs_.Parse(args); err != nil {
		return err
	}
	if *out == "" || *exe == "" {
		return errors.New("--layout and --executable are required")
	}
	if !versionRE.MatchString(*version) {
		return fmt.Errorf("version %q is not a semantic version without a v prefix or build metadata", *version)
	}
	if !platformRE.MatchString(*platform) {
		return fmt.Errorf("platform %q is not os/arch of linux or darwin on amd64 or arm64", *platform)
	}
	if *revision != "" && !shaRE.MatchString(*revision) {
		return errors.New("revision must be 40 lower-case hex characters")
	}
	cfg, err := loadPlatforms(*config)
	if err != nil {
		return err
	}
	entry, ok := cfg.find(*platform)
	if !ok {
		return fmt.Errorf("platform %s is not in the platform matrix", *platform)
	}
	if !entry.Enabled && !*local {
		return fmt.Errorf("platform %s is not enabled in the platform matrix", *platform)
	}
	if st, err := os.Stat(*out); err == nil {
		ents, _ := os.ReadDir(*out)
		if !st.IsDir() || len(ents) > 0 {
			return fmt.Errorf("layout %s exists and is not empty", *out)
		}
	}
	template, err := readRegular(filepath.Join(*root, cfg.Manifest))
	if err != nil {
		return err
	}
	manifest, err := stampManifest(template, *version, *platform)
	if err != nil {
		return err
	}
	m, err := contract.ParseManifest(manifest)
	if err != nil {
		return fmt.Errorf("manifest: %w", err)
	}
	if err := contract.ValidateManifest(m, hostFor(*platform)); err != nil {
		return fmt.Errorf("manifest: %w", err)
	}
	if m.ID != cfg.Addon || m.Executable != cfg.Executable {
		return errors.New("manifest id or executable differs from the platform matrix")
	}
	exeBytes, err := readRegular(*exe)
	if err != nil || len(exeBytes) == 0 {
		return fmt.Errorf("executable %s: missing, empty or not a regular file", *exe)
	}
	files := []fileEntry{{name: m.Executable, data: exeBytes, mode: 0o755}}
	for _, r := range entry.Runtime {
		if *runtimeDir == "" {
			return fmt.Errorf("runtime file %s is required by the platform and --runtime-dir is not set", r)
		}
		b, err := readRegular(filepath.Join(*runtimeDir, filepath.FromSlash(r)))
		if err != nil {
			return fmt.Errorf("runtime file %s: %w", r, err)
		}
		files = append(files, fileEntry{name: r, data: b, mode: 0o644})
	}
	type role struct {
		name    string
		entries []fileEntry
	}
	roles := []role{{roleExecutable, files}}
	if u := m.Capabilities.UI; u != nil {
		if cfg.UI == "" {
			return errors.New("the manifest declares a UI and the platform matrix names no ui directory")
		}
		ui, err := collectUI(filepath.Join(*root, cfg.UI), path.Dir(u.Entry))
		if err != nil {
			return err
		}
		declared := append([]string{u.Entry}, u.CSS...)
		if u.Icon != "" {
			declared = append(declared, u.Icon)
		}
		have := map[string]bool{}
		for _, f := range ui {
			have[f.name] = true
		}
		for _, d := range declared {
			if !have[d] {
				return fmt.Errorf("declared UI asset %s is missing from %s", d, cfg.UI)
			}
		}
		roles = append(roles, role{roleUI, ui})
	}
	roles = append(roles, role{roleManifest, []fileEntry{{name: "manifest.json", data: manifest, mode: 0o644}}})

	img := mutate.ConfigMediaType(mutate.MediaType(empty.Image, types.OCIManifestSchema1), types.OCIConfigJSON)
	for _, r := range roles {
		l, err := tarLayer(r.entries, r.name)
		if err != nil {
			return err
		}
		img, err = mutate.Append(img, mutate.Addendum{Layer: l, Annotations: map[string]string{AnnotationLayer: r.name}})
		if err != nil {
			return err
		}
	}
	osName, arch, _ := strings.Cut(*platform, "/")
	cf, err := img.ConfigFile()
	if err != nil {
		return err
	}
	cf.OS, cf.Architecture, cf.Created = osName, arch, v1.Time{}
	cf.History = nil
	if img, err = mutate.ConfigFile(img, cf); err != nil {
		return err
	}
	mf, err := img.Manifest()
	if err != nil {
		return err
	}
	mf.ArtifactType = ArtifactType
	mf.Annotations = map[string]string{AnnotationID: m.ID, AnnotationVersion: *version, AnnotationOCIVer: *version, AnnotationPlatform: *platform}
	if *revision != "" {
		mf.Annotations[AnnotationRevision] = *revision
	}
	raw, err := json.Marshal(mf)
	if err != nil {
		return err
	}
	final := rawImage{Image: img, raw: raw}
	digest, err := final.Digest()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*out, 0o750); err != nil {
		return err
	}
	lp, err := layout.Write(*out, empty.Index)
	if err != nil {
		return err
	}
	if err := lp.AppendImage(final, layout.WithPlatform(v1.Platform{OS: osName, Architecture: arch}), layout.WithAnnotations(map[string]string{"org.opencontainers.image.ref.name": *version})); err != nil {
		return err
	}
	r := Receipt{Schema: 1, Addon: m.ID, Version: *version, Platform: *platform, Revision: *revision, Publishable: entry.Enabled && !*local, ManifestDigest: digest.String(), ConfigDigest: mf.Config.Digest.String()}
	for i, d := range mf.Layers {
		r.Layers = append(r.Layers, ReceiptLayer{Role: roles[i].name, Digest: d.Digest.String(), Size: d.Size, MediaType: string(d.MediaType)})
	}
	rb, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(*out, receiptFile), append(rb, '\n'), 0o600); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "digest=%s\nplatform=%s\nversion=%s\npublishable=%t\n", digest, *platform, *version, r.Publishable)
	return nil
}

// collectUI reads every file under the UI directory that sits below prefix
// (the top directory of the declared entry). Links and special files are
// refused so a build output cannot smuggle content from outside the tree.
func collectUI(dir, prefix string) ([]fileEntry, error) {
	top := filepath.Join(dir, filepath.FromSlash(prefix))
	var out []fileEntry
	err := filepath.WalkDir(top, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("UI file %s is not a regular file", p)
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(p) // #nosec G122,G304 -- the tree is owned by this process and locked; the walk only normalises its own files
		if err != nil {
			return err
		}
		out = append(out, fileEntry{name: filepath.ToSlash(rel), data: b, mode: 0o644})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("UI directory: %w", err)
	}
	return out, nil
}
