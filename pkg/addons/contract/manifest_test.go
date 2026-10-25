// SPDX-License-Identifier: MIT
package contract

import (
	"bytes"
	"encoding/json"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"os"
	"strings"
)

func fixture() []byte {
	b, e := os.ReadFile("../../../tests/addons/fixtures/contracts/service.json")
	Expect(e).NotTo(HaveOccurred())
	return b
}
func compatible() HostCompatibility {
	return HostCompatibility{Host: 1, API: 1, Protocol: 1, SDK: 1, DataFormat: 1, OS: "linux", Arch: "amd64"}
}

var _ = Describe("Manifest", func() {
	It("refuses an add-on that needs a newer SDK than the host provides, and says so", func() {
		b, e := os.ReadFile("../../../tests/addons/fixtures/contracts/page.json")
		Expect(e).NotTo(HaveOccurred())
		m, e := ParseManifest(b)
		Expect(e).NotTo(HaveOccurred())
		m.Capabilities.UI.SDK = VersionRange{Min: 2, Max: 2}
		host := compatible() // SDK 1
		e = ValidateManifest(m, host)
		Expect(e).To(MatchError("the add-on needs SDK 2 to 2 and this host provides SDK 1"))
	})

	It("accepts a range that contains the host SDK", func() {
		b, e := os.ReadFile("../../../tests/addons/fixtures/contracts/page.json")
		Expect(e).NotTo(HaveOccurred())
		m, e := ParseManifest(b)
		Expect(e).NotTo(HaveOccurred())
		m.Capabilities.UI.SDK = VersionRange{Min: 1, Max: 2}
		host := compatible()
		host.SDK = HostSDKVersion
		Expect(ValidateManifest(m, host)).To(Succeed())
	})

	It("accepts a range that spans the host SDK", func() {
		b, e := os.ReadFile("../../../tests/addons/fixtures/contracts/page.json")
		Expect(e).NotTo(HaveOccurred())
		m, e := ParseManifest(b)
		Expect(e).NotTo(HaveOccurred())
		m.Capabilities.UI.SDK = VersionRange{Min: 1, Max: 3}
		host := compatible()
		host.SDK = HostSDKVersion
		Expect(ValidateManifest(m, host)).To(Succeed())
	})

	It("refuses an add-on built for SDK 1 only on a version 2 host", func() {
		b, e := os.ReadFile("../../../tests/addons/fixtures/contracts/page.json")
		Expect(e).NotTo(HaveOccurred())
		m, e := ParseManifest(b)
		Expect(e).NotTo(HaveOccurred())
		m.Capabilities.UI.SDK = VersionRange{Min: 1, Max: 1}
		host := compatible()
		host.SDK = HostSDKVersion
		Expect(ValidateManifest(m, host)).To(MatchError("the add-on needs SDK 1 to 1 and this host provides SDK 2"))
	})

	It("keeps the generic message for a range that is malformed", func() {
		b, e := os.ReadFile("../../../tests/addons/fixtures/contracts/page.json")
		Expect(e).NotTo(HaveOccurred())
		m, e := ParseManifest(b)
		Expect(e).NotTo(HaveOccurred())
		m.Capabilities.UI.SDK = VersionRange{Min: 3, Max: 2}
		Expect(ValidateManifest(m, compatible())).To(MatchError("invalid or incompatible UI"))
	})

	It("accepts independent service tool page and chat capabilities", func() {
		for _, name := range []string{"service", "headless", "page", "chat"} {
			b, e := os.ReadFile("../../../tests/addons/fixtures/contracts/" + name + ".json")
			Expect(e).NotTo(HaveOccurred())
			m, e := ParseManifest(b)
			Expect(e).NotTo(HaveOccurred())
			Expect(ValidateManifest(m, compatible())).To(Succeed())
		}
		m, e := ParseManifest(fixture())
		Expect(e).NotTo(HaveOccurred())
		h := compatible()
		h.SDK = 0
		Expect(ValidateManifest(m, h)).To(Succeed())
		m.Capabilities = Capabilities{Tools: []string{"query"}}
		Expect(ValidateManifest(m, h)).To(Succeed())
	})
	It("requires the existence tool of an addon whose resources are created or deleted", func() {
		raw, e := os.ReadFile("../../../addons/tables/manifest.json")
		Expect(e).NotTo(HaveOccurred())
		load := func() Manifest {
			m, e := ParseManifest(raw)
			Expect(e).NotTo(HaveOccurred())
			return m
		}
		without := func(m *Manifest) {
			var keep []string
			for _, t := range m.Capabilities.Tools {
				if t != "resource-exists" {
					keep = append(keep, t)
				}
			}
			m.Capabilities.Tools = keep
		}
		Expect(ValidateManifest(load(), compatible())).To(Succeed())

		// Delete and create both need it: without an explicit answer the
		// host could not tell a missing backing from an add-on that cannot
		// answer, and would drop the record of data that is still there.
		m := load()
		without(&m)
		Expect(ValidateManifest(m, compatible())).NotTo(Succeed())
		m = load()
		without(&m)
		m.Capabilities.Resources.Delete, m.Capabilities.AdminTools = "", nil
		Expect(ValidateManifest(m, compatible())).NotTo(Succeed(), "create only")
		m = load()
		without(&m)
		m.Capabilities.Resources.Create = ""
		Expect(ValidateManifest(m, compatible())).NotTo(Succeed(), "delete only")
		// A mapping that creates and deletes nothing needs no existence tool.
		m = load()
		without(&m)
		m.Capabilities.Resources.Create, m.Capabilities.Resources.Delete, m.Capabilities.AdminTools = "", "", nil
		Expect(ValidateManifest(m, compatible())).To(Succeed())
	})
	It("rejects invalid namespace IDs and executable paths", func() {
		for _, id := range []string{"", "../sample", "a/b", "A", "a.b", strings.Repeat("a", 65)} {
			m, _ := ParseManifest(fixture())
			m.ID = id
			Expect(ValidateManifest(m, compatible())).NotTo(Succeed(), id)
		}
		for _, p := range []string{"", "/bin/x", "..", "../x", "bin/../x", "bin//x", "C:/x", `bin\x`, "https://x", "bin/%2e/x"} {
			m, _ := ParseManifest(fixture())
			m.Executable = p
			Expect(ValidateManifest(m, compatible())).NotTo(Succeed(), p)
		}
	})
	It("rejects add-on IDs that the host serves as a route segment", func() {
		// Every first path segment the host uses under /api/addons/ would
		// otherwise capture the asset URL of an add-on with that ID.
		Expect(ReservedAddonIDs()).To(ConsistOf("apps", "proposals", "artifacts", "resources", "conversations", "dispatch", "relay", "catalog", "icon", "assets", "installed", "sources", "builtin", "pins"))
		for _, id := range ReservedAddonIDs() {
			m, _ := ParseManifest(fixture())
			m.ID = id
			e := ValidateManifest(m, compatible())
			Expect(e).To(HaveOccurred(), id)
			Expect(e.Error()).To(ContainSubstring("reserved"), id)
			Expect(ValidAddonID(id)).To(BeFalse(), id)
		}
		Expect(ValidAddonID("proposals-tool")).To(BeTrue())
		m, _ := ParseManifest(fixture())
		m.ID = "app"
		Expect(ValidateManifest(m, compatible())).To(Succeed())
	})
	It("rejects invalid versions ranges and capabilities", func() {
		for _, v := range []string{"", "latest", "development", "v1.0.0", "01.2.3", "1.2", "1.2.3+mutable", "1.2.3-a..b", "1.2.3-a."} {
			m, _ := ParseManifest(fixture())
			m.Version = v
			Expect(ValidateManifest(m, compatible())).NotTo(Succeed(), v)
		}
		m, _ := ParseManifest(fixture())
		m.SchemaVersion = 2
		Expect(ValidateManifest(m, compatible())).NotTo(Succeed())
		m, _ = ParseManifest(fixture())
		m.API = VersionRange{Min: 2, Max: 1}
		Expect(ValidateManifest(m, compatible())).NotTo(Succeed())
		m, _ = ParseManifest(fixture())
		m.Capabilities = Capabilities{}
		Expect(ValidateManifest(m, compatible())).NotTo(Succeed())
		m.Capabilities.Tools = []string{"query", "query"}
		Expect(ValidateManifest(m, compatible())).NotTo(Succeed())
	})
	It("rejects unknown duplicate trailing and oversized manifest JSON", func() {
		for _, b := range [][]byte{[]byte(strings.Replace(string(fixture()), `"service":true`, `"service":true,"shell":true`, 1)), []byte(strings.Replace(string(fixture()), `"id":"sample"`, `"id":"sample","id":"other"`, 1)), append(fixture(), []byte(` {}`)...), []byte(`null`), []byte(strings.Repeat(" ", int(DefaultLimits().ManifestBytes)+1))} {
			_, e := ParseManifest(b)
			Expect(e).To(HaveOccurred())
		}
	})
	It("rejects incompatible host API protocol SDK data and platform fixtures", func() {
		b, e := os.ReadFile("../../../tests/addons/fixtures/contracts/incompatible.json")
		Expect(e).NotTo(HaveOccurred())
		var hosts []HostCompatibility
		Expect(json.Unmarshal(b, &hosts)).To(Succeed())
		page, e := os.ReadFile("../../../tests/addons/fixtures/contracts/page.json")
		Expect(e).NotTo(HaveOccurred())
		m, e := ParseManifest(page)
		Expect(e).NotTo(HaveOccurred())
		for _, h := range hosts {
			Expect(ValidateManifest(m, h)).NotTo(Succeed())
		}
	})
	It("validates UI only when declared and rejects inconsistent contributions", func() {
		m, _ := ParseManifest(fixture())
		m.Capabilities.UI = &UI{Entry: "ui/main.js", SDK: VersionRange{Min: 1, Max: 1}, ChatActions: []ChatAction{{ID: "open"}}}
		Expect(ValidateManifest(m, compatible())).To(Succeed())
		for _, u := range []*UI{{}, {Entry: "../x", SDK: VersionRange{1, 1}}, {Entry: "ui/main.js", SDK: VersionRange{1, 1}, CSS: []string{"/x"}}, {Entry: "ui/main.js", SDK: VersionRange{1, 1}, Navigation: []string{"missing"}}, {Entry: "ui/main.js", SDK: VersionRange{1, 1}, ChatActions: []ChatAction{{ID: "open"}, {ID: "open"}}}} {
			m.Capabilities.UI = u
			Expect(ValidateManifest(m, compatible())).NotTo(Succeed())
		}
	})
	It("accepts the apps surface fields and keeps headless manifests valid", func() {
		b, e := os.ReadFile("../../../tests/addons/fixtures/contracts/app.json")
		Expect(e).NotTo(HaveOccurred())
		m, e := ParseManifest(b)
		Expect(e).NotTo(HaveOccurred())
		Expect(m.LaunchMode).To(Equal("always"))
		u := m.Capabilities.UI
		Expect(u.MainPage).To(Equal("home"))
		Expect(u.Icon).To(Equal("ui/icon.png"))
		Expect(u.Title.ByLanguage).To(Equal(map[string]string{"en": "Sample", "it": "Esempio"}))
		Expect(u.Description.Text).To(Equal("A sample addon."))
		// The canonical wire form is stable: decoding and encoding it again
		// gives the same bytes.
		out, e := json.Marshal(m)
		Expect(e).NotTo(HaveOccurred())
		var compact bytes.Buffer
		Expect(json.Compact(&compact, b)).To(Succeed())
		Expect(string(out)).To(Equal(compact.String()))
		// Every field is optional: a headless manifest and a UI without a
		// main page stay valid, and an explicit launch mode is not required.
		for _, name := range []string{"headless", "service", "page", "chat"} {
			b, e := os.ReadFile("../../../tests/addons/fixtures/contracts/" + name + ".json")
			Expect(e).NotTo(HaveOccurred())
			m, e := ParseManifest(b)
			Expect(e).NotTo(HaveOccurred(), name)
			Expect(m.LaunchMode).To(BeEmpty())
			if m.Capabilities.UI != nil {
				Expect(m.Capabilities.UI.MainPage).To(BeEmpty())
			}
		}
		headless, e := ParseManifest(fixture())
		Expect(e).NotTo(HaveOccurred())
		headless.LaunchMode = "always"
		Expect(ValidateManifest(headless, compatible())).To(Succeed())
	})
	It("rejects unknown and reserved launch modes", func() {
		for _, mode := range []string{"on-demand", "ON-DEMAND", "Always", "never", "manual", " always", "always ", "lazy"} {
			m, _ := ParseManifest(fixture())
			m.LaunchMode = mode
			Expect(ValidateManifest(m, compatible())).NotTo(Succeed(), mode)
			b, _ := json.Marshal(m)
			_, e := ParseManifest(b)
			Expect(e).To(HaveOccurred(), mode)
		}
	})
	It("rejects remote data absolute and traversing icon paths", func() {
		app := func() Manifest {
			b, e := os.ReadFile("../../../tests/addons/fixtures/contracts/app.json")
			Expect(e).NotTo(HaveOccurred())
			m, e := ParseManifest(b)
			Expect(e).NotTo(HaveOccurred())
			return m
		}
		for _, icon := range []string{"http://example.test/i.png", "https://example.test/i.png", "//example.test/i.png", "data:image/png;base64,AAAA", "/etc/icon.png", "../icon.png", "ui/../../icon.png", "ui//icon.png", "./ui/icon.png", `ui\icon.png`, "C:/icon.png", "ui/icon.png?x=1", "ui/icon.png#x", "ui/icon", "ui/icon.svg", "ui/icon.html", "ui/icon.js", "ui/", ".", "..", strings.Repeat("a/", 200) + "icon.png"} {
			m := app()
			m.Capabilities.UI.Icon = icon
			Expect(ValidateManifest(m, compatible())).NotTo(Succeed(), icon)
		}
		// An icon cannot also be the module or a stylesheet.
		for _, icon := range []string{"ui/main.js", "ui/main.css"} {
			m := app()
			m.Capabilities.UI.Icon = icon
			Expect(ValidateManifest(m, compatible())).NotTo(Succeed(), icon)
		}
		for _, icon := range []string{"ui/icon.png", "assets/icon.webp", "icon.jpg", "ui/icon.jpeg"} {
			m := app()
			m.Capabilities.UI.Icon = icon
			Expect(ValidateManifest(m, compatible())).To(Succeed(), icon)
		}
	})
	It("requires the main page to be one of the declared pages", func() {
		app := func() Manifest {
			b, e := os.ReadFile("../../../tests/addons/fixtures/contracts/app.json")
			Expect(e).NotTo(HaveOccurred())
			m, e := ParseManifest(b)
			Expect(e).NotTo(HaveOccurred())
			return m
		}
		for _, page := range []string{"missing", "Home", "../home", "/home", "home/", "home x"} {
			m := app()
			m.Capabilities.UI.MainPage = page
			Expect(ValidateManifest(m, compatible())).NotTo(Succeed(), page)
		}
		// A main page without any declared page is not a route either.
		m := app()
		m.Capabilities.UI.Pages = nil
		m.Capabilities.UI.Navigation = nil
		Expect(ValidateManifest(m, compatible())).NotTo(Succeed())
		m.Capabilities.UI.MainPage = ""
		Expect(ValidateManifest(m, compatible())).To(Succeed())
		// A main page need not be in the navigation.
		m = app()
		m.Capabilities.UI.MainPage = "settings"
		Expect(ValidateManifest(m, compatible())).To(Succeed())
	})
	It("bounds localized titles and descriptions and rejects markup", func() {
		app := func() Manifest {
			b, e := os.ReadFile("../../../tests/addons/fixtures/contracts/app.json")
			Expect(e).NotTo(HaveOccurred())
			m, e := ParseManifest(b)
			Expect(e).NotTo(HaveOccurred())
			return m
		}
		many := map[string]string{}
		for i := 0; i < 40; i++ {
			many[string(rune('a'+i%26))+string(rune('a'+i/26))+"-US"] = "x"
		}
		bad := []*Localized{
			{Text: ""}, {Text: strings.Repeat("a", 281)}, {Text: "<b>Sample</b>"}, {Text: "a > b"}, {Text: "line\nbreak"}, {Text: "bell\a"}, {Text: "bidi \u202e override"}, {Text: "\xff\xfe"},
			{ByLanguage: map[string]string{}}, {ByLanguage: many}, {ByLanguage: map[string]string{"en": ""}}, {ByLanguage: map[string]string{"en": strings.Repeat("a", 281)}},
			{ByLanguage: map[string]string{"": "x"}}, {ByLanguage: map[string]string{"not a tag": "x"}}, {ByLanguage: map[string]string{"en_US": "x"}}, {ByLanguage: map[string]string{"en": "x", "EN": "y"}}, {ByLanguage: map[string]string{"en": "<i>x</i>"}}, {ByLanguage: map[string]string{strings.Repeat("a", 40): "x"}},
		}
		for i, l := range bad {
			m := app()
			m.Capabilities.UI.Title = l
			Expect(ValidateManifest(m, compatible())).NotTo(Succeed(), "title %d", i)
			m = app()
			m.Capabilities.UI.Description = l
			Expect(ValidateManifest(m, compatible())).NotTo(Succeed(), "description %d", i)
		}
		m := app()
		m.Capabilities.UI.Title = &Localized{Text: strings.Repeat("a", 81)}
		Expect(ValidateManifest(m, compatible())).NotTo(Succeed(), "title is limited to 80 characters")
		m.Capabilities.UI.Title = &Localized{ByLanguage: map[string]string{"en": strings.Repeat("a", 81)}}
		Expect(ValidateManifest(m, compatible())).NotTo(Succeed())
		m = app()
		m.Capabilities.UI.Description = &Localized{Text: strings.Repeat("a", 280)}
		Expect(ValidateManifest(m, compatible())).To(Succeed())
		m.Capabilities.UI.Description = &Localized{Text: strings.Repeat("a", 281)}
		Expect(ValidateManifest(m, compatible())).NotTo(Succeed())
		m.Capabilities.UI.Title = &Localized{Text: strings.Repeat("\u00e8", 80)}
		m.Capabilities.UI.Description = nil
		Expect(ValidateManifest(m, compatible())).To(Succeed(), "bounds count characters")
		// Wire shapes: a string and a map decode, anything else is rejected.
		for _, w := range []string{`"title":7`, `"title":["a"]`, `"title":null,"x":1`, `"title":{"en":7}`, `"title":{"en":"a","en":"b"}`} {
			b, _ := os.ReadFile("../../../tests/addons/fixtures/contracts/app.json")
			c := strings.Replace(string(b), `"title": {
        "en": "Sample",
        "it": "Esempio"
      }`, w, 1)
			_, e := ParseManifest([]byte(c))
			Expect(e).To(HaveOccurred(), w)
		}
	})
	It("reserves the segments of the navigation routes", func() {
		Expect(ReservedAddonID("builtin")).To(BeTrue())
		Expect(ReservedAddonID("pins")).To(BeTrue())
		Expect(ValidAddonID("pins")).To(BeFalse())
	})
})
