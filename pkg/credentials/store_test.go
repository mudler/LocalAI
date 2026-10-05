package credentials_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/credentials"
)

var _ = Describe("Parse", func() {
	It("accepts an empty file", func() {
		Expect(mustParse("").Len()).To(Equal(0))
	})

	It("loads every valid entry kind", func() {
		s := mustParse(`
- match: ghcr.io/acme
  username: bot
  password_env: GHCR_TOKEN
- match: https://models.acme.internal/
  bearer_file: /var/run/secrets/acme/token
- match: artifactory.acme.internal
  header:
    name: X-JFrog-Art-Api
    value: key
`)
		Expect(s.Len()).To(Equal(3))
	})

	DescribeTable("rejects invalid entries",
		func(doc, msg string) {
			_, err := credentials.Parse([]byte(doc), noEnv)
			Expect(err).To(MatchError(ContainSubstring(msg)))
		},
		Entry("missing match", "- bearer: x\n", "match is required"),
		Entry("no auth kind", "- match: ghcr.io\n", "exactly one of"),
		Entry("two auth kinds", "- match: ghcr.io\n  bearer: x\n  username: u\n  password: p\n", "exactly one of"),
		Entry("username without password", "- match: ghcr.io\n  username: u\n", "username and password"),
		Entry("two forms for one secret", "- match: ghcr.io\n  bearer: x\n  bearer_env: Y\n", "only one of bearer"),
		Entry("header without name", "- match: ghcr.io\n  header:\n    value: x\n", "header.name is required"),
		Entry("header without value", "- match: ghcr.io\n  header:\n    name: X-Key\n", "header value is required"),
		Entry("misspelled key", "- match: ghcr.io\n  bearer: x\n  pasword: y\n", "pasword"),
		Entry("unsupported scheme", "- match: ftp://host\n  bearer: x\n", "unsupported scheme"),
	)

	It("does not echo secret literals in validation errors", func() {
		_, err := credentials.Parse([]byte("- match: ghcr.io\n  bearer: hunter2\n  bearer_env: ALSO\n"), noEnv)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).NotTo(ContainSubstring("hunter2"))
	})

	It("rejects userinfo in a match without echoing it", func() {
		_, err := credentials.Parse([]byte("- match: https://user:tok3n@ghcr.io\n  bearer: x\n"), noEnv)
		Expect(err).To(MatchError(ContainSubstring("userinfo")))
		Expect(err.Error()).NotTo(ContainSubstring("tok3n"))
	})

	DescribeTable("rejects a bad match without echoing the secret it holds",
		func(doc, msg, secret string) {
			_, err := credentials.Parse([]byte(doc), noEnv)
			Expect(err).To(MatchError(ContainSubstring(msg)))
			Expect(err.Error()).NotTo(ContainSubstring(secret))
		},
		Entry("userinfo with an unsupported scheme", "- match: ftp://user:tok123@host\n  bearer: x\n", "userinfo", "tok123"),
		Entry("query string", "- match: https://files.example.com/model?sig=abc123\n  bearer: x\n", "query", "abc123"),
		Entry("fragment", "- match: https://files.example.com/model#abc123\n  bearer: x\n", "fragment", "abc123"),
	)

	It("does not echo a secret from a YAML tag decode error", func() {
		_, err := credentials.Parse([]byte("- match: ghcr.io\n  password: !!int hunter2\n  username: u\n"), noEnv)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).NotTo(ContainSubstring("hunter2"))
	})

	DescribeTable("does not echo secret literals in YAML type errors",
		func(doc, secretFragment string) {
			_, err := credentials.Parse([]byte(doc), noEnv)
			Expect(err).To(MatchError(ContainSubstring("wrong type")))
			Expect(err.Error()).To(ContainSubstring("line 2"))
			Expect(err.Error()).NotTo(ContainSubstring(secretFragment))
		},
		Entry("short value", "- match: ghcr.io\n  header: hunter2\n", "hunter2"),
		Entry("long value", "- match: ghcr.io\n  header: supersecretvalue123\n", "superse"),
	)
})

var _ = Describe("Store.Match", func() {
	var store *credentials.Store

	BeforeEach(func() {
		store = mustParse(`
- match: ghcr.io/acme
  bearer: a
- match: ghcr.io/acme/private
  bearer: b
- match: https://files.example.com/models
  bearer: c
- match: github.com/acme
  bearer: d
- match: docker.io/acme
  bearer: e
- match: registry.lan:5000
  username: ci
  password: pw
  allow_insecure: true
- match: plain.example.com
  bearer: f
`)
	})

	DescribeTable("selects the rule for a URL",
		func(rawURL, want string) {
			c, ok := store.Match(rawURL)
			if want == "" {
				Expect(ok).To(BeFalse(), "unexpectedly matched %q", c.Match)
				return
			}
			Expect(ok).To(BeTrue())
			Expect(c.Match).To(Equal(want))
		},
		Entry("repository prefix", "https://ghcr.io/acme/img", "ghcr.io/acme"),
		Entry("exact path", "https://ghcr.io/acme", "ghcr.io/acme"),
		Entry("longest prefix wins", "https://ghcr.io/acme/private/img", "ghcr.io/acme/private"),
		Entry("path matches on segment boundaries", "https://ghcr.io/acme-evil/img", ""),
		Entry("host must be equal, not a suffix", "https://ghcr.io.evil.net/acme/img", ""),
		Entry("host is case-insensitive and default port is ignored", "https://GHCR.IO:443/acme/img", "ghcr.io/acme"),
		Entry("scheme in the rule is enforced", "http://files.example.com/models/a.gguf", ""),
		Entry("scheme in the rule matches", "https://files.example.com/models/a.gguf", "https://files.example.com/models"),
		Entry("raw.githubusercontent.com is matched as github.com", "https://raw.githubusercontent.com/acme/gallery/main/index.yaml", "github.com/acme"),
		Entry("docker.io is matched as index.docker.io", "https://index.docker.io/acme/img", "docker.io/acme"),
		Entry("plain http when the rule opts in", "http://registry.lan:5000/team/img", "registry.lan:5000"),
		Entry("no credentials over plain http by default", "http://plain.example.com/x", ""),
		Entry("unparseable target", "not a url", ""),
		Entry("scheme-relative target", "//ghcr.io/acme/img", ""),
		Entry("ftp target", "ftp://ghcr.io/acme/img", ""),
		Entry("websocket target", "ws://ghcr.io/acme/img", ""),
		Entry("dot-dot segment", "https://ghcr.io/acme/../other/img", ""),
		Entry("percent-encoded dot-dot segment", "https://ghcr.io/acme%2F..%2Fother/img", ""),
	)

	It("is safe on a nil store", func() {
		var nilStore *credentials.Store
		_, ok := nilStore.Match("https://ghcr.io/acme/img")
		Expect(ok).To(BeFalse())
		Expect(nilStore.Len()).To(Equal(0))
	})
})
