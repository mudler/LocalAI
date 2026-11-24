package agentworker_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/cli/workerregistry"
	"github.com/mudler/LocalAI/core/services/agentworker"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/worker"
)

var _ = Describe("The routes of an agent worker", func() {
	serve := agentworker.ServeConfig{NodeID: "node-1", FrontendURL: "http://127.0.0.1:1", ControlToken: "token"}

	It("has an attacher for NATS and one for the tunnel", func() {
		Expect(serve.Attachers()).To(HaveKey(worker.RouteNATS))
		Expect(serve.Attachers()).To(HaveKey(worker.RouteTunnel))
	})

	It("refuses the tunnel without a credential manager", func() {
		_, err := serve.Attachers()[worker.RouteTunnel](context.Background(), worker.Handover{Route: worker.RouteTunnel})
		Expect(err).To(MatchError(worker.ErrNoCredentials))
	})

	It("holds its tunnel over a websocket and says so", func() {
		creds := workerregistry.NewCredentialManagerFor(func(context.Context) (*workerregistry.RegisterResponse, error) {
			return &workerregistry.RegisterResponse{ID: "node-1", TunnelToken: "t"}, nil
		}, false, workerregistry.RouteTunnel)
		_, err := creds.Acquire(context.Background())
		Expect(err).ToNot(HaveOccurred())
		a, err := serve.Attachers()[worker.RouteTunnel](GinkgoT().Context(), worker.Handover{Route: worker.RouteTunnel, Creds: creds})
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(a.Close)
		tr, ok := a.(interface{ TunnelRoute() nodes.TunnelRoute })
		Expect(ok).To(BeTrue())
		Expect(tr.TunnelRoute()).To(Equal(nodes.TunnelRoute{Transport: nodes.TunnelWebSocket}))
		Expect(a.Connected()).To(BeFalse(), "nothing listens on the frontend URL")
	})

	It("refuses NATS when the server asks for a client certificate that the worker does not have", func() {
		_, err := serve.Attachers()[worker.RouteNATS](context.Background(), worker.Handover{
			Route: worker.RouteNATS, NATSURL: "tls://127.0.0.1:1", NATSClientTLS: true,
		})
		Expect(err).To(MatchError(ContainSubstring("client certificate")))
	})
})
