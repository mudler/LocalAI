package localaitools

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/LocalAI/pkg/modelsearch"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("search_models result", func() {
	It("adds no notice when only built-in sources answered", func() {
		r := wrapSearchResult(&ModelSearchResponse{Sources: []modelsearch.Source{{ID: "huggingface", Kind: "builtin"}}})
		Expect(r.Notice).To(BeEmpty())
	})

	It("says that text from an add-on source is untrusted", func() {
		r := wrapSearchResult(&ModelSearchResponse{Sources: []modelsearch.Source{{ID: "huggingface", Kind: "builtin"}, {ID: "peer", Kind: "addon"}}})
		Expect(r.Notice).To(ContainSubstring("untrusted"))
	})

	It("keeps the wire shape of the response and adds the notice beside it", func() {
		r := wrapSearchResult(&ModelSearchResponse{Results: []modelsearch.Result{{ID: "o/r"}}, Sources: []modelsearch.Source{{ID: "peer", Kind: "addon"}}})
		res := jsonResult(r)
		Expect(res.Content).NotTo(BeEmpty())
		Expect(res.Content[0].(*mcp.TextContent).Text).To(And(ContainSubstring(`"results"`), ContainSubstring(`"notice"`)))
	})
})
