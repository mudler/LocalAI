package workerregistry

import (
	"context"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("A credential for one route of a worker", func() {
	It("waits for the tunnel token on the tunnel route, whether or not the node is approved", func() {
		f := &fakeRegister{steps: []step{
			{res: &RegisterResponse{ID: "n", Status: "pending"}},
			{res: &RegisterResponse{ID: "n", Status: "pending", TunnelToken: "tok"}},
		}}
		m := NewCredentialManagerFor(f.fn(), true, RouteTunnel)
		m.initialBackoff = 1
		res, err := m.Acquire(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(res.TunnelToken).To(Equal("tok"))
		Expect(m.TunnelToken()).To(Equal("tok"))
		Expect(f.count()).To(Equal(2))
	})

	It("takes the NATS credential and the address of NATS on the NATS route", func() {
		f := &fakeRegister{steps: []step{{res: &RegisterResponse{ID: "n", Status: "healthy", NatsJWT: "j", NatsUserSeed: "s", NatsURL: "nats://x:4222"}}}}
		m := NewCredentialManagerFor(f.fn(), true, RouteNATS)
		res, err := m.Acquire(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(res.NatsURL).To(Equal("nats://x:4222"))
		jwt, seed := m.Current()
		Expect(jwt).To(Equal("j"))
		Expect(seed).To(Equal("s"))
		Expect(m.TunnelToken()).To(BeEmpty())
	})

	It("does not take a NATS credential for the tunnel route", func() {
		f := &fakeRegister{steps: []step{{res: &RegisterResponse{ID: "n", Status: "healthy", NatsJWT: "j", NatsUserSeed: "s"}}}}
		m := NewCredentialManagerFor(f.fn(), false, RouteTunnel)
		m.maxAttempts = 2
		m.initialBackoff = 1
		_, err := m.Acquire(context.Background())
		Expect(err).To(MatchError(ContainSubstring("tunnel")))
		Expect(m.TunnelToken()).To(BeEmpty())
	})

	It("asks again for the same route when it registers again", func() {
		f := &fakeRegister{steps: []step{
			{res: &RegisterResponse{ID: "n", Status: "healthy", TunnelToken: "a"}},
			{res: &RegisterResponse{ID: "n", Status: "healthy", TunnelToken: "b"}},
		}}
		m := NewCredentialManagerFor(f.fn(), false, RouteTunnel)
		_, err := m.Acquire(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(m.Reregister(context.Background())).To(Succeed())
		Expect(m.TunnelToken()).To(Equal("b"))
	})
})

var _ = Describe("The heartbeat client", func() {
	It("sends a heartbeat that the frontend takes", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			Expect(r.URL.Path).To(Equal("/api/node/n1/heartbeat"))
			_, _ = w.Write([]byte(`{"message":"heartbeat received"}`))
		}))
		DeferCleanup(srv.Close)
		c := &RegistrationClient{FrontendURL: srv.URL}
		Expect(c.SendHeartbeat(context.Background(), "n1", map[string]any{"routes": []string{"nats"}})).To(Succeed())
	})

	It("reports a refusal and a node the frontend does not know", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) }))
		DeferCleanup(srv.Close)
		c := &RegistrationClient{FrontendURL: srv.URL}
		Expect(c.SendHeartbeat(context.Background(), "n1", nil)).To(MatchError(ContainSubstring("404")))
	})

	It("keeps the heartbeat that fails only when the request cannot be made", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) }))
		DeferCleanup(srv.Close)
		c := &RegistrationClient{FrontendURL: srv.URL}
		Expect(c.Heartbeat(context.Background(), "n1", nil)).To(Succeed())
	})
})
