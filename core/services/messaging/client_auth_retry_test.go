// SPDX-License-Identifier: MIT
package messaging

import (
	"fmt"
	"net"

	"github.com/mudler/LocalAI/pkg/natsauth"
	"github.com/nats-io/nkeys"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Dynamic credential auth retry scope", func() {
	It("opts out of terminal auth retries only for a non-nil provider", func() {
		account, err := nkeys.CreateAccount()
		Expect(err).NotTo(HaveOccurred())
		defer account.Wipe()
		seed, err := account.Seed()
		Expect(err).NotTo(HaveOccurred())
		token, userSeed, err := (natsauth.Config{AccountSeed: string(seed)}).MintWorkerJWT("retry-scope", "backend")
		Expect(err).NotTo(HaveOccurred())
		// RetryOnFailedConnect lets us inspect the actual connection options without
		// a broker; the live worker fixture separately proves recovery behavior.
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		url := fmt.Sprintf("nats://%s", listener.Addr())
		Expect(listener.Close()).To(Succeed())
		provider := WithUserJWTProvider(func() (string, string) { return token, userSeed })
		for _, tc := range []struct {
			opts  []Option
			retry bool
		}{
			{nil, false},
			{[]Option{WithUserJWT(token, userSeed)}, false},
			{[]Option{WithUserJWTProvider(nil)}, false},
			{[]Option{provider}, true},
			{[]Option{WithUserJWT(token, userSeed), provider}, true},
		} {
			client, err := New(url, tc.opts...)
			Expect(err).NotTo(HaveOccurred())
			Expect(client.conn.Opts.IgnoreAuthErrorAbort).To(Equal(tc.retry))
			client.Close()
		}
	})
})
