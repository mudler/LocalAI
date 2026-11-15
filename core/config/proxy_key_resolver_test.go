package config

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type stubResolver struct {
	rk  ProxyRemoteKey
	err error
}

func (s stubResolver) ResolveKey(context.Context, string) (ProxyRemoteKey, error) { return s.rk, s.err }

var _ = Describe("ProxyKeyResolver", func() {
	AfterEach(func() { SetProxyKeyResolver(nil) })

	const up = "https://api.example.com/v1/chat/completions"
	bound := ProxyRemoteKey{Key: "k", Kind: "openai", Base: "https://api.example.com/v1"}

	It("fails when none is set", func() {
		_, err := ResolveProxyRemoteKey(context.Background(), "c1", up)
		Expect(err).To(MatchError(`proxy remote "c1": key unavailable`))
	})
	It("returns the key", func() {
		SetProxyKeyResolver(stubResolver{rk: bound})
		Expect(ResolveProxyRemoteKey(context.Background(), "c1", up)).To(Equal("k"))
	})
	It("refuses an address of another server and does not echo the key", func() {
		SetProxyKeyResolver(stubResolver{rk: ProxyRemoteKey{Key: "sk-SECRET", Kind: "openai", Base: "https://api.example.com/v1"}})
		_, err := ResolveProxyRemoteKey(context.Background(), "c1", "https://attacker.example.net/v1/chat/completions")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("not an address of this openai remote"))
		Expect(err.Error()).NotTo(ContainSubstring("SECRET"))
	})
	It("wraps the resolver error", func() {
		cause := errors.New("missing")
		SetProxyKeyResolver(stubResolver{err: cause})
		_, err := ResolveProxyRemoteKey(context.Background(), "c1", up)
		Expect(err).To(MatchError(cause))
		Expect(err.Error()).To(ContainSubstring("key unavailable"))
	})
	It("can be removed again", func() {
		SetProxyKeyResolver(stubResolver{rk: bound})
		SetProxyKeyResolver(nil)
		_, err := ResolveProxyRemoteKey(context.Background(), "c1", up)
		Expect(err).To(HaveOccurred())
	})
})

var _ = DescribeTable("UpstreamWithinBase",
	func(upstream, base string, want bool) {
		Expect(UpstreamWithinBase(upstream, base)).To(Equal(want))
	},
	Entry("same endpoint", "https://api.example.com/v1/chat/completions", "https://api.example.com/v1", true),
	Entry("base itself", "https://gw.example.com/ai", "https://gw.example.com/ai", true),
	Entry("trailing slash", "https://gw.example.com/ai/", "https://gw.example.com/ai", true),
	Entry("root base", "http://10.0.0.2:8080/anything", "http://10.0.0.2:8080", true),
	Entry("explicit default https port", "https://api.example.com:443/v1/chat/completions", "https://api.example.com/v1", true),
	Entry("explicit default http port on base", "http://h.example.com/x", "http://h.example.com:80", true),
	Entry("host case", "https://API.Example.com/v1/x", "https://api.example.com/v1", true),
	Entry("other host", "https://attacker.example.net/v1/chat/completions", "https://api.example.com/v1", false),
	Entry("suffix host", "https://api.example.com.attacker.net/v1", "https://api.example.com/v1", false),
	Entry("other port", "https://api.example.com:8443/v1/chat/completions", "https://api.example.com/v1", false),
	Entry("other scheme", "http://api.example.com/v1/chat/completions", "https://api.example.com/v1", false),
	Entry("outside the path", "https://api.example.com/v2/chat/completions", "https://api.example.com/v1", false),
	Entry("path prefix not on a segment", "https://api.example.com/v10/chat", "https://api.example.com/v1", false),
	Entry("dot dot", "https://api.example.com/v1/../admin", "https://api.example.com/v1", false),
	Entry("user info", "https://u:p@api.example.com/v1/chat/completions", "https://api.example.com/v1", false),
	Entry("not a url", "::", "https://api.example.com/v1", false),
	Entry("empty", "", "https://api.example.com/v1", false),
	Entry("ftp", "ftp://api.example.com/v1", "ftp://api.example.com/v1", false),
)
