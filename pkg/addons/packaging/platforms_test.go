// SPDX-License-Identifier: MIT
package packaging

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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

var _ = Describe("the packaged add-ons", func() {
	for _, id := range packagedAddons {
		Context(id, func() {
			It("lists every release platform with its runner", func() {
				p, err := loadPlatforms(filepath.Join("..", "..", "..", "addons", id, "package", "platforms.yaml"))
				Expect(err).ToNot(HaveOccurred())
				Expect(p.Addon).To(Equal(id))
				Expect(p.Executable).To(Equal(id))
				got := map[string]string{}
				for _, e := range p.Platforms {
					got[e.Platform] = e.Runner
					Expect(e.NativeCheck).To(Equal(NativeCheckKind), "%s: native check", e.Platform)
				}
				Expect(got).To(HaveLen(len(releasePlatforms)))
				for platform, runner := range releasePlatforms {
					Expect(got[platform]).To(Equal(runner), "platform %s", platform)
				}
			})

			It("enables a platform exactly when its native check is recorded", func() {
				p, err := loadPlatforms(filepath.Join("..", "..", "..", "addons", id, "package", "platforms.yaml"))
				Expect(err).ToNot(HaveOccurred())
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
				Expect(enabled).To(Equal(recorded), "enabled platforms and recorded native checks differ")
			})

			It("has a catalog template", func() {
				b, err := os.ReadFile(filepath.Join("..", "..", "..", "gallery", "addons", id+".yaml.tmpl"))
				Expect(err).ToNot(HaveOccurred())
				for _, want := range []string{`addonId: "{{addon}}"`, "requireSignature: true"} {
					Expect(strings.Contains(string(b), want)).To(BeTrue(), "template lacks %q", want)
				}
			})
		})
	}
})
