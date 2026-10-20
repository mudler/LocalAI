// SPDX-License-Identifier: MIT
package packaging

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The release platforms of the packaged first-party add-ons, with the runner
// that builds and checks each one natively.
var releasePlatforms = map[string]string{
	"linux/amd64":  "ubuntu-24.04",
	"linux/arm64":  "ubuntu-24.04-arm",
	"darwin/arm64": "macos-14",
	"darwin/amd64": "macos-15-intel",
}

// nativeChecked records the platforms whose native check has passed on a runner
// of that platform. A task of group G adds a platform here in the same change
// that enables it in the five files and shows the passing legs in its pull
// request. Enabling without this record, or recording without enabling, fails.
var nativeChecked = map[string]bool{"linux/amd64": true, "linux/arm64": true, "darwin/arm64": true, "darwin/amd64": true}

var packagedAddons = []string{"voice", "face", "fine-tuning", "quantization", "studio", "talk"}

func TestEveryPackagedAddonListsEveryReleasePlatformWithItsRunner(t *testing.T) {
	for _, id := range packagedAddons {
		p, err := loadPlatforms(filepath.Join("..", "..", "..", "addons", id, "package", "platforms.yaml"))
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if p.Addon != id || p.Executable != id {
			t.Fatalf("%s: addon %q executable %q", id, p.Addon, p.Executable)
		}
		got := map[string]string{}
		for _, e := range p.Platforms {
			got[e.Platform] = e.Runner
			if e.NativeCheck != NativeCheckKind {
				t.Fatalf("%s %s: native check %q", id, e.Platform, e.NativeCheck)
			}
		}
		if len(got) != len(releasePlatforms) {
			t.Fatalf("%s lists %d platforms, want %d", id, len(got), len(releasePlatforms))
		}
		for platform, runner := range releasePlatforms {
			if got[platform] != runner {
				t.Fatalf("%s: platform %s has runner %q, want %q", id, platform, got[platform], runner)
			}
		}
	}
}

func TestAPlatformIsEnabledExactlyWhenItsNativeCheckIsRecorded(t *testing.T) {
	for _, id := range packagedAddons {
		p, err := loadPlatforms(filepath.Join("..", "..", "..", "addons", id, "package", "platforms.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		var enabled []string
		for _, e := range p.Platforms {
			if e.Enabled {
				enabled = append(enabled, e.Platform)
			}
		}
		var recorded []string
		for platform := range nativeChecked {
			recorded = append(recorded, platform)
		}
		sort.Strings(enabled)
		sort.Strings(recorded)
		if len(enabled) != len(recorded) {
			t.Fatalf("%s: enabled %v, native check recorded for %v", id, enabled, recorded)
		}
		for i := range enabled {
			if enabled[i] != recorded[i] {
				t.Fatalf("%s: enabled %v, native check recorded for %v", id, enabled, recorded)
			}
		}
	}
}

func TestEveryPackagedAddonHasACatalogTemplate(t *testing.T) {
	for _, id := range packagedAddons {
		b, err := os.ReadFile(filepath.Join("..", "..", "..", "gallery", "addons", id+".yaml.tmpl"))
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		for _, want := range []string{`addonId: "{{addon}}"`, "requireSignature: true"} {
			if !strings.Contains(string(b), want) {
				t.Fatalf("%s template lacks %q", id, want)
			}
		}
	}
}
