package config_test

import (
	"encoding/json"
	"testing"

	"github.com/mudler/LocalAI/core/config"
	"gopkg.in/yaml.v3"
)

// Galleries are configured as a JSON list in LOCALAI_GALLERIES and edited as
// raw JSON in the settings UI, so both directions must round-trip or a user
// silently loses their mirrors the next time they save.
func TestGalleryMirrorsRoundTripJSON(t *testing.T) {
	const in = `[{"url":"https://primary.example/index.yaml","name":"localai",` +
		`"mirrors":["github:mudler/LocalAI/gallery/index.yaml@master","file:///srv/index.yaml"]}]`

	var galleries []config.Gallery
	if err := json.Unmarshal([]byte(in), &galleries); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(galleries) != 1 {
		t.Fatalf("got %d galleries, want 1", len(galleries))
	}
	// Order is load-bearing: mirrors are an ordered fallback chain, not a set.
	want := []string{"github:mudler/LocalAI/gallery/index.yaml@master", "file:///srv/index.yaml"}
	if got := galleries[0].Mirrors; !equalStrings(got, want) {
		t.Fatalf("Mirrors = %v, want %v", got, want)
	}

	out, err := json.Marshal(galleries)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var again []config.Gallery
	if err := json.Unmarshal(out, &again); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	if !equalStrings(again[0].Mirrors, want) {
		t.Fatalf("mirrors lost or reordered on round-trip: %s", out)
	}
	if again[0].URL != "https://primary.example/index.yaml" || again[0].Name != "localai" {
		t.Fatalf("primary fields damaged on round-trip: %+v", again[0])
	}
}

func TestGalleryMirrorsRoundTripYAML(t *testing.T) {
	const in = "- url: https://primary.example/index.yaml\n" +
		"  name: localai\n" +
		"  mirrors:\n" +
		"    - github:mudler/LocalAI/gallery/index.yaml@master\n" +
		"    - https://fallback.example/index.yaml\n"

	var galleries []config.Gallery
	if err := yaml.Unmarshal([]byte(in), &galleries); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := []string{"github:mudler/LocalAI/gallery/index.yaml@master", "https://fallback.example/index.yaml"}
	if got := galleries[0].Mirrors; !equalStrings(got, want) {
		t.Fatalf("Mirrors = %v, want %v", got, want)
	}

	out, err := yaml.Marshal(galleries)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var again []config.Gallery
	if err := yaml.Unmarshal(out, &again); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	if !equalStrings(again[0].Mirrors, want) {
		t.Fatalf("mirrors lost or reordered on YAML round-trip: %s", out)
	}
}

