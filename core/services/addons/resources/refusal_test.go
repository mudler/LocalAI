// SPDX-License-Identifier: MIT
package resources

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/policy"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/gomega"
)

// refusingClient refuses the preview or the apply as invalid, the way the
// runtime client reports an HTTP 400 of the add-on. Any other read answers
// as an outage, so the previewFailure probe cannot be what classifies it.
type refusingClient struct {
	contract.Runtime
	preview, apply bool
	applies        int
}

func (c *refusingClient) Read(context.Context, contract.Envelope) (contract.Result, error) {
	return contract.Result{}, addonruntime.ErrRuntime
}

func (c *refusingClient) Preview(_ context.Context, e contract.Envelope) (contract.Preview, error) {
	if c.preview {
		return contract.Preview{}, &addonruntime.Refusal{Status: 400, Reason: "invalid request"}
	}
	return contract.Preview{OperationID: e.Request.OperationID, Revision: contract.Revision{Schema: 1}, ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func (c *refusingClient) Apply(_ context.Context, e contract.Envelope, _ contract.Revision) (contract.Receipt, error) {
	c.applies++
	if c.apply {
		return contract.Receipt{}, &addonruntime.Refusal{Status: 400, Reason: "invalid request"}
	}
	return contract.Receipt{OperationID: e.Request.OperationID, State: contract.ReceiptCommitted, Revision: contract.Revision{Schema: 1, Data: 1}}, nil
}

func isInvalid(t *testing.T, err error) {
	t.Helper()
	var unknown *gateway.OutcomeUnknown
	g := NewWithT(t)
	g.Expect(errors.Is(err, gateway.ErrInvalidRequest)).To(BeTrue(), "want the typed invalid request, got %v", err)
	g.Expect(errors.As(err, &unknown)).To(BeFalse(), "want the typed invalid request, got %v", err)
	g.Expect(errors.Is(err, gateway.ErrUnavailable)).To(BeFalse(), "want the typed invalid request, got %v", err)
	g.Expect(gateway.StatusFor(err)).NotTo(Equal(503), "want the typed invalid request, got %v", err)
}

// A request the add-on refuses as invalid is a 400 from the typed apply and
// the typed change, whether the preview or the apply refused it, even when the
// add-on's own read cannot be answered. It frees the operation ID.
func TestAddonRefusalIsTheInvalidRequestAnswer(t *testing.T) {
	s, _, _, key := confirmedDeletion(t, func(context.Context, policy.ResourceKey) (bool, error) { return false, nil })
	client := &refusingClient{}
	inst := addonruntime.Instance{Generation: 1, Client: client}
	s.o.Runtime = fixedRuntime{inst}
	actor := contract.Actor{UserID: "bob", CredentialID: "key:bob", Mode: "named-user"}
	body := json.RawMessage(`{"a":1}`)
	want := binding{tool: "databases-write", hash: "h"}
	ctx := context.Background()

	client.preview = true
	_, err := s.typedApply(ctx, actor, key, inst, "write", "wire-1", body, contract.Revision{Schema: 1}, "op-1", want)
	isInvalid(t, err)
	err = s.typedChange(ctx, actor, key, inst, "schema", "wire-2", body, contract.Revision{Schema: 1}, "op-2")
	isInvalid(t, err)
	NewWithT(t).Expect(client.applies).To(BeZero(), "a refused preview must not reach apply")

	client.preview, client.apply = false, true
	_, err = s.typedApply(ctx, actor, key, inst, "write", "wire-1", body, contract.Revision{Schema: 1}, "op-1", want)
	isInvalid(t, err)
	err = s.typedChange(ctx, actor, key, inst, "schema", "wire-2", body, contract.Revision{Schema: 1}, "op-2")
	isInvalid(t, err)
	_, held := s.bound("wire-1")
	NewWithT(t).Expect(held).To(BeFalse(), "a refused apply must not keep the operation ID bound")
}
