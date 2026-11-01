package session

import (
	"encoding/json"
	"strings"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func appCard(mut func(*ToolApp)) ToolCard {
	a := &ToolApp{Server: "connector:abc", Tool: "get-time", ResourceURI: "ui://clock/app.html", Visibility: []string{"model", "app"},
		Input: json.RawMessage(`{"zone":"UTC"}`), Result: json.RawMessage(`{"content":[]}`)}
	if mut != nil {
		mut(a)
	}
	return ToolCard{Kind: "mcp_app", ID: "0123456789abcdef", App: a}
}

var _ = ginkgo.Describe("cleaning a tool card", func() {
	ginkgo.It("keeps a well formed app", func() {
		c, ok := cleanCard(appCard(nil))
		Expect(ok).To(BeTrue(), "clean = %+v", c)
		Expect(c.App).NotTo(BeNil())
		Expect(c.App.Server).To(Equal("connector:abc"))
		Expect(string(c.App.Result)).To(Equal(`{"content":[]}`))
	})

	ginkgo.It("refuses a bad reference", func() {
		for name, mut := range map[string]func(*ToolApp){
			"no server":      func(a *ToolApp) { a.Server = "" },
			"long server":    func(a *ToolApp) { a.Server = strings.Repeat("s", 300) },
			"not a ui uri":   func(a *ToolApp) { a.ResourceURI = "https://evil.example/x" },
			"control in uri": func(a *ToolApp) { a.ResourceURI = "ui://a\nb" },
		} {
			_, ok := cleanCard(appCard(mut))
			Expect(ok).To(BeFalse(), "%s: the card was accepted", name)
		}
	})

	ginkgo.It("drops an oversize input or result but keeps the reference", func() {
		c, ok := cleanCard(appCard(func(a *ToolApp) {
			a.Input = json.RawMessage(`"` + strings.Repeat("a", 20<<10) + `"`)
			a.Result = json.RawMessage(`"` + strings.Repeat("a", 400<<10) + `"`)
		}))
		Expect(ok).To(BeTrue())
		Expect(c.App).NotTo(BeNil())
		Expect(c.App.Input).To(BeNil())
		Expect(c.App.Result).To(BeNil())
		Expect(c.App.Cut).To(BeTrue())
	})

	ginkgo.It("drops invalid JSON", func() {
		c, ok := cleanCard(appCard(func(a *ToolApp) { a.Result = json.RawMessage(`{not json`) }))
		Expect(ok).To(BeTrue())
		Expect(c.App.Result).To(BeNil())
	})
})
