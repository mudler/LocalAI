package projects_test

import (
	"encoding/json"
	"math/rand"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/projects"
)

func validManifestJSON() []byte {
	return []byte(`{
  "version": 1,
  "id": "p-1",
  "name": "Client work",
  "description": "Notes for the client",
  "defaults": {
    "skills": ["review"],
    "addons": ["sandbox"],
    "connectors": ["github"],
    "mcpServers": ["docs"],
    "collections": ["handbook"]
  }
}`)
}

var _ = Describe("Manifest", func() {
	Describe("ParseManifest", func() {
		It("accepts a v1 manifest", func() {
			m, err := projects.ParseManifest(validManifestJSON())
			Expect(err).ToNot(HaveOccurred())
			Expect(m.Version).To(Equal(projects.ManifestVersion))
			Expect(m.ID).To(Equal("p-1"))
			Expect(m.Name).To(Equal("Client work"))
			Expect(m.Description).To(Equal("Notes for the client"))
			Expect(m.Defaults.Skills).To(Equal([]string{"review"}))
			Expect(m.Defaults.Addons).To(Equal([]string{"sandbox"}))
			Expect(m.Defaults.Connectors).To(Equal([]string{"github"}))
			Expect(m.Defaults.MCPServers).To(Equal([]string{"docs"}))
			Expect(m.Defaults.Collections).To(Equal([]string{"handbook"}))
		})

		It("round-trips through Marshal", func() {
			m, err := projects.ParseManifest(validManifestJSON())
			Expect(err).ToNot(HaveOccurred())

			again, err := projects.ParseManifest(m.Marshal())
			Expect(err).ToNot(HaveOccurred())
			Expect(again).To(Equal(m))
		})

		It("keeps unknown top-level fields byte for byte through a rewrite", func() {
			b := []byte(`{"version": 1, "name": "x", "zeta": [1,  2], "futureField": {"a":1}, "alpha": null}`)
			m, err := projects.ParseManifest(b)
			Expect(err).ToNot(HaveOccurred())
			m.Name = "renamed"
			out := m.Marshal()
			Expect(string(out)).To(ContainSubstring(`"futureField": {"a":1}`))
			Expect(string(out)).To(ContainSubstring(`"zeta": [1,  2]`))
			Expect(string(out)).To(ContainSubstring(`"alpha": null`))
			Expect(strings.Index(string(out), `"alpha"`)).To(BeNumerically("<", strings.Index(string(out), `"futureField"`)), "sorted by key")
			Expect(strings.Index(string(out), `"futureField"`)).To(BeNumerically("<", strings.Index(string(out), `"zeta"`)))
			Expect(strings.Index(string(out), `"defaults"`)).To(BeNumerically("<", strings.Index(string(out), `"alpha"`)), "after the known keys")
			again, err := projects.ParseManifest(out)
			Expect(err).ToNot(HaveOccurred())
			Expect(again.Name).To(Equal("renamed"))
			Expect(string(again.Marshal())).To(Equal(string(out)), "stable")
		})

		It("does not emit a known key twice when a differently cased copy exists", func() {
			m, err := projects.ParseManifest([]byte(`{"version": 1, "name": "x", "NAME": "y"}`))
			Expect(err).ToNot(HaveOccurred())
			Expect(strings.Count(strings.ToLower(string(m.Marshal())), `"name"`)).To(Equal(1))
		})

		It("ignores unknown fields for forward compatibility", func() {
			b := []byte(`{"version": 1, "name": "x", "future": {"a": 1}}`)
			m, err := projects.ParseManifest(b)
			Expect(err).ToNot(HaveOccurred())
			Expect(m.Name).To(Equal("x"))
		})

		It("accepts a name of exactly 120 characters", func() {
			b := []byte(`{"version": 1, "name": "` + strings.Repeat("n", 120) + `"}`)
			_, err := projects.ParseManifest(b)
			Expect(err).ToNot(HaveOccurred())
		})

		It("accepts a manifest with no ID, so the caller can assign one", func() {
			m, err := projects.ParseManifest([]byte(`{"version": 1, "name": "x"}`))
			Expect(err).ToNot(HaveOccurred())
			Expect(m.ID).To(BeEmpty())
		})

		It("accepts and ignores a sharedWith list, and never writes it back", func() {
			m, err := projects.ParseManifest([]byte(`{"version": 1, "name": "x", "sharedWith": ["someone"]}`))
			Expect(err).ToNot(HaveOccurred())
			Expect(m.SharedWith).To(BeEmpty())
			Expect(strings.ToLower(string(m.Marshal()))).NotTo(ContainSubstring("sharedwith"))
			Expect(strings.ToLower(string(m.Marshal()))).NotTo(ContainSubstring("someone"))
		})

		It("accepts a safe ID", func() {
			m, err := projects.ParseManifest([]byte(`{"version": 1, "name": "x", "id": "p-1"}`))
			Expect(err).ToNot(HaveOccurred())
			Expect(m.ID).To(Equal("p-1"))
		})

		DescribeTable("rejects a non-empty ID that is not IDSafe",
			func(id string) {
				b, err := json.Marshal(map[string]any{"version": 1, "name": "x", "id": id})
				Expect(err).ToNot(HaveOccurred())
				_, err = projects.ParseManifest(b)
				Expect(err).To(MatchError(projects.ErrInvalid))
			},
			Entry("path traversal", "../x"),
			Entry("slash", "a/b"),
			Entry("embedded NUL", "a\x00b"),
			Entry("trailing newline", "p-1\n"),
		)

		It("accepts exactly 64 entries in a list", func() {
			_, err := projects.ParseManifest(manifestWithSkills(64))
			Expect(err).ToNot(HaveOccurred())
		})

		It("reads and writes the attached lists, and reads a manifest without them as empty", func() {
			m, err := projects.ParseManifest([]byte(`{"version": 1, "name": "x", "attached": {"skills": ["s"], "mcpServers": ["m"], "connectors": ["c"]}}`))
			Expect(err).ToNot(HaveOccurred())
			Expect(m.Attached.Skills).To(Equal([]string{"s"}))
			Expect(m.Attached.MCPServers).To(Equal([]string{"m"}))
			Expect(m.Attached.Connectors).To(Equal([]string{"c"}))
			again, err := projects.ParseManifest(m.Marshal())
			Expect(err).ToNot(HaveOccurred())
			Expect(again).To(Equal(m))
			Expect(m.Extra).To(BeEmpty(), "attached is a known key")
			old, err := projects.ParseManifest([]byte(`{"version": 1, "name": "x"}`))
			Expect(err).ToNot(HaveOccurred())
			Expect(old.Attached).To(Equal(projects.Defaults{}))
			Expect(string(old.Marshal())).NotTo(ContainSubstring("attached"), "a project with nothing attached writes no key")
		})

		It("accepts exactly 64 attached entries and rejects 65 in each list", func() {
			for _, field := range []string{"skills", "addons", "connectors", "mcpServers", "collections"} {
				mk := func(n int) []byte {
					list := make([]string, n)
					for i := range list {
						list[i] = "e"
					}
					b, _ := json.Marshal(map[string]any{"version": 1, "name": "x", "attached": map[string]any{field: list}})
					return b
				}
				_, err := projects.ParseManifest(mk(64))
				Expect(err).ToNot(HaveOccurred(), field)
				_, err = projects.ParseManifest(mk(65))
				Expect(err).To(MatchError(projects.ErrInvalid), field)
				Expect(err.Error()).To(ContainSubstring("attached." + field))
			}
		})

		DescribeTable("rejects invalid manifests with ErrInvalid",
			func(raw string) {
				_, err := projects.ParseManifest([]byte(raw))
				Expect(err).To(MatchError(projects.ErrInvalid))
			},
			Entry("version 2", `{"version": 2, "name": "x"}`),
			Entry("missing version", `{"name": "x"}`),
			Entry("empty name", `{"version": 1, "name": ""}`),
			Entry("whitespace-only name", `{"version": 1, "name": "   "}`),
			Entry("name over 120 characters", `{"version": 1, "name": "`+strings.Repeat("n", 121)+`"}`),
			Entry("trailing garbage", `{"version": 1, "name": "x"} trailing`),
			Entry("not JSON", `version: 1`),
			Entry("empty input", ``),
		)

		It("rejects a list with 65 entries and names the field", func() {
			_, err := projects.ParseManifest(manifestWithSkills(65))
			Expect(err).To(MatchError(projects.ErrInvalid))
			Expect(err.Error()).To(ContainSubstring("defaults.skills"))
		})
	})

	Describe("Marshal", func() {
		It("writes indented JSON that ends with a newline", func() {
			m := projects.Manifest{Version: projects.ManifestVersion, Name: "x"}
			out := string(m.Marshal())
			Expect(out).To(HavePrefix("{\n  "))
			Expect(out).To(HaveSuffix("}\n"))
			Expect(json.Valid([]byte(out))).To(BeTrue())
		})

		It("is stable across calls", func() {
			m, err := projects.ParseManifest(validManifestJSON())
			Expect(err).ToNot(HaveOccurred())
			Expect(m.Marshal()).To(Equal(m.Marshal()))
		})

		It("omits archived when false and writes it when true", func() {
			m := projects.Manifest{Version: projects.ManifestVersion, Name: "x"}
			Expect(string(m.Marshal())).ToNot(ContainSubstring("archived"))

			m.Archived = true
			Expect(string(m.Marshal())).To(ContainSubstring(`"archived": true`))
		})
	})

	Describe("IDSafe", func() {
		It("accepts letters, digits, dash and underscore up to 64 characters", func() {
			Expect(projects.IDSafe("abc_DEF-123")).To(BeTrue())
			Expect(projects.IDSafe(strings.Repeat("a", 64))).To(BeTrue())
		})

		It("rejects path-like, empty and overlong values", func() {
			Expect(projects.IDSafe("../x")).To(BeFalse())
			Expect(projects.IDSafe("a/b")).To(BeFalse())
			Expect(projects.IDSafe("")).To(BeFalse())
			Expect(projects.IDSafe(strings.Repeat("a", 65))).To(BeFalse())
		})
	})

	Describe("NewID", func() {
		It("always produces an IDSafe value", func() {
			seen := map[string]bool{}
			for range 200 {
				id := projects.NewID()
				Expect(projects.IDSafe(id)).To(BeTrue(), id)
				Expect(seen).ToNot(HaveKey(id))
				seen[id] = true
			}
		})
	})
})

