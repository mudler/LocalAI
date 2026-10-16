// SPDX-License-Identifier: MIT
package packaging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/types"
)

// MergedFile lists the legs a merged layout joined, with their receipts.
const mergedFile = "merged.json"

type merged struct {
	Schema      int       `json:"schema"`
	Addon       string    `json:"addon"`
	Version     string    `json:"version"`
	Revision    string    `json:"revision,omitempty"`
	IndexDigest string    `json:"indexDigest"`
	Legs        []Receipt `json:"legs"`
}

// rawIndex replaces the manifest bytes of an image index.
type rawIndex struct {
	inner v1.ImageIndex
	raw   []byte
}

func (r rawIndex) MediaType() (types.MediaType, error) { return r.inner.MediaType() }
func (r rawIndex) RawManifest() ([]byte, error)        { return r.raw, nil }
func (r rawIndex) IndexManifest() (*v1.IndexManifest, error) {
	return v1.ParseIndexManifest(bytes.NewReader(r.raw))
}
func (r rawIndex) Digest() (v1.Hash, error) {
	h, _, err := v1.SHA256(bytes.NewReader(r.raw))
	return h, err
}
func (r rawIndex) Size() (int64, error)                        { return int64(len(r.raw)), nil }
func (r rawIndex) Image(h v1.Hash) (v1.Image, error)           { return r.inner.Image(h) }
func (r rawIndex) ImageIndex(h v1.Hash) (v1.ImageIndex, error) { return r.inner.ImageIndex(h) }

func cmdMatrix(args []string) error {
	fs := flag.NewFlagSet("matrix", flag.ContinueOnError)
	config := fs.String("config", "", "platform matrix file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadPlatforms(*config)
	if err != nil {
		return err
	}
	en, err := cfg.enabled()
	if err != nil {
		return err
	}
	type row struct {
		Platform string `json:"platform"`
		Runner   string `json:"runner"`
		Slug     string `json:"slug"`
	}
	var rows []row
	for _, e := range en {
		rows = append(rows, row{e.Platform, e.Runner, strings.ReplaceAll(e.Platform, "/", "-")})
	}
	b, err := json.Marshal(map[string]any{"include": rows})
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "%s\n", b)
	return nil
}

// verifyMerged checks a merged layout against the platform matrix: exactly
// the enabled platforms, one image each, the same version, every leg
// publishable and valid.
func verifyMerged(dir string, cfg *PlatformsFile) (*merged, v1.Hash, error) {
	var zero v1.Hash
	b, err := os.ReadFile(filepath.Join(dir, mergedFile)) // #nosec G304 -- the packaging tool reads paths the operator passes on its command line
	if err != nil {
		return nil, zero, fail("E_MERGE", "%s is missing", mergedFile)
	}
	var m merged
	if err := strictJSON(b, &m); err != nil {
		return nil, zero, fail("E_MERGE", "%s: %v", mergedFile, err)
	}
	idx, err := readRootIndex(dir)
	if err != nil {
		return nil, zero, err
	}
	if len(idx.Manifests) != 1 || idx.Manifests[0].MediaType != types.OCIImageIndex {
		return nil, zero, fail("E_MERGE", "a merged layout holds exactly one image index")
	}
	top := idx.Manifests[0]
	if top.Digest.String() != m.IndexDigest {
		return nil, zero, fail("E_DIGEST", "merge receipt names another index digest")
	}
	raw, err := readBlob(dir, top.Digest, top.Size)
	if err != nil {
		return nil, zero, err
	}
	var im v1.IndexManifest
	if err := strictJSON(raw, &im); err != nil {
		return nil, zero, fail("E_MERGE", "image index: %v", err)
	}
	if im.ArtifactType != IndexArtifactType || im.Annotations[AnnotationID] != cfg.Addon || im.Annotations[AnnotationVersion] != m.Version {
		return nil, zero, fail("E_ARTIFACT_TYPE", "image index is not a %s %s package index", cfg.Addon, m.Version)
	}
	enabled, err := cfg.enabled()
	if err != nil {
		return nil, zero, fail("E_PLATFORMS", "%v", err)
	}
	want := map[string]bool{}
	for _, e := range enabled {
		want[e.Platform] = true
	}
	got := map[string]bool{}
	receipts := map[string]Receipt{}
	for _, r := range m.Legs {
		receipts[r.Platform] = r
	}
	for _, d := range im.Manifests {
		if d.Platform == nil {
			return nil, zero, fail("E_PLATFORM", "index entry %s has no platform", d.Digest)
		}
		p := d.Platform.OS + "/" + d.Platform.Architecture
		if got[p] {
			return nil, zero, fail("E_PLATFORMS", "platform %s appears twice", p)
		}
		got[p] = true
		if !want[p] {
			return nil, zero, fail("E_PLATFORMS", "platform %s is not enabled in the platform matrix", p)
		}
		rec, ok := receipts[p]
		if !ok || !rec.Publishable {
			return nil, zero, fail("E_PLATFORMS", "platform %s has no publishable receipt", p)
		}
		l, err := loadLegByDescriptor(dir, d)
		if err != nil {
			return nil, zero, err
		}
		if _, err := verifyLoaded(l, &rec, p, m.Version, cfg); err != nil {
			return nil, zero, fmt.Errorf("%s: %w", p, err)
		}
	}
	for _, p := range sortedKeys(want) {
		if !got[p] {
			return nil, zero, fail("E_PLATFORMS", "platform %s is enabled and has no image: a partial index is never published", p)
		}
	}
	return &m, top.Digest, nil
}

