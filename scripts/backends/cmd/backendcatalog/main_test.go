// SPDX-License-Identifier: MIT
package main

import (
	"strings"
	"testing"

	"github.com/mudler/LocalAI/core/services/addons/catalog"
)

const fakeGallery = `
- &demo
  name: "demo"
  description: |
    A demo
    backend.
  capabilities:
    default: "cpu-demo"
- !!merge <<: *demo
  name: "cpu-demo"
  alias: "demo"
  uri: "quay.io/example/backends:latest-cpu-demo"
- !!merge <<: *demo
  name: "cpu-demo-development"
  uri: "quay.io/example/backends:master-cpu-demo"
`

func fakeDigest(image, platform string) (string, error) {
	return "sha256:" + strings.Repeat("ab", 32), nil
}

func TestRenderPinsTheGalleryImage(t *testing.T) {
	got, err := render([]byte(fakeGallery), []string{"cpu-demo"}, "linux", "amd64", fakeDigest)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 entry, got %d", len(got))
	}
	r := got[0]
	if r.EntryKind() != catalog.KindBackend || r.AddonID != "cpu-demo" || r.Alias != "demo" || r.Channel != "latest" ||
		r.Artifact != "oci://quay.io/example/backends:latest-cpu-demo" || r.Version != "0.0.0-latest.abababababab" ||
		r.Description != "A demo backend." {
		t.Fatalf("unexpected entry: %+v", r)
	}
	data, err := catalog.EncodeIndex(got)
	if err != nil {
		t.Fatal(err)
	}
	read, err := catalog.ReadIndex(data, catalog.KindBackend)
	if err != nil || len(read.Releases) != 1 || len(read.Skipped) != 0 {
		t.Fatalf("the rendered entry does not read back: %v %+v", err, read)
	}
}

func TestRenderRefusesWhatItCannotPin(t *testing.T) {
	for name, tc := range map[string][]string{
		"unknown":     {"nope"},
		"meta":        {"demo"},
		"master only": {"cpu-demo-development"},
	} {
		if _, err := render([]byte(fakeGallery), tc, "linux", "amd64", fakeDigest); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}
