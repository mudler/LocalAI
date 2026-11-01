// SPDX-License-Identifier: MIT
package server_test

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/mudler/LocalAI/addons/internal/protocol"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The undo calls are the host's alone: they are never model tools, and the
// add-on checks the action and the scope itself.
var _ = Describe("the add-on's undo calls", func() {
	ctx := context.Background()
	insert := func(r *exportRig, op string) contract.Receipt {
		GinkgoHelper()
		a := map[string]any{"tool": "rows-insert", "table": "People", "rows": []map[string]any{{"Name": "Ada", "Age": 36}}}
		p, e := r.t.Preview(ctx, r.env("owner", op, "preview", "write", a))
		Expect(e).NotTo(HaveOccurred())
		rc, e := r.t.Apply(ctx, r.env("owner", op, "apply", "write", a), p.Revision)
		Expect(e).NotTo(HaveOccurred())
		return rc
	}
	get := func(r *exportRig, change string) (contract.ChangeInfo, error) {
		res, e := r.t.Read(ctx, r.env("owner", "op-get", "read", "read", map[string]any{"tool": "changes-get", "change": change}))
		if e != nil {
			return contract.ChangeInfo{}, e
		}
		var info contract.ChangeInfo
		Expect(json.Unmarshal(res.Payload, &info)).To(Succeed())
		return info, nil
	}

	It("says in the receipt of a write that it can be undone, and reads and applies the undo", func() {
		r := newExportRig()
		rc := insert(r, "op-ins-1")
		n, ok := contract.ParseUndoNote(rc.Result.Payload)
		Expect(ok).To(BeTrue())
		Expect(n).To(Equal(contract.UndoNote{ID: "op-ins-1", Available: true, Summary: n.Summary}))
		info, e := get(r, "op-ins-1")
		Expect(e).NotTo(HaveOccurred())
		Expect(info.State).To(Equal(contract.ChangeAvailable))
		Expect(info.Class).To(Equal(contract.ChangeClassWrite))
		undo := map[string]any{"tool": "changes-undo", "change": "op-ins-1"}
		u, e := r.t.Apply(ctx, r.env("owner", "op-undo-1", "apply", "undo", undo), info.Current)
		Expect(e).NotTo(HaveOccurred())
		Expect(u.State).To(Equal(contract.ReceiptCommitted))
		again, e := r.t.Apply(ctx, r.env("owner", "op-undo-1", "apply", "undo", undo), info.Current)
		Expect(e).NotTo(HaveOccurred())
		Expect(again.Revision).To(Equal(u.Revision), "the same operation ID is the same undo")
		info, _ = get(r, "op-ins-1")
		Expect(info.State).To(Equal(contract.ChangeUndone))
	})

	It("rejects a stale undo with a typed code and the words of what changed", func() {
		r := newExportRig()
		insert(r, "op-ins-1")
		insert(r, "op-ins-2")
		info, _ := get(r, "op-ins-1")
		Expect(info.State).To(Equal(contract.ChangeStale))
		Expect(info.Detail).NotTo(BeEmpty())
		u, e := r.t.Apply(ctx, r.env("owner", "op-undo-1", "apply", "undo", map[string]any{"tool": "changes-undo", "change": "op-ins-1"}), info.Current)
		Expect(e).NotTo(HaveOccurred())
		Expect(u.State).To(Equal(contract.ReceiptRejected))
		var body struct{ Error, Message string }
		Expect(json.Unmarshal(u.Result.Payload, &body)).To(Succeed())
		Expect(body.Error).To(Equal(contract.UndoStale))
		Expect(body.Message).NotTo(BeEmpty())
	})

	It("rejects an unknown change as not_found and a repeated one as already_undone", func() {
		r := newExportRig()
		rc := insert(r, "op-ins-1")
		u, e := r.t.Apply(ctx, r.env("owner", "op-undo-x", "apply", "undo", map[string]any{"tool": "changes-undo", "change": "nothing-here"}), rc.Revision)
		Expect(e).NotTo(HaveOccurred())
		Expect(u.State).To(Equal(contract.ReceiptRejected))
		Expect(string(u.Result.Payload)).To(ContainSubstring(contract.UndoNotFound))
		ok, e := r.t.Apply(ctx, r.env("owner", "op-undo-1", "apply", "undo", map[string]any{"tool": "changes-undo", "change": "op-ins-1"}), rc.Revision)
		Expect(e).NotTo(HaveOccurred())
		u, e = r.t.Apply(ctx, r.env("owner", "op-undo-2", "apply", "undo", map[string]any{"tool": "changes-undo", "change": "op-ins-1"}), ok.Revision)
		Expect(e).NotTo(HaveOccurred())
		Expect(string(u.Result.Payload)).To(ContainSubstring(contract.UndoAlreadyUndone))
	})

	It("refuses the undo under any other action or scope, and with no actor", func() {
		r := newExportRig()
		rc := insert(r, "op-ins-1")
		undo := map[string]any{"tool": "changes-undo", "change": "op-ins-1"}
		for _, action := range []string{"write", "schema", "read", "delete"} {
			_, e := r.t.Apply(ctx, r.env("owner", "op-undo-"+action, "apply", action, undo), rc.Revision)
			Expect(errors.Is(e, protocol.ErrForbidden)).To(BeTrue(), "action %s: %v", action, e)
		}
		_, e := r.t.Apply(ctx, r.env("owner", "op-undo-s", "preview", "undo", undo), rc.Revision)
		Expect(errors.Is(e, protocol.ErrForbidden)).To(BeTrue(), "wrong scope: %v", e)
		_, e = r.t.Apply(ctx, r.env("", "op-undo-n", "apply", "undo", undo), rc.Revision)
		Expect(errors.Is(e, protocol.ErrForbidden)).To(BeTrue(), "no actor: %v", e)
		_, e = r.t.Read(ctx, r.env("owner", "op-get", "apply", "read", map[string]any{"tool": "changes-get", "change": "op-ins-1"}))
		Expect(errors.Is(e, protocol.ErrForbidden)).To(BeTrue(), "changes-get under the wrong scope: %v", e)
	})

	It("does not preview the undo: it is applied against the revision the host just read", func() {
		r := newExportRig()
		insert(r, "op-ins-1")
		_, e := r.t.Preview(ctx, r.env("owner", "op-undo-p", "preview", "undo", map[string]any{"tool": "changes-undo", "change": "op-ins-1"}))
		Expect(errors.Is(e, protocol.ErrBadRequest)).To(BeTrue(), "%v", e)
	})
})
