package workerregistry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/workerctl"
)

var _ = Describe("The split report of a node", func() {
	It("sends the report with the node credential and returns the attached workers", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			Expect(r.Method).To(Equal(http.MethodPost))
			Expect(r.URL.Path).To(Equal("/api/node/n1/split"))
			Expect(r.Header.Get("Authorization")).To(Equal("Bearer tok"))
			var rep workerctl.SplitReport
			Expect(json.NewDecoder(r.Body).Decode(&rep)).To(Succeed())
			Expect(rep.PeerID).To(Equal("peer-n1"))
			_ = json.NewEncoder(w).Encode(workerctl.SplitAssignment{
				Workers:   []workerctl.SplitAssigned{{DeviceID: "d1", PeerID: "peer-w", Engine: "llama"}},
				OwnRelays: []string{"relay-1"},
			})
		}))
		DeferCleanup(srv.Close)
		c := &RegistrationClient{FrontendURL: srv.URL, RegistrationToken: "tok"}
		got, err := c.SplitSync(context.Background(), "n1", workerctl.SplitReport{PeerID: "peer-n1"})
		Expect(err).ToNot(HaveOccurred())
		Expect(got.Workers).To(HaveLen(1))
		Expect(got.Workers[0].PeerID).To(Equal("peer-w"))
		Expect(got.OwnRelays).To(ConsistOf("relay-1"))
	})

	It("fails on a frontend that does not serve the route, so the node keeps its workers", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusConflict) }))
		DeferCleanup(srv.Close)
		c := &RegistrationClient{FrontendURL: srv.URL}
		_, err := c.SplitSync(context.Background(), "n1", workerctl.SplitReport{})
		Expect(err).To(MatchError(ContainSubstring("409")))
	})
})
