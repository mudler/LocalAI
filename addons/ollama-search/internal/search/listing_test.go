// SPDX-License-Identifier: MIT
package search_test

import (
	"os"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/addons/ollama-search/internal/search"
)

// The pages are recorded copies of the real pages of the site (see
// testdata/README). The tests never call the live site.
func page(name string) string {
	GinkgoHelper()
	b, err := os.ReadFile("testdata/" + name)
	Expect(err).NotTo(HaveOccurred())
	return string(b)
}

// item builds one result item in the shape of the search page.
func item(name, badges, extra string) string {
	return `<li class="border-t"><a href="/library/` + name + `" class="group"><h2 class="x" title="` + name + `"><span>` + name + `</span></h2>` +
		`<span title="Updated March 3, 2026">1w</span><div>` + badges + `</div><p class="line-clamp-2">d</p>` +
		`<span title="1,234 downloads"><span>1.2K</span></span>` + extra + `</a></li>`
}

func badge(t string) string {
	return `<span class="inline-flex items-center rounded-md bg-x px-2">` + t + `</span>`
}

func wrap(count, items string) string {
	return `<html><body><div hidden data-result-count="` + count + `" data-has-more="false"></div><ul role="list">` + items + `</ul></body></html>`
}

var _ = Describe("the search page", func() {
	It("reads the recorded page: name, link, downloads, update date, capability and size tags", func() {
		rows, err := search.ParseSearch(page("search-qwen.html"), 0)
		Expect(err).NotTo(HaveOccurred())
		// The page counts 20 results and shows the first 14; the rest load as the
		// person scrolls, which the add-on does not follow.
		Expect(rows).To(HaveLen(14))
		r := rows[0]
		Expect(r.ID).To(Equal("qwen3.8"))
		Expect(r.Title).To(Equal("qwen3.8"))
		Expect(r.URI).To(Equal("ollama://qwen3.8"))
		Expect(r.URL).To(Equal("https://ollama.com/library/qwen3.8"))
		Expect(r.Downloads).To(Equal(int64(7166939)))
		Expect(r.LastModified).To(Equal("2026-11-08T00:00:00Z"))
		Expect(r.Tags).To(Equal([]string{"vision", "tools", "thinking", "27b"}))
		for _, row := range rows {
			Expect(row.ID).NotTo(BeEmpty())
			Expect(row.URI).To(Equal("ollama://" + row.ID))
			Expect(row.Downloads).To(BeNumerically(">", 0), row.ID)
			Expect(row.LastModified).NotTo(BeEmpty(), row.ID)
		}
	})

	It("keeps the limit", func() {
		rows, err := search.ParseSearch(page("search-qwen.html"), 3)
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(HaveLen(3))
	})

	It("answers an empty list, with no error, for a page that counts no results", func() {
		rows, err := search.ParseSearch(page("search-empty.html"), 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(BeEmpty())
		Expect(rows).NotTo(BeNil())
	})

	It("leaves out a model that runs only in the cloud and keeps one that has a size", func() {
		body := wrap("3", item("cloudonly", badge("tools")+badge("cloud"), "")+item("both", badge("tools")+badge("cloud")+badge("8b"), "")+item("plain", badge("1.5b"), ""))
		rows, err := search.ParseSearch(body, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(HaveLen(2))
		Expect(rows[0].ID).To(Equal("both"))
		Expect(rows[1].ID).To(Equal("plain"))
	})

	It("caps the tags of a row at 32 and drops a tag the host would refuse", func() {
		var b strings.Builder
		for i := range 40 {
			b.WriteString(badge("t" + strings.Repeat("x", i)))
		}
		b.WriteString(badge(strings.Repeat("y", 70)))
		rows, err := search.ParseSearch(wrap("1", item("many", b.String(), "")), 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(rows[0].Tags).To(HaveLen(32))
		for _, t := range rows[0].Tags {
			Expect(len(t)).To(BeNumerically("<=", 64))
		}
	})

	It("keeps a row whose date or download count it cannot read, without those fields", func() {
		li := `<li><a href="/library/odd"><h2 title="odd"></h2><span title="Updated sometime">x</span><span title="many downloads">x</span></a></li>`
		rows, err := search.ParseSearch(wrap("1", li), 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(HaveLen(1))
		Expect(rows[0].LastModified).To(BeEmpty())
		Expect(rows[0].Downloads).To(BeZero())
	})

	It("names the page when the result count marker is missing", func() {
		_, err := search.ParseSearch("<html><body><ul><li>nothing</li></ul></body></html>", 0)
		Expect(err).To(MatchError(search.ErrLayout))
		Expect(err.Error()).To(ContainSubstring("Ollama search page"))
	})

	It("names the page when it counts results but shows none", func() {
		_, err := search.ParseSearch(wrap("20", `<li>renamed markup</li>`), 0)
		Expect(err).To(MatchError(search.ErrLayout))
		Expect(err.Error()).To(ContainSubstring("counts 20 results but lists none"))
	})

	It("refuses a link that is not a plain library name", func() {
		_, err := search.ParseSearch(wrap("1", `<li><a href="/library/../etc">x</a></li>`), 0)
		Expect(err).To(MatchError(search.ErrLayout))
	})
})

var _ = Describe("the tags page", func() {
	It("reads the recorded page: one variant per tag, latest first, with size and context", func() {
		v, err := search.ParseTags(page("tags-qwen3.html"), "qwen3")
		Expect(err).NotTo(HaveOccurred())
		Expect(v).To(HaveLen(58))
		Expect(v[0]).To(Equal(search.Variant{Tag: "latest", URI: "ollama://qwen3:latest", Digest: "500a1f067a9f", SizeBytes: 5_200_000_000, ContextTokens: 40 << 10}))
		Expect(v[1].Tag).To(Equal("0.6b"))
		Expect(v[1].URI).To(Equal("ollama://qwen3:0.6b"))
		last := v[len(v)-1]
		Expect(last.Tag).To(Equal("235b-thinking"))
		Expect(last.SizeBytes).To(Equal(int64(142_000_000_000)))
		Expect(last.ContextTokens).To(Equal(int64(256 << 10)))
		seen := map[string]bool{}
		for _, x := range v {
			Expect(seen[x.Tag]).To(BeFalse(), x.Tag)
			seen[x.Tag] = true
		}
	})

	It("reads a size in megabytes and a context window in millions, and leaves out a cloud tag", func() {
		tag := func(t, detail string) string {
			return `<a href="/library/m:` + t + `" class="md:hidden flex"><span class="font-mono">
				` + detail + `</a>`
		}
		body := tag("latest", "aaaaaaaaaaaa</span> • 523MB • 1M context window  •") +
			tag("480b-cloud", "bbbbbbbbbbbb</span> • cloud") +
			tag("nosize", "cccccccccccc</span> • 1.5GB  •")
		v, err := search.ParseTags(body, "m")
		Expect(err).NotTo(HaveOccurred())
		Expect(v).To(HaveLen(2))
		Expect(v[0].SizeBytes).To(Equal(int64(523_000_000)))
		Expect(v[0].ContextTokens).To(Equal(int64(1 << 20)))
		Expect(v[1].ContextTokens).To(BeZero())
	})

	It("leaves out the builds that are not GGUF files and keeps bf16 and quantized tags", func() {
		v, err := search.ParseTags(page("tags-q38.html"), "qwen3.8")
		Expect(err).NotTo(HaveOccurred())
		var tags []string
		for _, x := range v {
			tags = append(tags, x.Tag)
		}
		Expect(tags).To(Equal([]string{"latest", "27b", "27b-mtp-q4_K_M", "27b-mtp-q8_0", "27b-mtp-bf16", "27b-q4_K_M", "27b-q8_0", "27b-bf16"}))
	})

	It("leaves out a tag by its name, whatever its case and place", func() {
		tag := func(t string) string {
			return `<a href="/library/m:` + t + `" class="md:hidden"><span class="font-mono">
				aaaaaaaaaaaa</span> • 1GB • 4K context window</a>`
		}
		body := tag("latest") + tag("8b-MLX") + tag("mlx-8b") + tag("8b-nvfp4") + tag("8b-dflash2-mxfp8") + tag("8b-fp16")
		v, err := search.ParseTags(body, "m")
		Expect(err).NotTo(HaveOccurred())
		Expect(v).To(HaveLen(2))
		Expect(v[0].Tag).To(Equal("latest"))
		Expect(v[1].Tag).To(Equal("8b-fp16"))
	})

	It("names the page when every tag is left out", func() {
		_, err := search.ParseTags(`<a href="/library/m:8b-mlx" class="md:hidden">x</a>`, "m")
		Expect(err).To(MatchError(search.ErrLayout))
	})

	It("names the page when it lists no tag", func() {
		_, err := search.ParseTags("<html><body>nothing</body></html>", "qwen3")
		Expect(err).To(MatchError(search.ErrLayout))
		Expect(err.Error()).To(ContainSubstring("tags page of qwen3"))
	})

	It("names the page when a tag has no size line", func() {
		_, err := search.ParseTags(`<a href="/library/m:x" class="md:hidden">x</a>`, "m")
		Expect(err).To(MatchError(search.ErrLayout))
	})
})
