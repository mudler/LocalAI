// SPDX-License-Identifier: MIT
package main

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
)

const fakeGallery = `
- &demo
  name: "demo"
  alias: "demo"
  license: mit
  icon: https://example.com/demo.png
  description: |
    A demo
    backend.
  urls:
    - https://github.com/example/demo
  tags: [LLM, CPU]
  capabilities:
    default: "cpu-demo"
- !!merge <<: *demo
  name: "demo-development"
  capabilities:
    default: "cpu-demo-development"
- !!merge <<: *demo
  name: "cpu-demo"
  uri: "quay.io/example/backends:latest-cpu-demo"
  mirrors:
    - localai/backends:latest-cpu-demo
- !!merge <<: *demo
  name: "cpu-demo-development"
  uri: "quay.io/example/backends:master-cpu-demo"
  mirrors:
    - localai/backends:master-cpu-demo
- !!merge <<: *demo
  name: "metal-demo"
  uri: "quay.io/example/backends:latest-metal-darwin-arm64-demo"
- !!merge <<: *demo
  name: "cpu-demo"
  uri: "quay.io/example/backends:latest-cpu-demo"
  mirrors:
    - localai/backends:latest-cpu-demo
`

// fakePinner serves a fixed set of platforms for an image and fails for the
// others, like a registry that does not have the image.
type fakePinner map[string][]gallery.BackendPin

func (f fakePinner) Pin(_ context.Context, image string) ([]gallery.BackendPin, error) {
	if pins, ok := f[image]; ok {
		return pins, nil
	}
	return nil, errors.New("manifest unknown")
}

const (
	sumA = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	sumB = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	sumC = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
)

func demoPins() fakePinner {
	return fakePinner{
		"quay.io/example/backends:latest-cpu-demo": {{OS: "linux", Arch: "amd64", Digest: sumA}, {OS: "linux", Arch: "arm64", Digest: sumB}},
		// A development image is read for its platforms; its digest is dropped.
		"quay.io/example/backends:master-cpu-demo": {{OS: "linux", Arch: "amd64", Digest: sumC}},
	}
}

