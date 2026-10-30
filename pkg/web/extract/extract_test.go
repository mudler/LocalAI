package extract_test

import (
	"errors"
	"net/url"
	"strings"

	"github.com/mudler/LocalAI/pkg/web/extract"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var base, _ = url.Parse("https://example.com/docs/page.html")

func md(page string) string {
	t, err := extract.FromResponse("text/html", "utf-8", []byte(page), base, extract.Options{})
	Expect(err).NotTo(HaveOccurred())
	return t.Body
}

var _ = Describe("HTML", func() {
	It("drops scripts, styles, navigation and hidden elements", func() {
		out := md(`<html><head><title>T</title><style>p{}</style></head><body>
<nav><a href="/">Home</a></nav><header>Site</header>
<script>alert("x")</script><noscript>enable js</noscript>
<p>Visible text.</p><div hidden>secret one</div><span aria-hidden="true">secret two</span>
<p style="display: none">secret three</p>
<footer>Copyright</footer><aside>ads</aside><form><input value="q"></form></body></html>`)
		Expect(out).To(Equal("Visible text."))
	})

	It("prefers main over the rest of the body", func() {
		out := md(`<body><div>sidebar junk</div><main><h1>Title</h1><p>Body.</p></main></body>`)
		Expect(out).To(Equal("# Title\n\nBody."))
	})

	It("uses a single article when there is no main", func() {
		out := md(`<body><div>menu</div><article><p>Story.</p></article></body>`)
		Expect(out).To(Equal("Story."))
	})

	It("keeps headings, links with absolute hrefs and inline code", func() {
		out := md(`<body><h2>Install</h2><p>See <a href="../guide#x">the guide</a> and run <code>make  build</code>.</p></body>`)
		Expect(out).To(Equal("## Install\n\nSee [the guide](https://example.com/guide#x) and run `make build`."))
	})

	It("drops javascript links but keeps their text", func() {
		out := md(`<p><a href="javascript:alert(1)">click</a> <a href="https://u:p@example.org/x">there</a></p>`)
		Expect(out).To(Equal("click [there](https://example.org/x)"))
	})

	It("writes lists, nested lists and ordered lists", func() {
		out := md(`<body><p>Steps:</p><ol><li>One</li><li>Two<ul><li>a</li><li>b</li></ul></li></ol><p>After.</p></body>`)
		Expect(out).To(Equal("Steps:\n\n1. One\n2. Two\n  - a\n  - b\n\nAfter."))
	})

	It("writes code blocks fenced with their language", func() {
		out := md(`<pre><code class="language-go">func main() {
	fmt.Println("hi")
}</code></pre>`)
		Expect(out).To(Equal("```go\nfunc main() {\n\tfmt.Println(\"hi\")\n}\n```"))
	})

	It("writes tables as rows", func() {
		out := md(`<table><tr><th>Name</th><th>Size</th></tr><tr><td>a|b</td><td>1</td></tr></table>`)
		Expect(out).To(Equal("| Name | Size |\n| --- | --- |\n| a\\|b | 1 |"))
	})

	It("keeps image alt text and blockquotes", func() {
		out := md(`<blockquote><p>Quoted.</p></blockquote><p><img src="x.png" alt="A cat"></p>`)
		Expect(out).To(Equal("> Quoted.\n\n[image: A cat]"))
	})

	It("writes plain text without markup in text format", func() {
		t, err := extract.FromResponse("text/html", "", []byte(`<h1>T</h1><p><a href="/x">link</a> <code>c</code></p>`), base, extract.Options{Format: extract.FormatText})
		Expect(err).NotTo(HaveOccurred())
		Expect(t.Body).To(Equal("T\n\nlink c"))
	})

	It("returns the title", func() {
		t, err := extract.FromResponse("text/html", "", []byte(`<title>  My   Page </title><p>x</p>`), base, extract.Options{})
		Expect(err).NotTo(HaveOccurred())
		Expect(t.Title).To(Equal("My Page"))
	})

	It("decodes a declared charset", func() {
		t, err := extract.FromResponse("text/html", "iso-8859-1", []byte("<p>caf\xe9</p>"), base, extract.Options{})
		Expect(err).NotTo(HaveOccurred())
		Expect(t.Body).To(Equal("café"))
	})
})

var _ = Describe("FromResponse", func() {
	It("returns text formats as they are", func() {
		for _, ct := range []string{"text/plain", "text/markdown", "text/csv", "application/json", "application/ld+json", "application/xml"} {
			t, err := extract.FromResponse(ct, "", []byte("  {\"a\": 1}\n"), base, extract.Options{})
			Expect(err).NotTo(HaveOccurred(), ct)
			Expect(t.Body).To(Equal(`{"a": 1}`), ct)
		}
	})

	It("says a type is unsupported, with the type", func() {
		_, err := extract.FromResponse("image/png", "", []byte("x"), base, extract.Options{})
		Expect(errors.Is(err, extract.ErrUnsupported)).To(BeTrue())
		Expect(err.Error()).To(Equal("unsupported type: image/png"))
	})

	It("reads PDF only with a PDF reader", func() {
		_, err := extract.FromResponse("application/pdf", "", []byte("%PDF"), base, extract.Options{})
		Expect(err).To(MatchError(ContainSubstring("unsupported type: application/pdf")))
		t, err := extract.FromResponse("application/pdf", "", []byte("%PDF"), base, extract.Options{PDF: func([]byte) (string, error) { return " page text ", nil }})
		Expect(err).NotTo(HaveOccurred())
		Expect(t.Body).To(Equal("page text"))
	})

	It("cuts at the budget with a marker that counts what is left", func() {
		t, err := extract.FromResponse("text/plain", "", []byte(strings.Repeat("é", 120)), base, extract.Options{MaxChars: 100})
		Expect(err).NotTo(HaveOccurred())
		Expect(t.Omitted).To(Equal(20))
		Expect(t.Body).To(Equal(strings.Repeat("é", 100) + "\n\n[truncated, 20 chars more]"))
	})

	It("does not mark text within the budget", func() {
		t, err := extract.FromResponse("text/plain", "", []byte("short"), base, extract.Options{MaxChars: 100})
		Expect(err).NotTo(HaveOccurred())
		Expect(t.Body).To(Equal("short"))
		Expect(t.Omitted).To(BeZero())
	})
})