func cmdMerge(args []string) error {
	fs := flag.NewFlagSet("merge", flag.ContinueOnError)
	out := fs.String("out", "", "merged layout directory (must not exist or be empty)")
	config := fs.String("config", "", "platform matrix file")
	version := fs.String("version", "", "release version")
	revision := fs.String("revision", "", "source commit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	legs := fs.Args()
	if *out == "" || *version == "" || len(legs) == 0 {
		return errors.New("--out, --version, --config and at least one leg layout are required")
	}
	cfg, err := loadPlatforms(*config)
	if err != nil {
		return err
	}
	if !versionRE.MatchString(*version) {
		return fmt.Errorf("version %q is not a semantic version", *version)
	}
	if ents, err := os.ReadDir(*out); err == nil && len(ents) > 0 {
		return errors.New("output layout is not empty")
	}
	type item struct {
		platform string
		img      v1.Image
		rec      Receipt
	}
	var items []item
	seen := map[string]bool{}
	for _, dir := range legs {
		_, rec, err := loadLeg(dir)
		if err != nil {
			return fmt.Errorf("%s: %w", dir, err)
		}
		if seen[rec.Platform] {
			return fail("E_PLATFORMS", "platform %s appears in two legs", rec.Platform)
		}
		seen[rec.Platform] = true
		if _, err := verifyLeg(dir, rec.Platform, *version, cfg); err != nil {
			return fmt.Errorf("%s: %w", rec.Platform, err)
		}
		if !rec.Publishable {
			return fail("E_PLATFORMS", "leg %s is a local check layout, not publishable", rec.Platform)
		}
		lp, err := layout.FromPath(dir)
		if err != nil {
			return err
		}
		ii, err := lp.ImageIndex()
		if err != nil {
			return err
		}
		im, err := ii.IndexManifest()
		if err != nil || len(im.Manifests) != 1 {
			return fail("E_LAYOUT", "leg %s: unreadable index", dir)
		}
		img, err := ii.Image(im.Manifests[0].Digest)
		if err != nil {
			return err
		}
		items = append(items, item{rec.Platform, img, *rec})
	}
	// Completeness is checked on the final index too (verifyMerged), but a
	// missing leg is reported before any output exists.
	enabled, err := cfg.enabled()
	if err != nil {
		return fail("E_PLATFORMS", "%v", err)
	}
	for _, e := range enabled {
		if !seen[e.Platform] {
			return fail("E_PLATFORMS", "platform %s is enabled and has no leg: a partial index is never created", e.Platform)
		}
	}
	for p := range seen {
		if _, ok := cfg.find(p); !ok {
			return fail("E_PLATFORMS", "leg platform %s is not in the platform matrix", p)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].platform < items[j].platform })
	var idx v1.ImageIndex = mutate.IndexMediaType(empty.Index, types.OCIImageIndex)
	var receipts []Receipt
	for _, it := range items {
		osName, arch, _ := strings.Cut(it.platform, "/")
		idx = mutate.AppendManifests(idx, mutate.IndexAddendum{Add: it.img, Descriptor: v1.Descriptor{Platform: &v1.Platform{OS: osName, Architecture: arch}}})
		receipts = append(receipts, it.rec)
	}
	im, err := idx.IndexManifest()
	if err != nil {
		return err
	}
	im.ArtifactType = IndexArtifactType
	im.Annotations = map[string]string{AnnotationID: cfg.Addon, AnnotationVersion: *version, AnnotationOCIVer: *version}
	if *revision != "" {
		if !shaRE.MatchString(*revision) {
			return errors.New("revision must be 40 lower-case hex characters")
		}
		im.Annotations[AnnotationRevision] = *revision
	}
	raw, err := json.Marshal(im)
	if err != nil {
		return err
	}
	final := rawIndex{inner: idx, raw: raw}
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
	if err := lp.AppendIndex(final); err != nil {
		return err
	}
	mb, err := json.MarshalIndent(merged{Schema: 1, Addon: cfg.Addon, Version: *version, Revision: *revision, IndexDigest: digest.String(), Legs: receipts}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(*out, mergedFile), append(mb, '\n'), 0o600); err != nil {
		return err
	}
	if _, _, err := verifyMerged(*out, cfg); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "index=%s\nplatforms=%d\nversion=%s\n", digest, len(items), *version)
	return nil
}

// cmdNativeCheck extracts the verified package and starts its executable
// with an argument the add-on protocol rejects. An executable that loads and
// runs on this platform exits 2; a missing library, a wrong format or a crash
// does not. The check refuses to run for a platform other than the host's, so
// an advertised platform is only ever checked natively.
func cmdNativeCheck(args []string) error {
	fs := flag.NewFlagSet("native-check", flag.ContinueOnError)
	dir := fs.String("layout", "", "platform OCI layout")
	platform := fs.String("platform", "", "platform of the layout")
	version := fs.String("version", "", "version of the layout")
	config := fs.String("config", "", "platform matrix file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	host := runtime.GOOS + "/" + runtime.GOARCH
	if *platform != host {
		return fmt.Errorf("native check of %s needs a native runner; this host is %s", *platform, host)
	}
	cfg, err := loadPlatforms(*config)
	if err != nil {
		return err
	}
	v, err := verifyLeg(*dir, *platform, *version, cfg)
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "addon-native-check-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	if err := writeExtracted(v, tmp); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(tmp, v.Manifest.Executable)) // #nosec G204 -- runs the add-on executable of the package under verification, from a private temporary directory
	cmd.Dir = tmp
	cmd.Stdin = nil
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = io.Discard, &buf
	err = cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 2 {
		_, _ = fmt.Fprintf(stdout, "native-check=passed platform=%s\n", *platform)
		return nil
	}
	return fmt.Errorf("native check failed: executable did not start and reject its arguments (%v): %s", err, strings.TrimSpace(buf.String()))
}
