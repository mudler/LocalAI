// SPDX-License-Identifier: MIT
package gallery

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"gopkg.in/yaml.v3"
)

// The backend library, gallery/backends/index.yaml, is the source of truth for
// the backends, the way the model library is for the models. The backend
// gallery, backend/index.yaml, is a generated copy that stays only so releases
// before the cutover keep working; scripts/backends/cmd/backendcatalog writes
// it from the library and it is never edited by hand.
//
// A library entry is a catalog.Release of kind backend. One gallery backend
// becomes one library entry per platform its image is built for, because the
// library pins the digest of the image of each platform. A meta backend
// becomes one entry with capabilities and no image. Converting library entries
// back to a gallery entry gives the entry the gallery had, field for field,
// for every field the gallery reader uses. The tests check that on every entry
// of the real library.

const (
	// BackendChannelLatest and BackendChannelDevelopment are the channels of a
	// backend entry. The tag of the image says which: latest-* is a release,
	// master-* is the development build of the branch.
	BackendChannelLatest      = "latest"
	BackendChannelDevelopment = "development"

	backendOCIPrefix = "oci://"
)

var backendTagPattern = regexp.MustCompile(`:(latest|master)-[A-Za-z0-9._-]+$`)

// BackendPin is the digest of the image of one platform. An empty digest means
// the image is not pinned in the library: a development image, which is pinned
// at install, or a release image the registry could not be read for.
type BackendPin struct {
	OS     string
	Arch   string
	Digest string
}

// backendChannel is the channel of a gallery backend. A concrete backend takes
// it from the tag of its image; a meta backend from its name, as the gallery
// names its development variants.
func backendChannel(b GalleryBackend) (string, error) {
	if b.URI == "" {
		if strings.HasSuffix(b.Name, "-"+defaultDevSuffix) {
			return BackendChannelDevelopment, nil
		}
		return BackendChannelLatest, nil
	}
	m := backendTagPattern.FindStringSubmatch(b.URI)
	switch {
	case m == nil:
		return "", fmt.Errorf("the image %q has no latest-* or master-* tag", b.URI)
	case m[1] == "master":
		return BackendChannelDevelopment, nil
	}
	return BackendChannelLatest, nil
}

// checkExpressible refuses a gallery backend that sets a field the library
// body cannot carry, so nothing is lost without notice. The real gallery sets
// none of them.
func checkExpressible(b GalleryBackend) error {
	switch {
	case b.Name == "":
		return errors.New("gallery entry has no name")
	case b.Version != "":
		return fmt.Errorf("backend %q sets version, which the library does not carry", b.Name)
	case b.SHA256 != "":
		return fmt.Errorf("backend %q sets sha256, which the library does not carry", b.Name)
	case b.Metadata.URL != "", len(b.AdditionalFiles) > 0, b.Size != "", b.Metadata.Backend != "":
		return fmt.Errorf("backend %q sets a model field (url, files, size or backend)", b.Name)
	}
	return nil
}

func ociRef(uri string) (string, error) {
	if uri == "" || strings.Contains(uri, "://") {
		return "", fmt.Errorf("the image %q must be a plain image reference: the library adds the oci:// scheme", uri)
	}
	return backendOCIPrefix + uri, nil
}

