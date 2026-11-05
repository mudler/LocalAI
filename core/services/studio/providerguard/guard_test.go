package providerguard_test

// Core must not name a shipped add-on. The rules of the add-on architecture are
// that core has generic capabilities and no special case for one add-on: no id
// compared with a string, no title in a sentence that a person or a model
// reads. This guard enforces that on four surfaces: the string literals of the
// Go code of core and pkg, the source of the React UI, the locale files of the
// UI and the documentation pages that describe core.
//
// It is driven by addons/*/manifest.json, so a new add-on is covered the day
// its manifest lands. Two lists excuse a hit, and both are exact:
//
//   - allowed: the name is not the add-on (a third-party service such as the
//     OAuth login, a release source or a token format; an ordinary word).
//   - knownDebt: the name IS a special case that still has to go. Each entry
//     says what removes it. The list may only shrink: a new hit fails, a hit
//     that is fixed makes its entry fail until the entry is deleted, and
//     maxKnownDebt falls with the list.
//
// What is matched (never loosened to hide a hit):
//
//   - the id of an add-on as a whole string literal ("sandbox"), or as the
//     prefix of a tool name ("tables__list");
//   - a declared title as a whole word, case sensitive ("Sandbox", "Short
//     memory"), in every locale the manifest gives.
//
// Only add-ons with a role are matched (see providerguard.Addon.Role). Face,
// voice, talk, studio, fine-tuning and quantization declare no role: each is a
// page and a service that core owns, and its name is an ordinary word of the
// product, so the guard cannot tell the add-on from the feature.

