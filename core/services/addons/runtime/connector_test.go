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
})
