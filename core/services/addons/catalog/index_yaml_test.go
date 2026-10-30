// SPDX-License-Identifier: MIT
package catalog

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const yamlDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

const yamlEntry = `- id: tables
  name: Tables
  description: Tables for LocalAI
  version: 1.0.0
  artifact: oci://registry.example/tables
  digest: ` + yamlDigest + `
  os: linux
  arch: amd64
  ui:
    title: Tables
    mainPage: tables
`

func refreshBody(body string) (RefreshResult, error, *Sources) {
	server := library(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) })
	s := setup()
	save(s, "lib", server.URL)
	r, e := s.Refresh(ctx, "lib")
	return r, e, s
}

var _ = Describe("library index document (YAML)", func() {
	It("reads a YAML list of releases with the documented fields", func() {
		r, e, s := refreshBody(yamlEntry)
		Expect(e).NotTo(HaveOccurred())
		Expect(r.Count).To(Equal(1))
		got, e := (Catalog{Sources: s, TargetOS: "linux", TargetArch: "amd64"}).Resolve(ctx, "lib", "tables", "1.0.0")
		Expect(e).NotTo(HaveOccurred())
		Expect(got.Name).To(Equal("Tables"))
		Expect(got.Digest).To(Equal(yamlDigest))
		Expect(got.UI.MainPage).To(Equal("tables"))
	})
	It("reads the legacy addonId as id, and refuses an entry that has both", func() {
		legacy := strings.Replace(yamlEntry, "- id: tables", "- addonId: tables", 1)
		got, err := DecodeIndex([]byte(legacy))
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(HaveLen(1))
		Expect(got[0].ID).To(Equal("tables"))
		current, err := DecodeIndex([]byte(yamlEntry))
		Expect(err).NotTo(HaveOccurred())
		Expect(current).To(Equal(got))
		_, err = DecodeIndex([]byte(yamlEntry + "  addonId: tables\n"))
		Expect(err).To(HaveOccurred())
		// A writer emits id, never the legacy name.
		out, err := EncodeIndex(got)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(out)).To(HavePrefix("- "))
		Expect(string(out)).NotTo(ContainSubstring("addonId"))
		Expect(string(out)).To(ContainSubstring("id: tables"))
	})
	It("accepts an empty library written as an empty list", func() {
		r, e, _ := refreshBody("[]\n")
		Expect(e).NotTo(HaveOccurred())
		Expect(r.Count).To(BeZero())
	})
	It("refuses a JSON index", func() {
		_, e, _ := refreshBody(`[{"addonId":"tables","name":"Tables","version":"1.0.0","artifact":"oci://registry.example/tables","digest":"` + yamlDigest + `","os":"linux","arch":"amd64"}]`)
		Expect(e).To(MatchError(ErrRefresh))
		_, e, _ = refreshBody("{\n  \"addonId\": \"tables\"\n}")
		Expect(e).To(MatchError(ErrRefresh))
	})
	It("refuses an unknown field, at the top and inside ui", func() {
		_, e, _ := refreshBody(yamlEntry + "  surprise: true\n")
		Expect(e).To(MatchError(ErrRefresh))
		_, e, _ = refreshBody(yamlEntry + "    surprise: true\n")
		Expect(e).To(MatchError(ErrRefresh))
	})
	It("refuses a second document, a mapping, an empty body and a duplicate key", func() {
		for name, body := range map[string]string{
			"second document": yamlEntry + "---\n" + strings.Replace(yamlEntry, "version: 1.0.0", "version: 2.0.0", 1),
			"mapping":         "id: tables\n",
			"empty":           "",
			"duplicate key":   strings.Replace(yamlEntry, "  os: linux\n", "  os: linux\n  os: linux\n", 1),
			"number version":  strings.Replace(yamlEntry, "version: 1.0.0", "version: 1.0", 1),
			"bad digest":      strings.Replace(yamlEntry, yamlDigest, "sha256:abc", 1),
		} {
			_, e, _ := refreshBody(body)
			Expect(e).To(MatchError(ErrRefresh), name)
		}
	})
	It("refuses duplicate identities", func() {
		_, e, _ := refreshBody(yamlEntry + yamlEntry)
		Expect(e).To(MatchError(ErrRefresh))
	})
	It("enforces the release count and the size limit", func() {
		var many strings.Builder
		for i := 0; i < MaxIndexReleases; i++ {
			many.WriteString(strings.Replace(yamlEntry, "version: 1.0.0", fmt.Sprintf("version: 1.0.%d", i), 1))
		}
		r, e, _ := refreshBody(many.String())
		Expect(e).NotTo(HaveOccurred())
		Expect(r.Count).To(Equal(MaxIndexReleases))
		many.WriteString(strings.Replace(yamlEntry, "version: 1.0.0", "version: 2.0.0", 1))
		_, e, _ = refreshBody(many.String())
		Expect(e).To(MatchError(ErrRefresh))
		_, e, _ = refreshBody(yamlEntry + "# " + strings.Repeat("x", MaxIndexBytes) + "\n")
		Expect(e).To(MatchError(ErrRefresh))
	})
	Context("tool hint", func() {
		It("carries the built-in tools an add-on replaces, and refuses one that may not be replaced", func() {
			body := yamlEntry + "  toolCount: 1\n  tools:\n    - bash\n  shadows:\n    - bash\n"
			_, e, s := refreshBody(body)
			Expect(e).NotTo(HaveOccurred())
			page, e := (Catalog{Sources: s, TargetOS: "linux", TargetArch: "amd64"}).Search(ctx, Query{})
			Expect(e).NotTo(HaveOccurred())
			Expect(page.Releases).To(HaveLen(1))
			Expect(page.Releases[0].Shadows).To(Equal([]string{"bash"}))
			_, e, _ = refreshBody(yamlEntry + "  toolCount: 1\n  tools:\n    - bash\n  shadows:\n    - write_file\n")
			Expect(e).To(HaveOccurred())
		})
		It("carries toolCount and tools from the index to the listing", func() {
			body := yamlEntry + "  toolCount: 2\n  tools:\n    - resource-exists\n    - tables-create\n"
			_, e, s := refreshBody(body)
			Expect(e).NotTo(HaveOccurred())
			page, e := (Catalog{Sources: s, TargetOS: "linux", TargetArch: "amd64"}).Search(ctx, Query{})
			Expect(e).NotTo(HaveOccurred())
			Expect(page.Releases).To(HaveLen(1))
			Expect(page.Releases[0].ToolCount).To(Equal(2))
			Expect(page.Releases[0].Tools).To(Equal([]string{"resource-exists", "tables-create"}))
			out, e := json.Marshal(page.Releases[0])
			Expect(e).NotTo(HaveOccurred())
			Expect(string(out)).To(ContainSubstring(`"toolCount":2`))
			Expect(string(out)).To(ContainSubstring(`"tools":["resource-exists","tables-create"]`))
		})
		It("leaves both fields out for an entry without chat tools", func() {
			_, e, s := refreshBody(yamlEntry)
			Expect(e).NotTo(HaveOccurred())
			page, e := (Catalog{Sources: s, TargetOS: "linux", TargetArch: "amd64"}).Search(ctx, Query{})
			Expect(e).NotTo(HaveOccurred())
			out, _ := json.Marshal(page.Releases[0])
			Expect(string(out)).NotTo(ContainSubstring("toolCount"))
			Expect(string(out)).NotTo(ContainSubstring(`"tools"`))
		})
		It("keeps a count above the listed names when the names are capped", func() {
			var names strings.Builder
			for i := 0; i < MaxEntryTools; i++ {
				fmt.Fprintf(&names, "    - tool-%c%c\n", 'a'+i/26, 'a'+i%26)
			}
			_, e, s := refreshBody(yamlEntry + "  toolCount: 40\n  tools:\n" + names.String())
			Expect(e).NotTo(HaveOccurred())
			page, _ := (Catalog{Sources: s, TargetOS: "linux", TargetArch: "amd64"}).Search(ctx, Query{})
			Expect(page.Releases[0].ToolCount).To(Equal(40))
			Expect(page.Releases[0].Tools).To(HaveLen(MaxEntryTools))
		})
		It("refuses a malformed hint", func() {
			for name, extra := range map[string]string{
				"negative count":         "  toolCount: -1\n",
				"names above count":      "  toolCount: 1\n  tools:\n    - aa\n    - bb\n",
				"count above short list": "  toolCount: 5\n  tools:\n    - aa\n",
				"bad name":               "  toolCount: 1\n  tools:\n    - Bad_Name\n",
				"duplicate name":         "  toolCount: 2\n  tools:\n    - aa\n    - aa\n",
			} {
				_, e, _ := refreshBody(yamlEntry + extra)
				Expect(e).To(MatchError(ErrRefresh), name)
			}
		})
	})
})
