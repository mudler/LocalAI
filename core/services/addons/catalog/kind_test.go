// SPDX-License-Identifier: MIT
package catalog

import (
	"net/http"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const kindDigest = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

const oldShapeIndex = `- addonId: tables
  name: Tables
  version: 1.0.0
  artifact: oci://registry.example/tables
  digest: ` + yamlDigest + `
  os: linux
  arch: amd64
`

const backendEntry = `- kind: backend
  id: cpu-local-store
  alias: local-store
  name: Local store (CPU)
  version: 0.0.0-latest.bbbbbbbbbbbb
  artifact: oci://registry.example/backends:latest-cpu-local-store
  digest: ` + kindDigest + `
  os: linux
  arch: amd64
`

const unknownKindEntry = `- kind: gizmo
  id: widget
  futureField: 7
  nested: {a: [1, 2]}
`

var _ = Describe("entry kinds", func() {
	It("reads an index written before kinds existed, and the entry is an add-on", func() {
		res, err := ReadIndex([]byte(oldShapeIndex), KindAddon)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Skipped).To(BeEmpty())
		Expect(res.Releases).To(HaveLen(1))
		Expect(res.Releases[0].Kind).To(BeEmpty())
		Expect(res.Releases[0].EntryKind()).To(Equal(KindAddon))
		releases, err := DecodeIndex([]byte(oldShapeIndex))
		Expect(err).NotTo(HaveOccurred())
		Expect(releases).To(HaveLen(1))
	})

	It("reads a backend entry on a reader that knows backends", func() {
		res, err := ReadIndex([]byte(oldShapeIndex+backendEntry), KindAddon, KindBackend)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Skipped).To(BeEmpty())
		Expect(res.Releases).To(HaveLen(2))
		Expect(res.Releases[1].EntryKind()).To(Equal(KindBackend))
		Expect(res.Releases[1].Alias).To(Equal("local-store"))
		// The reader of this host knows backends too.
		releases, err := DecodeIndex([]byte(oldShapeIndex + backendEntry))
		Expect(err).NotTo(HaveOccurred())
		Expect(releases).To(HaveLen(2))
	})

	It("skips a backend entry on an add-on only reader and says so", func() {
		res, err := ReadIndex([]byte(oldShapeIndex+backendEntry), KindAddon)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Releases).To(HaveLen(1))
		Expect(res.Skipped).To(Equal([]SkippedEntry{{Position: 1, Kind: "backend", ID: "cpu-local-store"}}))
	})

	It("skips an unknown kind without reading its fields, and keeps the rest", func() {
		res, err := ReadIndex([]byte(oldShapeIndex+unknownKindEntry+backendEntry), KindAddon, KindBackend)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Releases).To(HaveLen(2))
		Expect(res.Skipped).To(Equal([]SkippedEntry{{Position: 1, Kind: "gizmo", ID: "widget"}}))
		Expect(res.SkippedKinds()).To(Equal([]string{"gizmo"}))
		releases, err := DecodeIndex([]byte(unknownKindEntry))
		Expect(err).NotTo(HaveOccurred())
		Expect(releases).To(BeEmpty())
	})

	It("skips the reserved model kind in this phase", func() {
		res, err := ReadIndex([]byte("- kind: model\n  addonId: m\n  files: []\n"), ReadableKinds...)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Releases).To(BeEmpty())
		Expect(res.SkippedKinds()).To(Equal([]string{"model"}))
	})

	It("reports the id of a skipped entry, written as id or as the legacy addonId", func() {
		res, err := ReadIndex([]byte("- kind: model\n  id: m1\n- kind: model\n  addonId: m2\n"), ReadableKinds...)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Skipped).To(HaveLen(2))
		Expect(res.Skipped[0].ID).To(Equal("m1"))
		Expect(res.Skipped[1].ID).To(Equal("m2"))
	})

	It("reads a backend entry that still uses the legacy addonId, and refuses one that has both", func() {
		legacy := strings.Replace(backendEntry, "  id: cpu-local-store", "  addonId: cpu-local-store", 1)
		res, err := ReadIndex([]byte(legacy), ReadableKinds...)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Releases).To(HaveLen(1))
		Expect(res.Releases[0].ID).To(Equal("cpu-local-store"))
		_, err = ReadIndex([]byte(backendEntry+"  addonId: cpu-local-store\n"), ReadableKinds...)
		Expect(err).To(HaveOccurred())
		out, err := EncodeIndex(res.Releases)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(out)).NotTo(ContainSubstring("addonId"))
	})

	It("still refuses an unknown field on an entry of a known kind", func() {
		_, err := ReadIndex([]byte(backendEntry+"  surprise: 1\n"), ReadableKinds...)
		Expect(err).To(HaveOccurred())
	})

	It("refuses a malformed kind", func() {
		for _, bad := range []string{"kind: 3", "kind: \"\"", "kind: Backend", "kind: [backend]", "kind: " + strings.Repeat("a", 40)} {
			_, err := ReadIndex([]byte("- "+bad+"\n  addonId: x\n"), ReadableKinds...)
			Expect(err).To(HaveOccurred(), bad)
		}
	})
})

