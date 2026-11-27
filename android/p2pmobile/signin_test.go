// SPDX-License-Identifier: MIT

package p2pmobile

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/p2pnet"
	"github.com/mudler/LocalAI/core/services/p2ppair"
	"github.com/mudler/LocalAI/core/services/p2ppair/pairclient"
)

// redeemServer is a server with pairing open whose redeem route answers
// the base answer plus extra. It returns an invite link to it.
func redeemServer(extra map[string]any) string {
	GinkgoHelper()
	gate := p2pnet.NewAllowlist()
	gate.SetPairing(true)
	h, err := p2pnet.New(context.Background(), p2pnet.Config{
		IdentityPath: filepath.Join(GinkgoT().TempDir(), "server.key"),
		ListenAddrs:  []string{"/ip4/127.0.0.1/tcp/0"},
		Gate:         gate,
	})
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(h.Close)
	ln, err := h.Listen(p2pnet.ProtoHTTP, nil)
	Expect(err).ToNot(HaveOccurred())
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+pairclient.RedeemPath, func(w http.ResponseWriter, _ *http.Request) {
		answer := map[string]any{
			"device_id": "dev-1", "kind": "remote", "phrase": "apple banana cherry",
			"server": map[string]string{"peer_id": h.ID().String(), "name": "test", "version": "v1"},
		}
		maps.Copy(answer, extra)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(answer)
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	DeferCleanup(srv.Close)
	l := &p2ppair.Link{V: 1, Kind: p2ppair.KindRemote, Peer: h.ID(), Code: "ABCDE23456", Expires: time.Now().Add(time.Hour), Addrs: h.Libp2p().Addrs()}
	return l.String()
}

var _ = Describe("PairJSON of a sign-in invite", func() {
	It("puts the device credential, the sign-in flag and the owner name at the top level of the result", func() {
		a := pairJSON(redeemServer(map[string]any{"device_credential": "cred-secret", "sign_in": true, "owner_name": "alice"}), false)
		Expect(a.OK).To(BeTrue(), a.Detail)
		Expect(a.Result).To(HaveKeyWithValue("device_credential", "cred-secret"))
		Expect(a.Result).To(HaveKeyWithValue("sign_in", true))
		Expect(a.Result).To(HaveKeyWithValue("owner_name", "alice"))
		Expect(a.Result).To(HaveKeyWithValue("device_id", "dev-1"))
		Expect(a.Result["server"]).To(HaveKey("peer_id"))
	})

	It("has none of them when the server does not send them", func() {
		a := pairJSON(redeemServer(nil), false)
		Expect(a.OK).To(BeTrue(), a.Detail)
		Expect(a.Result).ToNot(HaveKey("device_credential"))
		Expect(a.Result).ToNot(HaveKey("sign_in"))
		Expect(a.Result).ToNot(HaveKey("owner_name"))
		Expect(a.Result).To(HaveKeyWithValue("device_id", "dev-1"))
	})
})
