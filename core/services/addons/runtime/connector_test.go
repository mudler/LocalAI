//go:build linux

// SPDX-License-Identifier: MIT
package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the connector call of the runtime client", func() {
	credential := strings.Repeat("a", 64)

	It("posts the request with the incarnation credential and decodes the answer", func() {
		var got contract.ConnectorRequest
		var auth, inc string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth, inc = r.Header.Get("Authorization"), r.Header.Get("X-LocalAI-Incarnation")
			Expect(r.URL.Path).To(Equal("/connector"))
			Expect(json.NewDecoder(r.Body).Decode(&got)).To(Succeed())
			_ = json.NewEncoder(w).Encode(contract.ConnectorResponse{NextOffset: 9, Error: &contract.ConnectorError{Code: contract.ConnectorRateLimited, RetryAfter: 2}})
		}))
		DeferCleanup(srv.Close)
		reg := Registration{AddonID: "telegram", Generation: 1, Incarnation: "inc", Protocol: 1, Endpoint: srv.URL,
			Capabilities: contract.Capabilities{Connector: &contract.ConnectorCapability{Kinds: []contract.ConnectorKind{{ID: "telegram", Title: "Telegram", Delivery: contract.DeliveryPull}}}}}
		c, err := newClient(reg, credential, contract.DefaultLimits())
		Expect(err).NotTo(HaveOccurred())
		c.accept = true

		out, err := c.Connector(context.Background(), contract.ConnectorRequest{Op: "poll", Kind: "telegram", Token: "t", Offset: 4})
		Expect(err).NotTo(HaveOccurred())
		Expect(auth).To(Equal("Bearer " + credential))
		Expect(inc).To(Equal("inc"))
		Expect(got).To(Equal(contract.ConnectorRequest{Op: "poll", Kind: "telegram", Token: "t", Offset: 4}))
		Expect(out.NextOffset).To(Equal(int64(9)))
		Expect(out.Error.RetryAfter).To(Equal(2))
	})

	It("is not sent to an add-on that declared no connector", func() {
		reg := Registration{AddonID: "tables", Generation: 1, Incarnation: "inc", Protocol: 1, Endpoint: "http://127.0.0.1:1"}
		c, err := newClient(reg, credential, contract.DefaultLimits())
		Expect(err).NotTo(HaveOccurred())
		c.accept = true
		_, err = c.Connector(context.Background(), contract.ConnectorRequest{Op: "me"})
		Expect(err).To(MatchError(ErrNotSent))
	})

	Describe("long polls and short calls", func() {
		// An add-on whose poll waits until released and whose other ops answer at once.
		var release chan struct{}
		var started chan struct{}
		newPollClient := func() *Client {
			release, started = make(chan struct{}), make(chan struct{}, 4*maxPolls)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req contract.ConnectorRequest
				Expect(json.NewDecoder(r.Body).Decode(&req)).To(Succeed())
				if req.Op == contract.ConnectorOpPoll {
					started <- struct{}{}
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
				}
				_ = json.NewEncoder(w).Encode(contract.ConnectorResponse{})
			}))
			DeferCleanup(srv.Close)
			reg := Registration{AddonID: "telegram", Generation: 1, Incarnation: "inc", Protocol: 1, Endpoint: srv.URL,
				Capabilities: contract.Capabilities{Connector: &contract.ConnectorCapability{Kinds: []contract.ConnectorKind{{ID: "telegram", Title: "Telegram", Delivery: contract.DeliveryPull}}}}}
			c, err := newClient(reg, credential, contract.DefaultLimits())
			Expect(err).NotTo(HaveOccurred())
			c.accept = true
			return c
		}
		poll := func(ctx context.Context, c *Client) chan error {
			out := make(chan error, 1)
			go func() {
				_, err := c.Connector(ctx, contract.ConnectorRequest{Op: contract.ConnectorOpPoll, Kind: "telegram", Token: "t"})
				out <- err
			}()
			return out
		}
		short := func(c *Client) error {
			_, err := c.Connector(context.Background(), contract.ConnectorRequest{Op: contract.ConnectorOpMe, Kind: "telegram", Token: "t"})
			return err
		}

		It("keeps the short-call slots free while more polls than the concurrency limit are open", func() {
			c := newPollClient()
			Expect(contract.DefaultLimits().Concurrency).To(BeEquivalentTo(4))
			ctx, cancel := context.WithCancel(context.Background())
			DeferCleanup(cancel)
			var done []chan error
			for i := 0; i < 8; i++ {
				done = append(done, poll(ctx, c))
			}
			for i := 0; i < 8; i++ {
				Eventually(started).Should(Receive())
			}
			for i := 0; i < 4; i++ { // the full short-call budget is still there
				Expect(short(c)).To(Succeed())
			}
			close(release)
			for _, d := range done {
				Eventually(d).Should(Receive(BeNil()))
			}
		})

		It("refuses a poll past the poll pool, and frees the slot when a poll is cancelled", func() {
			c := newPollClient()
			ctx, cancel := context.WithCancel(context.Background())
			DeferCleanup(cancel)
			var done []chan error
			for i := 0; i < maxPolls; i++ {
				done = append(done, poll(ctx, c))
			}
			for i := 0; i < maxPolls; i++ {
				Eventually(started).Should(Receive())
			}
			_, err := c.Connector(context.Background(), contract.ConnectorRequest{Op: contract.ConnectorOpPoll, Kind: "telegram", Token: "t"})
			Expect(err).To(MatchError(ErrNotSent))
			Expect(short(c)).To(Succeed())

			cancel()
			for _, d := range done {
				Eventually(d).Should(Receive(Not(BeNil())))
			}
			Eventually(func() int { c.mu.Lock(); defer c.mu.Unlock(); return c.polls }).Should(BeZero())
			close(release)
			Eventually(poll(context.Background(), c)).Should(Receive(BeNil()))
		})

		It("refuses a poll after the client is withdrawn and waits for open polls to drain", func() {
			c := newPollClient()
			d := poll(context.Background(), c)
			Eventually(started).Should(Receive())
			c.withdraw()
			_, err := c.Connector(context.Background(), contract.ConnectorRequest{Op: contract.ConnectorOpPoll, Kind: "telegram", Token: "t"})
			Expect(err).To(MatchError(ErrNotSent))
			Expect(short(c)).To(MatchError(ErrNotSent))

			drained := make(chan struct{})
			go func() { c.drain(context.Background()); close(drained) }()
			Consistently(drained, "50ms").ShouldNot(BeClosed())
			close(release)
			Eventually(d).Should(Receive(BeNil()))
			Eventually(drained).Should(BeClosed())
		})
	})
})
