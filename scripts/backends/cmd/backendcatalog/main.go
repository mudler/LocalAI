// SPDX-License-Identifier: MIT

// Command backendcatalog works with the backend library, the source of truth
// of the backends, and the backend gallery, backend/index.yaml, which is a
// generated copy of it.
//
// The library, gallery/backends/index.yaml, is where backends are edited. The
// gallery is written from it by this command and stays only so releases before
// the cutover keep working; it is never edited by hand. Before anything is
// written every backend is converted to the gallery and back, and the command
// fails when one does not come back equal on every field the gallery reader
// uses.
//
// Generate the gallery from the library (no network; this is what CI checks):
//
//	go run ./scripts/backends/cmd/backendcatalog \
//	  --library gallery/backends/index.yaml --out backend/index.yaml
//
// Write the golden files the tests in core/gallery compare against:
//
//	go run ./scripts/backends/cmd/backendcatalog \
//	  --library gallery/backends/index.yaml --golden core/gallery/testdata/backend_library
//
// Pin the release images of the library again, after the backend images are
// rebuilt. This reads the registry; it is the only mode that does. A digest is
// never edited by hand:
//
//	go run ./scripts/backends/cmd/backendcatalog \
//	  --library gallery/backends/index.yaml --repin
//
// Add or change backends: write them in the gallery shape, as backend/index.yaml
// used to hold them, in a scratch file, and merge them into the library. The
// new backends are pinned from the registry; the other entries are not touched.
// A backend whose image is not published yet is written unpinned and mutable:
//
//	go run ./scripts/backends/cmd/backendcatalog \
//	  --add my-backends.yaml --library gallery/backends/index.yaml
//
// Build the library from a backend gallery, once, pinning every release image
// by the digest the registry serves for each platform:
//
//	go run ./scripts/backends/cmd/backendcatalog \
//	  --import-gallery backend/index.yaml --library gallery/backends/index.yaml
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"gopkg.in/yaml.v3"
)

const libraryHeader = `# Backend library: the backends of LocalAI in the library entry shape. This
# file is the source of truth; edit it, not backend/index.yaml, which is
# generated from it by scripts/backends/cmd/backendcatalog and stays only so
# releases before the cutover keep working. CI fails when backend/index.yaml
# differs from the generated copy.
#
# An entry is one backend image for one platform, so a backend built for
# several platforms has several entries. A release image (latest-*) is pinned
# by its digest. A development image (master-*) is mutable: it keeps its tag
# and the install pins it. An entry marked mutable that is not a development
# image is a release image the registry could not be read for when the library
# was rendered; pin it again with --repin. A meta backend has capabilities and
# no image. Never edit a digest by hand: run --repin.
`

const galleryHeader = `# Backend gallery: a GENERATED copy of the backend library,
# gallery/backends/index.yaml. The library is the source of truth; edit it, not
# this file. This file is written by scripts/backends/cmd/backendcatalog and
# stays only so releases before the cutover keep working. CI fails when it
# differs from the generated copy:
#   go run ./scripts/backends/cmd/backendcatalog --library gallery/backends/index.yaml --out backend/index.yaml
`

// pinner finds the platforms of an image and the digest of each.
type pinner interface {
	Pin(ctx context.Context, image string) ([]gallery.BackendPin, error)
}

// registryPinner reads the manifest of the image, or of the index that names
// one image for each platform, and returns the digest of each platform image.
type registryPinner struct{}

func (registryPinner) Pin(ctx context.Context, image string) ([]gallery.BackendPin, error) {
	ref, err := name.ParseReference(image)
	if err != nil {
		return nil, err
	}
	desc, err := remote.Get(ref, remote.WithContext(ctx))
	if err != nil {
		return nil, err
	}
	var pins []gallery.BackendPin
	seen := map[string]bool{}
	add := func(os, arch, digest string) {
		if os == "" || os == "unknown" || arch == "" || arch == "unknown" || seen[os+"/"+arch] {
			return
		}
		seen[os+"/"+arch] = true
		pins = append(pins, gallery.BackendPin{OS: os, Arch: arch, Digest: digest})
	}
	if desc.MediaType.IsIndex() {
		idx, err := desc.ImageIndex()
		if err != nil {
			return nil, err
		}
		m, err := idx.IndexManifest()
		if err != nil {
			return nil, err
		}
		for _, d := range m.Manifests {
			if d.Platform != nil {
				add(d.Platform.OS, d.Platform.Architecture, d.Digest.String())
			}
		}
	} else {
		img, err := desc.Image()
		if err != nil {
			return nil, err
		}
		cfg, err := img.ConfigFile()
		if err != nil {
			return nil, err
		}
		add(cfg.OS, cfg.Architecture, desc.Digest.String())
	}
	if len(pins) == 0 {
		return nil, errors.New("the image names no platform")
	}
	sort.Slice(pins, func(i, j int) bool { return pins[i].OS+pins[i].Arch < pins[j].OS+pins[j].Arch })
	return pins, nil
}

