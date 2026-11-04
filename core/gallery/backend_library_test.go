package gallery

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"gopkg.in/yaml.v3"
)

const (
	realBackendLibrary  = "../../gallery/backends/index.yaml"
	backendLibraryGolds = "testdata/backend_library"
	// The gallery before the library became the source, kept as the golden
	// input: 1077 entries, five of them twins of another entry's name.
	goldenGalleryEntries = 1077
	goldenGalleryNames   = 1072
)

func loadRealBackendLibrary(t *testing.T) []catalog.Release {
	t.Helper()
	src, err := os.ReadFile(realBackendLibrary)
	if err != nil {
		t.Fatal(err)
	}
	res, err := catalog.ReadIndex(src, catalog.ReadableKinds...)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Releases) == 0 {
		t.Fatal("the real backend library has no entries")
	}
	return res.Releases
}

func loadGoldenGallery(t *testing.T) []GalleryBackend {
	t.Helper()
	src, err := os.ReadFile(filepath.Join(backendLibraryGolds, "gallery-before.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var list []GalleryBackend
	if err := yaml.Unmarshal(src, &list); err != nil {
		t.Fatal(err)
	}
	return list
}

func loadGoldenPins(t *testing.T) map[string][]BackendPin {
	t.Helper()
	f, err := os.Open(filepath.Join(backendLibraryGolds, "pins.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	pins := map[string][]BackendPin{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		p := strings.Split(line, "\t")
		if len(p) != 4 {
			t.Fatalf("malformed pin line %q", line)
		}
		pins[p[0]] = append(pins[p[0]], BackendPin{OS: p[1], Arch: p[2], Digest: p[3]})
	}
	return pins
}

func loadGoldenDigests(t *testing.T) map[string]string {
	t.Helper()
	f, err := os.Open(filepath.Join(backendLibraryGolds, "digests.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		p := strings.Split(line, "\t")
		if len(p) != 2 {
			t.Fatalf("malformed golden line %q", line)
		}
		out[p[0]] = p[1]
	}
	return out
}

func backendDigest(t *testing.T, b GalleryBackend) string {
	t.Helper()
	raw, err := EncodeBackendGalleryIndex([]GalleryBackend{b})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// Every entry of the real backend gallery, as it was before the library became
// the source, must survive the library: gallery -> library -> gallery gives the
// same entry on every field the gallery reader uses. The count is asserted and
// logged so a run shows how many real entries it covered.
func TestBackendLibraryRoundTripsEveryRealGalleryEntry(t *testing.T) {
	entries := loadGoldenGallery(t)
	if len(entries) != goldenGalleryEntries {
		t.Fatalf("the golden gallery has %d entries, want %d", len(entries), goldenGalleryEntries)
	}
	pins := loadGoldenPins(t)
	golden := loadGoldenDigests(t)
	covered, meta, concrete, dev, platformsRows := 0, 0, 0, 0, 0
	first := map[string]GalleryBackend{}
	var failures []string
	for _, b := range entries {
		if twin, ok := first[b.Name]; ok {
			// A name that appears twice must be the same entry, so folding it
			// loses nothing.
			if !SameBackend(twin, b) {
				failures = append(failures, b.Name+": the twin entry differs from the first one")
				continue
			}
		} else {
			first[b.Name] = b
		}
		if err := VerifyBackendRoundTrip(b, pins[b.Name]); err != nil {
			failures = append(failures, b.Name+": "+err.Error())
			continue
		}
		// The generated gallery entry is the one the golden file recorded.
		if want, ok := golden[b.Name]; !ok || backendDigest(t, b) != want {
			failures = append(failures, b.Name+": the golden digest does not match the gallery entry")
			continue
		}
		covered++
		switch {
		case b.URI == "":
			meta++
		case strings.Contains(b.URI, ":master-"):
			concrete++
			dev++
		default:
			concrete++
		}
		platformsRows += len(pins[b.Name])
	}
	for _, f := range failures {
		t.Error(f)
	}
	t.Logf("backend library round trip: %d of %d gallery entries covered (%d names; %d concrete of which %d development, %d meta); the golden pins hold %d platform entries",
		covered, len(entries), len(first), concrete, dev, meta, platformsRows)
	if covered != goldenGalleryEntries {
		t.Errorf("covered %d gallery entries, want %d", covered, goldenGalleryEntries)
	}
	if len(first) != goldenGalleryNames || len(golden) != goldenGalleryNames {
		t.Errorf("%d names and %d golden digests, want %d", len(first), len(golden), goldenGalleryNames)
	}
}

// The library file in the repository is read by the host, so it must decode and
// every entry must pass the checks a host applies. Every backend must convert
// to a gallery backend and come back as itself, and a meta backend must name
// backends the library has.
func TestRealBackendLibraryHasTheLibraryShape(t *testing.T) {
	rows := loadRealBackendLibrary(t)
	ids := map[string]bool{}
	concrete := map[string]bool{}
	keys := map[string]bool{}
	pinned, development, unpinned, meta := 0, 0, 0, 0
	for _, r := range rows {
		if r.EntryKind() != catalog.KindBackend {
			t.Errorf("%s is of kind %q", r.ID, r.EntryKind())
			continue
		}
		if err := catalog.ValidateEntry(r); err != nil {
			t.Error(err)
		}
		key := r.ID + "/" + r.OS + "/" + r.Arch
		if keys[key] {
			t.Errorf("%s is listed twice", key)
		}
		keys[key] = true
		ids[r.ID] = true
		if r.SchemaVersion != catalog.SchemaVersion {
			t.Errorf("%s has schemaVersion %d", r.ID, r.SchemaVersion)
		}
		switch {
		case r.IsMeta():
			meta++
		case r.Digest != "":
			pinned++
			concrete[r.ID] = true
			if r.Channel != BackendChannelLatest || !strings.Contains(r.Artifact, ":latest-") {
				t.Errorf("%s: a pinned entry is a release image: %s", r.ID, r.Artifact)
			}
		case r.Channel == BackendChannelDevelopment:
			development++
			concrete[r.ID] = true
			if !r.Mutable || !strings.Contains(r.Artifact, ":master-") {
				t.Errorf("%s: a development entry keeps its master-* tag and is mutable: %s", r.ID, r.Artifact)
			}
		default:
			unpinned++
			concrete[r.ID] = true
			t.Logf("not pinned: %s (%s/%s)", r.ID, r.OS, r.Arch)
		}
	}
	for _, r := range rows {
		if !r.IsMeta() {
			continue
		}
		for capability, target := range r.Capabilities {
			if !concrete[target] {
				t.Errorf("meta backend %s: capability %s names %s, which the library does not have as a concrete backend", r.ID, capability, target)
			}
		}
	}
	list, err := BackendsFromLibrary(rows)
	if err != nil {
		t.Fatal(err)
	}
	pins := LibraryPins(rows)
	for _, b := range list {
		if err := VerifyBackendRoundTrip(*b, pins[b.Name]); err != nil {
			t.Errorf("%s: %v", b.Name, err)
		}
	}
	t.Logf("backend library: %d entries for %d backends (%d meta, %d pinned by digest, %d development, %d release entries not pinned)", len(rows), len(list), meta, pinned, development, unpinned)
}

// An entry whose gallery digest is in the golden file must still produce the
// recorded gallery entry. An entry changed or added since the golden file was
// rendered has no matching digest and is covered by the round trip only.
func TestBackendLibraryMatchesGoldenDigests(t *testing.T) {
	golden := loadGoldenDigests(t)
	list, err := BackendsFromLibrary(loadRealBackendLibrary(t))
	if err != nil {
		t.Fatal(err)
	}
	checked, unchanged := 0, 0
	for _, b := range list {
		want, ok := golden[b.Name]
		if !ok {
			continue
		}
		checked++
		if backendDigest(t, *b) == want {
			unchanged++
		}
	}
	t.Logf("golden digests: %d of %d golden backends are in the library, %d unchanged", checked, len(golden), unchanged)
	if checked < len(golden)*9/10 {
		t.Errorf("only %d of %d golden backends are still in the library; render the golden files again", checked, len(golden))
	}
	if unchanged < checked*9/10 {
		t.Errorf("only %d of %d golden backends kept their gallery entry; render the golden files again if the change is intended", unchanged, checked)
	}
}

// The golden samples show what a library file holds and what the gallery copy
// holds for the same backend.
func TestBackendLibraryMatchesGoldenSamples(t *testing.T) {
	libraries, err := filepath.Glob(filepath.Join(backendLibraryGolds, "samples", "*.library.yaml"))
	if err != nil || len(libraries) == 0 {
		t.Fatalf("no golden samples: %v", err)
	}
	for _, lpath := range libraries {
		name := strings.TrimSuffix(filepath.Base(lpath), ".library.yaml")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(lpath)
			if err != nil {
				t.Fatal(err)
			}
			galWant, err := os.ReadFile(strings.TrimSuffix(lpath, ".library.yaml") + ".gallery.yaml")
			if err != nil {
				t.Fatal(err)
			}
			res, err := catalog.ReadIndex(src, catalog.ReadableKinds...)
			if err != nil || len(res.Releases) == 0 {
				t.Fatalf("reading the sample: %v", err)
			}
			again, err := catalog.EncodeIndex(res.Releases)
			if err != nil || string(again) != string(src) {
				t.Errorf("the library sample does not encode back to itself: %v", err)
			}
			list, err := BackendsFromLibrary(res.Releases)
			if err != nil || len(list) != 1 {
				t.Fatalf("converting the sample: %v (%d backends)", err, len(list))
			}
			got, err := EncodeBackendGalleryIndex([]GalleryBackend{*list[0]})
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(galWant) {
				t.Errorf("the gallery entry differs from the golden file:\n%s", got)
			}
			var loaded []GalleryBackend
			if err := yaml.Unmarshal(galWant, &loaded); err != nil || len(loaded) != 1 {
				t.Fatalf("the gallery reader on the golden entry: %v", err)
			}
			if !SameBackend(loaded[0], *list[0]) {
				t.Errorf("the golden gallery entry does not load as the library entry")
			}
			if err := VerifyBackendRoundTrip(loaded[0], LibraryPins(res.Releases)[loaded[0].Name]); err != nil {
				t.Error(err)
			}
		})
	}
}

func demoBackend() GalleryBackend {
	return GalleryBackend{
		Metadata: Metadata{
			Name: "cpu-demo", Description: "A demo.\n", License: "mit", Icon: "https://example.com/i.png",
			URLs: []string{"https://example.com/demo"}, Tags: []string{"CPU", "LLM"},
		},
		Alias:           "demo",
		URI:             "quay.io/acme/backends:latest-cpu-demo",
		Mirrors:         []string{"docker.io/acme/backends:latest-cpu-demo", "ghcr.io/acme/backends:latest-cpu-demo"},
		CapabilitiesMap: map[string]string{"default": "cpu-demo", "nvidia": "cuda12-demo"},
	}
}

const (
	pinAMD = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	pinARM = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func TestBackendLibraryEntriesExpressEveryField(t *testing.T) {
	rows, err := BackendLibraryEntries(demoBackend(), []BackendPin{{"linux", "arm64", pinARM}, {"linux", "amd64", pinAMD}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Arch != "amd64" || rows[1].Arch != "arm64" {
		t.Fatalf("one entry for each platform, in a fixed order: %+v", rows)
	}
	r := rows[0]
	if r.Kind != catalog.KindBackend || r.ID != "cpu-demo" || r.Alias != "demo" || r.License != "mit" || r.Icon != "https://example.com/i.png" ||
		r.Description != "A demo.\n" || r.Artifact != "oci://quay.io/acme/backends:latest-cpu-demo" || r.Digest != pinAMD || r.Mutable ||
		r.Channel != "latest" || r.SchemaVersion != 1 || r.Version != "0.0.0-latest.aaaaaaaaaaaa" || r.OS != "linux" {
		t.Errorf("unexpected entry: %+v", r)
	}
	if strings.Join(r.Mirrors, " ") != "oci://docker.io/acme/backends:latest-cpu-demo oci://ghcr.io/acme/backends:latest-cpu-demo" {
		t.Errorf("mirrors keep their order and get the oci scheme: %v", r.Mirrors)
	}
	if r.Capabilities["nvidia"] != "cuda12-demo" || len(r.URLs) != 1 || len(r.Tags) != 2 {
		t.Errorf("capabilities, urls and tags: %+v", r)
	}
	// The entry holds copies, not the gallery's own list and map.
	r.Capabilities["nvidia"] = "changed"
	r.Tags[0] = "changed"
	if b := demoBackend(); b.CapabilitiesMap["nvidia"] != "cuda12-demo" || b.Tags[0] != "CPU" {
		t.Error("the library entry shares memory with the gallery entry")
	}
	for _, p := range []BackendPin{{"linux", "amd64", pinAMD}, {"linux", "arm64", pinARM}} {
		if err := VerifyBackendRoundTrip(demoBackend(), []BackendPin{p}); err != nil {
			t.Error(err)
		}
	}
}

func TestBackendLibraryKeepsDevelopmentTagsMutable(t *testing.T) {
	b := demoBackend()
	b.Name, b.URI = "cpu-demo-development", "quay.io/acme/backends:master-cpu-demo"
	b.Mirrors = []string{"docker.io/acme/backends:master-cpu-demo"}
	rows, err := BackendLibraryEntries(b, []BackendPin{{OS: "linux", Arch: "amd64"}})
	if err != nil {
		t.Fatal(err)
	}
	r := rows[0]
	if !r.Mutable || r.Digest != "" || r.Channel != "development" || r.Version != "0.0.0-development" || r.Artifact != "oci://quay.io/acme/backends:master-cpu-demo" {
		t.Errorf("a development entry keeps its tag and has no digest: %+v", r)
	}
	// A development image is pinned at install: a digest in the library is refused.
	if _, err := BackendLibraryEntries(b, []BackendPin{{"linux", "amd64", pinAMD}}); err == nil {
		t.Error("a development entry must not be pinned at render")
	}
	if err := VerifyBackendRoundTrip(b, []BackendPin{{OS: "linux", Arch: "amd64"}}); err != nil {
		t.Error(err)
	}
}

func TestBackendLibraryMetaBackend(t *testing.T) {
	meta := GalleryBackend{
		Metadata:        Metadata{Name: "demo", Description: "Meta", Tags: []string{"LLM"}},
		Alias:           "demo",
		CapabilitiesMap: map[string]string{"default": "cpu-demo", "nvidia": "cuda12-demo"},
	}
	rows, err := BackendLibraryEntries(meta, nil)
	if err != nil || len(rows) != 1 {
		t.Fatalf("%v %d", err, len(rows))
	}
	r := rows[0]
	if !r.IsMeta() || r.Artifact != "" || r.Digest != "" || r.OS != "" || r.Arch != "" || r.Channel != "latest" || r.Version != "0.0.0-latest" {
		t.Errorf("a meta entry has capabilities and no image: %+v", r)
	}
	if err := VerifyBackendRoundTrip(meta, nil); err != nil {
		t.Error(err)
	}
	dev := meta
	dev.Name = "demo-development"
	if rows, _ := BackendLibraryEntries(dev, nil); rows[0].Channel != "development" {
		t.Errorf("a development meta backend is on the development channel: %+v", rows[0])
	}
	if _, err := BackendLibraryEntries(meta, []BackendPin{{"linux", "amd64", pinAMD}}); err == nil {
		t.Error("a meta backend has no image to pin")
	}
	meta.Mirrors = []string{"docker.io/acme/x:latest-y"}
	if _, err := BackendLibraryEntries(meta, nil); err == nil {
		t.Error("a meta backend has no image to mirror")
	}
	if _, err := BackendLibraryEntries(GalleryBackend{Metadata: Metadata{Name: "empty"}}, nil); err == nil {
		t.Error("an entry with no image and no capabilities names nothing")
	}
}

func TestBackendLibraryRefusesWhatItCannotCarry(t *testing.T) {
	pins := []BackendPin{{"linux", "amd64", pinAMD}}
	for name, mod := range map[string]func(*GalleryBackend){
		"version":       func(b *GalleryBackend) { b.Version = "1.0" },
		"sha256":        func(b *GalleryBackend) { b.SHA256 = "abc" },
		"url":           func(b *GalleryBackend) { b.Metadata.URL = "https://x" },
		"files":         func(b *GalleryBackend) { b.AdditionalFiles = []File{{Filename: "f"}} },
		"size":          func(b *GalleryBackend) { b.Size = "1GB" },
		"backend":       func(b *GalleryBackend) { b.Metadata.Backend = "x" },
		"no name":       func(b *GalleryBackend) { b.Name = "" },
		"uri scheme":    func(b *GalleryBackend) { b.URI = "oci://quay.io/acme/backends:latest-cpu-demo" },
		"https uri":     func(b *GalleryBackend) { b.URI = "https://example.com/backend.tar" },
		"mirror scheme": func(b *GalleryBackend) { b.Mirrors = []string{"https://example.com/b.tar"} },
		"odd tag":       func(b *GalleryBackend) { b.URI = "quay.io/acme/backends:v1.2.3" },
	} {
		b := demoBackend()
		mod(&b)
		if _, err := BackendLibraryEntries(b, pins); err == nil {
			t.Errorf("%s: a field the library cannot carry must be refused, not dropped", name)
		}
	}
	if _, err := BackendLibraryEntries(demoBackend(), nil); err == nil {
		t.Error("a concrete backend needs a platform")
	}
	if _, err := BackendLibraryEntries(demoBackend(), []BackendPin{{"linux", "amd64", pinAMD}, {"linux", "amd64", pinARM}}); err == nil {
		t.Error("two pins for one platform")
	}
	if _, err := BackendLibraryEntries(demoBackend(), []BackendPin{{"linux", "amd64", "latest"}}); err == nil {
		t.Error("a pin must be a sha256 digest")
	}
	if _, err := BackendLibraryEntries(demoBackend(), []BackendPin{{"", "amd64", pinAMD}}); err == nil {
		t.Error("a pin needs a platform")
	}
}

func TestBackendRoundTripTreatsEmptyAndAbsentAlike(t *testing.T) {
	b := demoBackend()
	b.URLs, b.Tags, b.Mirrors, b.Icon, b.License, b.Alias = []string{}, []string{}, []string{}, "", "", ""
	if err := VerifyBackendRoundTrip(b, []BackendPin{{"linux", "amd64", pinAMD}}); err != nil {
		t.Errorf("the gallery reader sees no difference between an empty list and an absent one: %v", err)
	}
}

func TestBackendsFromLibraryRefusesEntriesThatDisagree(t *testing.T) {
	rows, err := BackendLibraryEntries(demoBackend(), []BackendPin{{"linux", "amd64", pinAMD}, {"linux", "arm64", pinARM}})
	if err != nil {
		t.Fatal(err)
	}
	if list, err := BackendsFromLibrary(rows); err != nil || len(list) != 1 {
		t.Fatalf("two platforms of one backend are one gallery backend: %v %d", err, len(list))
	}
	rows[1].License = "gpl"
	if _, err := BackendsFromLibrary(rows); err == nil || !strings.Contains(err.Error(), "disagree") {
		t.Errorf("the entries of one backend must agree, got %v", err)
	}
	rows[1] = rows[0]
	rows[1].Arch = "arm64"
	rows[1].Artifact = "oci://quay.io/acme/backends:latest-other"
	if _, err := BackendsFromLibrary(rows); err == nil {
		t.Error("two images for one name cannot be one gallery backend")
	}
}

func TestBackendsFromLibrarySkipsOtherKindsAndKeepsOrder(t *testing.T) {
	a, b := demoBackend(), demoBackend()
	a.Name, b.Name = "z-first", "a-second"
	ra, _ := BackendLibraryEntries(a, []BackendPin{{"linux", "amd64", pinAMD}})
	rb, _ := BackendLibraryEntries(b, []BackendPin{{"linux", "amd64", pinAMD}})
	other := catalog.Release{ID: "t", Name: "t", Version: "1.0.0"}
	list, err := BackendsFromLibrary(append(append(ra, other), rb...))
	if err != nil {
		t.Fatal(err)
	}
	// The add-on entry has no kind, so it is an add-on and is skipped.
	if len(list) != 2 || list[0].Name != "z-first" || list[1].Name != "a-second" {
		t.Errorf("library order is kept: %+v", list)
	}
}

func TestToGalleryBackendUsesTheIdAsTheName(t *testing.T) {
	rows, _ := BackendLibraryEntries(demoBackend(), []BackendPin{{"linux", "amd64", pinAMD}})
	rows[0].Name = "Display name"
	b, err := ToGalleryBackend(rows[0])
	if err != nil {
		t.Fatal(err)
	}
	if b.Name != "cpu-demo" || b.URI != "quay.io/acme/backends:latest-cpu-demo" {
		t.Errorf("the id is the name the gallery knows: %+v", b)
	}
	if _, err := ToGalleryBackend(catalog.Release{Kind: catalog.KindModel, ID: "m"}); err == nil {
		t.Error("only a backend entry converts to a gallery backend")
	}
	rows[0].Icon = "icons/x.png"
	if _, err := ToGalleryBackend(rows[0]); err == nil {
		t.Error("an entry the host would refuse does not convert")
	}
}

func TestEncodeBackendGalleryIndexReadsBackAsTheEntries(t *testing.T) {
	a := demoBackend()
	b := GalleryBackend{Metadata: Metadata{Name: "demo", Description: "x"}, CapabilitiesMap: map[string]string{"default": "cpu-demo"}}
	raw, err := EncodeBackendGalleryIndex([]GalleryBackend{a, b})
	if err != nil {
		t.Fatal(err)
	}
	var loaded []GalleryBackend
	if err := yaml.Unmarshal(raw, &loaded); err != nil || len(loaded) != 2 {
		t.Fatalf("%v %d", err, len(loaded))
	}
	if !SameBackend(loaded[0], a) || !SameBackend(loaded[1], b) {
		t.Error("the gallery file does not read back as the entries")
	}
	a.Installed = true
	a.Gallery.Name = "runtime"
	again, err := EncodeBackendGalleryIndex([]GalleryBackend{a, b})
	if err != nil || string(again) != string(raw) {
		t.Errorf("runtime fields are not written: %v", err)
	}
	a.Version = "1"
	if _, err := EncodeBackendGalleryIndex([]GalleryBackend{a}); err == nil {
		t.Error("a field the file does not write is refused")
	}
}
