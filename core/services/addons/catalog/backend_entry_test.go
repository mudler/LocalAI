// SPDX-License-Identifier: MIT
package catalog

import (
	"net/http"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// libraryEntry is a concrete backend entry that uses every field of the
// backend body. A test changes one field of it and checks the reader.
const libraryEntry = `- kind: backend
  id: cuda12-demo
  alias: demo
  name: cuda12-demo
  description: |
    A demo backend.
  version: 0.0.0-latest.bbbbbbbbbbbb
  channel: latest
  schemaVersion: 1
  license: apache-2.0
  icon: https://example.com/demo.png
  urls:
    - https://github.com/example/demo
  tags:
    - CUDA
    - LLM
  artifact: oci://quay.example/backends:latest-gpu-nvidia-cuda-12-demo
  digest: ` + kindDigest + `
  mirrors:
    - oci://hub.example/backends:latest-gpu-nvidia-cuda-12-demo
  capabilities:
    default: cpu-demo
    nvidia: cuda12-demo
  os: linux
  arch: amd64
`

const metaEntry = `- kind: backend
  id: demo
  alias: demo
  name: demo
  version: 0.0.0-latest
  channel: latest
  schemaVersion: 1
  capabilities:
    default: cpu-demo
    nvidia: cuda12-demo
`

const mutableEntry = `- kind: backend
  id: cuda12-demo-development
  name: cuda12-demo-development
  version: 0.0.0-development
  channel: development
  schemaVersion: 1
  mutable: true
  artifact: oci://quay.example/backends:master-gpu-nvidia-cuda-12-demo
  os: linux
  arch: amd64
`

var _ = Describe("the backend body of a library entry", func() {
	read := func(body string) (RefreshResult, *Sources, error) {
		server := library(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) })
		s := setup()
		Expect(s.Save(ctx, admin, Source{ID: "lib", Name: "lib", URL: server.URL, Enabled: true})).To(Succeed())
		res, err := s.Refresh(ctx, "lib")
		return res, s, err
	}
	with := func(old, repl string) string {
		GinkgoHelper()
		Expect(libraryEntry).To(ContainSubstring(old))
		return strings.Replace(libraryEntry, old, repl, 1)
	}

	It("reads every new field and keeps it", func() {
		_, s, err := read(libraryEntry)
		Expect(err).NotTo(HaveOccurred())
		c := Catalog{Sources: s, TargetOS: "linux", TargetArch: "amd64"}
		r, err := c.ResolveKind(ctx, KindBackend, "lib", "cuda12-demo", "latest")
		Expect(err).NotTo(HaveOccurred())
		Expect(r.License).To(Equal("apache-2.0"))
		Expect(r.URLs).To(Equal([]string{"https://github.com/example/demo"}))
		Expect(r.Tags).To(Equal([]string{"CUDA", "LLM"}))
		Expect(r.Icon).To(Equal("https://example.com/demo.png"))
		Expect(r.Mirrors).To(Equal([]string{"oci://hub.example/backends:latest-gpu-nvidia-cuda-12-demo"}))
		Expect(r.Capabilities).To(HaveKeyWithValue("nvidia", "cuda12-demo"))
		Expect(r.SchemaVersion).To(Equal(1))
		Expect(r.Mutable).To(BeFalse())
	})

	It("keeps the reader strict about an unknown field of the backend kind", func() {
		_, err := ReadIndex([]byte(libraryEntry+"  surprise: 1\n"), ReadableKinds...)
		Expect(err).To(HaveOccurred())
		_, err = ReadIndex([]byte(strings.Replace(libraryEntry, "  mirrors:\n", "  mirror:\n", 1)), ReadableKinds...)
		Expect(err).To(HaveOccurred())
	})

	It("refuses a license, urls, tags, icon, mirrors or capabilities on an add-on", func() {
		for _, extra := range []string{
			"  license: mit\n", "  urls: [https://example.com]\n", "  tags: [a]\n", "  icon: https://example.com/i.png\n",
			"  mirrors: [oci://hub.example/x:y]\n", "  capabilities: {default: x}\n", "  schemaVersion: 1\n",
		} {
			_, _, err := read(oldShapeIndex + extra)
			Expect(err).To(HaveOccurred(), extra)
		}
	})

	It("refuses a schemaVersion this host does not know", func() {
		_, _, err := read(with("schemaVersion: 1", "schemaVersion: 2"))
		Expect(err).To(HaveOccurred())
		_, _, err = read(with("schemaVersion: 1", "schemaVersion: -1"))
		Expect(err).To(HaveOccurred())
		_, _, err = read(strings.Replace(libraryEntry, "  schemaVersion: 1\n", "", 1))
		Expect(err).NotTo(HaveOccurred(), "an entry without a schemaVersion is version 1")
	})

	Describe("icon", func() {
		It("is a URL, and the host does not fetch it", func() {
			fetched := false
			icon := library(func(http.ResponseWriter, *http.Request) { fetched = true })
			_, _, err := read(with("https://example.com/demo.png", "https://"+strings.TrimPrefix(icon.URL, "http://")))
			Expect(err).NotTo(HaveOccurred())
			Expect(fetched).To(BeFalse())
		})
		It("must be an https URL with a host and no credentials", func() {
			for _, bad := range []string{"http://example.com/i.png", "icon.png", "../icon.png", "data:image/png;base64,AAAA", "https://user:pw@example.com/i.png", "javascript:alert(1)", "https:///i.png"} {
				_, _, err := read(with("https://example.com/demo.png", bad))
				Expect(err).To(HaveOccurred(), bad)
			}
		})
		It("is not the icon of the add-on UI, which stays a path in the package", func() {
			_, _, err := read(with("  mirrors:", "  ui:\n    icon: icons/demo.png\n  mirrors:"))
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("urls, tags and license", func() {
		It("refuses a url that is not http or https", func() {
			for _, bad := range []string{"ftp://example.com/x", "file:///etc/passwd", "example.com/x", "https://u:p@example.com/"} {
				_, _, err := read(with("https://github.com/example/demo", bad))
				Expect(err).To(HaveOccurred(), bad)
			}
		})
		It("refuses too many urls and too many tags", func() {
			many := "  urls:\n" + strings.Repeat("    - https://example.com/a\n", maxBackendURLs+1)
			_, _, err := read(strings.Replace(libraryEntry, "  urls:\n    - https://github.com/example/demo\n", many, 1))
			Expect(err).To(HaveOccurred())
			tags := "  tags:\n" + strings.Repeat("    - t\n", maxBackendTags+1)
			_, _, err = read(strings.Replace(libraryEntry, "  tags:\n    - CUDA\n    - LLM\n", tags, 1))
			Expect(err).To(HaveOccurred())
		})
		It("refuses a tag or a license with a control character or a length over the bound", func() {
			_, _, err := read(with("- CUDA", "- \"C\\nUDA\""))
			Expect(err).To(HaveOccurred())
			_, _, err = read(with("- CUDA", "- "+strings.Repeat("a", maxTagBytes+1)))
			Expect(err).To(HaveOccurred())
			_, _, err = read(with("license: apache-2.0", "license: "+strings.Repeat("a", maxLicenseBytes+1)))
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("mirrors", func() {
		It("keeps the order", func() {
			body := with("    - oci://hub.example/backends:latest-gpu-nvidia-cuda-12-demo\n",
				"    - oci://b.example/xx:t\n    - oci://a.example/xx:t\n")
			_, s, err := read(body)
			Expect(err).NotTo(HaveOccurred())
			r, err := Catalog{Sources: s, TargetOS: "linux", TargetArch: "amd64"}.ResolveKind(ctx, KindBackend, "lib", "cuda12-demo", "latest")
			Expect(err).NotTo(HaveOccurred())
			Expect(r.Mirrors).To(Equal([]string{"oci://b.example/xx:t", "oci://a.example/xx:t"}))
		})
		It("refuses a mirror that is not an oci image, a duplicate, the artifact itself, and too many", func() {
			dup := "    - oci://a.example/xx:t\n    - oci://a.example/xx:t\n"
			for name, body := range map[string]string{
				"https mirror":    with("oci://hub.example/backends:latest-gpu-nvidia-cuda-12-demo", "https://hub.example/backends"),
				"no scheme":       with("oci://hub.example/backends:latest-gpu-nvidia-cuda-12-demo", "hub.example/backends:x"),
				"duplicate":       with("    - oci://hub.example/backends:latest-gpu-nvidia-cuda-12-demo\n", dup),
				"the artifact":    with("oci://hub.example/backends:latest-gpu-nvidia-cuda-12-demo", "oci://quay.example/backends:latest-gpu-nvidia-cuda-12-demo"),
				"not a reference": with("oci://hub.example/backends:latest-gpu-nvidia-cuda-12-demo", "oci://hub.example/Backends:UPPER:x"),
				"with a secret":   with("oci://hub.example/backends:latest-gpu-nvidia-cuda-12-demo", "oci://user:pw@hub.example/backends:x"),
				"too many":        with("    - oci://hub.example/backends:latest-gpu-nvidia-cuda-12-demo\n", strings.Repeat("    - oci://m.example/xx:t\n", maxBackendMirrors+1)),
			} {
				if name == "too many" {
					// Distinct mirrors, so only the count can refuse it.
					var b strings.Builder
					for i := 0; i <= maxBackendMirrors; i++ {
						b.WriteString("    - oci://m" + string(rune('a'+i)) + ".example/xx:t\n")
					}
					body = with("    - oci://hub.example/backends:latest-gpu-nvidia-cuda-12-demo\n", b.String())
				}
				_, _, err := read(body)
				Expect(err).To(HaveOccurred(), name)
			}
		})
	})

	Describe("capabilities", func() {
		It("refuses a malformed capability name or backend name", func() {
			for name, body := range map[string]string{
				"upper case key": with("    default: cpu-demo", "    Default: cpu-demo"),
				"empty target":   with("    default: cpu-demo", "    default: \"\""),
				"slash target":   with("    default: cpu-demo", "    default: a/b"),
				"dots target":    with("    default: cpu-demo", "    default: a..b"),
			} {
				_, _, err := read(body)
				Expect(err).To(HaveOccurred(), name)
			}
		})
	})

	Describe("a meta backend", func() {
		It("has capabilities and no artifact, digest or platform, and is listed for every platform", func() {
			_, s, err := read(metaEntry + libraryEntry)
			Expect(err).NotTo(HaveOccurred())
			for _, plat := range [][2]string{{"linux", "amd64"}, {"linux", "arm64"}, {"darwin", "arm64"}} {
				c := Catalog{Sources: s, TargetOS: plat[0], TargetArch: plat[1]}
				page, err := c.Search(ctx, Query{Kind: KindBackend})
				Expect(err).NotTo(HaveOccurred())
				ids := []string{}
				for _, r := range page.Releases {
					ids = append(ids, r.ID)
				}
				Expect(ids).To(ContainElement("demo"), plat[0]+"/"+plat[1])
				meta, err := c.ResolveKind(ctx, KindBackend, "lib", "demo", "latest")
				Expect(err).NotTo(HaveOccurred())
				Expect(meta.IsMeta()).To(BeTrue())
			}
		})
		It("cannot carry an artifact-only field", func() {
			for name, extra := range map[string]string{
				"a digest":    "  digest: " + kindDigest + "\n",
				"an os":       "  os: linux\n",
				"an arch":     "  arch: amd64\n",
				"mirrors":     "  mirrors: [oci://a.example/xx:t]\n",
				"mutable":     "  mutable: true\n",
				"a main page": "  ui:\n    mainPage: x\n",
			} {
				_, _, err := read(metaEntry + extra)
				Expect(err).To(HaveOccurred(), name)
			}
		})
		It("needs capabilities: an entry with no artifact and none is refused", func() {
			_, _, err := read("- kind: backend\n  id: empty\n  name: empty\n  version: 0.0.0-latest\n")
			Expect(err).To(HaveOccurred())
		})
		It("is not a meta backend when it has an artifact", func() {
			var r Release
			Expect(r.IsMeta()).To(BeFalse())
			res, err := ReadIndex([]byte(libraryEntry), ReadableKinds...)
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Releases[0].IsMeta()).To(BeFalse())
		})
	})

	Describe("a mutable development entry", func() {
		It("keeps its tag, has no digest and is listed", func() {
			_, s, err := read(mutableEntry)
			Expect(err).NotTo(HaveOccurred())
			r, err := Catalog{Sources: s, TargetOS: "linux", TargetArch: "amd64"}.ResolveKind(ctx, KindBackend, "lib", "cuda12-demo-development", "development")
			Expect(err).NotTo(HaveOccurred())
			Expect(r.Mutable).To(BeTrue())
			Expect(r.Digest).To(BeEmpty())
			Expect(r.Artifact).To(HaveSuffix(":master-gpu-nvidia-cuda-12-demo"))
		})
		It("is refused with a digest, and an entry with no digest is refused unless it is mutable", func() {
			_, _, err := read(mutableEntry + "  digest: " + kindDigest + "\n")
			Expect(err).To(HaveOccurred())
			_, _, err = read(strings.Replace(mutableEntry, "  mutable: true\n", "", 1))
			Expect(err).To(HaveOccurred())
		})
		It("is accepted on an add-on without a digest, as on a backend", func() {
			entry := strings.Replace(oldShapeIndex, "  digest: "+yamlDigest+"\n", "  mutable: true\n", 1)
			_, s, err := read(entry)
			Expect(err).NotTo(HaveOccurred())
			r, err := Catalog{Sources: s, TargetOS: "linux", TargetArch: "amd64"}.Resolve(ctx, "lib", "tables", "1.0.0")
			Expect(err).NotTo(HaveOccurred())
			Expect(r.Mutable).To(BeTrue())
			Expect(r.Digest).To(BeEmpty())
		})
		It("is refused on an add-on with a digest, without a digest unless mutable, or on an archive", func() {
			_, _, err := read(oldShapeIndex + "  mutable: true\n")
			Expect(err).To(HaveOccurred())
			_, _, err = read(strings.Replace(oldShapeIndex, "  digest: "+yamlDigest+"\n", "", 1))
			Expect(err).To(HaveOccurred())
			_, _, err = read(strings.Replace(strings.Replace(oldShapeIndex, "  digest: "+yamlDigest+"\n", "  mutable: true\n", 1), "oci://registry.example/tables", "https://registry.example/tables.tar", 1))
			Expect(err).To(HaveOccurred())
		})
	})

	It("reads an index of more than a thousand entries", func() {
		var b strings.Builder
		for i := 0; i < 1500; i++ {
			b.WriteString(strings.Replace(metaEntry, "id: demo\n", "id: demo"+strings.Repeat("a", 0)+itoa(i)+"\n", 1))
		}
		res, err := ReadIndex([]byte(b.String()), ReadableKinds...)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Releases).To(HaveLen(1500))
	})

	It("encodes and decodes an entry with the new fields unchanged", func() {
		res, err := ReadIndex([]byte(libraryEntry+metaEntry+mutableEntry), ReadableKinds...)
		Expect(err).NotTo(HaveOccurred())
		out, err := EncodeIndex(res.Releases)
		Expect(err).NotTo(HaveOccurred())
		again, err := ReadIndex(out, ReadableKinds...)
		Expect(err).NotTo(HaveOccurred())
		Expect(again.Releases).To(Equal(res.Releases))
	})
})

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var d []byte
	for ; i > 0; i /= 10 {
		d = append([]byte{byte('0' + i%10)}, d...)
	}
	return string(d)
}