// guessPlatform is the platform of an image the registry could not be read
// for, from the tag the build matrix gives it.
func guessPlatform(uri string) (string, string) {
	switch {
	case strings.Contains(uri, "-darwin-arm64-"):
		return "darwin", "arm64"
	case strings.Contains(uri, "-arm64-"):
		return "linux", "arm64"
	}
	return "linux", "amd64"
}

// report counts what a run of pinning did.
type report struct {
	Entries     int // gallery entries read
	Folded      int // entries folded into an earlier entry of the same name
	Meta        int
	Concrete    int
	Rows        int
	Pinned      int      // rows pinned by a digest
	Development int      // rows of a development image, pinned at install
	Unpinned    []string // release images the registry could not be read for
}

func (r report) print(w io.Writer) {
	fmt.Fprintf(w, "backendcatalog: %d gallery entries: %d concrete, %d meta, %d folded into a twin of the same name\n", r.Entries, r.Concrete, r.Meta, r.Folded)
	fmt.Fprintf(w, "backendcatalog: %d library entries: %d meta, %d pinned by digest, %d development (pinned at install), %d release backends not pinned\n", r.Rows, r.Meta, r.Pinned, r.Development, len(r.Unpinned))
	if len(r.Unpinned) > 0 {
		fmt.Fprintf(w, "backendcatalog: not pinned (registry unreachable or image missing): %s\n", strings.Join(r.Unpinned, ", "))
	}
}

// build converts gallery backends to library entries. A development image is
// never pinned. A release image is pinned by the registry; if the pinner fails
// the entry is written unpinned and mutable and is reported, never given an
// invented digest. A nil pinner leaves every release image unpinned.
func build(ctx context.Context, backends []gallery.GalleryBackend, p pinner, workers int) ([]catalog.Release, report, error) {
	rep := report{Entries: len(backends)}
	// A name that appears twice must describe the same backend; the later copy
	// is dropped, as the gallery reader finds only the first by name.
	var unique []gallery.GalleryBackend
	byName := map[string]int{}
	for _, b := range backends {
		if i, dup := byName[b.Name]; dup {
			if !gallery.SameBackend(unique[i], b) {
				return nil, rep, fmt.Errorf("the gallery lists backend %q twice with different content", b.Name)
			}
			rep.Folded++
			continue
		}
		byName[b.Name] = len(unique)
		unique = append(unique, b)
	}
	type result struct {
		pins []gallery.BackendPin
		err  error
	}
	results := make([]result, len(unique))
	jobs := make(chan int)
	var wg sync.WaitGroup
	if workers < 1 {
		workers = 1
	}
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				b := unique[i]
				if b.URI == "" || p == nil {
					results[i] = result{err: errors.New("not read")}
					continue
				}
				c, cancel := context.WithTimeout(ctx, 60*time.Second)
				pins, err := p.Pin(c, b.URI)
				cancel()
				results[i] = result{pins, err}
			}
		}()
	}
	for i := range unique {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	var out []catalog.Release
	for i, b := range unique {
		var pins []gallery.BackendPin
		switch {
		case b.URI == "":
			rep.Meta++
		default:
			rep.Concrete++
			channel := gallery.BackendChannelLatest
			if strings.Contains(b.URI, ":master-") {
				channel = gallery.BackendChannelDevelopment
			}
			got := results[i]
			if got.err != nil || len(got.pins) == 0 {
				os, arch := guessPlatform(b.URI)
				got.pins = []gallery.BackendPin{{OS: os, Arch: arch}}
				if channel == gallery.BackendChannelLatest {
					rep.Unpinned = append(rep.Unpinned, b.Name)
				}
			}
			for _, pin := range got.pins {
				if channel == gallery.BackendChannelDevelopment {
					pin.Digest = ""
				}
				pins = append(pins, pin)
			}
		}
		rows, err := gallery.BackendLibraryEntries(b, pins)
		if err != nil {
			return nil, rep, err
		}
		for _, r := range rows {
			switch {
			case r.Artifact == "":
			case r.Digest != "":
				rep.Pinned++
			case r.Channel == gallery.BackendChannelDevelopment:
				rep.Development++
			}
		}
		out = append(out, rows...)
	}
	rep.Rows = len(out)
	// Rows of one release image that are all mutable have no digest: the count
	// of the unpinned entries is by backend, listed above.
	return out, rep, nil
}

