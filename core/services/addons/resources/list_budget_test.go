// SPDX-License-Identifier: MIT
package resources

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/policy"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/gomega"
)

// readCounter is an add-on client that answers every read with a name after a
// short while and records how many reads were in flight at once.
type readCounter struct {
	contract.Runtime
	mu       sync.Mutex
	now, max int
}

func (c *readCounter) Read(_ context.Context, e contract.Envelope) (contract.Result, error) {
	c.mu.Lock()
	c.now++
	if c.now > c.max {
		c.max = c.now
	}
	c.mu.Unlock()
	time.Sleep(25 * time.Millisecond)
	c.mu.Lock()
	c.now--
	c.mu.Unlock()
	body, _ := json.Marshal(map[string]any{"name": "db " + e.Request.ResourceID[len(e.Request.ResourceID)-2:], "revision": contract.Revision{Schema: 1}})
	return contract.Result{Payload: body}, nil
}

// A listing names many resources, one read each. It must leave part of the
// add-on's concurrency budget to the person's other reads: the runtime refuses
// a call over the budget, so a listing that takes all of it makes a page that
// reads while it lists fail.
func TestListingLeavesPartOfTheAddonBudgetToOtherReads(t *testing.T) {
	g := NewWithT(t)
	store, err := policy.OpenStore(t.TempDir(), policy.ModeNamedUser)
	g.Expect(err).NotTo(HaveOccurred())
	t.Cleanup(func() { _ = store.Close() })
	p := policy.New(store, policy.Options{
		Revalidate:      func(context.Context, contract.Actor) error { return nil },
		CreateAuthority: func(context.Context, contract.Actor, string) error { return nil },
	})
	bob := contract.Actor{UserID: "bob", CredentialID: "key:bob", Mode: "named-user"}
	ctx := context.Background()
	for i := 0; i < 12; i++ {
		id := fmt.Sprintf("r%031x", i)
		_, err = p.CreateWithID(ctx, bob, "tables", id, fmt.Sprintf("ref-%d", i), func(context.Context, policy.ResourceKey) error { return nil })
		g.Expect(err).NotTo(HaveOccurred(), "create")
	}
	client := &readCounter{}
	limits := contract.DefaultLimits()
	caps := contract.Capabilities{
		Tools:     []string{"databases-inspect"},
		Resources: &contract.ResourceTools{Inspect: "databases-inspect"},
	}
	s := New(Options{Policy: p, Runtime: fixedRuntime{addonruntime.Instance{Generation: 1, Client: client}}, Capabilities: fixedCaps{caps}, Limits: limits})
	list, err := s.List(ctx, bob, "tables")
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(list.Resources).To(HaveLen(12))
	for _, r := range list.Resources {
		g.Expect(r.Name).NotTo(BeEmpty(), "a name did not arrive: %+v", r)
	}
	want := int(limits.Concurrency / 2)
	g.Expect(client.max).To(BeNumerically("<=", want), "the add-on allows %d reads at once", limits.Concurrency)
	g.Expect(client.max).To(BeNumerically(">=", 1))
}