// omitempty keeps existing configs byte-identical when they declare no
// mirrors, so this change cannot churn anyone's stored settings.
func TestGalleryWithoutMirrorsMarshalsUnchanged(t *testing.T) {
	out, err := json.Marshal(config.Gallery{URL: "https://x/index.yaml", Name: "n"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(out) != `{"url":"https://x/index.yaml","name":"n"}` {
		t.Errorf("got %s, want no mirrors key", out)
	}

	y, err := yaml.Marshal(config.Gallery{URL: "https://x/index.yaml", Name: "localai"})
	if err != nil {
		t.Fatalf("yaml marshal: %v", err)
	}
	if string(y) != "url: https://x/index.yaml\nname: localai\n" {
		t.Errorf("got %q, want no mirrors key", y)
	}
}

// The runtime settings registry diffs gallery lists to decide whether the
// persisted settings differ from the startup baseline. A Gallery carrying a
// slice is no longer comparable with ==, so that diff must still notice a
// change confined to the mirror list — otherwise editing mirrors in the
// settings UI would be dropped as a no-op.
func TestGalleryListsDifferByMirrorsAlone(t *testing.T) {
	base := []config.Gallery{{URL: "https://x/index.yaml", Name: "n"}}
	withMirror := []config.Gallery{{URL: "https://x/index.yaml", Name: "n", Mirrors: []string{"github:mudler/LocalAI/gallery/index.yaml@master"}}}

	if config.GalleriesEqual(base, withMirror) {
		t.Error("GalleriesEqual reported equal for lists that differ only by mirrors")
	}
	if !config.GalleriesEqual(base, []config.Gallery{{URL: "https://x/index.yaml", Name: "n"}}) {
		t.Error("GalleriesEqual reported unequal for identical mirror-less lists")
	}
	if !config.GalleriesEqual(withMirror, []config.Gallery{{URL: "https://x/index.yaml", Name: "n", Mirrors: []string{"github:mudler/LocalAI/gallery/index.yaml@master"}}}) {
		t.Error("GalleriesEqual reported unequal for identical mirrored lists")
	}
	// Reordering the fallback chain is a real change, not a no-op.
	a := []config.Gallery{{URL: "u", Mirrors: []string{"m1", "m2"}}}
	b := []config.Gallery{{URL: "u", Mirrors: []string{"m2", "m1"}}}
	if config.GalleriesEqual(a, b) {
		t.Error("GalleriesEqual ignored mirror ordering")
	}
	// A nil mirror list and an empty one both mean "no mirrors".
	if !config.GalleriesEqual(
		[]config.Gallery{{URL: "u"}},
		[]config.Gallery{{URL: "u", Mirrors: []string{}}}) {
		t.Error("GalleriesEqual distinguished nil from empty mirrors")
	}
	if config.GalleriesEqual(base, nil) {
		t.Error("GalleriesEqual reported equal for lists of different length")
	}
}

// Equal replaced ==, so it has to keep covering every field == covered:
// missing one would make an env/CLI-set gallery list look like the default.
func TestGalleryEqualComparesEveryField(t *testing.T) {
	if config.GalleriesEqual(
		[]config.Gallery{{URL: "https://a/index.yaml", Name: "n"}},
		[]config.Gallery{{URL: "https://b/index.yaml", Name: "n"}}) {
		t.Error("GalleriesEqual ignored URL")
	}
	if config.GalleriesEqual(
		[]config.Gallery{{URL: "https://a/index.yaml", Name: "one"}},
		[]config.Gallery{{URL: "https://a/index.yaml", Name: "two"}}) {
		t.Error("GalleriesEqual ignored Name")
	}
	// The verification pointer must be compared by value, not identity.
	v1 := &config.GalleryVerification{Issuer: "i"}
	v2 := &config.GalleryVerification{Issuer: "i"}
	if !config.GalleriesEqual(
		[]config.Gallery{{URL: "u", Verification: v1}},
		[]config.Gallery{{URL: "u", Verification: v2}}) {
		t.Error("GalleriesEqual compared verification by pointer identity")
	}
	if config.GalleriesEqual(
		[]config.Gallery{{URL: "u", Verification: v1}},
		[]config.Gallery{{URL: "u", Verification: &config.GalleryVerification{Issuer: "other"}}}) {
		t.Error("GalleriesEqual ignored a differing verification block")
	}
	if config.GalleriesEqual(
		[]config.Gallery{{URL: "u"}},
		[]config.Gallery{{URL: "u", Verification: v1}}) {
		t.Error("GalleriesEqual ignored a verification block appearing")
	}
	// GalleryVerification has five string fields; a value comparison must
	// notice a change in any of them, not just the first.
	full := config.GalleryVerification{
		Issuer: "i", IssuerRegex: "ir", Identity: "id", IdentityRegex: "idr", NotBefore: "2026-05-01T00:00:00Z",
	}
	for _, mutate := range []func(*config.GalleryVerification){
		func(v *config.GalleryVerification) { v.Issuer = "x" },
		func(v *config.GalleryVerification) { v.IssuerRegex = "x" },
		func(v *config.GalleryVerification) { v.Identity = "x" },
		func(v *config.GalleryVerification) { v.IdentityRegex = "x" },
		func(v *config.GalleryVerification) { v.NotBefore = "2030-01-01T00:00:00Z" },
	} {
		other := full
		mutate(&other)
		if config.GalleriesEqual(
			[]config.Gallery{{URL: "u", Verification: &full}},
			[]config.Gallery{{URL: "u", Verification: &other}}) {
			t.Errorf("GalleriesEqual ignored a verification change: %+v vs %+v", full, other)
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