import (
	"strconv"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/providerguard"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// entry excuses the findings of one file whose text holds a substring. A file
// that ends in "/" stands for every file under that folder. For a locale
// file the text is "<key path> = <value>", so a key excuses it in every
// language.
type entry struct{ file, text, why string }

// debt is an entry for a special case that is still in the code.
type debt struct {
	entry
	removedBy string
}

func (e entry) covers(f finding) bool {
	if e.file != f.file && !(strings.HasSuffix(e.file, "/") && strings.HasPrefix(f.file, e.file)) {
		return false
	}
	return strings.Contains(f.text, e.text)
}

const (
	ui      = "core/http/react-ui/src/"
	locales = "core/http/react-ui/public/locales/"
)

var allowed = []entry{
	{ui + "hooks/useToolGroups.js", "memory", "the switch group of the built-in memory"},
	{ui + "utils/agentPresets.js", "'memory'", "the switch group of the built-in memory in the agent presets"},
	{ui + "utils/agentEffective.js", "memory", "the effective view lines of the built-in memory in the agent editor"},
	{ui + "utils/agentTryIt.js", "memory", "the list of what a test chat does not use, which includes the built-in memory"},
	{locales + "en/agents.json", "create.try.skip.memory", "the name of the built-in memory in the list of what a test chat skips"},
	{ui + "utils/memoryState.js", "memory", "the switch group of the built-in memory"},
	// The memory choice of an agent and the notes list belong to the built-in.
	{ui + "utils/memoryFiles.js", "memory", "the source of the built-in notes in the list of context files"},
	{ui + "components/MemorySettings.jsx", "memory", "the provider slot of the built-in memory, drawn by the generic provider chooser"},
	{ui + "pages/AgentCreate.jsx", "memory", "the memory choice of the agent form and the icon of its section: the built-in memory"},
	{"core/http/endpoints/studio/agents.go", "memory", "the memory query of the agent delete: what to do with the notes of the built-in memory"},
	// Memory is built into the host. The add-on of that name is the alias of
	// the built-in: its index files and skills are the host's now, and the
	// host ignores its copies.
	{"core/services/studio/memory.go", `memory`, "the id the retired Memory add-on had; the host serves its index files and skills itself"},
	{"core/services/studio/builtins/builtins.go", `memory`, "the switch group of the built-in memory"},
	{"pkg/addons/providerconformance/conformance.go", `memory`, "the id of the notes slot, which the suite tests; it equals the id of the retired add-on"},
	{"pkg/addons/contract/skills.go", `memory`, "the two memory skills are built in; the retired add-on may still declare them"},

	// Third-party services: the login, the release source, the token issuer.
	{"core/cli/run.go", "GitHub OAuth App", "help of the GitHub OAuth login: GitHub the service, not the add-on"},
	{"core/http/auth/models.go", `github`, "the id of the GitHub OAuth login provider"},
	{ui + "pages/Login.jsx", "github", "the GitHub OAuth login button: the service, not the add-on"},
	{ui + "pages/Login.jsx", "'GitHub'", "the GitHub OAuth login button: the service, not the add-on"},
	{ui + "App.jsx", `name="github"`, "the GitHub glyph of the footer link to the project"},
	{ui + "utils/faIcon.js", "github:", "maps the glyph name of the GitHub brand icon"},
	{locales, "login.signInWithGitHub", "the GitHub OAuth login button"},
	{locales, "footer.github", "the link to the project on GitHub"},
	{locales, "egress.presetHint.registries", "GitHub releases are a package source that commands reach"},
	{"core/services/routing/piipattern/builtins.go", "GitHub access token", "secret pattern: GitHub issues the token"},
	{"core/services/routing/piipattern/builtins.go", "GitHub fine-grained personal access token", "secret pattern: GitHub issues the token"},
	{"core/services/routing/piipattern/builtins.go", "Slack token", "secret pattern: Slack issues the token"},
	{"core/services/routing/piipattern/builtins.go", "JSON Web Token", "the name of a token format"},
	{"pkg/addons/packaging/catalog.go", "GitHub owner/repository", "release signing: GitHub Actions is the build system"},
	{"pkg/addons/packaging/catalog.go", "GitHub Actions OIDC issuer", "release signing: GitHub Actions is the build system"},
	{"pkg/addons/packaging/channel.go", "GitHub event name", "release tooling: GitHub Actions is the build system"},
	{"pkg/addons/packaging/registry.go", "GitHub event name", "release tooling: GitHub Actions is the build system"},
	{"docs/content/reference/binaries.md", "GitHub", "release downloads: the service"},
	{"docs/content/reference/cli-reference.md", "GitHub OAuth", "the options of the GitHub OAuth login"},
	{"docs/content/advanced/private-sources.md", "GitHub", "private gallery sources on the service"},
	{"docs/content/getting-started/linux.md", "GitHub Releases", "release downloads: the service"},
	{"docs/content/getting-started/macos.md", "GitHub releases", "release downloads: the service"},
	{"docs/content/getting-started/models.md", "GitHub", "release downloads: the service"},
	{"docs/content/getting-started/troubleshooting.md", "GitHub", "the issue tracker of the project: the service"},

	// Chat platforms named as examples of what a connector carries. The
	// platform is a third-party service; the add-on is not named as one.
	{"core/cli/run.go", "Telegram, Slack", "help text that gives chat platforms as examples"},
	{"core/http/auth/features.go", "Telegram bots", "the name of a feature permission; the platform is the content"},
	{ui + "components/users/UsersPanel.jsx", "Telegram bots", "the name of a feature permission; the platform is the content"},
	{"core/services/addons/tools/consumers.go", "such as Telegram", "an example of a chat platform in a note for add-on authors"},
	{locales, "mcpPage.family", "chat platforms as examples of messaging connectors"},
	{locales, "connectorsPage.noAddonHint", "a chat platform as an example of what a connector add-on carries"},
	{"docs/content/reference/cli-reference.md", "(Telegram, Slack)", "help text that gives chat platforms as examples"},
	{"docs/content/getting-started/_index.en.md", "Telegram", "a chat platform as an example of where an agent can answer"},
	{"docs/content/getting-started/first-agent.md", "Telegram", "a chat platform as an example of where an agent can answer"},
	{"docs/content/reference/ai-coding-assistants.md", "Discord channel", "the project community server, not the add-on"},
	{"docs/content/getting-started/troubleshooting.md", "LocalAI Discord", "the project community server, not the add-on"},

	// Ordinary words that equal an id or a title.
	{"core/schema/chats.go", `example:"Sandbox"`, "a sample value in the API documentation"},
	{locales + "de/pages.json", "Sie öffnen sich hier in einer Sandbox", "German for the isolated frame of a page: the ordinary noun"},
	{"core/application/studio_service.go", `memory`, "the in-memory kind of the store"},
	{"core/application/studio_store.go", `memory`, "the in-memory kind of the store"},
	{"core/services/studio/nibharness/tools.go", `memory`, "the built-in memory tool of the agent engine"},
	{"pkg/xsysinfo/gpu.go", `memory`, "a GPU memory counter"},
	{"core/http/auth/usage.go", `web`, "usage source of a request from the web UI session"},
	{"core/services/studio/builtins/builtins.go", `web`, "the id of the built-in Web tool group of chats"},
	{ui + "pages/Home.jsx", `data-testid="home-assistant"`, "test id of the LocalAI assistant button on the Home page: the built-in assistant, not the add-on"},
	{"core/services/studio/webtools.go", "Web search", "the label of the built-in web search tool"},
	{"core/services/studio/webtools.go", "Web:", "the built-in Web tool group, the web as a noun"},
	{"pkg/web/search/brave.go", `web`, "the kind of search of the Brave API"},
	{locales, "tools.builtin.web", "the label of the built-in Web tool group"},
	{locales, "settingsTools.labels.web_search", "the label of the built-in web search tool"},
	{locales, "usage.sources.", "the usage source of a request from the web UI"},
	{locales, "sources.title = Web sources", "the web as a kind of source of a collection"},
	{locales, "Im Web gesucht", "the web as a noun"},
	{locales, "Durchsucht das Web", "the web as a noun"},
	{ui + "hooks/useAudioPeaks.js", "Web Audio API", "a browser API"},
	{ui + "hooks/useToolGroups.js", "'web'", "the id of the built-in Web tool group of chats"},
	{ui + "utils/toolLabels.js", "'web'", "reads the word web in the name of a tool"},
	{ui + "utils/traffic.js", "'web'", "usage source of a request from the web UI session"},
	{ui + "utils/chatApps.js", "'web'", "the platform of a chat made in the web UI"},
	{ui + "utils/settingsSchema.js", "'memory'", "the settings group of model memory (eviction, watchdog)"},
	{ui + "utils/settingsSchema.js", "Web ", "the labels of the built-in web tool settings"},
	{ui + "utils/operateStatus.js", "'memory'", "the key of the host memory pressure check"},
	{ui + "pages/OperateOverview.jsx", "'memory'", "the key of the host memory pressure check"},
	{ui + "utils/tools.js", "'memory'", "the id of the memory check of the model tools"},
	{ui + "pages/Settings.jsx", "'memory'", "the settings group of model memory (eviction, watchdog)"},
	{ui + "", `name="memory"`, "the memory glyph"},
	{ui + "", `icon="memory"`, "the memory glyph"},
	{ui + "components/StarterModels.jsx", "'memory'", "the memory glyph"},
	{ui + "addons/sdk.js", "'memory'", "the memory glyph in the list of glyphs that an add-on may use"},
	{ui + "components/home/homeActions.js", "'memory'", "the /memory command opens the collections that agents use as memory"},
	{ui + "pages/Home.jsx", "case 'memory'", "the /memory command opens the collections that agents use as memory"},
	{ui + "components/agents/AgentBits.jsx", "'memory'", "the collections that an agent uses as memory"},
	{ui + "pages/AgentPage.jsx", `"memory"`, "the collections that an agent uses as memory"},
	{"docs/content/advanced/reverse-proxy-tls.md", "Web UI", "the web UI of LocalAI"},
	{"docs/content/getting-started/quickstart.md", "Web Interface", "the web UI of LocalAI"},
	{"docs/content/getting-started/troubleshooting.md", "Web UI", "the web UI of LocalAI"},
	{"docs/content/getting-started/troubleshooting.md", "Web ", "the web UI of LocalAI"},
}

var knownDebt = []debt{
	// propose_change kinds and the Memory/ root: core still knows the Memory
	// add-on through the kind "memory" and the capability that guards it.
	{entry{"core/services/studio/changetools.go", `memory`, "the kind \"memory\" of propose_change names the Memory add-on"},
		"kinds of change declared by the add-ons that handle them (contract change-kind registry)"},
	{entry{"pkg/addons/contract/changeproposal.go", `memory`, "the contract lists the fixed kinds of change, memory first"},
		"kinds of change declared by the add-ons that handle them (contract change-kind registry)"},
	{entry{"pkg/addons/contract/contributions.go", `memory`, "the contract names the Memory root"},
		"context files declared by the add-on with their own root (no fixed Memory/ root in core)"},
	{entry{ui + "utils/changeProposal.js", "'memory'", "the card has wording for the fixed kinds of change"},
		"kinds of change declared by the add-ons that handle them (contract change-kind registry)"},
	{entry{ui + "pages/Account.jsx", "'memory'", "the Account page has a fixed Memory tab"},
		"the Memory add-on gives the memory page through its own page declaration"},
}

// maxKnownDebt is the size of knownDebt now. Lower it when an entry goes; a
// higher number is a new special case that is excused instead of fixed.
const maxKnownDebt = 5

var _ = Describe("the names of the shipped add-ons in core", func() {
	It("reads every add-on from its manifest and marks the ones with a role", func() {
		addons, err := providerguard.Addons()
		Expect(err).NotTo(HaveOccurred())
		role := map[string]bool{}
		for _, a := range addons {
			role[a.ID] = a.Role
		}
		Expect(len(addons)).To(BeNumerically(">=", 10))
		for _, id := range []string{"sandbox", "office", "web", "slack", "telegram", "github", "jellyfin", "tables", "memory"} {
			Expect(role).To(HaveKeyWithValue(id, true), "%s gives the host something to act on", id)
		}
		for _, id := range []string{"face", "voice", "talk", "studio", "fine-tuning", "quantization"} {
			Expect(role).To(HaveKeyWithValue(id, false), "%s is a page and a service of core", id)
		}
	})

	It("matches an id as a whole string and a title as a whole word, case sensitive", func() {
		n := newNamer([]providerguard.Addon{
			{ID: "sandbox", Titles: []string{"Sandbox"}, Role: true},
			{ID: "office", Titles: []string{"Simple Office"}, Role: true},
			{ID: "tables", Titles: []string{"Tables", "Tabelle"}, Role: true},
			{ID: "voice", Titles: []string{"Voice"}},
		})
		for _, s := range []string{"sandbox", "tables__list", "Install the Sandbox", "Open Simple Office.", "Tabelle", "(Sandbox)"} {
			Expect(n.find(s)).NotTo(BeEmpty(), s)
		}
		for _, s := range []string{"curl works in the sandbox", "a Sandboxed frame", "sandbox-runner", "Sandbox-less", "sandboxes", "Voice", "voice", "tables_list", "stables"} {
			Expect(n.find(s)).To(BeEmpty(), s)
		}
		Expect(n.findQuoted(`const id = 'office'`)).To(Equal("office"))
		Expect(n.findQuoted(`const office = 1`)).To(BeEmpty())
	})

	It("keeps the debt small and explicit", func() {
		Expect(len(knownDebt)).To(BeNumerically("<=", maxKnownDebt), "knownDebt may only shrink: fix the hit instead of adding an entry")
		for _, d := range knownDebt {
			Expect(d.why).NotTo(BeEmpty())
			Expect(d.removedBy).NotTo(BeEmpty(), "%s %s: say what removes this entry", d.file, d.text)
		}
		for _, a := range allowed {
			Expect(a.why).NotTo(BeEmpty())
		}
	})

	It("names no add-on in Go strings, UI source, locales and core docs, except the listed ones", func() {
		root, err := providerguard.RepoRoot()
		Expect(err).NotTo(HaveOccurred())
		addons, err := providerguard.Addons()
		Expect(err).NotTo(HaveOccurred())
		n := newNamer(addons)

		var found []finding
		add := func(fs []finding, err error) {
			Expect(err).NotTo(HaveOccurred())
			found = append(found, fs...)
		}
		// The sandbox backend is the code of the Sandbox add-on that lives in
		// core for now; it is skipped as a whole (audit item 1.6).
		add(scanGo(root, n, []string{"core/services/studio/sandbox"}))
		add(scanUI(root, n))
		add(scanLocales(root, n))
		add(scanDocs(root, n, []string{"docs/content/reference", "docs/content/advanced", "docs/content/getting-started"}))

		usedAllowed := make([]bool, len(allowed))
		usedDebt := make([]bool, len(knownDebt))
		var unexcused []string
		for _, f := range found {
			excused := false
			for i, a := range allowed {
				if a.covers(f) {
					usedAllowed[i], excused = true, true
				}
			}
			for i, d := range knownDebt {
				if d.covers(f) {
					usedDebt[i], excused = true, true
				}
			}
			if !excused {
				loc := f.file
				if f.line > 0 {
					loc += ":" + strconv.Itoa(f.line)
				}
				unexcused = append(unexcused, loc+": "+f.name+": "+strconv.Quote(f.text))
			}
		}
		Expect(unexcused).To(BeEmpty(), "core names an add-on: declare it in the manifest and read it, or word it for any provider; if the name is not the add-on, add it to allowed with the reason")
		for i, a := range allowed {
			Expect(usedAllowed[i]).To(BeTrue(), "allowed entry %s %q (%s) matches nothing; remove it", a.file, a.text, a.why)
		}
		for i, d := range knownDebt {
			Expect(usedDebt[i]).To(BeTrue(), "knownDebt entry %s %q is fixed; delete it and lower maxKnownDebt", d.file, d.text)
		}
	})
})
