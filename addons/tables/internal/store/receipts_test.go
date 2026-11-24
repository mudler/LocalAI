// SPDX-License-Identifier: MIT
package store_test

import (
	"context"
	"errors"
	"sync"

	"github.com/mudler/LocalAI/addons/tables/internal/store"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Tables schema receipts", func() {
	ctx := context.Background()
	create := store.SchemaChange{ResourceID: idA, Kind: store.KindTableCreate, Table: "Orders"}

	It("leaves no change and no receipt when the process dies before commit, and the ID can still apply", func() {
		r := newSchemaRig(store.Options{})
		r.s.SetFailpoint(func(p string) error {
			if p == store.FailBeforeCommit {
				return errors.New("killed before commit")
			}
			return nil
		})
		_, e := r.s.ApplySchema(ctx, create, r.rev, "op-crash-a")
		Expect(e).To(MatchError(ContainSubstring("killed")))
		// A new process opens the same directory.
		Expect(r.s.Close()).To(Succeed())
		s2 := open(r.dir, store.Options{})
		Expect(s2.LookupReceipt(ctx, "op-crash-a")).To(Equal(contract.Receipt{OperationID: "op-crash-a", State: contract.ReceiptUnknown}))
		got, e := s2.Inspect(ctx, idA)
		Expect(e).NotTo(HaveOccurred())
		Expect(got.Tables).To(BeEmpty())
		Expect(got.Revision).To(Equal(r.rev))
		rc, e := s2.ApplySchema(ctx, create, r.rev, "op-crash-a")
		Expect(e).NotTo(HaveOccurred())
		Expect(rc.State).To(Equal(contract.ReceiptCommitted))
		Expect(rc.Revision.Schema).To(Equal(r.rev.Schema + 1))
	})

	It("finds the committed receipt after a crash between commit and reply and never applies twice", func() {
		r := newSchemaRig(store.Options{})
		r.s.SetFailpoint(func(p string) error {
			if p == store.FailAfterCommit {
				return errors.New("killed after commit")
			}
			return nil
		})
		_, e := r.s.ApplySchema(ctx, create, r.rev, "op-crash-b")
		Expect(e).To(MatchError(ContainSubstring("killed")))
		Expect(r.s.Close()).To(Succeed())
		s2 := open(r.dir, store.Options{})
		rc, e := s2.LookupReceipt(ctx, "op-crash-b")
		Expect(e).NotTo(HaveOccurred())
		Expect(rc.State).To(Equal(contract.ReceiptCommitted))
		Expect(rc.Revision.Schema).To(Equal(r.rev.Schema + 1))
		Expect(rc.Validate(contract.DefaultLimits())).To(Succeed())
		// The host retries with the revision it previewed: same receipt, one table.
		again, e := s2.ApplySchema(ctx, create, r.rev, "op-crash-b")
		Expect(e).NotTo(HaveOccurred())
		Expect(again).To(Equal(rc))
		got, _ := s2.Inspect(ctx, idA)
		Expect(got.Tables).To(Equal([]string{"Orders"}))
		Expect(got.Revision.Schema).To(Equal(r.rev.Schema + 1))
		// A third lookup is stable.
		third, e := s2.Receipt(ctx, "op-crash-b")
		Expect(e).NotTo(HaveOccurred())
		Expect(third.State).To(Equal(contract.ReceiptCommitted))
	})

	It("replays one operation ID to one receipt and refuses another request under the same ID", func() {
		r := newSchemaRig(store.Options{})
		first, e := r.s.ApplySchema(ctx, create, r.rev, "op-dup")
		Expect(e).NotTo(HaveOccurred())
		replay, e := r.s.ApplySchema(ctx, create, r.rev, "op-dup")
		Expect(e).NotTo(HaveOccurred())
		Expect(replay).To(Equal(first))
		other := create
		other.Table = "Invoices"
		_, e = r.s.ApplySchema(ctx, other, r.rev, "op-dup")
		Expect(e).To(MatchError(store.ErrConflict))
		_, e = r.s.ApplySchema(ctx, other, first.Revision, "op-dup")
		Expect(e).To(MatchError(store.ErrConflict))
		got, _ := r.s.Inspect(ctx, idA)
		Expect(got.Tables).To(Equal([]string{"Orders"}))
		Expect(got.Revision).To(Equal(first.Revision))
		// An operation ID used by a rename is not a schema change.
		_, e = r.s.Rename(ctx, idA, "Renamed", "op-rn")
		Expect(e).NotTo(HaveOccurred())
		_, e = r.s.ApplySchema(ctx, other, first.Revision, "op-rn")
		Expect(e).To(MatchError(store.ErrConflict))
	})

	It("applies one change once when the same operation arrives concurrently", func() {
		r := newSchemaRig(store.Options{})
		var wg sync.WaitGroup
		recs := make([]contract.Receipt, 8)
		errs := make([]error, 8)
		for i := range recs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				recs[i], errs[i] = r.s.ApplySchema(ctx, create, r.rev, "op-conc")
			}()
		}
		wg.Wait()
		for i := range recs {
			Expect(errs[i]).NotTo(HaveOccurred())
			Expect(recs[i]).To(Equal(recs[0]))
		}
		got, _ := r.s.Inspect(ctx, idA)
		Expect(got.Revision.Schema).To(Equal(r.rev.Schema + 1))
	})

	It("keeps a rejected change out of the receipts so its ID is not burnt", func() {
		r := newSchemaRig(store.Options{})
		bad := store.SchemaChange{ResourceID: idA, Kind: store.KindTableDrop, Table: "Nope"}
		_, e := r.s.ApplySchema(ctx, bad, r.rev, "op-rej")
		Expect(e).To(MatchError(store.ErrTableNotFound))
		rc, e := r.s.LookupReceipt(ctx, "op-rej")
		Expect(e).NotTo(HaveOccurred())
		Expect(rc.State).To(Equal(contract.ReceiptUnknown))
		_, e = r.s.ApplySchema(ctx, create, r.rev, "op-rej")
		Expect(e).NotTo(HaveOccurred())
	})

	It("encodes the receipt payload so the host can read what changed", func() {
		r := newSchemaRig(store.Options{})
		rc, e := r.s.ApplySchema(ctx, create, r.rev, "op-payload")
		Expect(e).NotTo(HaveOccurred())
		Expect(rc.Validate(contract.DefaultLimits())).To(Succeed())
		Expect(string(rc.Result.Payload)).To(ContainSubstring(`"kind":"table-create"`))
		Expect(string(rc.Result.Payload)).To(ContainSubstring(`"resourceId":"` + idA + `"`))
		unknown, e := r.s.LookupReceipt(ctx, "op-never")
		Expect(e).NotTo(HaveOccurred())
		Expect(unknown.State).To(Equal(contract.ReceiptUnknown))
		_, e = r.s.LookupReceipt(ctx, "BAD ID")
		Expect(e).To(MatchError(store.ErrInvalidOperation))
	})
})