// BackendLibraryEntries converts a gallery backend to its library entries. A
// concrete backend gets one entry for each pin; a meta backend takes no pins.
// A development image is never pinned in the library, and neither is a pin
// without a digest: such an entry is mutable and the install pins it.
func BackendLibraryEntries(b GalleryBackend, pins []BackendPin) ([]catalog.Release, error) {
	b = stripBackendRuntime(b)
	if err := checkExpressible(b); err != nil {
		return nil, err
	}
	channel, err := backendChannel(b)
	if err != nil {
		return nil, fmt.Errorf("backend %q: %w", b.Name, err)
	}
	base := catalog.Release{
		Kind:          catalog.KindBackend,
		ID:            b.Name,
		Name:          b.Name,
		Alias:         b.Alias,
		Description:   b.Description,
		Channel:       channel,
		SchemaVersion: catalog.SchemaVersion,
		License:       b.License,
		URLs:          cloneList(b.URLs),
		Tags:          cloneList(b.Tags),
		Icon:          b.Icon,
		Capabilities:  cloneMap(b.CapabilitiesMap),
	}
	if b.URI == "" {
		if len(b.CapabilitiesMap) == 0 {
			return nil, fmt.Errorf("backend %q has no image and no capabilities", b.Name)
		}
		if len(pins) > 0 || len(b.Mirrors) > 0 {
			return nil, fmt.Errorf("meta backend %q has no image to pin or mirror", b.Name)
		}
		base.Version = "0.0.0-" + channel
		return []catalog.Release{base}, nil
	}
	artifact, err := ociRef(b.URI)
	if err != nil {
		return nil, fmt.Errorf("backend %q: %w", b.Name, err)
	}
	base.Artifact = artifact
	for _, m := range b.Mirrors {
		ref, err := ociRef(m)
		if err != nil {
			return nil, fmt.Errorf("backend %q mirror: %w", b.Name, err)
		}
		base.Mirrors = append(base.Mirrors, ref)
	}
	if len(pins) == 0 {
		return nil, fmt.Errorf("backend %q has no platform", b.Name)
	}
	sorted := append([]BackendPin(nil), pins...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].OS+"/"+sorted[i].Arch < sorted[j].OS+"/"+sorted[j].Arch
	})
	var out []catalog.Release
	for i, p := range sorted {
		if p.OS == "" || p.Arch == "" {
			return nil, fmt.Errorf("backend %q: a pin has no platform", b.Name)
		}
		if i > 0 && p.OS == sorted[i-1].OS && p.Arch == sorted[i-1].Arch {
			return nil, fmt.Errorf("backend %q: two pins for %s/%s", b.Name, p.OS, p.Arch)
		}
		r := base
		r.OS, r.Arch = p.OS, p.Arch
		switch {
		case p.Digest == "":
			r.Mutable = true
			r.Version = "0.0.0-" + channel
		case channel == BackendChannelDevelopment:
			return nil, fmt.Errorf("backend %q is a development image: it is pinned at install, not in the library", b.Name)
		default:
			hex := strings.TrimPrefix(p.Digest, "sha256:")
			if len(hex) != 64 || hex == p.Digest {
				return nil, fmt.Errorf("backend %q: %q is not a sha256 digest", b.Name, p.Digest)
			}
			r.Digest = p.Digest
			r.Version = "0.0.0-" + channel + "." + hex[:12]
		}
		out = append(out, r)
	}
	return out, nil
}

// ToGalleryBackend converts one library entry of kind backend to the gallery
// entry the installer and the listing read. The id is the name the gallery
// knows the backend by.
func ToGalleryBackend(r catalog.Release) (GalleryBackend, error) {
	if r.EntryKind() != catalog.KindBackend {
		return GalleryBackend{}, fmt.Errorf("entry %q is of kind %q, not backend", r.ID, r.EntryKind())
	}
	if err := catalog.ValidateEntry(r); err != nil {
		return GalleryBackend{}, err
	}
	b := GalleryBackend{
		Metadata: Metadata{
			Name:        r.ID,
			Description: r.Description,
			License:     r.License,
			URLs:        cloneList(r.URLs),
			Icon:        r.Icon,
			Tags:        cloneList(r.Tags),
		},
		Alias:           r.Alias,
		CapabilitiesMap: cloneMap(r.Capabilities),
	}
	if r.Artifact != "" {
		b.URI = strings.TrimPrefix(r.Artifact, backendOCIPrefix)
	}
	for _, m := range r.Mirrors {
		b.Mirrors = append(b.Mirrors, strings.TrimPrefix(m, backendOCIPrefix))
	}
	return b, nil
}

// BackendsFromLibrary converts library entries of kind backend to gallery
// backends, in the order the ids first appear. The entries of one id, one for
// each platform, must describe the same gallery backend; if they differ the
// library is refused, because the gallery cannot hold both. Entries of another
// kind are skipped.
func BackendsFromLibrary(releases []catalog.Release) ([]*GalleryBackend, error) {
	var out []*GalleryBackend
	index := map[string]int{}
	for _, r := range releases {
		if r.EntryKind() != catalog.KindBackend {
			continue
		}
		b, err := ToGalleryBackend(r)
		if err != nil {
			return nil, err
		}
		i, seen := index[b.Name]
		if !seen {
			index[b.Name] = len(out)
			out = append(out, &b)
			continue
		}
		if d := firstBackendDifference(*out[i], b); d != "" {
			return nil, fmt.Errorf("the library entries of backend %q disagree: %s", b.Name, d)
		}
	}
	return out, nil
}

// LibraryPins collects the pins of the backend entries of a library by id.
func LibraryPins(releases []catalog.Release) map[string][]BackendPin {
	out := map[string][]BackendPin{}
	for _, r := range releases {
		if r.EntryKind() == catalog.KindBackend && r.Artifact != "" {
			out[r.ID] = append(out[r.ID], BackendPin{OS: r.OS, Arch: r.Arch, Digest: r.Digest})
		}
	}
	return out
}

func stripBackendRuntime(b GalleryBackend) GalleryBackend {
	b.Gallery = config.Gallery{}
	b.Installed = false
	b.Library, b.MaxBytes, b.Stage = "", 0, nil
	return b
}

