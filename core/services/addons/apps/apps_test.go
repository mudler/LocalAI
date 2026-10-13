// SPDX-License-Identifier: MIT
package apps

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/core/services/addons/packages"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestApps(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Addon apps") }

const secretDir = "/srv/owner-only/packages/package-abc"

func snapshot(id string, enabled bool, state string, ui *contract.UI, tools ...string) Snapshot {
	v := contract.VersionRange{Min: 1, Max: 1}
	m := contract.Manifest{SchemaVersion: 1, ID: id, Version: "1.2.3", Executable: "bin/x", OS: "linux", Arch: "amd64", Host: v, API: v, Protocol: v, DataFormat: v, Capabilities: contract.Capabilities{Service: true, Tools: tools, UI: ui}}
	r := packages.PackageReceipt{Digest: "sha256:aaaa", SourceID: "main", SourceURL: "https://catalog.example/private", Reference: "oci://registry.example/x", Manifest: m, Directory: secretDir}
	if ui != nil && ui.Icon != "" {
		r.Icon = &packages.PackageIcon{Path: ui.Icon, Digest: "sha256:bbbb", Size: 10, MediaType: "image/png"}
	}
	return Snapshot{AddonID: id, Installed: true, Enabled: enabled, Generation: 3, State: state, Receipt: r}
}

func page(main string) *contract.UI {
	return &contract.UI{Entry: "ui/main.js", SDK: contract.VersionRange{Min: 1, Max: 1}, Pages: []string{"home", "other"}, MainPage: main, Icon: "ui/icon.png",
		Title: &contract.Localized{ByLanguage: map[string]string{"it": "Appunti", "en": "Notes"}}, Description: &contract.Localized{Text: "Keep notes."}}
}

