package config

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type stubResolver struct {
	key string
	err error
}

func (s stubResolver) ResolveKey(context.Context, string) (string, error) { return s.key, s.err }

var _ = Describe("ProxyKeyResolver", func() {
	AfterEach(func() { SetProxyKeyResolver(nil) })

	It("fails when none is set", func() {
		_, err := ResolveProxyConnectionKey(context.Background(), "c1")
		Expect(err).To(MatchError(`proxy connection "c1": key unavailable`))
	})
	It("returns the key", func() {
		SetProxyKeyResolver(stubResolver{key: "k"})
		Expect(ResolveProxyConnectionKey(context.Background(), "c1")).To(Equal("k"))
	})
	It("wraps the resolver error", func() {
		cause := errors.New("missing")
		SetProxyKeyResolver(stubResolver{err: cause})
		_, err := ResolveProxyConnectionKey(context.Background(), "c1")
		Expect(err).To(MatchError(cause))
		Expect(err.Error()).To(ContainSubstring("key unavailable"))
	})
	It("can be removed again", func() {
		SetProxyKeyResolver(stubResolver{key: "k"})
		SetProxyKeyResolver(nil)
		_, err := ResolveProxyConnectionKey(context.Background(), "c1")
		Expect(err).To(HaveOccurred())
	})
})