var _ = Describe("kinds in a library", func() {
	refresh := func(body string, policy VerificationPolicy) (RefreshResult, *Sources, error) {
		server := library(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) })
		s := setup()
		Expect(s.Save(ctx, admin, Source{ID: "lib", Name: "lib", URL: server.URL, Enabled: true, Policy: policy})).To(Succeed())
		res, err := s.Refresh(ctx, "lib")
		return res, s, err
	}

	It("refreshes an index with an unknown kind and reports what it skipped", func() {
		res, s, err := refresh(oldShapeIndex+unknownKindEntry+backendEntry, VerificationPolicy{})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Count).To(Equal(2))
		Expect(res.Skipped).To(Equal(1))
		Expect(res.SkippedKinds).To(Equal([]string{"gizmo"}))
		views, err := s.Views(ctx, admin)
		Expect(err).NotTo(HaveOccurred())
		Expect(views[0].Refresh.Skipped).To(Equal(1))
	})

	It("lists add-ons by default and backends on request", func() {
		_, s, err := refresh(oldShapeIndex+backendEntry, VerificationPolicy{})
		Expect(err).NotTo(HaveOccurred())
		c := Catalog{Sources: s, TargetOS: "linux", TargetArch: "amd64"}
		page, err := c.Search(ctx, Query{})
		Expect(err).NotTo(HaveOccurred())
		Expect(page.Releases).To(HaveLen(1))
		Expect(page.Releases[0].ID).To(Equal("tables"))
		page, err = c.Search(ctx, Query{Kind: KindBackend})
		Expect(err).NotTo(HaveOccurred())
		Expect(page.Releases).To(HaveLen(1))
		Expect(page.Releases[0].ID).To(Equal("cpu-local-store"))
		page, err = c.Search(ctx, Query{Kind: QueryAllKinds})
		Expect(err).NotTo(HaveOccurred())
		Expect(page.Total).To(Equal(2))
		_, err = c.Search(ctx, Query{Kind: "model"})
		Expect(err).To(MatchError(ErrInvalid))
	})

	It("resolves a backend only as a backend", func() {
		_, s, err := refresh(backendEntry, VerificationPolicy{})
		Expect(err).NotTo(HaveOccurred())
		c := Catalog{Sources: s, TargetOS: "linux", TargetArch: "amd64"}
		_, err = c.Resolve(ctx, "lib", "cpu-local-store", "0.0.0-latest.bbbbbbbbbbbb")
		Expect(err).To(MatchError(ErrNotFound))
		r, err := c.ResolveKind(ctx, KindBackend, "lib", "cpu-local-store", "0.0.0-latest.bbbbbbbbbbbb")
		Expect(err).NotTo(HaveOccurred())
		Expect(r.EntryKind()).To(Equal(KindBackend))
	})

	It("refuses entries that mix the fields of the kinds", func() {
		for name, body := range map[string]string{
			"backend with an https artifact": strings.Replace(backendEntry, "oci://registry.example", "https://registry.example", 1),
			"backend with a main page":       backendEntry + "  ui:\n    mainPage: x\n",
			"backend name with a slash":      strings.Replace(backendEntry, "id: cpu-local-store", "id: a/b", 1),
			"backend name with dots":         strings.Replace(backendEntry, "id: cpu-local-store", "id: a..b", 1),
			"add-on with an alias":           oldShapeIndex + "  alias: x\n",
		} {
			_, _, err := refresh(body, VerificationPolicy{})
			Expect(err).To(HaveOccurred(), name)
		}
	})

	It("accepts an anchored identity pattern and refuses a loose one", func() {
		good := VerificationPolicy{RequireSignature: true, Issuer: "https://issuer.example", IdentityRegex: `^https://github\.com/o/r/\.github/workflows/w\.yml@refs/(heads/master|tags/.+)$`}
		Expect(validPolicy(good)).To(BeTrue())
		for name, p := range map[string]VerificationPolicy{
			"no anchors":       {RequireSignature: true, Issuer: "i", IdentityRegex: `https://x/.*`},
			"alternation":      {RequireSignature: true, Issuer: "i", IdentityRegex: `^a|b$`},
			"does not compile": {RequireSignature: true, Issuer: "i", IdentityRegex: `^(a$`},
			"both forms":       {RequireSignature: true, Issuer: "i", Identity: "x", IdentityRegex: `^a$`},
			"no issuer":        {RequireSignature: true, IdentityRegex: `^a$`},
			"unused pattern":   {IdentityRegex: `^a$`},
		} {
			Expect(validPolicy(p)).To(BeFalse(), name)
		}
	})
})