var _ = Describe("apps list", func() {
	It("lists installed enabled addons for users and adds installed disabled ones only for administrators", func() {
		snaps := []Snapshot{
			snapshot("zeta", true, "running", nil, "query"),
			snapshot("alpha", true, "starting", page("home")),
			snapshot("disabled", false, "running", page("home")),
			{AddonID: "gone", Installed: false, Enabled: true, Generation: 2, State: "running", Receipt: snapshot("gone", true, "running", nil).Receipt},
		}
		user := Build(snaps, false)
		Expect(user).To(HaveLen(2))
		Expect([]string{user[0].ID, user[1].ID}).To(Equal([]string{"alpha", "zeta"}), "sorted, enabled only, no uninstalled")
		admin := Build(snaps, true)
		ids := []string{}
		for _, a := range admin {
			ids = append(ids, a.ID)
		}
		Expect(ids).To(Equal([]string{"alpha", "disabled", "zeta"}))
		// A disabled addon is stopped whatever the process observation says.
		Expect(admin[1].State).To(Equal("stopped"))
		Expect(admin[1].Enabled).To(BeFalse())
		Expect(admin[1].MainPage).To(BeEmpty(), "a stopped app has nothing to open")
		Expect(Build(nil, true)).To(BeEmpty())
		Expect(Build(nil, true)).NotTo(BeNil())
	})
	It("derives display fields, the main page and the icon URL from the verified manifest only", func() {
		got := Build([]Snapshot{snapshot("notes", true, "running", page("home"), "add", "list")}, false)
		Expect(got).To(HaveLen(1))
		a := got[0]
		Expect(a.ID).To(Equal("notes"))
		Expect(a.Name).To(Equal("notes"))
		Expect(a.Title).To(Equal("Notes"))
		Expect(a.Titles).To(Equal(map[string]string{"en": "Notes", "it": "Appunti"}))
		Expect(a.Description).To(Equal("Keep notes."))
		Expect(a.Descriptions).To(BeNil())
		Expect(a.Version).To(Equal("1.2.3"))
		Expect(a.Generation).To(Equal(uint64(3)))
		Expect(a.State).To(Equal("running"))
		Expect(a.HasUI).To(BeTrue())
		Expect(a.MainPage).To(Equal("/app/addons/notes/home"))
		Expect(a.Icon).To(Equal("/api/addons/apps/notes/icon?generation=3"))
		Expect(a.LaunchMode).To(Equal("always"))
		Expect(a.Tools).To(Equal([]string{"add", "list"}))
		Expect(a.ToolCount).To(Equal(2))
		Expect(a.Admin).To(BeNil())

		// Without a main page there is no Open path; without UI there is no
		// icon and the app is still listed with its tools.
		a = Build([]Snapshot{snapshot("nav", true, "running", page(""))}, false)[0]
		Expect(a.MainPage).To(BeEmpty())
		Expect(a.HasUI).To(BeTrue())
		a = Build([]Snapshot{snapshot("headless", true, "running", nil, "query")}, false)[0]
		Expect(a.HasUI).To(BeFalse())
		Expect(a.MainPage).To(BeEmpty())
		Expect(a.Icon).To(BeEmpty())
		Expect(a.Title).To(Equal("headless"), "the identifier is the title of last resort")
		Expect(a.Tools).To(Equal([]string{"query"}))
		// A main page that is not a declared page is never offered, even if a
		// damaged receipt carries it.
		a = Build([]Snapshot{snapshot("bad", true, "running", page("nowhere"))}, false)[0]
		Expect(a.MainPage).To(BeEmpty())
		// An icon the receipt did not verify is never linked.
		s := snapshot("noicon", true, "running", page("home"))
		s.Receipt.Icon = nil
		Expect(Build([]Snapshot{s}, false)[0].Icon).To(BeEmpty())
	})
	It("describes the immutable UI assets of an enabled addon by the package digest and never otherwise", Label("assets"), func() {
		hex := strings.Repeat("ab", 32)
		ui := page("home")
		ui.Entry = "ui/main.js"
		ui.CSS = []string{"ui/a b.css"}
		ui.Navigation = []string{"home", "other"}
		ui.ChatActions = []contract.ChatAction{{ID: "summarize"}}
		s := snapshot("notes", true, "running", ui, "add")
		s.Receipt.Digest = "sha256:" + hex
		a := Build([]Snapshot{s}, false)[0]
		Expect(a.UI).NotTo(BeNil())
		Expect(a.UI.Entry).To(Equal("/api/addons/notes/assets/" + hex + "/ui/main.js"))
		Expect(a.UI.CSS).To(Equal([]string{"/api/addons/notes/assets/" + hex + "/ui/a%20b.css"}))
		Expect(a.UI.SDK).To(Equal(contract.VersionRange{Min: 1, Max: 1}))
		Expect(a.UI.Pages).To(Equal([]string{"home", "other"}))
		Expect(a.UI.Navigation).To(Equal([]string{"home", "other"}))
		Expect(a.UI.ChatActions).To(Equal([]string{"summarize"}))
		b, e := json.Marshal(a)
		Expect(e).NotTo(HaveOccurred())
		Expect(string(b)).NotTo(ContainSubstring("sha256:"), "only the bare hex is part of the URL")
		Expect(string(b)).NotTo(ContainSubstring(secretDir))

		// A disabled addon has no UI for anyone, administrators included.
		off := s
		off.Enabled = false
		Expect(Build([]Snapshot{off}, true)[0].UI).To(BeNil())
		// A headless addon and an addon whose digest is not a sha256 have none.
		Expect(Build([]Snapshot{snapshot("headless", true, "running", nil, "q")}, false)[0].UI).To(BeNil())
		for _, digest := range []string{"", "sha256:aaaa", "md5:" + hex, "sha256:" + strings.ToUpper(hex), hex} {
			bad := s
			bad.Receipt.Digest = digest
			Expect(Build([]Snapshot{bad}, false)[0].UI).To(BeNil(), digest)
		}
		// A failed process still lists its UI so the shell can say why it is unavailable.
		failed := s
		failed.State = "failed"
		Expect(Build([]Snapshot{failed}, false)[0].UI).NotTo(BeNil())
	})
	It("bounds the tool summary and coerces unknown process states to failed", func() {
		tools := []string{}
		for i := 0; i < MaxTools+10; i++ {
			tools = append(tools, fmt.Sprintf("tool-%02d", i))
		}
		a := Build([]Snapshot{snapshot("big", true, "running", nil, tools...)}, false)[0]
		Expect(a.Tools).To(HaveLen(MaxTools))
		Expect(a.ToolCount).To(Equal(MaxTools + 10))
		for _, state := range []string{"", "running-ish", "update-pending", "RUNNING"} {
			Expect(Build([]Snapshot{snapshot("x", true, state, nil, "q")}, false)[0].State).To(Equal("failed"), state)
		}
		for _, state := range []string{"running", "starting", "failed", "stopped"} {
			Expect(Build([]Snapshot{snapshot("x", true, state, nil, "q")}, false)[0].State).To(Equal(state))
		}
	})
	It("shows controls and the management link only to administrators and never leaks owner details", func() {
		snaps := []Snapshot{snapshot("on", true, "failed", page("home"), "q"), snapshot("off", false, "stopped", nil, "q")}
		for _, a := range Build(snaps, false) {
			b, e := json.Marshal(a)
			Expect(e).NotTo(HaveOccurred())
			for _, forbidden := range []string{"admin", "canStart", "canStop", "managementUrl", "/app/addons?manage"} {
				Expect(string(b)).NotTo(ContainSubstring(forbidden), forbidden)
			}
		}
		byID := map[string]App{}
		for _, a := range Build(snaps, true) {
			byID[a.ID] = a
		}
		Expect(byID["on"].Admin).To(Equal(&AdminView{CanStart: false, CanStop: true, ManagementURL: "/app/addons?manage=on"}))
		Expect(byID["off"].Admin).To(Equal(&AdminView{CanStart: true, CanStop: false, ManagementURL: "/app/addons?manage=off"}))
		// Nothing of the package location, source or addon endpoint is emitted.
		b, e := json.Marshal(Build(snaps, true))
		Expect(e).NotTo(HaveOccurred())
		for _, secret := range []string{secretDir, "catalog.example", "registry.example", "sha256:aaaa", "127.0.0.1", "endpoint"} {
			Expect(strings.Contains(string(b), secret)).To(BeFalse(), secret)
		}
	})
})