// load reads the library the way a host reads it, and checks every entry the
// way a host does.
func load(src []byte) ([]catalog.Release, error) {
	res, err := catalog.ReadIndex(src, catalog.ReadableKinds...)
	if err != nil {
		return nil, fmt.Errorf("reading the backend library: %w", err)
	}
	var rows []catalog.Release
	for _, r := range res.Releases {
		if r.EntryKind() != catalog.KindBackend {
			continue
		}
		if err := catalog.ValidateEntry(r); err != nil {
			return nil, err
		}
		rows = append(rows, r)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("the backend library has no backend entries (%d entries of another kind skipped); is this the library and not a gallery?", len(res.Skipped))
	}
	return rows, nil
}

// galleryBackends converts the library to gallery backends, in library order,
// after proving that every backend goes through the library and back equal.
func galleryBackends(rows []catalog.Release) ([]gallery.GalleryBackend, int, error) {
	list, err := gallery.BackendsFromLibrary(rows)
	if err != nil {
		return nil, 0, err
	}
	pins := gallery.LibraryPins(rows)
	out := make([]gallery.GalleryBackend, 0, len(list))
	var failures []string
	for _, b := range list {
		if err := gallery.VerifyBackendRoundTrip(*b, pins[b.Name]); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", b.Name, err))
			continue
		}
		out = append(out, *b)
	}
	if len(failures) > 0 {
		return nil, 0, fmt.Errorf("%d of %d backends do not convert to the library and back:\n%s", len(failures), len(list), strings.Join(failures, "\n"))
	}
	return out, len(list), nil
}

// readGallery reads a backend gallery file the way the host does.
func readGallery(src []byte) ([]gallery.GalleryBackend, error) {
	var list []gallery.GalleryBackend
	if err := yaml.Unmarshal(src, &list); err != nil {
		return nil, fmt.Errorf("reading the backend gallery: %w", err)
	}
	return list, nil
}

func encodeLibrary(rows []catalog.Release) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString(libraryHeader)
	if err := catalog.WriteIndex(&buf, rows); err != nil {
		return nil, err
	}
	if _, err := load(buf.Bytes()); err != nil {
		return nil, fmt.Errorf("the rendered library is not valid: %w", err)
	}
	return buf.Bytes(), nil
}

