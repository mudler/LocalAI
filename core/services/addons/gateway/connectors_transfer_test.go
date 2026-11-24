// SPDX-License-Identifier: MIT
package gateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"

	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/hostops"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// handleSource is an add-on that takes handles: it runs fn during the call.
type handleSource struct {
	transfer bool
	fn       func(contract.ConnectorRequest) contract.ConnectorResponse
}

func (s handleSource) ConnectorKinds(context.Context) ([]addons.ConnectorKind, error) {
	k := contract.ConnectorKind{ID: "slack", Delivery: contract.DeliveryPull}
	if s.transfer {
		k.Files = &contract.ConnectorFiles{Receive: &contract.FileAbility{MaxBytes: 1 << 30}}
	}
	return []addons.ConnectorKind{{AddonID: "slack", ConnectorKind: k}}, nil
}

func (s handleSource) Connector(_ context.Context, _ string, r contract.ConnectorRequest) (contract.ConnectorResponse, error) {
	return s.fn(r), nil
}

var _ = Describe("Connector calls that carry a file handle", func() {
	scope := connectors.ConnectorCallScope{AddonID: "slack", Kind: "slack", BindingID: "b1"}
	var (
		t    *hostops.Transfers
		host http.Handler
	)
	BeforeEach(func() {
		blobs, err := blob.NewFS(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		t = hostops.NewTransfers(blobs)
		b := hostops.New(hostops.NewInFlight(), func(string, string) bool { return true }, contract.DefaultLimits())
		Expect(hostops.RegisterConnectorTransfer(b, t)).To(Succeed())
		host = b.Handler("slack", "k", []string{"connector."})
	})
	put := func(h string, off int64, data []byte, final bool) int {
		raw, _ := json.Marshal(contract.ConnectorFilePut{Handle: h, Offset: off, Data: data, Final: final})
		body, _ := json.Marshal(contract.HostRequest{OperationID: "o", Op: contract.ConnectorFilePutOp, Arguments: raw})
		req := httptest.NewRequest(http.MethodPost, "/host", bytes.NewReader(body))
		req.Header.Set(contract.HostCredentialHeader, "k")
		rec := httptest.NewRecorder()
		host.ServeHTTP(rec, req)
		return rec.Code
	}
	media := func(s handleSource, tr *hostops.Transfers) (contract.ConnectorResponse, error) {
		g := &Gateway{Connectors: s, ConnectorAuthority: allowAllConnectors{}, Transfers: tr}
		return g.ConnectorCall(context.Background(), scope, contract.ConnectorRequest{Op: contract.ConnectorOpMedia, Kind: "slack", Token: "t",
			Transfer: &contract.ConnectorTransfer{Handle: "hh", MaxBytes: 100}})
	}

	It("registers the handle for the call and gives the host's size and hash, not the add-on's", func() {
		src := handleSource{transfer: true, fn: func(r contract.ConnectorRequest) contract.ConnectorResponse {
			Expect(t.Live()).To(Equal(1))
			Expect(put(r.Transfer.Handle, 0, []byte("hello"), true)).To(Equal(http.StatusOK))
			return contract.ConnectorResponse{Received: &contract.ConnectorReceived{Name: "a.txt", Size: 999, SHA256: "forged"}}
		}}
		out, err := media(src, t)
		Expect(err).NotTo(HaveOccurred())
		sum := sha256.Sum256([]byte("hello"))
		Expect(out.Received.SHA256).To(Equal(hex.EncodeToString(sum[:])))
		Expect(out.Received.Size).To(BeEquivalentTo(5))
		Expect(t.Live()).To(BeZero(), "the handle ends with the call")
	})

	It("drops an answer for a file the add-on did not finish", func() {
		src := handleSource{transfer: true, fn: func(r contract.ConnectorRequest) contract.ConnectorResponse {
			Expect(put(r.Transfer.Handle, 0, []byte("half"), false)).To(Equal(http.StatusOK))
			return contract.ConnectorResponse{Received: &contract.ConnectorReceived{Name: "a.txt", Size: 4, SHA256: "forged"}}
		}}
		out, err := media(src, t)
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Received).To(BeNil())
		Expect(t.Live()).To(BeZero())
	})

	It("refuses a handle for a kind that declared no files, and without a table", func() {
		src := handleSource{fn: func(contract.ConnectorRequest) contract.ConnectorResponse { return contract.ConnectorResponse{} }}
		_, err := media(src, t)
		Expect(err).To(MatchError(ErrForbidden))
		src.transfer = true
		_, err = media(src, nil)
		Expect(err).To(MatchError(ErrUnavailable))
	})

	It("refuses a media or send_file call that carries no handle", func() {
		src := handleSource{transfer: true, fn: func(contract.ConnectorRequest) contract.ConnectorResponse { return contract.ConnectorResponse{} }}
		g := &Gateway{Connectors: src, ConnectorAuthority: allowAllConnectors{}, Transfers: t}
		for _, op := range []string{contract.ConnectorOpMedia, contract.ConnectorOpSendFile} {
			_, err := g.ConnectorCall(context.Background(), scope, contract.ConnectorRequest{Op: op, Kind: "slack", Token: "t", Media: "ref"})
			Expect(err).To(MatchError(ErrForbidden), op)
		}
	})

	It("refuses a handle on an op that moves no file, and an outbound handle with no content", func() {
		src := handleSource{transfer: true, fn: func(contract.ConnectorRequest) contract.ConnectorResponse { return contract.ConnectorResponse{} }}
		g := &Gateway{Connectors: src, ConnectorAuthority: allowAllConnectors{}, Transfers: t}
		_, err := g.ConnectorCall(context.Background(), scope, contract.ConnectorRequest{Op: contract.ConnectorOpSend, Kind: "slack", Transfer: &contract.ConnectorTransfer{Handle: "x"}})
		Expect(err).To(MatchError(ErrForbidden))
		_, err = g.ConnectorCall(context.Background(), scope, contract.ConnectorRequest{Op: contract.ConnectorOpSendFile, Kind: "slack", Transfer: &contract.ConnectorTransfer{Handle: "x", Size: 1}})
		Expect(err).To(MatchError(ErrForbidden), "the frontend names the content in the scope")
	})
})
