// SPDX-License-Identifier: MIT
package policy

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// existsClient answers the existence read with a fixed payload.
type existsClient struct {
	contract.Runtime
	payload string
	asked   int
}

func (c *existsClient) Read(context.Context, contract.Envelope) (contract.Result, error) {
	c.asked++
	return contract.Result{Payload: json.RawMessage(c.payload)}, nil
}

type oneRuntime struct {
	inst addonruntime.Instance
	err  error
}

func (r oneRuntime) Runtime(context.Context, string) (addonruntime.Instance, error) {
	return r.inst, r.err
}

var _ gateway.CurrentRuntime = oneRuntime{}

var _ = Describe("existence of a backing for a deletion", func() {
	key := ResourceKey{AddonID: "fixture", ResourceID: "res-1"}
	opts := func(client *existsClient, tools []string, installed bool) BackingOptions {
		rt := oneRuntime{inst: addonruntime.Instance{Generation: 1, Client: client}}
		if client == nil {
			rt = oneRuntime{err: errors.New("not running")}
		}
		return BackingOptions{Runtime: rt, Tools: &mutableTools{tools: tools},
			Installed: func(context.Context, string) (bool, error) { return installed, nil }}
	}

	It("never reads an add-on that cannot answer as a backing that is gone", func() {
		ctx := context.Background()
		silent := &existsClient{payload: `{"exists":false}`}
		// Declares nothing: a reservation is dropped as before, a deletion
		// gets no answer at all and the data must not be orphaned.
		lenient, err := BackingExists(opts(silent, []string{"databases-create"}, true))(ctx, key)
		Expect(err).NotTo(HaveOccurred())
		Expect(lenient).To(BeFalse())
		_, err = BackingConfirmed(opts(silent, []string{"databases-create"}, true))(ctx, key)
		Expect(errors.Is(err, ErrBackingUnanswered)).To(BeTrue(), "%v", err)
		Expect(errors.Is(err, gateway.ErrUnavailable)).To(BeTrue())
		Expect(silent.asked).To(BeZero())

		// Not installed, not running: the same.
		lenient, err = BackingExists(opts(nil, nil, false))(ctx, key)
		Expect(err).NotTo(HaveOccurred())
		Expect(lenient).To(BeFalse())
		_, err = BackingConfirmed(opts(nil, nil, false))(ctx, key)
		Expect(errors.Is(err, ErrBackingUnanswered)).To(BeTrue(), "%v", err)
		// Installed but down is plain unavailability for both.
		for _, f := range []func(BackingOptions) func(context.Context, ResourceKey) (bool, error){BackingExists, BackingConfirmed} {
			_, err = f(opts(nil, nil, true))(ctx, key)
			Expect(errors.Is(err, ErrUnavailable)).To(BeTrue())
		}
		// No manager wired at all.
		_, err = BackingConfirmed(BackingOptions{})(ctx, key)
		Expect(err).To(HaveOccurred())
	})

	It("accepts the explicit answer of the declared existence tool, true or false", func() {
		ctx := context.Background()
		for _, exists := range []bool{true, false} {
			c := &existsClient{payload: `{"exists":` + map[bool]string{true: "true", false: "false"}[exists] + `}`}
			got, err := BackingConfirmed(opts(c, []string{ReconcileTool}, true))(ctx, key)
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal(exists))
			Expect(c.asked).To(Equal(1))
		}
		// An answer that is not the typed boolean is not an answer.
		bad := &existsClient{payload: `{}`}
		_, err := BackingConfirmed(opts(bad, []string{ReconcileTool}, true))(ctx, key)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, ErrBackingUnanswered)).To(BeFalse())
	})
})
