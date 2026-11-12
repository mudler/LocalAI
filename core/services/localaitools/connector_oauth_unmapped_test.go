package localaitools

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The OAuth routes of the connections (/api/chats/connectors/.../oauth and
// /api/chats/admin/oauth-clients) carry a client secret, a login's state and
// the approval of an add-on's login. A secret must not pass through a chat,
// and a login is a person's act in their own browser, so no MCP tool maps to
// them and nothing in this package calls them. The two administrator routes
// are listed in adminRoutesWithoutTool with that reason.
var _ = Describe("OAuth logins of connections and the MCP tools", func() {
	It("maps no tool to an OAuth route", func() {
		for tool, route := range toolToHTTPRoute {
			Expect(route).NotTo(ContainSubstring("/oauth"), tool)
			Expect(route).NotTo(ContainSubstring("oauth-clients"), tool)
		}
		for _, route := range []string{"GET /api/chats/admin/oauth-clients/:addonId", "PUT /api/chats/admin/oauth-clients/:addonId"} {
			Expect(adminRoutesWithoutTool).To(HaveKey(route))
		}
	})

	It("never names the routes in the code of the MCP server, its clients or its prompts", func() {
		var hits []string
		Expect(filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || strings.HasSuffix(path, "_test.go") {
				return err
			}
			b, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			// A compiled test binary (ginkgo leaves <package>.test here) holds
			// the strings of the test files; only text sources count.
			if bytes.IndexByte(b, 0) >= 0 {
				return nil
			}
			s := strings.ToLower(string(b))
			if strings.Contains(s, "oauth-clients") || strings.Contains(s, "connectors/oauth") || strings.Contains(s, "/oauth/start") || strings.Contains(s, "/oauth/disconnect") {
				hits = append(hits, path)
			}
			return nil
		})).To(Succeed())
		Expect(hits).To(BeEmpty(), "an MCP tool or client must not reach the OAuth routes of the connections")
	})
})
