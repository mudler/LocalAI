package workerregistry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("A credential for the carrier a worker follows to", func() {
	It("waits for the tunnel token when it asks for the tunnel, although the active carrier is NATS", func() {
		f := &fakeRegister{steps: []step{
			{res: &RegisterResponse{ID: "n", Status: "healthy", Carrier: "nats", CredentialFor: "tunnel"}},
			{res: &RegisterResponse{ID: "n", Status: "healthy", Carrier: "nats", CredentialFor: "tunnel", TunnelToken: "tok"}},
		}}
		m := NewCredentialManagerFor(f.fn(), false, "tunnel")
		m.initialBackoff = 1
		res, err := m.Acquire(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(res.TunnelToken).To(Equal("tok"))
		Expect(m.TunnelToken()).To(Equal("tok"))
		Expect(f.count()).To(Equal(2))
	})

	It("takes the NATS credential while the tunnel is the active carrier", func() {
		f := &fakeRegister{steps: []step{{res: &RegisterResponse{ID: "n", Status: "healthy", Carrier: "tunnel", CredentialFor: "nats", NatsJWT: "j", NatsUserSeed: "s", NatsURL: "nats://x:4222"}}}}
		m := NewCredentialManagerFor(f.fn(), true, "nats")
		res, err := m.Acquire(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(res.NatsURL).To(Equal("nats://x:4222"))
		jwt, seed := m.Current()
		Expect(jwt).To(Equal("j"))
		Expect(seed).To(Equal("s"))
	})

	It("does not take the credential of another carrier for the one it asked for", func() {
		f := &fakeRegister{steps: []step{{res: &RegisterResponse{ID: "n", Status: "healthy", Carrier: "nats", CredentialFor: "nats", NatsJWT: "j", NatsUserSeed: "s"}}}}
		m := NewCredentialManagerFor(f.fn(), false, "tunnel")
		m.maxAttempts = 2
		m.initialBackoff = 1
		_, err := m.Acquire(context.Background())
		Expect(err).To(MatchError(ContainSubstring("tunnel")))
		Expect(m.TunnelToken()).To(BeEmpty())
	})

	It("asks again for the same carrier when it registers again", func() {
		f := &fakeRegister{steps: []step{
			{res: &RegisterResponse{ID: "n", Status: "healthy", Carrier: "nats", CredentialFor: "tunnel", TunnelToken: "a"}},
			{res: &RegisterResponse{ID: "n", Status: "healthy", Carrier: "nats", CredentialFor: "tunnel", TunnelToken: "b"}},
		}}
		m := NewCredentialManagerFor(f.fn(), false, "tunnel")
		_, err := m.Acquire(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(m.Reregister(context.Background())).To(Succeed())
		Expect(m.TunnelToken()).To(Equal("b"))
	})
})

var _ = Describe("The heartbeat client", func() {
	It("returns what the frontend says about the carrier", func() {
		var calls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			Expect(r.URL.Path).To(Equal("/api/node/n1/heartbeat"))
			var body map[string]any
			Expect(json.NewDecoder(r.Body).Decode(&body)).To(Succeed())
			Expect(body).To(HaveKey("follow_capabilities"))
			_ = json.NewEncoder(w).Encode(map[string]any{"message": "heartbeat received", "carrier": "nats", "carrier_epoch": 4, "carrier_state": "prepare", "carrier_target": "tunnel", "nats_client_tls": true})
		}))
		DeferCleanup(srv.Close)
		c := &RegistrationClient{FrontendURL: srv.URL}
		reply, err := c.HeartbeatFull(context.Background(), "n1", map[string]any{"follow_capabilities": []string{"nats"}})
		Expect(err).ToNot(HaveOccurred())
		Expect(reply.Carrier).To(Equal("nats"))
		Expect(reply.CarrierEpoch).To(BeEquivalentTo(4))
		Expect(reply.CarrierState).To(Equal("prepare"))
		Expect(reply.CarrierTarget).To(Equal("tunnel"))
		Expect(reply.NatsClientTLS).To(BeTrue())
	})

	It("returns an empty answer from a frontend that predates carriers", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "heartbeat received"})
		}))
		DeferCleanup(srv.Close)
		c := &RegistrationClient{FrontendURL: srv.URL}
		reply, err := c.HeartbeatFull(context.Background(), "n1", nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(reply.Carrier).To(BeEmpty())
	})

	It("reports a refusal and a node the frontend does not know", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) }))
		DeferCleanup(srv.Close)
		c := &RegistrationClient{FrontendURL: srv.URL}
		_, err := c.HeartbeatFull(context.Background(), "n1", nil)
		Expect(err).To(HaveOccurred())
	})

	It("keeps the heartbeat that returns only an error working as before", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
		DeferCleanup(srv.Close)
		c := &RegistrationClient{FrontendURL: srv.URL}
		Expect(c.Heartbeat(context.Background(), "n1", nil)).To(Succeed())
	})
})
