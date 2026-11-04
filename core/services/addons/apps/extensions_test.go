// SPDX-License-Identifier: MIT
package apps

import (
	"encoding/json"
	"strings"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("typed home metadata", func() {
	It("projects home commands with resolved declared pages display metadata immutable icons and generation", func() {
		u := page("home")
		u.HomeCommands = []contract.HomeCommand{{ID: "open", Alias: "notes"}, {ID: "history", Page: "other"}, {ID: "custom", Page: "other", Label: &contract.Localized{ByLanguage: map[string]string{"en": "History", "it": "Cronologia"}}, Icon: "clock"}, {ID: "packaged", Icon: u.Icon}}
		a := Build([]Snapshot{homeSnapshot(u)}, false)[0]
		Expect(a.Generation).To(Equal(uint64(3)))
		b, e := json.Marshal(a.UI)
		Expect(e).NotTo(HaveOccurred())
		var wire struct {
			HomeCommands []struct {
				ID, Alias, Page, Label, Icon string
				Labels                       map[string]string
			} `json:"homeCommands"`
		}
		Expect(json.Unmarshal(b, &wire)).To(Succeed())
		Expect(wire.HomeCommands).To(HaveLen(4))
		commands := wire.HomeCommands
		Expect(commands[0].ID).To(Equal("open"))
		Expect(commands[0].Alias).To(Equal("notes"))
		Expect(commands[0].Page).To(Equal("home"))
		Expect(commands[0].Label).To(Equal("Notes"))
		Expect(commands[0].Labels).To(Equal(u.Title.ByLanguage))
		Expect(commands[0].Icon).To(Equal("/api/addons/notes/assets/" + strings.Repeat("a", 64) + "/ui/icon.png"))
		Expect(commands[1].Page).To(Equal("other"))
		Expect(commands[1].Label).To(Equal("Notes — other"))
		Expect(commands[2].Label).To(Equal("History"))
		Expect(commands[2].Labels["it"]).To(Equal("Cronologia"))
		Expect(commands[2].Icon).To(Equal("clock"))
		Expect(commands[3].Icon).To(Equal(commands[0].Icon))
		Expect(string(b)).NotTo(ContainSubstring(secretDir))
	})
	It("keeps disabled home apps installed without UI and never invents a destination", func() {
		u := page("home")
		u.HomeCommands = []contract.HomeCommand{{ID: "open"}}
		s := snapshot("notes", false, "running", u)
		Expect(BuildInstalled([]Snapshot{s})).To(HaveLen(1))
		a := Build([]Snapshot{s}, true)[0]
		Expect(a.UI).To(BeNil())
		Expect(a.Generation).To(Equal(uint64(3)))
		s.Receipt.Digest = "sha256:" + strings.Repeat("a", 64)
		s.Enabled = true
		u.MainPage = "missing"
		u.HomeCommands = append(u.HomeCommands, contract.HomeCommand{ID: "bad", Page: "missing"})
		b, e := json.Marshal(Build([]Snapshot{s}, false)[0].UI)
		Expect(e).NotTo(HaveOccurred())
		Expect(string(b)).NotTo(ContainSubstring("homeCommands"))
		u.MainPage = "home"
		u.Icon = ""
		u.Title = nil
		u.HomeCommands = []contract.HomeCommand{{ID: "open"}}
		b, e = json.Marshal(Build([]Snapshot{s}, false)[0].UI)
		Expect(e).NotTo(HaveOccurred())
		Expect(string(b)).To(ContainSubstring(`"label":"notes"`))
		Expect(string(b)).To(ContainSubstring(`"icon":"puzzle"`))
	})
})

func homeSnapshot(u *contract.UI) Snapshot {
	s := snapshot("notes", true, "running", u)
	s.Receipt.Digest = "sha256:" + strings.Repeat("a", 64)
	return s
}

var _ = Describe("generic Home contributions", func() {
	It("projects two non-Studio addon pages without identity dispatch", func() {
		for _, id := range []string{"paint", "notebook"} {
			u := page("canvas")
			u.Pages = []string{"canvas", "other"}
			u.HomeCommands = []contract.HomeCommand{{ID: "open"}, {ID: "past", Page: "other"}}
			s := snapshot(id, true, "running", u)
			s.Receipt.Digest = "sha256:" + strings.Repeat("a", 64)
			a := Build([]Snapshot{s}, false)[0]
			Expect(a.ID).To(Equal(id))
			Expect(a.UI.HomeCommands).To(HaveLen(2))
			Expect(a.UI.HomeCommands[0].Page).To(Equal("canvas"))
			Expect(a.UI.HomeCommands[1].Page).To(Equal("other"))
		}
	})
})
