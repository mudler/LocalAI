// SPDX-License-Identifier: MIT
package mcpapps_test

import (
	"encoding/json"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/mcpapps"
)

var _ = Describe("ParseUI", func() {
	It("reads the resource URI and defaults the visibility to model and app", func() {
		u := mcpapps.ParseUI(map[string]any{"ui": map[string]any{"resourceUri": "ui://clock/app.html"}})
		Expect(u.ResourceURI).To(Equal("ui://clock/app.html"))
		Expect(u.Visibility).To(ConsistOf("model", "app"))
		Expect(u.ForModel()).To(BeTrue())
		Expect(u.ForApp()).To(BeTrue())
	})
	It("reads the legacy flat key", func() {
		u := mcpapps.ParseUI(map[string]any{"ui/resourceUri": "ui://a/b"})
		Expect(u.ResourceURI).To(Equal("ui://a/b"))
	})
	It("keeps a model-only tool away from the app", func() {
		u := mcpapps.ParseUI(map[string]any{"ui": map[string]any{"resourceUri": "ui://a/b", "visibility": []any{"model"}}})
		Expect(u.ForModel()).To(BeTrue())
		Expect(u.ForApp()).To(BeFalse())
	})
	It("keeps an app-only tool away from the model", func() {
		u := mcpapps.ParseUI(map[string]any{"ui": map[string]any{"visibility": []any{"app"}}})
		Expect(u.ForModel()).To(BeFalse())
		Expect(u.ForApp()).To(BeTrue())
	})
	It("treats an empty visibility list as visible to nobody and drops unknown words", func() {
		u := mcpapps.ParseUI(map[string]any{"ui": map[string]any{"visibility": []any{}}})
		Expect(u.ForModel() || u.ForApp()).To(BeFalse())
		u = mcpapps.ParseUI(map[string]any{"ui": map[string]any{"visibility": []any{"app", "root", 3}}})
		Expect(u.Visibility).To(ConsistOf("app"))
	})
	It("gives a tool without ui metadata the defaults and no resource", func() {
		u := mcpapps.ParseUI(nil)
		Expect(u.ResourceURI).To(BeEmpty())
		Expect(u.ForModel()).To(BeTrue())
		Expect(u.ForApp()).To(BeTrue())
	})
	It("ignores a resource URI that is not a ui:// string", func() {
		Expect(mcpapps.ParseUI(map[string]any{"ui": map[string]any{"resourceUri": "https://evil/x"}}).ResourceURI).To(BeEmpty())
		Expect(mcpapps.ParseUI(map[string]any{"ui": map[string]any{"resourceUri": 7}}).ResourceURI).To(BeEmpty())
	})
})

var _ = Describe("CheckURI", func() {
	It("accepts ui:// and refuses everything else", func() {
		Expect(mcpapps.CheckURI("ui://x/y.html")).To(Succeed())
		for _, bad := range []string{"", "file:///etc/passwd", "https://x", "ui:/x", "UI://x", "ui://x\n", "ui://", strings.Repeat("a", 3000)} {
			Expect(mcpapps.CheckURI(bad)).NotTo(Succeed(), bad)
		}
	})
})

var _ = Describe("CheckContent", func() {
	It("accepts the MCP App and plain HTML types and refuses the rest", func() {
		Expect(mcpapps.CheckContent("text/html;profile=mcp-app", 10)).To(Succeed())
		Expect(mcpapps.CheckContent("text/html; profile=mcp-app", 10)).To(Succeed())
		Expect(mcpapps.CheckContent("text/html", 10)).To(Succeed())
		Expect(mcpapps.CheckContent("text/html; charset=utf-8", 10)).To(Succeed())
		Expect(mcpapps.CheckContent("application/json", 10)).NotTo(Succeed())
		Expect(mcpapps.CheckContent("", 10)).NotTo(Succeed())
	})
	It("refuses a body over the cap", func() {
		Expect(mcpapps.CheckContent("text/html", mcpapps.MaxResourceBytes)).To(Succeed())
		Expect(mcpapps.CheckContent("text/html", mcpapps.MaxResourceBytes+1)).NotTo(Succeed())
	})
})

