// SPDX-License-Identifier: MIT
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"

	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/hostops"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Host requirements carrier", func() {
	It("preserves bounded owner admission refusals over mTLS without frontend grants", func() {
		actor := contract.Actor{UserID: "admin", CredentialID: "key:admin", Mode: "named-user"}
		raw := `{"schemaVersion":1,"id":"sample","version":"1.2.3","executable":"bin/sample","os":"linux","arch":"amd64","host":{"min":1,"max":2},"api":{"min": 2, "max": 2},"protocol":{"min":1,"max":1},"dataFormat":{"min":1,"max":2},"capabilities":{"service":true,"hostOperations":{"required":["workspace.materialize"]}}}`
		m := ownerManagerPackage(actor, false, raw, nil, 1, map[string][]string{})
		owner := &Gateway{Manager: m, Revalidate: func(context.Context, contract.Actor) error { return nil }}
		_, cs := carrierFixture(func(c *Carrier) http.Handler {
			h, e := c.OwnerHandler(owner)
			Expect(e).NotTo(HaveOccurred())
			return h
		})
		front := &Gateway{Forward: cs["front-a"], Revalidate: func(context.Context, contract.Actor) error { return nil }}
		_, e := front.Dispatch(context.Background(), actor, contract.Request{Action: "install", AddonID: "sample", OperationID: "required-missing", Arguments: json.RawMessage(`{"sourceId":"test","version":"1.2.3"}`)})
		Expect(e).To(HaveOccurred())
		var refusal *addons.HostRequirementsRefusal
		Expect(errors.As(e, &refusal)).To(BeTrue())
		Expect(refusal.Summary.MissingRequired).To(Equal([]string{"workspace.materialize"}))
		st, e := m.Status(context.Background(), "sample")
		Expect(e).NotTo(HaveOccurred())
		Expect(st.Installed).To(BeFalse())
		rec := httptest.NewRecorder()
		typedError(rec, &addons.HostRequirementsRefusal{Summary: hostops.RequestSummary{MissingRequired: []string{"workspace.materialize"}}})
		Expect(rec.Code).To(Equal(http.StatusForbidden))
		Expect(rec.Body.String()).To(ContainSubstring("workspace.materialize"))
		Expect(rec.Body.String()).NotTo(ContainSubstring("directory"))
	})
})