var _ = Describe("Slugify", func() {
	It("makes a lowercase kebab-case slug", func() {
		Expect(projects.Slugify("My  Client/Work", nil)).To(Equal("my-client-work"))
	})

	It("falls back to project when nothing usable is left", func() {
		Expect(projects.Slugify("  ", nil)).To(Equal("project"))
		Expect(projects.Slugify("!!!", nil)).To(Equal("project"))
	})

	It("appends a counter while the slug is taken", func() {
		taken := func(s string) bool { return s == "a" || s == "a-2" }
		Expect(projects.Slugify("A", taken)).To(Equal("a-3"))
	})

	It("keeps the slug at 48 characters or fewer, including the counter", func() {
		long := strings.Repeat("word ", 20)
		Expect(len(projects.Slugify(long, nil))).To(BeNumerically("<=", 48))

		taken := func(s string) bool { return s == projects.Slugify(long, nil) }
		slug := projects.Slugify(long, taken)
		Expect(len(slug)).To(BeNumerically("<=", 48))
		Expect(slug).To(HaveSuffix("-2"))
		Expect(strings.HasSuffix(slug, "--2")).To(BeFalse())
	})

	It("never ends with a dash", func() {
		Expect(projects.Slugify("trailing ---", nil)).To(Equal("trailing"))
	})

	It("treats taken case-insensitively, so a mixed-case clash gets a counter", func() {
		taken := func(s string) bool { return strings.EqualFold(s, "a") }
		Expect(projects.Slugify("A", taken)).To(Equal("a-2"))
	})

	It("passes the same lowercase base for differently spaced and cased names", func() {
		var seen []string
		record := func(s string) bool { seen = append(seen, s); return false }
		Expect(projects.Slugify(" a ", record)).To(Equal("a"))
		Expect(projects.Slugify("A", record)).To(Equal("a"))
		Expect(seen).To(Equal([]string{"a", "a"}))
	})

	It("folds Latin accents into ASCII", func() {
		Expect(projects.Slugify("Zürich Notes", nil)).To(Equal("zurich-notes"))
		Expect(projects.Slugify("Café Crème", nil)).To(Equal("cafe-creme"))
	})

	DescribeTable("folds letters that have no Unicode decomposition",
		func(name, want string) {
			Expect(projects.Slugify(name, nil)).To(Equal(want))
		},
		Entry("o with stroke", "Ørsted", "orsted"),
		Entry("sharp s", "Straße", "strasse"),
		Entry("capital sharp s", "STRAẞE", "strasse"),
		Entry("l with stroke", "Łódź", "lodz"),
		Entry("d with stroke", "Đorđe", "dorde"),
		Entry("ligatures", "Æon Œuvre", "aeon-oeuvre"),
		Entry("thorn and eth", "Þór Ðe", "thor-de"),
		Entry("CJK text next to Latin text is dropped", "Acme 日本", "acme"),
	)

	It("gives an emoji-only name a stable short id", func() {
		slug := projects.Slugify("🚀🔥", nil)
		Expect(slug).To(MatchRegexp(`^project-[0-9a-f]{4}$`))
		Expect(projects.Slugify("🚀🔥", nil)).To(Equal(slug))
	})

	It("uses a stable short id for names with no Latin letters", func() {
		first := projects.Slugify("日本語", nil)
		Expect(first).To(MatchRegexp(`^project-[0-9a-f]{4}$`))
		Expect(projects.Slugify("日本語", nil)).To(Equal(first))
		Expect(projects.Slugify("Проект", nil)).To(MatchRegexp(`^project-[0-9a-f]{4}$`))
		Expect(projects.Slugify("Проект", nil)).NotTo(Equal(first))
	})

	It("appends a counter when the short id is taken", func() {
		first := projects.Slugify("日本語", nil)
		taken := func(s string) bool { return s == first }
		Expect(projects.Slugify("日本語", taken)).To(Equal(first + "-2"))
	})

	It("always returns an IDSafe slug", func() {
		rng := rand.New(rand.NewSource(1))
		for i := 0; i < 200; i++ {
			rs := make([]rune, 1+rng.Intn(30))
			for j := range rs {
				rs[j] = rune(rng.Intn(0x30000))
			}
			name := string(rs)
			Expect(projects.IDSafe(projects.Slugify(name, nil))).To(BeTrue(), name)
		}
	})
})

