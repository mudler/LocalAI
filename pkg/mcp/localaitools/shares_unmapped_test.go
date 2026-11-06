package localaitools

import (
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The sharing routes (/api/shares) decide who may use a resource. Like the
// add-on grant routes they accept only the owner's own browser session, so an
// agent or an MCP client, which holds a key or a tool session, can never
// share, lower or revoke. REST-to-MCP parity is intentionally absent here:
// no tool maps to them, and nothing under pkg/mcp calls them.
var _ = Describe("sharing and the MCP tools", func() {
	It("maps no tool to any /api/shares route", func() {
		for tool, route := range toolToHTTPRoute {
			Expect(route).NotTo(ContainSubstring("/api/shares"), tool+": sharing is never a tool")
		}
	})

	It("maps no tool to the share, references or needs routes of a saved agent", func() {
		for tool, route := range toolToHTTPRoute {
			if !strings.Contains(route, "/api/chats/agents") {
				continue
			}
			last := route[strings.LastIndex(route, "/"):]
			Expect([]string{"/share", "/references", "/needs"}).NotTo(ContainElement(last), tool+": sharing an agent is never a tool")
		}
	})

	It("maps no tool to the routes that share a folder of a person's home", func() {
		for tool, route := range toolToHTTPRoute {
			Expect(route).NotTo(ContainSubstring("/api/chats/files/shares"), tool+": sharing a folder is never a tool")
		}
	})

	It("never names the routes in the code of the MCP server, its clients or its prompts", func() {
		var hits []string
		Expect(filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || strings.HasSuffix(path, "_test.go") || !(strings.HasSuffix(path, ".go") || strings.HasSuffix(path, ".md")) {
				return err
			}
			b, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			if low := strings.ToLower(string(b)); strings.Contains(low, "/api/shares") || strings.Contains(low, "/files/shares") {
				hits = append(hits, path)
			}
			return nil
		})).To(Succeed())
		Expect(hits).To(BeEmpty(), "an MCP tool, client or prompt must not reach the sharing routes")
	})
})
