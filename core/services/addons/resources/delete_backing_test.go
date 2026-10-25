// SPDX-License-Identifier: MIT
package resources

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/policy"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/gomega"
)

// deleteClient records what the owner sent. Its delete tool commits.
type deleteClient struct {
	contract.Runtime
	mu       sync.Mutex
	previews int
	applies  int
}

func (c *deleteClient) Preview(_ context.Context, e contract.Envelope) (contract.Preview, error) {
	c.mu.Lock()
	c.previews++
	c.mu.Unlock()
	return contract.Preview{OperationID: e.Request.OperationID, Revision: contract.Revision{Schema: 1}, ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func (c *deleteClient) Apply(_ context.Context, e contract.Envelope, _ contract.Revision) (contract.Receipt, error) {
	c.mu.Lock()
	c.applies++
	c.mu.Unlock()
	return contract.Receipt{OperationID: e.Request.OperationID, State: contract.ReceiptCommitted}, nil
}

type fixedRuntime struct{ inst addonruntime.Instance }

func (f fixedRuntime) Runtime(context.Context, string) (addonruntime.Instance, error) {
	return f.inst, nil
}

type fixedCaps struct{ caps contract.Capabilities }

func (f fixedCaps) DeclaredCapabilities(context.Context, string, uint64) (contract.Capabilities, error) {
	return f.caps, nil
}

// confirmedDeletion is an owner with one resource whose deletion is confirmed
// and not settled yet.
func confirmedDeletion(t *testing.T, confirmed func(context.Context, policy.ResourceKey) (bool, error)) (*Service, *policy.Policy, *deleteClient, policy.ResourceKey) {
	t.Helper()
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
	key, err := p.CreateWithID(ctx, bob, "tables", "r0123456789abcdef0123456789abcde", "ref", func(context.Context, policy.ResourceKey) error { return nil })
	g.Expect(err).NotTo(HaveOccurred(), "create")
	g.Expect(p.BeginDelete(ctx, bob, key, "op-1")).To(Succeed(), "begin")
	g.Expect(p.ConfirmDelete(key)).To(Succeed(), "confirm")
	client := &deleteClient{}
	caps := contract.Capabilities{
		Tools:      []string{"databases-inspect"},
		AdminTools: []string{"databases-delete"},
		Resources:  &contract.ResourceTools{Delete: "databases-delete", Inspect: "databases-inspect"},
	}
	s := New(Options{Policy: p, Runtime: fixedRuntime{addonruntime.Instance{Generation: 1, Client: client}}, Capabilities: fixedCaps{caps}, BackingConfirmed: confirmed})
	return s, p, client, key
}

// A deletion whose add-on gave no explicit existence answer is never settled
// by dropping the record: the data would stay behind with no host record.
func TestDeletionKeepsTheRecordWhenTheAddonCannotAnswer(t *testing.T) {
	s, p, client, key := confirmedDeletion(t, func(context.Context, policy.ResourceKey) (bool, error) {
		return false, policy.ErrBackingUnanswered
	})
	s.Resume(context.Background())
	g := NewWithT(t)
	d := p.Deleting()
	g.Expect(d).To(HaveLen(1), "the deletion must stay confirmed and be retried")
	g.Expect(d[0].Key).To(Equal(key))
	g.Expect(d[0].Confirmed).To(BeTrue())
	_, known := p.Key(key.ResourceID)
	g.Expect(known).To(BeTrue(), "the record was removed without an existence answer")
	done, _ := p.DeletedBy(context.Background(), contract.Actor{UserID: "bob", CredentialID: "key:bob", Mode: "named-user"}, key.ResourceID, "op-1")
	g.Expect(done).To(BeFalse(), "a tombstone was written without an existence answer")
	g.Expect(client.applies).To(BeZero(), "the delete tool must not run without a check")
}

// An explicit "gone" still finishes the deletion, and an explicit "exists"
// runs the delete tool.
func TestDeletionFinishesOnExplicitAnswers(t *testing.T) {
	s, p, client, key := confirmedDeletion(t, func(context.Context, policy.ResourceKey) (bool, error) { return false, nil })
	s.Resume(context.Background())
	g := NewWithT(t)
	g.Expect(p.Deleting()).To(BeEmpty(), "an explicit absence finishes the deletion")
	g.Expect(client.applies).To(BeZero(), "an explicit absence runs no delete tool")
	_ = key

	s, p, client, _ = confirmedDeletion(t, func(context.Context, policy.ResourceKey) (bool, error) { return true, nil })
	s.Resume(context.Background())
	g.Expect(p.Deleting()).To(BeEmpty(), "an existing backing is deleted by the tool")
	g.Expect(client.applies).To(Equal(1))
}

// The owner's own repeat of the delete reports the unknown outcome with its
// original operation and never Deleted.
func TestDeleteRepeatIsOutcomeUnknownWhenTheAddonCannotAnswer(t *testing.T) {
	s, p, _, key := confirmedDeletion(t, func(context.Context, policy.ResourceKey) (bool, error) {
		return false, policy.ErrBackingUnanswered
	})
	bob := contract.Actor{UserID: "bob", CredentialID: "key:bob", Mode: "named-user"}
	_, err := s.finishDelete(context.Background(), bob, key, "op-1")
	var unknown interface{ Error() string }
	g := NewWithT(t)
	g.Expect(err).To(HaveOccurred(), "expected an unknown outcome")
	g.Expect(errors.As(err, &unknown)).To(BeTrue(), "expected an unknown outcome, got %v", err)
	b, _ := json.Marshal(err.Error())
	g.Expect(p.Deleting()).To(HaveLen(1), "deletion left deleting state: %s", b)
}