func mustGallery(t *testing.T) []gallery.GalleryBackend {
	t.Helper()
	list, err := readGallery([]byte(fakeGallery))
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func TestBuildPinsReleaseImagesPerPlatformAndNeverPinsDevelopment(t *testing.T) {
	rows, rep, err := build(context.Background(), mustGallery(t), demoPins(), 4)
	if err != nil {
		t.Fatal(err)
	}
	byKey := map[string]catalog.Release{}
	for _, r := range rows {
		byKey[r.ID+" "+r.OS+"/"+r.Arch] = r
	}
	if r := byKey["cpu-demo linux/amd64"]; r.Digest != sumA || r.Mutable || r.Channel != "latest" || r.Version != "0.0.0-latest.aaaaaaaaaaaa" {
		t.Errorf("amd64 release entry: %+v", r)
	}
	if r := byKey["cpu-demo linux/arm64"]; r.Digest != sumB || r.Mutable {
		t.Errorf("arm64 release entry: %+v", r)
	}
	dev := byKey["cpu-demo-development linux/amd64"]
	if dev.Digest != "" || !dev.Mutable || dev.Channel != "development" || dev.Artifact != "oci://quay.io/example/backends:master-cpu-demo" || dev.Version != "0.0.0-development" {
		t.Errorf("development entry must keep its tag and have no digest: %+v", dev)
	}
	if m := byKey["demo /"]; !m.IsMeta() || m.Channel != "latest" || m.Version != "0.0.0-latest" {
		t.Errorf("meta entry: %+v", m)
	}
	if m := byKey["demo-development /"]; !m.IsMeta() || m.Channel != "development" {
		t.Errorf("development meta entry: %+v", m)
	}
	if rep.Entries != 6 || rep.Folded != 1 || rep.Meta != 2 || rep.Concrete != 3 || rep.Pinned != 2 || rep.Development != 1 {
		t.Errorf("report: %+v", rep)
	}
	for _, r := range rows {
		if err := catalog.ValidateEntry(r); err != nil {
			t.Errorf("%s: %v", r.ID, err)
		}
	}
}

func TestBuildMarksWhatTheRegistryCannotPinAndInventsNoDigest(t *testing.T) {
	// The registry has no image for metal-demo and nothing for cpu-demo.
	rows, rep, err := build(context.Background(), mustGallery(t), fakePinner{}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(rep.Unpinned, ","); got != "cpu-demo,metal-demo" {
		t.Errorf("unpinned release backends: %q", got)
	}
	for _, r := range rows {
		if r.Artifact != "" && r.Digest != "" {
			t.Errorf("%s has a digest the registry never gave: %s", r.ID, r.Digest)
		}
		if r.ID == "metal-demo" && (!r.Mutable || r.OS != "darwin" || r.Arch != "arm64") {
			t.Errorf("an unpinned entry is mutable and takes the platform of its tag: %+v", r)
		}
		if r.ID == "cpu-demo" && (!r.Mutable || r.OS != "linux" || r.Arch != "amd64") {
			t.Errorf("unexpected unpinned entry: %+v", r)
		}
	}
	// No pinner at all leaves every release image unpinned.
	if _, rep, err = build(context.Background(), mustGallery(t), nil, 1); err != nil || len(rep.Unpinned) != 2 {
		t.Errorf("without a registry: %v %+v", err, rep)
	}
}

func TestBuildRefusesTwinsThatDiffer(t *testing.T) {
	list := mustGallery(t)
	list = append(list, list[2])
	list[len(list)-1].License = "gpl"
	if _, _, err := build(context.Background(), list, demoPins(), 1); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Errorf("want a refusal for twins that differ, got %v", err)
	}
}

func TestLibraryRoundTripsToTheGalleryItCameFrom(t *testing.T) {
	list := mustGallery(t)
	rows, _, err := build(context.Background(), list, demoPins(), 2)
	if err != nil {
		t.Fatal(err)
	}
	body, err := encodeLibrary(rows)
	if err != nil {
		t.Fatal(err)
	}
	back, err := load(body)
	if err != nil {
		t.Fatal(err)
	}
	got, n, err := galleryBackends(back)
	if err != nil {
		t.Fatal(err)
	}
	// Six gallery entries, one of them the twin of another.
	if n != 5 || len(got) != 5 {
		t.Fatalf("want 5 backends, got %d and %d", n, len(got))
	}
	want := map[string]gallery.GalleryBackend{}
	for _, b := range list {
		want[b.Name] = b
	}
	for _, b := range got {
		if !gallery.SameBackend(want[b.Name], b) {
			t.Errorf("%s changed on the way through the library", b.Name)
		}
	}
}

func TestOutputIsDeterministic(t *testing.T) {
	var first []byte
	for i := 0; i < 3; i++ {
		rows, _, err := build(context.Background(), mustGallery(t), demoPins(), 1+i*3)
		if err != nil {
			t.Fatal(err)
		}
		body, err := encodeLibrary(rows)
		if err != nil {
			t.Fatal(err)
		}
		if first == nil {
			first = body
		} else if string(first) != string(body) {
			t.Fatal("the library differs between runs")
		}
	}
}

func TestRunWritesTheGalleryFromTheLibraryWithoutTheRegistry(t *testing.T) {
	dir := t.TempDir()
	rows, _, err := build(context.Background(), mustGallery(t), demoPins(), 1)
	if err != nil {
		t.Fatal(err)
	}
	body, err := encodeLibrary(rows)
	if err != nil {
		t.Fatal(err)
	}
	lib, out := filepath.Join(dir, "lib.yaml"), filepath.Join(dir, "gallery.yaml")
	if err := os.WriteFile(lib, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"--library", lib, "--out", out}); err != nil {
		t.Fatal(err)
	}
	gen, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(gen), "# Backend gallery: a GENERATED copy") {
		t.Error("the generated gallery says it is generated")
	}
	loaded, err := readGallery(gen)
	if err != nil || len(loaded) != 5 {
		t.Fatalf("the generated gallery loads %d entries: %v", len(loaded), err)
	}
	// The same library gives the same bytes.
	again := filepath.Join(dir, "again.yaml")
	if err := run([]string{"--library", lib, "--out", again}); err != nil {
		t.Fatal(err)
	}
	other, _ := os.ReadFile(again)
	if string(other) != string(gen) {
		t.Error("the generated gallery differs between runs")
	}
}

