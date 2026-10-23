package gallery

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/system"
	"gopkg.in/yaml.v3"
)

// backend/index.yaml is generated and never edited. The CI check regenerates it
// and fails on a diff; this test does the same in the Go suite, so a hand edit
// is found by go test too.
func TestCheckedInBackendGalleryIsTheGeneratedCopy(t *testing.T) {
	list, err := BackendsFromLibrary(loadRealBackendLibrary(t))
	if err != nil {
		t.Fatal(err)
	}
	flat := make([]GalleryBackend, len(list))
	for i, b := range list {
		flat[i] = *b
	}
	want, err := EncodeBackendGalleryIndex(flat)
	if err != nil {
		t.Fatal(err)
	}
	have, err := os.ReadFile("../../backend/index.yaml")
	if err != nil {
		t.Fatal(err)
	}
	body := string(have)
	for strings.HasPrefix(body, "#") {
		_, rest, _ := strings.Cut(body, "\n")
		body = rest
	}
	if body != string(want) {
		t.Error("backend/index.yaml is not the copy generated from gallery/backends/index.yaml. It is generated; do not edit it by hand. Regenerate: go run ./scripts/backends/cmd/backendcatalog --library gallery/backends/index.yaml --out backend/index.yaml")
	}
	var loaded []GalleryBackend
	if err := yaml.Unmarshal(have, &loaded); err != nil {
		t.Fatalf("the gallery reader on the generated gallery: %v", err)
	}
	if len(loaded) != len(flat) {
		t.Fatalf("the generated gallery loads %d entries, the library has %d backends", len(loaded), len(flat))
	}
	for i := range loaded {
		if !SameBackend(loaded[i], flat[i]) {
			t.Errorf("%s loads differently from the library", flat[i].Name)
		}
	}
	t.Logf("generated gallery: %d entries read back equal to the library", len(loaded))
}

// A gallery marked as a library is read as a library; any other gallery is
// read as an index. The server hands the shipped backend library to the reader
// this way, so the backends it lists are the backends of the library.
func TestLibraryGalleryReadsTheBackendLibrary(t *testing.T) {
	library, err := os.ReadFile(realBackendLibrary)
	if err != nil {
		t.Fatal(err)
	}
	var libraryHits, galleryHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/backends/index.yaml":
			libraryHits++
			_, _ = w.Write(library)
		case "/gallery/index.yaml":
			galleryHits++
			_, _ = w.Write([]byte("- name: from-gallery\n  uri: quay.io/acme/backends:latest-cpu-x\n"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	state, err := system.GetSystemState(system.WithBackendPath(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	names := func(g config.Gallery) GalleryElements[*GalleryBackend] {
		t.Helper()
		backends, err := AvailableBackendsUnfiltered([]config.Gallery{g}, state)
		if err != nil {
			t.Fatal(err)
		}
		return backends
	}
	official := config.Gallery{Name: "localai", URL: srv.URL + "/gallery/index.yaml"}

	// Default: the gallery index, and the library file is never requested.
	if got := names(official); len(got) != 1 || got[0].Name != "from-gallery" {
		t.Errorf("default source gave %d backends", len(got))
	}
	if libraryHits != 0 {
		t.Errorf("the library was read %d times with the default source", libraryHits)
	}

	// Library: a gallery marked as a library is read as one.
	official.Library = true
	official.URL = srv.URL + "/backends/index.yaml"
	backends := names(official)
	expected, err := BackendsFromLibrary(loadRealBackendLibrary(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(backends) != len(expected) {
		t.Fatalf("the library gave %d backends, want %d", len(backends), len(expected))
	}
	llama := backends.FindByName("llama-cpp")
	if llama == nil || !llama.IsMeta() || llama.CapabilitiesMap["nvidia"] != "cuda12-llama-cpp" || llama.Alias != "llama-cpp" {
		t.Fatalf("the meta backend lost its body: %+v", llama)
	}
	cpu := backends.FindByName("cpu-llama-cpp")
	if cpu == nil || cpu.IsMeta() || !strings.HasPrefix(cpu.URI, "quay.io/go-skynet/local-ai-backends:latest-") || len(cpu.Mirrors) != 1 || cpu.License == "" || cpu.Icon == "" {
		t.Fatalf("the concrete backend lost its body: %+v", cpu)
	}
	if g := cpu.GetGallery(); g.Name != "localai" || g.URL != official.URL || !g.Library {
		t.Errorf("the entry should carry the configured gallery, got %+v", g)
	}
	// The backends the server lists are the backends of the generated gallery.
	var generated []GalleryBackend
	raw, err := os.ReadFile("../../backend/index.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(raw, &generated); err != nil {
		t.Fatal(err)
	}
	if len(generated) != len(backends) {
		t.Fatalf("the generated gallery has %d backends, the library %d", len(generated), len(backends))
	}
	for i, b := range generated {
		if !SameBackend(b, *backends[i]) {
			t.Errorf("%s differs between the library and the generated gallery", b.Name)
		}
	}
	// The meta backends resolve to a concrete one through the library.
	if best := llama.FindBestBackendFromMeta(state, backends); best != nil && best.IsMeta() {
		t.Errorf("a meta backend resolved to another meta backend: %s", best.Name)
	}

	// A gallery the owner added keeps its own index.
	other := config.Gallery{Name: "mine", URL: srv.URL + "/gallery/index.yaml"}
	if got := names(other); len(got) != 1 || got[0].Name != "from-gallery" {
		t.Errorf("another gallery gave %d backends", len(got))
	}

	// An unreachable library is an error, not a silent fall back.
	official.URL = srv.URL + "/missing.yaml"
	if _, err := AvailableBackendsUnfiltered([]config.Gallery{official}, state); err == nil {
		t.Error("a missing library fell back to the gallery")
	}

	// A library with an entry the host refuses is an error too, and is not kept.
	bad := config.Gallery{Name: "bad", URL: srv.URL + "/bad.yaml", Library: true}
	badSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("- kind: backend\n  id: bad\n  name: bad\n  version: 0.0.0-latest\n  artifact: https://example.org/bad.tar\n  digest: " + strings.Repeat("x", 10) + "\n  os: linux\n  arch: amd64\n"))
	}))
	t.Cleanup(badSrv.Close)
	bad.URL = badSrv.URL + "/bad.yaml"
	if _, err := AvailableBackendsUnfiltered([]config.Gallery{bad}, state); err == nil {
		t.Error("an invalid library entry was accepted")
	}
}
