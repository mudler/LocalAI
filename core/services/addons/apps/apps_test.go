// SPDX-License-Identifier: MIT
package apps

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/core/services/addons/packages"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestApps(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Addon apps") }

const secretDir = "/srv/owner-only/packages/package-abc"

func snapshot(id string, enabled bool, state string, ui *contract.UI, tools ...string) Snapshot {
	v := contract.VersionRange{Min: 1, Max: 1}
	m := contract.Manifest{SchemaVersion: 1, ID: id, Version: "1.2.3", Executable: "bin/x", OS: "linux", Arch: "amd64", Host: v, API: contract.VersionRange{Min: 2, Max: 2}, Protocol: v, DataFormat: v, Capabilities: contract.Capabilities{Service: true, Tools: tools, UI: ui}}
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
	It("carries the install time to the list and the management view, and none for an old record", func() {
		s := snapshot("notes", true, "running", nil, "add")
		Expect(Build([]Snapshot{s}, false)[0].InstalledAt).To(BeEmpty())
		Expect(BuildInstalled([]Snapshot{s})[0].InstalledAt).To(BeEmpty())
		s.InstalledAt = "2026-10-01T10:00:00Z"
		Expect(Build([]Snapshot{s}, false)[0].InstalledAt).To(Equal("2026-10-01T10:00:00Z"))
		Expect(BuildInstalled([]Snapshot{s})[0].InstalledAt).To(Equal("2026-10-01T10:00:00Z"))
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
		Expect(byID["on"].Admin).To(Equal(&AdminView{CanStart: false, CanStop: true, ManagementURL: "/app/addons?manage=on", SourceID: "main"}))
		Expect(byID["off"].Admin).To(Equal(&AdminView{CanStart: true, CanStop: false, ManagementURL: "/app/addons?manage=off", SourceID: "main"}))
		// Nothing of the package location, the address of its library or the
		// addon endpoint is emitted; an administrator gets the ID of the
		// library, as the list of installed add-ons gives it.
		b, e := json.Marshal(Build(snaps, true))
		Expect(e).NotTo(HaveOccurred())
		for _, secret := range []string{secretDir, "catalog.example", "registry.example", "sha256:aaaa", "127.0.0.1", "endpoint"} {
			Expect(strings.Contains(string(b), secret)).To(BeFalse(), secret)
		}
	})
	It("names the jobs tool of an execution provider, and none for an add-on without one", func() {
		s := snapshot("sandbox", true, "running", nil, "bash", "status", "job")
		s.Receipt.Manifest.Capabilities.ExecutionProvider = &contract.ExecutionProvider{Tools: []string{"bash"}, StatusTool: "status", Jobs: &contract.ExecutionJobs{Tool: "job"}}
		old := snapshot("old", true, "running", nil, "bash")
		list := Build([]Snapshot{s, old}, false)
		Expect(list[0].ID).To(Equal("old"))
		Expect(list[0].JobsTool).To(BeEmpty())
		Expect(list[1].JobsTool).To(Equal("job"))
	})
	It("lists the host controls an add-on is entitled to, whatever its ID", func() {
		bash := contract.ToolDefinition{Name: "bash", Description: "Run.", Kind: contract.ToolKindAction, Shadows: "bash",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"],"additionalProperties":false}`)}
		runner := func(id string, grants ...string) Snapshot {
			ui := page("home")
			ui.Controls = []string{contract.ControlNetworkPolicy, contract.ControlToolProviders}
			s := snapshot(id, true, "running", ui, "bash")
			s.Receipt.Manifest.Capabilities.ToolDefinitions = []contract.ToolDefinition{bash}
			s.Receipt.Manifest.Capabilities.Network = &contract.NetworkCapability{Kind: contract.NetworkKindEgressCheck}
			s.HostOps = grants
			s.Receipt.Digest = "sha256:" + strings.Repeat("a", 64)
			return s
		}
		list := Build([]Snapshot{runner("sandbox", "workspace.", "egress."), runner("devbox", "egress."), runner("plain")}, false)
		Expect(list).To(HaveLen(3))
		byID := map[string]App{}
		for _, a := range list {
			byID[a.ID] = a
		}
		Expect(byID["sandbox"].UI.Controls).To(Equal([]string{"network-policy", "tool-providers"}))
		Expect(byID["devbox"].UI.Controls).To(Equal([]string{"network-policy", "tool-providers"}), "a second add-on reuses the control with its grant")
		Expect(byID["plain"].UI.Controls).To(Equal([]string{"tool-providers"}), "no grant, no network policy")
		b, err := json.Marshal(byID["devbox"])
		Expect(err).NotTo(HaveOccurred())
		Expect(string(b)).To(ContainSubstring(`"controls":["network-policy","tool-providers"]`))
		// A manifest without controls lists none.
		notes := snapshot("notes", true, "running", page("home"))
		notes.Receipt.Digest = "sha256:" + strings.Repeat("a", 64)
		Expect(Build([]Snapshot{notes}, false)[0].UI.Controls).To(BeNil())
	})
	It("names the scopes of an OAuth login, and nothing of a token slot", func() {
		creds := func(id string, slot contract.CredentialSlot) Snapshot {
			s := snapshot(id, true, "running", nil, "q")
			s.Receipt.Manifest.Capabilities.Credentials = []contract.CredentialSlot{slot}
			return s
		}
		login := contract.CredentialSlot{ID: "login", Title: "Login", Kind: contract.CredentialKindOAuth2, OAuth: &contract.OAuthDescriptor{
			AuthorizeURL: "https://auth.example.test/authorize", TokenURL: "https://auth.example.test/token", Hosts: []string{"auth.example.test"},
			Scopes: contract.OAuthScopes{Required: []string{"read"}, Optional: []string{"write", "admin"}}, PKCE: contract.OAuthPKCES256, ClientAuth: contract.OAuthClientAuthBasic}}
		bare := login
		bare.OAuth = &contract.OAuthDescriptor{AuthorizeURL: login.OAuth.AuthorizeURL, TokenURL: login.OAuth.TokenURL, Hosts: login.OAuth.Hosts, PKCE: contract.OAuthPKCES256, ClientAuth: contract.OAuthClientAuthNone}
		byID := map[string]App{}
		for _, a := range Build([]Snapshot{creds("providera", login), creds("bare", bare), creds("tokens", contract.CredentialSlot{ID: "token", Title: "Token", Kind: contract.CredentialKindToken})}, false) {
			byID[a.ID] = a
		}
		Expect(byID["providera"].OAuth).To(Equal(&OAuthView{RequiredScopes: []string{"read"}, OptionalScopes: []string{"write", "admin"}}))
		Expect(byID["tokens"].OAuth).To(BeNil())
		Expect(byID["tokens"].Credentialed).To(BeTrue())
		b, err := json.Marshal(byID["bare"])
		Expect(err).NotTo(HaveOccurred())
		Expect(string(b)).To(ContainSubstring(`"oauth":{"requiredScopes":[],"optionalScopes":[]}`))
		Expect(string(b)).NotTo(ContainSubstring("example.test"), "the addresses of the login are for the administrator's client read")
	})
	It("tells an administrator whether an add-on comes from the official library, and nobody else", func() {
		from := func(id, source, url, verification string) Snapshot {
			s := snapshot(id, true, "running", nil, "q")
			s.Receipt.SourceID, s.Receipt.SourceURL, s.Receipt.Verification = source, url, verification
			return s
		}
		snaps := []Snapshot{
			from("signed", catalog.OfficialLibraryID, catalog.OfficialLibraryURL, "sha256+sigstore"),
			from("named", catalog.OfficialLibraryID, "https://mirror.example.test/index.yaml", "sha256+sigstore"),
			from("unsigned", catalog.OfficialLibraryID, catalog.OfficialLibraryURL, "sha256"),
			from("other", "community", "https://community.example.test/index.yaml", "sha256+sigstore"),
		}
		byID := map[string]App{}
		for _, a := range Build(snaps, true) {
			byID[a.ID] = a
		}
		Expect(byID["signed"].Admin.Official).To(BeTrue())
		Expect(byID["signed"].Admin.SourceID).To(Equal(catalog.OfficialLibraryID))
		for _, id := range []string{"named", "unsigned", "other"} {
			Expect(byID[id].Admin.Official).To(BeFalse(), id)
		}
		Expect(byID["other"].Admin.SourceID).To(Equal("community"))
		for _, a := range Build(snaps, false) {
			Expect(a.Admin).To(BeNil())
		}
	})
	It("draws the network policy for a declared capability, with the declared title, and for no other add-on", func() {
		declared := func(id, title string, grants ...string) Snapshot {
			ui := page("home")
			ui.Controls = []string{contract.ControlNetworkPolicy}
			s := snapshot(id, true, "running", ui, "q")
			s.Receipt.Manifest.Capabilities.Network = &contract.NetworkCapability{Kind: contract.NetworkKindEgressCheck, Title: title,
				Presets: []contract.NetworkPreset{{ID: "off", Title: "Closed"}}}
			s.HostOps = grants
			s.Receipt.Digest = "sha256:" + strings.Repeat("a", 64)
			return s
		}
		byID := map[string]App{}
		for _, a := range Build([]Snapshot{declared("runbox", "Runbox network", "egress."), declared("boxy", "", "egress."), declared("nogrant", "Nope")}, false) {
			byID[a.ID] = a
		}
		Expect(byID["runbox"].UI.Controls).To(Equal([]string{"network-policy"}), "no tool shadows bash: the declaration attaches the control")
		Expect(byID["runbox"].Network).To(Equal(&NetworkView{Kind: contract.NetworkKindEgressCheck, Title: "Runbox network", Presets: []contract.NetworkPreset{{ID: "off", Title: "Closed"}}}))
		Expect(byID["boxy"].Network.Title).To(Equal(byID["boxy"].Title), "the add-on title is the fallback")
		Expect(byID["nogrant"].Network).To(BeNil(), "no grant, no network")
		b, err := json.Marshal(byID["runbox"])
		Expect(err).NotTo(HaveOccurred())
		Expect(string(b)).To(ContainSubstring(`"network":{"kind":"egress-check","title":"Runbox network"`))
	})
})
