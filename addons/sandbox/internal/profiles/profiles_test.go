package profiles_test

import (
	"strings"
	"testing"

	"github.com/mudler/LocalAI/addons/sandbox/internal/profiles"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

func TestDeclaredProfilesAreValidAndNested(t *testing.T) {
	d := &contract.ExecutionDescriptor{Class: contract.ExecutionClassCommands, Profiles: profiles.Declared()}
	if d.Profiles[0].ID != profiles.Base {
		t.Fatalf("the first profile is %q, want the fallback", d.Profiles[0].ID)
	}
	have := map[string]map[string]bool{}
	for _, p := range d.Profiles {
		have[p.ID] = map[string]bool{}
		for _, tl := range p.Tools {
			have[p.ID][tl.Name] = true
			if len(tl.Check) == 0 {
				t.Errorf("profile %s: tool %s has no build check", p.ID, tl.Name)
			}
		}
	}
	// media is base plus more, video is media plus more: a profile never has
	// fewer tools than the one it extends.
	for _, pair := range [][2]string{{profiles.Base, profiles.Media}, {profiles.Media, profiles.Video}} {
		for name := range have[pair[0]] {
			if !have[pair[1]][name] {
				t.Errorf("%s lacks %s, which %s has", pair[1], name, pair[0])
			}
		}
	}
	for _, want := range []string{"ffmpeg", "sox", "ImageMagick"} {
		if !have[profiles.Media][want] || have[profiles.Base][want] {
			t.Errorf("%s must be in media and not in base", want)
		}
	}
	if !have[profiles.Video]["fframes"] || !have[profiles.Video]["Rust"] {
		t.Error("video needs Rust and fframes")
	}
}

func TestParseImages(t *testing.T) {
	d := strings.Repeat("a", 64)
	got, err := profiles.ParseImages("media=quay.io/x/guest-media@sha256:" + d + ", video=quay.io/x/guest-video@sha256:" + d)
	if err != nil || len(got) != 2 || got["media"] == "" {
		t.Fatalf("got %v, %v", got, err)
	}
	if got, err := profiles.ParseImages(""); err != nil || len(got) != 0 {
		t.Fatalf("empty list: %v, %v", got, err)
	}
	for _, bad := range []string{"media", "media=quay.io/x@sha256:abc", "Media=quay.io/x@sha256:" + d, "media=quay.io/x:latest",
		"media=a/b@sha256:" + d + ",media=a/b@sha256:" + d} {
		if _, err := profiles.ParseImages(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}