// manifestWithSkills builds a manifest whose defaults.skills has n entries.
func manifestWithSkills(n int) []byte {
	skills := make([]string, n)
	for i := range skills {
		skills[i] = "s"
	}
	b, _ := json.Marshal(map[string]any{
		"version":  1,
		"name":     "x",
		"defaults": map[string]any{"skills": skills},
	})
	return b
}

var _ = Describe("the attached lists of a manifest", func() {
	It("refuses an entry of a bad shape and a connector listed as an MCP server", func() {
		for name, in := range map[string]projects.Defaults{
			"empty":         {Addons: []string{""}},
			"padded":        {Skills: []string{" a"}},
			"control":       {Collections: []string{"a\nb"}},
			"too long":      {Addons: []string{strings.Repeat("x", 257)}},
			"connector tag": {MCPServers: []string{"connector:c1"}},
		} {
			err := projects.CheckAttached(projects.Defaults{}, in)
			Expect(err).To(MatchError(projects.ErrInvalid), name)
		}
		Expect(projects.CheckAttached(projects.Defaults{}, projects.Defaults{Connectors: []string{"c1"}, MCPServers: []string{"srv"}})).To(Succeed())
	})

	It("keeps only the entries a write would accept when it reads them", func() {
		limit := projects.Defaults{Addons: []string{"a"}}
		got := projects.EffectiveAttached(limit, projects.Defaults{
			Addons: []string{"a", "b", ""}, MCPServers: []string{"connector:c", "s"}, Skills: []string{"k"}})
		Expect(got).To(Equal(projects.Defaults{Addons: []string{"a"}, MCPServers: []string{"s"}, Skills: []string{"k"}}))
		Expect(got.Empty()).To(BeFalse())
		Expect(projects.Defaults{}.Empty()).To(BeTrue())
	})
})