func TestRunRefusesAGalleryGivenAsTheLibraryAndAnEmptyCommand(t *testing.T) {
	dir := t.TempDir()
	notLibrary := filepath.Join(dir, "gallery.yaml")
	if err := os.WriteFile(notLibrary, []byte(fakeGallery), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"--library", notLibrary, "--out", filepath.Join(dir, "o.yaml")}); err == nil {
		t.Error("a gallery is not a library")
	}
	if err := run(nil); err == nil {
		t.Error("a command with nothing to do is a usage error")
	}
	if err := run([]string{"--import-gallery", notLibrary, "--repin"}); err == nil {
		t.Error("import and repin are exclusive")
	}
}

func TestGuessPlatformFollowsTheBuildMatrixTags(t *testing.T) {
	for uri, want := range map[string]string{
		"q/b:latest-metal-darwin-arm64-x": "darwin/arm64",
		"q/b:latest-nvidia-l4t-arm64-x":   "linux/arm64",
		"q/b:latest-gpu-nvidia-cuda-12-x": "linux/amd64",
		"q/b:master-cpu-x":                "linux/amd64",
	} {
		if os, arch := guessPlatform(uri); os+"/"+arch != want {
			t.Errorf("%s: %s/%s, want %s", uri, os, arch, want)
		}
	}
}

