// SPDX-License-Identifier: MIT
package catalog

import (
	"fmt"
	"net/http"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const yamlDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

const yamlEntry = `- addonId: tables
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
			"mapping":         "addonId: tables\n",
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
		for i := 0; i < 1000; i++ {
			many.WriteString(strings.Replace(yamlEntry, "version: 1.0.0", fmt.Sprintf("version: 1.0.%d", i), 1))
		}
		r, e, _ := refreshBody(many.String())
		Expect(e).NotTo(HaveOccurred())
		Expect(r.Count).To(Equal(1000))
		many.WriteString(strings.Replace(yamlEntry, "version: 1.0.0", "version: 2.0.0", 1))
		_, e, _ = refreshBody(many.String())
		Expect(e).To(MatchError(ErrRefresh))
		_, e, _ = refreshBody(yamlEntry + "# " + strings.Repeat("x", 1<<20) + "\n")
		Expect(e).To(MatchError(ErrRefresh))
	})
})
