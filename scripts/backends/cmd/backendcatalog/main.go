// SPDX-License-Identifier: MIT

// Command backendcatalog renders library entries of kind backend from the
// backend gallery, backend/index.yaml. The gallery stays the source of truth:
// this command reads it and pins what it names, it never edits it.
//
// For each backend named on the command line it finds the concrete gallery
// entry, resolves the image the entry names to the digest of the image for the
// platform, and writes one library entry. The registry is read at render time,
// so the digests are never edited by hand: render the file again after the
// backend images are rebuilt.
//
//	go run ./scripts/backends/cmd/backendcatalog \
//	  --index backend/index.yaml --backends cpu-local-store \
//	  --out gallery/backends/index.yaml
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/pkg/oci"
	"gopkg.in/yaml.v3"
)

const header = `# Pilot backend library: backends from the backend gallery, one entry for each
# image. The gallery, backend/index.yaml, stays the source of truth. The entries
# are rendered with scripts/backends/cmd/backendcatalog, so the digests pin the
# images the gallery names today: render the file again after the backend images
# are rebuilt, never edit a digest by hand.
`

// resolver returns the manifest digest of the image for a platform.
type resolver func(image, platform string) (string, error)

func registryResolver(image, platform string) (string, error) {
	img, err := oci.GetImage(image, platform, nil, nil)
	if err != nil {
		return "", err
	}
	d, err := img.Digest()
	if err != nil {
		return "", err
	}
	return d.String(), nil
}

var tagPattern = regexp.MustCompile(`:([a-z][a-z0-9]*)-[A-Za-z0-9._-]+$`)

// render builds the library entries of the named backends for one platform.
func render(gallerySource []byte, names []string, osName, arch string, resolve resolver) ([]catalog.Release, error) {
	var entries []gallery.GalleryBackend
	if err := yaml.Unmarshal(gallerySource, &entries); err != nil {
		return nil, fmt.Errorf("reading the backend gallery: %w", err)
	}
	byName := map[string]gallery.GalleryBackend{}
	for _, e := range entries {
		byName[e.Name] = e
	}
	var out []catalog.Release
	for _, name := range names {
		e, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("backend %q is not in the gallery", name)
		}
		if e.IsMeta() || e.URI == "" {
			return nil, fmt.Errorf("backend %q is a meta backend; name a concrete one", name)
		}
		image := strings.TrimPrefix(e.URI, "oci://")
		m := tagPattern.FindStringSubmatch(image)
		if m == nil || m[1] != "latest" {
			return nil, fmt.Errorf("backend %q: the image %q is not a released latest-* tag", name, image)
		}
		digest, err := resolve(image, osName+"/"+arch)
		if err != nil {
			return nil, fmt.Errorf("backend %q: resolving %s: %w", name, image, err)
		}
		hex := strings.TrimPrefix(digest, "sha256:")
		if len(hex) != 64 {
			return nil, fmt.Errorf("backend %q: unexpected digest %q", name, digest)
		}
		out = append(out, catalog.Release{
			Kind:        catalog.KindBackend,
			ID:     e.Name,
			Alias:       e.Alias,
			Name:        e.Name,
			Description: strings.Join(strings.Fields(e.Description), " "),
			Version:     "0.0.0-latest." + hex[:12],
			Channel:     "latest",
			Artifact:    "oci://" + image,
			Digest:      digest,
			OS:          osName,
			Arch:        arch,
			// The host replaces this with the policy of the library; it is
			// written so the file says what the entries are checked against.
			Policy: catalog.OfficialBackendLibrary().Policy,
		})
	}
	return out, nil
}

func run(args []string) error {
	fs := flag.NewFlagSet("backendcatalog", flag.ContinueOnError)
	index := fs.String("index", "backend/index.yaml", "the backend gallery")
	backends := fs.String("backends", "", "comma list of concrete backend names")
	platform := fs.String("platform", "linux/amd64", "os/arch")
	out := fs.String("out", "", "file to write (default: standard output)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	osName, arch, ok := strings.Cut(*platform, "/")
	if !ok || *backends == "" {
		return errors.New("usage: --backends a,b [--platform os/arch] [--index file] [--out file]")
	}
	src, err := os.ReadFile(*index)
	if err != nil {
		return err
	}
	releases, err := render(src, strings.Split(*backends, ","), osName, arch, registryResolver)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	buf.WriteString(header)
	if err := catalog.WriteIndex(&buf, releases); err != nil {
		return err
	}
	// The result must be a valid library index for this host.
	if _, err := catalog.DecodeIndex(buf.Bytes()); err != nil {
		return fmt.Errorf("the rendered index is not valid: %w", err)
	}
	if *out == "" {
		_, err = os.Stdout.Write(buf.Bytes())
		return err
	}
	return os.WriteFile(*out, buf.Bytes(), 0o644)
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "backendcatalog:", err)
		os.Exit(1)
	}
}