// normalizeBackend makes an empty list or map the same as an absent one,
// which is how the gallery reader sees them, and drops the runtime fields.
func normalizeBackend(b GalleryBackend) GalleryBackend {
	b = stripBackendRuntime(b)
	if len(b.URLs) == 0 {
		b.URLs = nil
	}
	if len(b.Tags) == 0 {
		b.Tags = nil
	}
	if len(b.Mirrors) == 0 {
		b.Mirrors = nil
	}
	if len(b.CapabilitiesMap) == 0 {
		b.CapabilitiesMap = nil
	}
	return b
}

// SameBackend reports whether two gallery backends are equal on every field
// the gallery reader uses.
func SameBackend(a, b GalleryBackend) bool {
	return reflect.DeepEqual(normalizeBackend(a), normalizeBackend(b))
}

// firstBackendDifference names the first field of two gallery backends that
// differ, or "" when they are the same.
func firstBackendDifference(a, b GalleryBackend) string {
	a, b = normalizeBackend(a), normalizeBackend(b)
	av, bv := reflect.ValueOf(a), reflect.ValueOf(b)
	for i := 0; i < av.NumField(); i++ {
		f := av.Type().Field(i)
		if f.Name == "Metadata" {
			mv, nv := reflect.ValueOf(a.Metadata), reflect.ValueOf(b.Metadata)
			for j := 0; j < mv.NumField(); j++ {
				if !reflect.DeepEqual(mv.Field(j).Interface(), nv.Field(j).Interface()) {
					return fmt.Sprintf("%s: %#v became %#v", mv.Type().Field(j).Name, mv.Field(j).Interface(), nv.Field(j).Interface())
				}
			}
			continue
		}
		if f.Type.Kind() == reflect.Func {
			continue
		}
		if !reflect.DeepEqual(av.Field(i).Interface(), bv.Field(i).Interface()) {
			return fmt.Sprintf("%s: %#v became %#v", f.Name, av.Field(i).Interface(), bv.Field(i).Interface())
		}
	}
	return ""
}

// VerifyBackendRoundTrip checks that a gallery backend survives the library:
// it is converted to library entries, written as an index, read the way a host
// reads a library, validated as a host validates it, and converted back. The
// result must equal the original on every field the gallery reader uses. It
// returns a description of the first difference.
func VerifyBackendRoundTrip(b GalleryBackend, pins []BackendPin) error {
	entries, err := BackendLibraryEntries(b, pins)
	if err != nil {
		return err
	}
	raw, err := catalog.EncodeIndex(entries)
	if err != nil {
		return fmt.Errorf("writing the library entries: %w", err)
	}
	res, err := catalog.ReadIndex(raw, catalog.ReadableKinds...)
	if err != nil {
		return fmt.Errorf("reading the library entries: %w", err)
	}
	if len(res.Releases) != len(entries) {
		return fmt.Errorf("%d library entries came back as %d", len(entries), len(res.Releases))
	}
	got, err := BackendsFromLibrary(res.Releases)
	if err != nil {
		return err
	}
	if len(got) != 1 {
		return fmt.Errorf("the library entries came back as %d gallery backends", len(got))
	}
	if d := firstBackendDifference(b, *got[0]); d != "" {
		return fmt.Errorf("gallery entry changed on the way through the library: %s", d)
	}
	return nil
}

// backendFileEntry is a gallery backend as it is written to the gallery file:
// the fields of GalleryBackend in the order the file has always used.
type backendFileEntry struct {
	Name         string            `yaml:"name"`
	Alias        string            `yaml:"alias,omitempty"`
	License      string            `yaml:"license,omitempty"`
	Icon         string            `yaml:"icon,omitempty"`
	Description  string            `yaml:"description,omitempty"`
	URLs         []string          `yaml:"urls,omitempty"`
	Tags         []string          `yaml:"tags,omitempty"`
	Capabilities map[string]string `yaml:"capabilities,omitempty"`
	URI          string            `yaml:"uri,omitempty"`
	Mirrors      []string          `yaml:"mirrors,omitempty"`
}

// EncodeBackendGalleryIndex writes gallery backends as the gallery file the
// host reads (backend/index.yaml). The runtime fields are not written. It
// refuses a backend that sets a field the file does not write, so the file
// reads back as the entries it was written from.
func EncodeBackendGalleryIndex(backends []GalleryBackend) ([]byte, error) {
	out := make([]backendFileEntry, len(backends))
	for i, b := range backends {
		b = normalizeBackend(b)
		if err := checkExpressible(b); err != nil {
			return nil, err
		}
		out[i] = backendFileEntry{
			Name: b.Name, Alias: b.Alias, License: b.License, Icon: b.Icon, Description: b.Description,
			URLs: b.URLs, Tags: b.Tags, Capabilities: b.CapabilitiesMap, URI: b.URI, Mirrors: b.Mirrors,
		}
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(out); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func cloneList(s []string) []string {
	if s == nil {
		return nil
	}
	return append([]string{}, s...)
}

func cloneMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