// The registry reader returns the digest of each platform image of an index,
// leaves out attestation manifests, and reads a single image too.
func TestRegistryPinnerReadsIndexesAndSingleImages(t *testing.T) {
	reg := httptest.NewServer(registry.New())
	defer reg.Close()
	host := strings.TrimPrefix(reg.URL, "http://")

	image := func(content string) v1.Image {
		layer, err := crane.Layer(map[string][]byte{"run.sh": []byte(content)})
		if err != nil {
			t.Fatal(err)
		}
		img, err := mutate.AppendLayers(empty.Image, layer)
		if err != nil {
			t.Fatal(err)
		}
		return img
	}
	platformed := func(img v1.Image, os, arch string) v1.Image {
		cfg, err := img.ConfigFile()
		if err != nil {
			t.Fatal(err)
		}
		cfg = cfg.DeepCopy()
		cfg.OS, cfg.Architecture = os, arch
		out, err := mutate.ConfigFile(img, cfg)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	amd, arm := platformed(image("amd"), "linux", "amd64"), platformed(image("arm"), "linux", "arm64")
	idx := mutate.AppendManifests(empty.Index,
		mutate.IndexAddendum{Add: amd, Descriptor: v1.Descriptor{Platform: &v1.Platform{OS: "linux", Architecture: "amd64"}}},
		mutate.IndexAddendum{Add: arm, Descriptor: v1.Descriptor{Platform: &v1.Platform{OS: "linux", Architecture: "arm64"}}},
		mutate.IndexAddendum{Add: image("att"), Descriptor: v1.Descriptor{Platform: &v1.Platform{OS: "unknown", Architecture: "unknown"}}},
	)
	multi, err := name.ParseReference(host + "/acme/backends:latest-cpu-demo")
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.WriteIndex(multi, idx); err != nil {
		t.Fatal(err)
	}
	single, err := name.ParseReference(host + "/acme/backends:latest-metal-darwin-arm64-demo")
	if err != nil {
		t.Fatal(err)
	}
	darwin := platformed(image("darwin"), "darwin", "arm64")
	if err := remote.Write(single, darwin); err != nil {
		t.Fatal(err)
	}

	pins, err := registryPinner{}.Pin(context.Background(), host+"/acme/backends:latest-cpu-demo")
	if err != nil {
		t.Fatal(err)
	}
	amdDigest, _ := amd.Digest()
	armDigest, _ := arm.Digest()
	if len(pins) != 2 || pins[0] != (gallery.BackendPin{OS: "linux", Arch: "amd64", Digest: amdDigest.String()}) || pins[1] != (gallery.BackendPin{OS: "linux", Arch: "arm64", Digest: armDigest.String()}) {
		t.Errorf("index pins: %+v", pins)
	}
	pins, err = registryPinner{}.Pin(context.Background(), host+"/acme/backends:latest-metal-darwin-arm64-demo")
	if err != nil {
		t.Fatal(err)
	}
	darwinDigest, _ := darwin.Digest()
	if len(pins) != 1 || pins[0] != (gallery.BackendPin{OS: "darwin", Arch: "arm64", Digest: darwinDigest.String()}) {
		t.Errorf("single image pins: %+v", pins)
	}
	if _, err := (registryPinner{}).Pin(context.Background(), host+"/acme/backends:latest-missing"); err == nil {
		t.Error("an image that is not in the registry cannot be pinned")
	}
}

func TestAddMergesNewBackendsAndLeavesTheOtherEntriesAlone(t *testing.T) {
	dir := t.TempDir()
	rows, _, err := build(context.Background(), mustGallery(t), demoPins(), 1)
	if err != nil {
		t.Fatal(err)
	}
	body, err := encodeLibrary(rows)
	if err != nil {
		t.Fatal(err)
	}
	lib := filepath.Join(dir, "lib.yaml")
	if err := os.WriteFile(lib, body, 0o644); err != nil {
		t.Fatal(err)
	}
	// One new backend, and one that is already in the library, changed.
	const snippet = `
- name: cpu-newone
  alias: newone
  license: mit
  uri: quay.io/example/backends:latest-cpu-newone
- name: metal-demo
  license: gpl-3.0
  uri: quay.io/example/backends:latest-metal-darwin-arm64-demo
`
	add := filepath.Join(dir, "add.yaml")
	if err := os.WriteFile(add, []byte(snippet), 0o644); err != nil {
		t.Fatal(err)
	}
	// The registry has the new image and not the changed one: the changed
	// backend keeps no old digest and is written unpinned.
	p := fakePinner{"quay.io/example/backends:latest-cpu-newone": {{OS: "linux", Arch: "amd64", Digest: sumC}}}
	if err := mergeLibrary(context.Background(), lib, mustRead(t, add), p, 1); err != nil {
		t.Fatal(err)
	}
	merged, err := os.ReadFile(lib)
	if err != nil {
		t.Fatal(err)
	}
	got, err := load(merged)
	if err != nil {
		t.Fatal(err)
	}
	byKey := map[string]catalog.Release{}
	var order []string
	for _, r := range got {
		byKey[r.ID+" "+r.OS+"/"+r.Arch] = r
		order = append(order, r.ID)
	}
	if r := byKey["cpu-demo linux/amd64"]; r.Digest != sumA {
		t.Errorf("an entry that was not added is kept with its digest: %+v", r)
	}
	if r := byKey["cpu-newone linux/amd64"]; r.Digest != sumC || r.Alias != "newone" {
		t.Errorf("the new backend is pinned: %+v", r)
	}
	if r := byKey["metal-demo darwin/arm64"]; r.License != "gpl-3.0" || !r.Mutable || r.Description != "" {
		t.Errorf("a backend that is added again is replaced: %+v", r)
	}
	if strings.Join(order, ",") != "demo,demo-development,cpu-demo,cpu-demo,cpu-demo-development,metal-demo,cpu-newone" {
		t.Errorf("a replaced backend keeps its place and a new one goes last: %v", order)
	}
	// The merged library still produces a gallery.
	if err := run([]string{"--library", lib, "--out", filepath.Join(dir, "g.yaml")}); err != nil {
		t.Fatal(err)
	}
}

func mustRead(t *testing.T, path string) []gallery.GalleryBackend {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	list, err := readGallery(src)
	if err != nil {
		t.Fatal(err)
	}
	return list
}
