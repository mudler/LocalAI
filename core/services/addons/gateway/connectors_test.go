// SPDX-License-Identifier: MIT
package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type fakeConnectorSource struct{ kindsErr, callErr error }

func (s fakeConnectorSource) ConnectorKinds(context.Context) ([]addons.ConnectorKind, error) {
	if s.kindsErr != nil {
		return nil, s.kindsErr
	}
	return []addons.ConnectorKind{{AddonID: "slack", ConnectorKind: contract.ConnectorKind{ID: "slack", Delivery: contract.DeliveryPush}}}, nil
}

func (s fakeConnectorSource) Connector(context.Context, string, contract.ConnectorRequest) (contract.ConnectorResponse, error) {
	return contract.ConnectorResponse{}, s.callErr
}

type allowAllConnectors struct{}

func (allowAllConnectors) AuthorizeConnector(_ context.Context, _ connectors.ConnectorCallScope, r contract.ConnectorRequest) (contract.ConnectorRequest, error) {
	return r, nil
}

var _ = Describe("Connector errors", func() {
	scope := connectors.ConnectorCallScope{AddonID: "slack", Kind: "slack"}
	call := func(src fakeConnectorSource) error {
		g := &Gateway{Connectors: src, ConnectorAuthority: allowAllConnectors{}}
		_, err := g.ConnectorCall(context.Background(), scope, contract.ConnectorRequest{Op: contract.ConnectorOpMe, Kind: "slack"})
		return err
	}

	It("keeps the cause of an outage and still reports it as unavailable", func() {
		cause := errors.New("socket closed")
		err := call(fakeConnectorSource{callErr: cause})
		Expect(err).To(MatchError(ErrUnavailable))
		Expect(err).To(MatchError(cause))

		_, err = (&Gateway{Connectors: fakeConnectorSource{kindsErr: cause}}).ConnectorKinds(context.Background())
		Expect(err).To(MatchError(ErrUnavailable))
		Expect(err).To(MatchError(cause))
	})

	It("tells a definite refusal of the add-on apart from an outage", func() {
		err := call(fakeConnectorSource{callErr: fmt.Errorf("runtime: %w", contract.ErrRefused)})
		Expect(err).To(MatchError(ErrInvalidRequest))
		Expect(err).NotTo(MatchError(ErrUnavailable))

		err = call(fakeConnectorSource{callErr: fmt.Errorf("runtime: %w", contract.ErrDenied)})
		Expect(err).To(MatchError(ErrNotPermitted))
		Expect(err).NotTo(MatchError(ErrUnavailable))
	})
})

var _ = Describe("Forwarded connector answers", func() {
	forwarded := func(status int) error {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
		defer server.Close()
		c := &Carrier{config: CarrierConfig{OwnerAddress: server.URL}, client: server.Client()}
		return c.connectorRequest(context.Background(), "GET", "/addons/connectors/kinds", nil, &[]connectors.Kind{}, connectorTimeout)
	}

	It("keeps a refusal apart from an outage", func() {
		Expect(forwarded(http.StatusBadRequest)).To(MatchError(ErrInvalidRequest))
		Expect(forwarded(http.StatusForbidden)).To(MatchError(ErrForbidden))
		err := forwarded(http.StatusServiceUnavailable)
		Expect(err).To(MatchError(ErrUnavailable))
		Expect(err).NotTo(MatchError(ErrForbidden))
	})
})
