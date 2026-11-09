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
	{ui + "utils/agentPresets.js", "'memory'", "the switch group of the built-in memory in the agent presets"},
	// The word mail in plain text: an example of what a run reads and the
	// envelope glyph of an empty list. Neither names the Mail add-on.
	{"core/services/studio/triggertools.go", "Mail from the last 24 hours", "an example of what a scheduled run reads"},
	{ui + "components/users/InvitesPanel.jsx", `name="mail"`, "the envelope glyph of the empty invitations list"},

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
	{"docs/content/models/private-sources.md", "GitHub", "private gallery sources on the service"},
	{"docs/content/start/install/linux.md", "GitHub Releases", "release downloads: the service"},
	{"docs/content/start/install/macos.md", "GitHub releases", "release downloads: the service"},
	{"docs/content/models/setting-up-models.md", "GitHub", "release downloads: the service"},
	{"docs/content/administration/troubleshooting.md", "GitHub", "the issue tracker of the project: the service"},

	// Chat platforms named as examples of what a connector carries. The
	// platform is a third-party service; the add-on is not named as one.
	{"core/cli/run.go", "Telegram, Slack", "help text that gives chat platforms as examples"},
	{"core/http/auth/features.go", "Telegram bots", "the name of a feature permission; the platform is the content"},
	{ui + "components/users/UsersPanel.jsx", "Telegram bots", "the name of a feature permission; the platform is the content"},
	{"core/services/addons/tools/consumers.go", "such as Telegram", "an example of a chat platform in a note for add-on authors"},
	{locales, "connectorsPage.noAddonHint", "a chat platform as an example of what a connector add-on carries"},
	{"docs/content/reference/cli-reference.md", "(Telegram, Slack)", "help text that gives chat platforms as examples"},
	{"docs/content/start/first-agent.md", "Telegram", "a chat platform as an example of where an agent can answer"},
	{"docs/content/contributing/ai-coding-assistants.md", "Discord channel", "the project community server, not the add-on"},
	{"docs/content/administration/troubleshooting.md", "LocalAI Discord", "the project community server, not the add-on"},

	// The profiles of the environment that runs commands. Any add-on that
	// runs commands can declare them; "sandbox" here is the kind of
	// environment, not the add-on.
	{"core/services/studio/skills/parse.go", `sandbox`, "the frontmatter key a skill uses to name the profile of the environment that runs its commands"},
	{locales, "sandboxProfile.", "the name of the profile feature of the environment that runs commands"},
	{locales, "create.sandboxProfile", "the name of the profile feature of the environment that runs commands"},

	// Ordinary words that equal an id or a title.
	{"core/schema/chats.go", `example:"Sandbox"`, "a sample value in the API documentation"},
	{locales + "de/pages.json", "Sie öffnen sich hier in einer Sandbox", "German for the isolated frame of a page: the ordinary noun"},
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
	{ui + "utils/settingsSchema.js", "Web ", "the labels of the built-in web tool settings"},
	{"docs/content/administration/reverse-proxy-tls.md", "Web UI", "the web UI of LocalAI"},
	{"docs/content/start/quickstart.md", "Web Interface", "the web UI of LocalAI"},
	{"docs/content/administration/troubleshooting.md", "Web UI", "the web UI of LocalAI"},
	{"docs/content/administration/troubleshooting.md", "Web ", "the web UI of LocalAI"},
}

var knownDebt = []debt{}

// maxKnownDebt is the size of knownDebt now. Lower it when an entry goes; a
// higher number is a new special case that is excused instead of fixed.
const maxKnownDebt = 0

// coreDocs are the docs pages that describe LocalAI itself, not an add-on. They
// were the reference, advanced and getting-started folders before the docs were
// grouped by task, so the list names each page.
var coreDocs = []string{
	"docs/content/administration/embedded-routing-log.md",
	"docs/content/administration/p2p-api.md",
	"docs/content/administration/reverse-proxy-tls.md",
	"docs/content/administration/troubleshooting.md",
	"docs/content/api/api-errors.md",
	"docs/content/concepts/architecture.md",
	"docs/content/contributing/ai-coding-assistants.md",
	"docs/content/contributing/build-from-source.md",
	"docs/content/models/advanced-usage.md",
	"docs/content/models/compatibility-table.md",
	"docs/content/models/model-configuration.md",
	"docs/content/models/private-sources.md",
	"docs/content/models/setting-up-models.md",
	"docs/content/models/vram-management.md",
	"docs/content/reference/_index.md",
	"docs/content/reference/binaries.md",
	"docs/content/reference/cli-reference.md",
	"docs/content/reference/runtime-errors.md",
	"docs/content/reference/shell-completion.md",
	"docs/content/reference/system-info.md",
	"docs/content/start/_index.md",
	"docs/content/start/customize-model.md",
	"docs/content/start/first-agent.md",
	"docs/content/start/install/_index.md",
	"docs/content/start/install/containers.md",
	"docs/content/start/install/docker.md",
	"docs/content/start/install/kubernetes.md",
	"docs/content/start/install/linux.md",
	"docs/content/start/install/macos.md",
	"docs/content/start/install/nvidia-l4t.md",
	"docs/content/start/quickstart.md",
	"docs/content/start/try-it-out.md",
}

var _ = Describe("the names of the shipped add-ons in core", func() {
	It("reads every add-on from its manifest and marks the ones with a role", func() {
		addons, err := providerguard.Addons()
		Expect(err).NotTo(HaveOccurred())
		role := map[string]bool{}
		for _, a := range addons {
			role[a.ID] = a.Role
		}
		Expect(len(addons)).To(BeNumerically(">=", 10))
		for _, id := range []string{"sandbox", "office", "web", "slack", "telegram", "github", "jellyfin", "tables"} {
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
		add(scanDocs(root, n, coreDocs))

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
