package localaitools

import (
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The host access routes (/api/addons/host-grants) decide what an add-on may
// ask the host to do. They accept only the administrator's own browser
// session, so an agent or an MCP client, which holds a key or a tool session,
// can never grant, revoke, deny or turn the blanket approval on. This spec
// keeps that true: no tool maps to them, and nothing under pkg/mcp calls them.
var _ = Describe("host access and the MCP tools", func() {
	It("maps no tool to any /api/addons/host-grants route", func() {
		for tool, route := range toolToHTTPRoute {
			Expect(route).NotTo(ContainSubstring("host-grants"), tool)
			Expect(route).NotTo(ContainSubstring("/api/addons"), tool+": add-on lifecycle and host access are not tools")
		}
	})

	It("never names the routes in the code of the MCP server, its clients or its prompts", func() {
		root := "."
		var hits []string
		Expect(filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			// Only source files: a test binary built into the tree holds the names too.
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			b, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			s := strings.ToLower(string(b))
			if strings.Contains(s, "host-grants") || strings.Contains(s, "hostgrants") {
				hits = append(hits, path)
			}
			return nil
		})).To(Succeed())
		Expect(hits).To(BeEmpty(), "an MCP tool or client must not reach the host access routes")
	})
})