var _ = Describe("ResourceMeta", func() {
	It("keeps the csp domains, the permissions and prefersBorder as the spec names them", func() {
		m := mcpapps.ResourceMeta(map[string]any{"ui": map[string]any{
			"csp": map[string]any{
				"connectDomains":  []any{"https://api.example.com", "wss://live.example.com:8443"},
				"resourceDomains": []any{"https://*.cdn.example.com"},
			},
			"permissions":   map[string]any{"camera": map[string]any{}},
			"prefersBorder": true,
		}})
		Expect(m.CSP.ConnectDomains).To(Equal([]string{"https://api.example.com", "wss://live.example.com:8443"}))
		Expect(m.CSP.ResourceDomains).To(Equal([]string{"https://*.cdn.example.com"}))
		Expect(m.Permissions.Camera).To(BeTrue())
		Expect(m.PrefersBorder).NotTo(BeNil())
		Expect(*m.PrefersBorder).To(BeTrue())
		raw, _ := json.Marshal(m)
		Expect(string(raw)).To(ContainSubstring(`"connectDomains"`))
	})
	It("drops a domain that could inject into a CSP header", func() {
		m := mcpapps.ResourceMeta(map[string]any{"ui": map[string]any{"csp": map[string]any{
			"connectDomains": []any{"https://ok.example.com", "https://x.com; script-src *", "'unsafe-eval'", "*", "http://plain.example.com", "https://a.com https://b.com", "data:", "https://x.com/path"},
		}}})
		Expect(m.CSP.ConnectDomains).To(Equal([]string{"https://ok.example.com"}))
	})
	It("is empty for a resource with no ui metadata", func() {
		m := mcpapps.ResourceMeta(nil)
		Expect(m.CSP.ConnectDomains).To(BeEmpty())
		Expect(m.PrefersBorder).To(BeNil())
	})
	It("bounds the number of domains", func() {
		var many []any
		for i := 0; i < 100; i++ {
			many = append(many, "https://h"+strings.Repeat("a", i%5)+string(rune('a'+i%26))+".example.com")
		}
		m := mcpapps.ResourceMeta(map[string]any{"ui": map[string]any{"csp": map[string]any{"connectDomains": many}}})
		Expect(len(m.CSP.ConnectDomains)).To(BeNumerically("<=", mcpapps.MaxDomains))
	})
})

var _ = Describe("FromCallResult", func() {
	It("keeps content, structured content and the error flag", func() {
		r := mcpapps.FromCallResult(&mcp.CallToolResult{
			Content:           []mcp.Content{&mcp.TextContent{Text: "12:00"}},
			StructuredContent: map[string]any{"time": "12:00"},
		})
		var got map[string]any
		Expect(json.Unmarshal(r, &got)).To(Succeed())
		Expect(got["structuredContent"]).To(HaveKeyWithValue("time", "12:00"))
		Expect(got["content"]).To(HaveLen(1))
	})
	It("cuts a result over the cap to its text and marks it", func() {
		big := strings.Repeat("x", mcpapps.MaxResultBytes*2)
		r := mcpapps.FromCallResult(&mcp.CallToolResult{
			Content:           []mcp.Content{&mcp.TextContent{Text: big}},
			StructuredContent: map[string]any{"big": big},
			IsError:           true,
		})
		Expect(len(r)).To(BeNumerically("<=", mcpapps.MaxResultBytes+1024))
		var got map[string]any
		Expect(json.Unmarshal(r, &got)).To(Succeed())
		Expect(got).NotTo(HaveKey("structuredContent"))
		Expect(got["isError"]).To(BeTrue())
		Expect(string(r)).To(ContainSubstring("result cut"))
	})
})

var _ = Describe("FromReadResult", func() {
	const uri = "ui://clock/app.html"
	It("returns the HTML with its cleaned metadata", func() {
		res, err := mcpapps.FromReadResult(&mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
			URI: uri, MIMEType: "text/html;profile=mcp-app", Text: "<p>hi</p>",
			Meta: mcp.Meta{"ui": map[string]any{"csp": map[string]any{"connectDomains": []any{"https://a.example.com"}}}},
		}}}, uri)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.HTML).To(Equal("<p>hi</p>"))
		Expect(res.MIMEType).To(Equal("text/html;profile=mcp-app"))
		Expect(res.Meta.CSP.ConnectDomains).To(Equal([]string{"https://a.example.com"}))
	})
	It("reads an HTML blob", func() {
		res, err := mcpapps.FromReadResult(&mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: uri, MIMEType: "text/html", Blob: []byte("<b>x</b>")}}}, uri)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.HTML).To(Equal("<b>x</b>"))
	})
	It("refuses a wrong type, an oversize body, an empty answer and a different URI", func() {
		_, err := mcpapps.FromReadResult(&mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: uri, MIMEType: "application/json", Text: "{}"}}}, uri)
		Expect(err).To(HaveOccurred())
		_, err = mcpapps.FromReadResult(&mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: uri, MIMEType: "text/html", Text: strings.Repeat("a", mcpapps.MaxResourceBytes+1)}}}, uri)
		Expect(err).To(HaveOccurred())
		_, err = mcpapps.FromReadResult(&mcp.ReadResourceResult{}, uri)
		Expect(err).To(HaveOccurred())
		_, err = mcpapps.FromReadResult(&mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: "ui://other", MIMEType: "text/html", Text: "x"}}}, uri)
		Expect(err).To(HaveOccurred())
	})
})
