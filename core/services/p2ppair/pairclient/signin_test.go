package pairclient_test

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/p2pnet"
	"github.com/mudler/LocalAI/core/services/p2ppair/pairclient"
)

// redeemAnswering starts a server whose redeem route answers the base
// answer plus extra, and redeems a link to it.
func redeemAnswering(extra map[string]any) *pairclient.Result {
	GinkgoHelper()
	gate := p2pnet.NewAllowlist()
	gate.SetPairing(true)
	server := newHost(p2pnet.Config{Gate: gate})
	l, err := server.Listen(p2pnet.ProtoHTTP, nil)
	Expect(err).ToNot(HaveOccurred())
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+pairclient.RedeemPath, func(w http.ResponseWriter, _ *http.Request) {
		answer := map[string]any{
			"device_id": "dev-1", "kind": "device", "phrase": "apple banana cherry",
			"server": map[string]string{"peer_id": server.ID().String(), "name": "test"},
		}
		maps.Copy(answer, extra)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(answer)
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(l) }()
	DeferCleanup(srv.Close)

	device := newHost(p2pnet.Config{})
	lk := link(server.ID())
	lk.Addrs = server.Libp2p().Addrs()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := pairclient.Redeem(ctx, device, lk, pairclient.Request{Name: "phone"})
	Expect(err).ToNot(HaveOccurred())
	return out
}

var _ = Describe("Redeem of a sign-in invite", func() {
	It("returns the device credential, the sign-in flag and the owner name of the answer", func() {
		out := redeemAnswering(map[string]any{"device_credential": "cred-secret", "sign_in": true, "owner_name": "alice"})
		Expect(out.DeviceCredential).To(Equal("cred-secret"))
		Expect(out.SignIn).To(BeTrue())
		Expect(out.OwnerName).To(Equal("alice"))
	})

	It("leaves them empty when the server does not send them", func() {
		out := redeemAnswering(nil)
		Expect(out.DeviceID).To(Equal("dev-1"))
		Expect(out.DeviceCredential).To(BeEmpty())
		Expect(out.SignIn).To(BeFalse())
		Expect(out.OwnerName).To(BeEmpty())
	})
})
