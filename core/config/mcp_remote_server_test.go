package config_test

import (
	"bytes"
	"fmt"
	"log/slog"

	. "github.com/mudler/LocalAI/core/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("MCP remote server OAuth2 configuration", func() {
	parse := func(remote string) (MCPGenericConfig[MCPRemoteServers], error) {
		c := MCPConfig{Servers: remote}
		r, _, err := c.MCPConfigFromYAML()
		return r, err
	}

	It("parses an oauth2 block from the remote JSON", func() {
		r, err := parse(`{"mcpServers": {"gw": {"url": "https://mcp.example.com/mcp",
			"oauth2": {"token_url": "https://idp.example.com/token", "client_id": "localai",
			"client_secret_env": "MCP_SECRET", "scopes": ["mcp"], "endpoint_params": {"audience": "mcp"}}}}}`)
		Expect(err).NotTo(HaveOccurred())
		o := r.Servers["gw"].OAuth2
		Expect(o).NotTo(BeNil())
		Expect(o.TokenURL).To(Equal("https://idp.example.com/token"))
		Expect(o.ClientID).To(Equal("localai"))
		Expect(o.ClientSecretEnv).To(Equal("MCP_SECRET"))
		Expect(o.Scopes).To(Equal([]string{"mcp"}))
		Expect(o.EndpointParams).To(HaveKeyWithValue("audience", "mcp"))
	})

	It("keeps the static token form valid (counter-check)", func() {
		r, err := parse(`{"mcpServers": {"s": {"url": "https://x", "token": "abc"}}}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Servers["s"].Token).To(Equal("abc"))
		Expect(r.Servers["s"].OAuth2).To(BeNil())
	})

	DescribeTable("rejects inconsistent oauth2 blocks",
		func(remote, msg string) {
			_, err := parse(remote)
			Expect(err).To(MatchError(ContainSubstring(msg)))
		},
		Entry("token plus oauth2",
			`{"mcpServers": {"s": {"url": "https://x", "token": "t", "oauth2": {"token_url": "https://i", "client_id": "a", "client_secret": "b"}}}}`,
			"mutually exclusive"),
		Entry("missing token_url",
			`{"mcpServers": {"s": {"url": "https://x", "oauth2": {"client_id": "a", "client_secret": "b"}}}}`,
			"token_url"),
		Entry("missing secret",
			`{"mcpServers": {"s": {"url": "https://x", "oauth2": {"token_url": "https://i", "client_id": "a"}}}}`,
			"client_secret"),
		Entry("secret given twice",
			`{"mcpServers": {"s": {"url": "https://x", "oauth2": {"token_url": "https://i", "client_id": "a", "client_secret": "b", "client_secret_env": "B"}}}}`,
			"mutually exclusive"),
	)

	It("does not write credentials to logs", func() {
		var buf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&buf, nil))
		withToken := MCPRemoteServer{URL: "https://x", Token: "static-secret"}
		withOAuth2 := MCPRemoteServer{
			URL:    "https://y",
			OAuth2: &MCPOAuth2Config{TokenURL: "https://idp", ClientID: "id", ClientSecret: "oauth-secret"},
		}
		logger.Info("cfg", "a", withToken, "b", withOAuth2)
		out := buf.String() + fmt.Sprintf("%v %+v", withToken, MCPRemoteServers{"s": withOAuth2})
		Expect(out).To(ContainSubstring("https://x"))
		Expect(out).To(ContainSubstring("https://idp"))
		Expect(out).NotTo(ContainSubstring("static-secret"))
		Expect(out).NotTo(ContainSubstring("oauth-secret"))
	})
})
