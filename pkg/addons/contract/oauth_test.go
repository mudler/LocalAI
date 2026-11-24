// SPDX-License-Identifier: MIT
package contract

import (
	"encoding/json"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("The OAuth descriptor of a credential slot", func() {
	valid := func() OAuthDescriptor {
		return OAuthDescriptor{
			AuthorizeURL: "https://auth.example.test/authorize",
			TokenURL:     "https://auth.example.test/token",
			RevokeURL:    "https://auth.example.test/revoke",
			Hosts:        []string{"auth.example.test"},
			Scopes:       OAuthScopes{Required: []string{"read"}, Optional: []string{"write"}},
			PKCE:         OAuthPKCES256,
			ClientAuth:   OAuthClientAuthBasic,
		}
	}
	const schema = `{"type":"object","properties":{"repo":{"type":"string"}},"required":["repo"],"additionalProperties":false}`
	manifest := func(mod func(*CredentialSlot)) Manifest {
		m, err := ParseManifest(fixture())
		Expect(err).NotTo(HaveOccurred())
		m.API = VersionRange{Min: 2, Max: 2}
		d := valid()
		slot := CredentialSlot{ID: "login", Title: "Login", Kind: CredentialKindOAuth2, ResourceArgument: "repo", OAuth: &d}
		if mod != nil {
			mod(&slot)
		}
		m.Capabilities = Capabilities{
			Tools:           []string{"read-file"},
			ToolDefinitions: []ToolDefinition{{Name: "read-file", Description: "Read a file.", Kind: ToolKindRead, InputSchema: json.RawMessage(schema)}},
			Credentials:     []CredentialSlot{slot},
			Connector:       &ConnectorCapability{Kinds: []ConnectorKind{{ID: "sample", Title: "Sample", Delivery: DeliveryPull}}},
		}
		return m
	}
	host := func() HostCompatibility {
		h := compatible()
		h.SDK = 0
		return h
	}

	It("is valid in a slot of kind oauth2", func() {
		Expect(ValidateManifest(manifest(nil), host())).To(Succeed())
		Expect(valid().Validate()).To(Succeed())
	})
	It("is required for kind oauth2 and refused for kind token", func() {
		Expect(ValidateManifest(manifest(func(s *CredentialSlot) { s.OAuth = nil }), host())).NotTo(Succeed())
		Expect(ValidateManifest(manifest(func(s *CredentialSlot) { s.Kind = CredentialKindToken }), host())).NotTo(Succeed())
	})
	It("round-trips through the strict manifest parser and refuses unknown fields", func() {
		b, err := json.Marshal(valid())
		Expect(err).NotTo(HaveOccurred())
		var back OAuthDescriptor
		Expect(json.Unmarshal(b, &back)).To(Succeed())
		Expect(back).To(Equal(valid()))
		var slot CredentialSlot
		raw := `{"id":"login","title":"Login","kind":"oauth2","oauth":{"authorizeUrl":"https://a.example.test/a","extra":1}}`
		dec := json.NewDecoder(strings.NewReader(raw))
		dec.DisallowUnknownFields()
		Expect(dec.Decode(&slot)).NotTo(Succeed())
	})

	DescribeTable("refuses an unsafe or malformed URL",
		func(mod func(*OAuthDescriptor)) {
			d := valid()
			mod(&d)
			Expect(d.Validate()).NotTo(Succeed())
		},
		Entry("empty authorize URL", func(d *OAuthDescriptor) { d.AuthorizeURL = "" }),
		Entry("empty token URL", func(d *OAuthDescriptor) { d.TokenURL = "" }),
		Entry("http", func(d *OAuthDescriptor) { d.TokenURL = "http://auth.example.test/token" }),
		Entry("user info", func(d *OAuthDescriptor) { d.TokenURL = "https://u:p@auth.example.test/token" }),
		Entry("query", func(d *OAuthDescriptor) { d.AuthorizeURL = "https://auth.example.test/a?x=1" }),
		Entry("empty query mark", func(d *OAuthDescriptor) { d.AuthorizeURL = "https://auth.example.test/a?" }),
		Entry("fragment", func(d *OAuthDescriptor) { d.AuthorizeURL = "https://auth.example.test/a#f" }),
		Entry("IPv4 literal", func(d *OAuthDescriptor) {
			d.TokenURL = "https://127.0.0.1/token"
			d.Hosts = []string{"127.0.0.1"}
		}),
		Entry("IPv6 literal", func(d *OAuthDescriptor) { d.TokenURL = "https://[::1]/token" }),
		Entry("other port", func(d *OAuthDescriptor) { d.TokenURL = "https://auth.example.test:8443/token" }),
		Entry("upper-case host", func(d *OAuthDescriptor) { d.TokenURL = "https://AUTH.example.test/token" }),
		Entry("host outside the patterns", func(d *OAuthDescriptor) { d.RevokeURL = "https://other.example.test/revoke" }),
		Entry("backslash", func(d *OAuthDescriptor) { d.TokenURL = `https://auth.example.test\@evil.test/token` }),
		Entry("space", func(d *OAuthDescriptor) { d.TokenURL = "https://auth.example.test/to ken" }),
	)
	It("accepts port 443", func() {
		d := valid()
		d.TokenURL = "https://auth.example.test:443/token"
		Expect(d.Validate()).To(Succeed())
	})
	It("allows the revoke URL to be absent", func() {
		d := valid()
		d.RevokeURL = ""
		Expect(d.Validate()).To(Succeed())
	})

	Describe("hosts", func() {
		DescribeTable("refuses a bad list or pattern",
			func(hosts []string) {
				d := valid()
				d.Hosts = hosts
				Expect(d.Validate()).NotTo(Succeed())
			},
			Entry("none", []string{}),
			Entry("five", []string{"a.example.test", "b.example.test", "c.example.test", "d.example.test", "auth.example.test"}),
			Entry("upper case", []string{"Auth.example.test"}),
			Entry("wildcard of one label", []string{"*.test"}),
			Entry("bare wildcard", []string{"*"}),
			Entry("wildcard in the middle", []string{"auth.*.test"}),
			Entry("wildcard without dot", []string{"*example.test"}),
			Entry("port", []string{"auth.example.test:443"}),
			Entry("IP address", []string{"10.0.0.1"}),
			Entry("empty label", []string{"auth..example.test"}),
			Entry("leading hyphen", []string{"-a.example.test"}),
			Entry("repeat", []string{"auth.example.test", "auth.example.test"}),
		)
		// A browser parses these as IPv4 addresses or as the loopback host, so
		// an approved descriptor could send the person to this machine.
		DescribeTable("refuses a host that a browser reads as an address or a local name",
			func(host string) {
				d := valid()
				d.Hosts = []string{host}
				d.AuthorizeURL, d.TokenURL, d.RevokeURL = "https://"+host+"/authorize", "https://"+host+"/token", ""
				Expect(d.Validate()).NotTo(Succeed())
				d = valid()
				d.Hosts = []string{"*." + host}
				Expect(d.Validate()).NotTo(Succeed(), "as a wildcard suffix")
			},
			Entry("hex number", "0x7f000001"),
			Entry("dotted hex", "0x7f.0x0.0x0.0x1"),
			Entry("decimal number", "2130706433"),
			Entry("short dotted form", "127.1"),
			Entry("dotted octal", "0177.0.0.01"),
			Entry("localhost", "localhost"),
			Entry("localhost with a trailing dot", "localhost."),
			Entry("a name under localhost", "auth.localhost"),
			Entry("a trailing dot", "auth.example.test."),
			Entry("one label", "intranet"),
			Entry("a numeric label", "10.example.test"),
			Entry("a hex label", "0x10.example.test"),
			Entry("a numeric top-level label", "auth.example.123"),
			Entry("a top-level label with digits", "auth.example.t3st"),
		)
		It("accepts registrable names, punycode top-level labels and labels that only start with digits", func() {
			for _, host := range []string{"auth.example.test", "a1.example.test", "1a.example.test", "auth.example.xn--p1ai", "login.example.co"} {
				d := valid()
				d.Hosts = []string{host}
				d.AuthorizeURL, d.TokenURL, d.RevokeURL = "https://"+host+"/authorize", "https://"+host+"/token", ""
				Expect(d.Validate()).To(Succeed(), host)
			}
		})
		It("matches a wildcard against exactly one label before the suffix", func() {
			d := valid()
			d.Hosts = []string{"*.example.test"}
			Expect(d.Validate()).To(Succeed())
			d.TokenURL = "https://a.b.example.test/token"
			Expect(d.Validate()).NotTo(Succeed())
			d.TokenURL = "https://example.test/token"
			Expect(d.Validate()).NotTo(Succeed())
		})
	})

	Describe("scopes", func() {
		many := func(n int, prefix string) []string {
			var out []string
			for i := range n {
				out = append(out, prefix+strings.Repeat("a", i+1))
			}
			return out
		}
		DescribeTable("refuses a bad scope list",
			func(sc OAuthScopes) {
				d := valid()
				d.Scopes = sc
				Expect(d.Validate()).NotTo(Succeed())
			},
			Entry("17 required", OAuthScopes{Required: many(17, "r")}),
			Entry("17 optional", OAuthScopes{Optional: many(17, "o")}),
			Entry("overlap", OAuthScopes{Required: []string{"a"}, Optional: []string{"a"}}),
			Entry("repeat", OAuthScopes{Required: []string{"a", "a"}}),
			Entry("empty token", OAuthScopes{Required: []string{""}}),
			Entry("space", OAuthScopes{Required: []string{"a b"}}),
			Entry("quote", OAuthScopes{Required: []string{`a"b`}}),
			Entry("backslash", OAuthScopes{Required: []string{`a\b`}}),
			Entry("non-ASCII", OAuthScopes{Required: []string{"é"}}),
			Entry("129 bytes", OAuthScopes{Required: []string{strings.Repeat("a", 129)}}),
		)
		It("accepts 16 of each, 128 bytes and URL-like scopes", func() {
			d := valid()
			d.Scopes = OAuthScopes{Required: many(16, "r"), Optional: append(many(15, "o"), strings.Repeat("a", 128))}
			Expect(d.Validate()).To(Succeed())
			d.Scopes = OAuthScopes{Required: []string{"https://api.example.test/auth/read", "a:b.c-d_e"}}
			Expect(d.Validate()).To(Succeed())
		})
		It("accepts no scope at all", func() {
			d := valid()
			d.Scopes = OAuthScopes{}
			Expect(d.Validate()).To(Succeed())
		})
	})

	It("accepts only S256 as the PKCE method", func() {
		for _, v := range []string{"", "plain", "s256", "none"} {
			d := valid()
			d.PKCE = v
			Expect(d.Validate()).NotTo(Succeed(), v)
		}
	})
	It("accepts the three client authentications and no other", func() {
		for _, v := range []string{OAuthClientAuthBasic, OAuthClientAuthPost, OAuthClientAuthNone} {
			d := valid()
			d.ClientAuth = v
			Expect(d.Validate()).To(Succeed(), v)
		}
		for _, v := range []string{"", "jwt", "Basic"} {
			d := valid()
			d.ClientAuth = v
			Expect(d.Validate()).NotTo(Succeed(), v)
		}
	})

	Describe("authorize parameters", func() {
		with := func(p ...OAuthParam) OAuthDescriptor {
			d := valid()
			d.AuthorizeParams = p
			return d
		}
		It("accepts the names of the closed registry", func() {
			d := with(
				OAuthParam{Name: "prompt", Value: "consent"},
				OAuthParam{Name: "audience", Value: "https://api.example.test/"},
				OAuthParam{Name: "access_type", Value: "offline"},
				OAuthParam{Name: "include_granted_scopes", Value: "true"},
			)
			Expect(d.Validate()).To(Succeed())
			d = with(OAuthParam{Name: "resource", Value: "urn:example:thing"})
			Expect(d.Validate()).To(Succeed())
		})
		DescribeTable("refuses",
			func(p ...OAuthParam) {
				d := with(p...)
				Expect(d.Validate()).NotTo(Succeed())
			},
			Entry("an unknown name", OAuthParam{Name: "login_hint", Value: "x"}),
			Entry("client_id", OAuthParam{Name: "client_id", Value: "x"}),
			Entry("client_secret", OAuthParam{Name: "client_secret", Value: "x"}),
			Entry("redirect_uri", OAuthParam{Name: "redirect_uri", Value: "x"}),
			Entry("response_type", OAuthParam{Name: "response_type", Value: "x"}),
			Entry("scope", OAuthParam{Name: "scope", Value: "x"}),
			Entry("state", OAuthParam{Name: "state", Value: "x"}),
			Entry("code", OAuthParam{Name: "code", Value: "x"}),
			Entry("code_challenge", OAuthParam{Name: "code_challenge", Value: "x"}),
			Entry("code_challenge_method", OAuthParam{Name: "code_challenge_method", Value: "x"}),
			Entry("grant_type", OAuthParam{Name: "grant_type", Value: "x"}),
			Entry("access_type other than offline", OAuthParam{Name: "access_type", Value: "online"}),
			Entry("include_granted_scopes other than true", OAuthParam{Name: "include_granted_scopes", Value: "false"}),
			Entry("a value with a space", OAuthParam{Name: "prompt", Value: "a b"}),
			Entry("a value with an ampersand", OAuthParam{Name: "prompt", Value: "a&b=c"}),
			Entry("an empty value", OAuthParam{Name: "prompt", Value: ""}),
			Entry("a value of 129 bytes", OAuthParam{Name: "prompt", Value: strings.Repeat("a", 129)}),
			Entry("a repeated name", OAuthParam{Name: "prompt", Value: "a"}, OAuthParam{Name: "prompt", Value: "b"}),
			Entry("five parameters",
				OAuthParam{Name: "prompt", Value: "a"}, OAuthParam{Name: "audience", Value: "a"}, OAuthParam{Name: "resource", Value: "a"},
				OAuthParam{Name: "access_type", Value: "offline"}, OAuthParam{Name: "include_granted_scopes", Value: "true"}),
		)
		It("keeps every protocol name out of the registry", func() {
			for _, s := range OAuthAuthorizeParams {
				Expect(oauthProtocolParam(s.Name)).To(BeFalse(), s.Name)
			}
		})
	})

	Describe("the digest", func() {
		It("is stable across field order, whitespace and list order", func() {
			a := valid()
			a.Hosts = []string{"auth.example.test", "api.example.test"}
			a.Scopes = OAuthScopes{Required: []string{"b", "a"}, Optional: []string{"d", "c"}}
			a.AuthorizeParams = []OAuthParam{{Name: "prompt", Value: "consent"}, {Name: "audience", Value: "x"}}
			b := valid()
			b.Hosts = []string{"api.example.test", "auth.example.test"}
			b.Scopes = OAuthScopes{Required: []string{"a", "b"}, Optional: []string{"c", "d"}}
			b.AuthorizeParams = []OAuthParam{{Name: "audience", Value: "x"}, {Name: "prompt", Value: "consent"}}
			Expect(a.Digest()).To(Equal(b.Digest()))

			var c OAuthDescriptor
			Expect(json.Unmarshal([]byte(`{
				"clientAuth": "basic", "pkce": "S256",
				"scopes": {"optional": ["write"], "required": ["read"]},
				"hosts": ["auth.example.test"],
				"revokeUrl": "https://auth.example.test/revoke",
				"tokenUrl": "https://auth.example.test/token",
				"authorizeUrl": "https://auth.example.test/authorize"}`), &c)).To(Succeed())
			Expect(c.Digest()).To(Equal(valid().Digest()))
		})
		It("changes with every field", func() {
			base := valid().Digest()
			Expect(base).To(MatchRegexp(`^[0-9a-f]{64}$`))
			for name, mod := range map[string]func(*OAuthDescriptor){
				"authorize": func(d *OAuthDescriptor) { d.AuthorizeURL = "https://auth.example.test/authorize2" },
				"token":     func(d *OAuthDescriptor) { d.TokenURL = "https://auth.example.test/token2" },
				"revoke":    func(d *OAuthDescriptor) { d.RevokeURL = "" },
				"hosts":     func(d *OAuthDescriptor) { d.Hosts = []string{"auth.example.test", "b.example.test"} },
				"required":  func(d *OAuthDescriptor) { d.Scopes.Required = []string{"read", "x"} },
				"optional":  func(d *OAuthDescriptor) { d.Scopes.Optional = nil },
				"client":    func(d *OAuthDescriptor) { d.ClientAuth = OAuthClientAuthPost },
				"params":    func(d *OAuthDescriptor) { d.AuthorizeParams = []OAuthParam{{Name: "prompt", Value: "consent"}} },
			} {
				d := valid()
				mod(&d)
				Expect(d.Digest()).NotTo(Equal(base), name)
			}
		})
		It("does not move a scope from one list to the other unseen", func() {
			a, b := valid(), valid()
			a.Scopes = OAuthScopes{Required: []string{"x"}, Optional: []string{"y"}}
			b.Scopes = OAuthScopes{Required: []string{"y"}, Optional: []string{"x"}}
			Expect(a.Digest()).NotTo(Equal(b.Digest()))
		})
		It("treats a missing and an empty list alike", func() {
			a, b := valid(), valid()
			a.AuthorizeParams, b.AuthorizeParams = nil, []OAuthParam{}
			Expect(a.Digest()).To(Equal(b.Digest()))
		})
	})
})