func digestOf(b gallery.GalleryBackend) (string, error) {
	raw, err := gallery.EncodeBackendGalleryIndex([]gallery.GalleryBackend{b})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func writeGolden(dir string, rows []catalog.Release, backends []gallery.GalleryBackend, before []byte) error {
	samples := filepath.Join(dir, "samples")
	if err := os.RemoveAll(samples); err != nil {
		return err
	}
	if err := os.MkdirAll(samples, 0o755); err != nil {
		return err
	}
	if len(before) > 0 {
		if err := os.WriteFile(filepath.Join(dir, "gallery-before.yaml"), before, 0o644); err != nil {
			return err
		}
	}
	var pins []string
	for _, r := range rows {
		if r.Artifact != "" {
			pins = append(pins, strings.Join([]string{r.ID, r.OS, r.Arch, r.Digest}, "\t"))
		}
	}
	sort.Strings(pins)
	if err := os.WriteFile(filepath.Join(dir, "pins.tsv"), []byte("# id, os, arch, digest (empty: not pinned in the library)\n# Written by scripts/backends/cmd/backendcatalog --golden; do not edit by hand.\n"+strings.Join(pins, "\n")+"\n"), 0o644); err != nil {
		return err
	}
	var digests []string
	for _, b := range backends {
		d, err := digestOf(b)
		if err != nil {
			return err
		}
		digests = append(digests, b.Name+"\t"+d)
	}
	sort.Strings(digests)
	if err := os.WriteFile(filepath.Join(dir, "digests.tsv"), []byte("# id, digest of the gallery entry generated from the library entries\n# Written by scripts/backends/cmd/backendcatalog --golden; do not edit by hand.\n"+strings.Join(digests, "\n")+"\n"), 0o644); err != nil {
		return err
	}
	byID := map[string][]catalog.Release{}
	for _, r := range rows {
		byID[r.ID] = append(byID[r.ID], r)
	}
	for _, c := range sampleClasses {
		picked := -1
		for i := range backends {
			if c.has(backends[i], byID[backends[i].Name]) {
				picked = i
				break
			}
		}
		if picked < 0 {
			return fmt.Errorf("no backend for the sample class %q", c.name)
		}
		l, err := catalog.EncodeIndex(byID[backends[picked].Name])
		if err != nil {
			return err
		}
		g, err := gallery.EncodeBackendGalleryIndex([]gallery.GalleryBackend{backends[picked]})
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(samples, c.name+".library.yaml"), l, 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(samples, c.name+".gallery.yaml"), g, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// sampleClasses picks readable golden entries: the first backend that has each
// feature.
var sampleClasses = []struct {
	name string
	has  func(gallery.GalleryBackend, []catalog.Release) bool
}{
	{"meta", func(b gallery.GalleryBackend, _ []catalog.Release) bool {
		return b.URI == "" && !strings.HasSuffix(b.Name, "-development")
	}},
	{"meta-development", func(b gallery.GalleryBackend, _ []catalog.Release) bool {
		return b.URI == "" && strings.HasSuffix(b.Name, "-development")
	}},
	{"release", func(b gallery.GalleryBackend, rows []catalog.Release) bool {
		return b.URI != "" && len(rows) == 1 && rows[0].Digest != "" && len(b.Mirrors) > 0 && b.Icon != ""
	}},
	{"development", func(b gallery.GalleryBackend, rows []catalog.Release) bool {
		return b.URI != "" && len(rows) > 0 && rows[0].Mutable && rows[0].Channel == gallery.BackendChannelDevelopment
	}},
	{"multi-platform", func(b gallery.GalleryBackend, rows []catalog.Release) bool {
		return len(rows) > 1 && rows[0].Digest != ""
	}},
	{"no-icon", func(b gallery.GalleryBackend, rows []catalog.Release) bool { return b.URI != "" && b.Icon == "" }},
	{"no-mirrors", func(b gallery.GalleryBackend, rows []catalog.Release) bool { return b.URI != "" && len(b.Mirrors) == 0 }},
	{"no-description", func(b gallery.GalleryBackend, rows []catalog.Release) bool { return b.Description == "" }},
	{"multiline-description", func(b gallery.GalleryBackend, rows []catalog.Release) bool {
		return strings.Count(strings.TrimRight(b.Description, "\n"), "\n") > 0
	}},
}

func run(args []string) error {
	fs := flag.NewFlagSet("backendcatalog", flag.ContinueOnError)
	libraryPath := fs.String("library", "gallery/backends/index.yaml", "the backend library, the source")
	out := fs.String("out", "", "generated gallery file to write")
	golden := fs.String("golden", "", "directory to write the golden files to")
	importGallery := fs.String("import-gallery", "", "build the library from this backend gallery, reading the registry")
	add := fs.String("add", "", "merge the backends of this gallery-shaped file into the library, pinning only those")
	repin := fs.Bool("repin", false, "pin the release images of the library again, reading the registry")
	noRegistry := fs.Bool("no-registry", false, "with --import-gallery or --repin: do not read the registry; release images stay unpinned")
	workers := fs.Int("workers", 16, "registry reads in parallel")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx := context.Background()
	var p pinner = registryPinner{}
	if *noRegistry {
		p = nil
	}

	var before []byte
	switch {
	case (*importGallery != "" && *repin) || (*add != "" && (*repin || *importGallery != "")):
		return errors.New("--import-gallery, --add and --repin are exclusive")
	case *add != "":
		src, err := os.ReadFile(*add)
		if err != nil {
			return err
		}
		list, err := readGallery(src)
		if err != nil {
			return err
		}
		if err := mergeLibrary(ctx, *libraryPath, list, p, *workers); err != nil {
			return err
		}
	case *importGallery != "":
		src, err := os.ReadFile(*importGallery)
		if err != nil {
			return err
		}
		before = src
		list, err := readGallery(src)
		if err != nil {
			return err
		}
		if err := writeLibrary(ctx, list, p, *workers, *libraryPath); err != nil {
			return err
		}
	case *repin:
		src, err := os.ReadFile(*libraryPath)
		if err != nil {
			return err
		}
		rows, err := load(src)
		if err != nil {
			return err
		}
		list, err := gallery.BackendsFromLibrary(rows)
		if err != nil {
			return err
		}
		flat := make([]gallery.GalleryBackend, len(list))
		for i, b := range list {
			flat[i] = *b
		}
		if err := writeLibrary(ctx, flat, p, *workers, *libraryPath); err != nil {
			return err
		}
	case *out == "" && *golden == "":
		return errors.New("usage: --out file and/or --golden dir [--library file], or --add file, or --import-gallery file, or --repin")
	}

	if *out == "" && *golden == "" {
		return nil
	}
	src, err := os.ReadFile(*libraryPath)
	if err != nil {
		return err
	}
	rows, err := load(src)
	if err != nil {
		return err
	}
	backends, n, err := galleryBackends(rows)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "backendcatalog: %d of %d library backends convert to the gallery and back (%d library entries)\n", len(backends), n, len(rows))
	if *out != "" {
		body, err := gallery.EncodeBackendGalleryIndex(backends)
		if err != nil {
			return err
		}
		// The result must load the way the host loads a gallery, and give the
		// same entries.
		loaded, err := readGallery(body)
		if err != nil {
			return fmt.Errorf("the generated gallery does not load: %w", err)
		}
		if len(loaded) != len(backends) {
			return fmt.Errorf("the generated gallery loads %d entries, the library has %d", len(loaded), len(backends))
		}
		for i := range loaded {
			if !gallery.SameBackend(loaded[i], backends[i]) {
				return fmt.Errorf("the generated gallery entry %q loads differently from the library entry", backends[i].Name)
			}
		}
		if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(*out, append([]byte(galleryHeader), body...), 0o644); err != nil {
			return err
		}
	}
	if *golden != "" {
		if err := writeGolden(*golden, rows, backends, before); err != nil {
			return err
		}
	}
	return nil
}

// mergeLibrary adds backends to the library file. The rows of a backend that
// is already in the library are replaced where they stand; the rows of a new
// backend are appended. Every other row is kept as it is, with its digest.
func mergeLibrary(ctx context.Context, path string, add []gallery.GalleryBackend, p pinner, workers int) error {
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	existing, err := load(src)
	if err != nil {
		return err
	}
	fresh, rep, err := build(ctx, add, p, workers)
	if err != nil {
		return err
	}
	rows := mergeRows(existing, fresh)
	body, err := encodeLibrary(rows)
	if err != nil {
		return err
	}
	// The merged library must still convert to the gallery and back.
	if _, _, err := galleryBackends(rows); err != nil {
		return err
	}
	rep.print(os.Stderr)
	return os.WriteFile(path, body, 0o644)
}

// mergeRows puts the fresh rows in place of the rows of the same ids and
// appends the rows of the ids that are new.
func mergeRows(existing, fresh []catalog.Release) []catalog.Release {
	byID := map[string][]catalog.Release{}
	var order []string
	for _, r := range fresh {
		if _, ok := byID[r.ID]; !ok {
			order = append(order, r.ID)
		}
		byID[r.ID] = append(byID[r.ID], r)
	}
	var out []catalog.Release
	placed := map[string]bool{}
	for _, r := range existing {
		rows, replaced := byID[r.ID]
		switch {
		case !replaced:
			out = append(out, r)
		case !placed[r.ID]:
			out = append(out, rows...)
			placed[r.ID] = true
		}
	}
	for _, id := range order {
		if !placed[id] {
			out = append(out, byID[id]...)
		}
	}
	return out
}

func writeLibrary(ctx context.Context, list []gallery.GalleryBackend, p pinner, workers int, path string) error {
	rows, rep, err := build(ctx, list, p, workers)
	if err != nil {
		return err
	}
	body, err := encodeLibrary(rows)
	if err != nil {
		return err
	}
	rep.print(os.Stderr)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, body, 0o644)
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "backendcatalog:", err)
		os.Exit(1)
	}
}
