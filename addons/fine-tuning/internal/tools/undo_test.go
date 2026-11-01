// SPDX-License-Identifier: MIT
package tools

import (
	"encoding/json"
	"errors"

	"github.com/mudler/LocalAI/addons/internal/protocol"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// undoEnv is an envelope of the host's undo calls.
func undoEnv(tool, op, res, change, scope, action string) contract.Envelope {
	e := envelope(tool, op, res, `{"change":"`+change+`"}`)
	e.Request.Action, e.Authority.Scope = action, scope
	return e
}

func changeOf(rt contract.Runtime, res, change string) contract.ChangeInfo {
	GinkgoHelper()
	r, err := rt.Read(ctx, undoEnv("changes-get", "get-"+change, res, change, "read", "read"))
	Expect(err).NotTo(HaveOccurred())
	var info contract.ChangeInfo
	Expect(json.Unmarshal(r.Payload, &info)).To(Succeed())
	return info
}

var _ = Describe("undo of a job start", func() {
	It("says in the receipt of the start that it can be undone, and that a stop, an export and a dataset cannot", func() {
		eng := &fakeEngine{result: map[string]any{"finetune.start": map[string]any{"id": "job-1", "status": "queued"}, "finetune.stop": map[string]any{}, "finetune.export": map[string]any{}, "finetune.dataset": map[string]any{"datasetRef": "ref-1"}}}
		rt := New(eng, GinkgoT().TempDir())
		rec, err := rt.Apply(ctx, envelope("fine-tune-start", "create-res1", "res1", startJSON), contract.Revision{Schema: 1})
		Expect(err).NotTo(HaveOccurred())
		n, ok := contract.ParseUndoNote(rec.Result.Payload)
		Expect(ok).To(BeTrue())
		Expect(n.Available).To(BeTrue())
		Expect(n.ID).To(Equal("create-res1"))
		Expect(n.Summary).To(ContainSubstring("my run"))
		stop, err := rt.Apply(ctx, envelope("fine-tune-stop", "stop-1", "res1", ""), contract.Revision{Schema: 1, Data: 1})
		Expect(err).NotTo(HaveOccurred())
		n, ok = contract.ParseUndoNote(stop.Result.Payload)
		Expect(ok).To(BeTrue())
		Expect(n.Available).To(BeFalse())
		Expect(n.Reason).To(ContainSubstring("cannot be undone"))
		ds, err := rt.Apply(ctx, envelope("fine-tune-dataset", "create-ds", "ds1", `{"name":"d.jsonl","content":"{}"}`), contract.Revision{Schema: 1})
		Expect(err).NotTo(HaveOccurred())
		n, _ = contract.ParseUndoNote(ds.Result.Payload)
		Expect(n.Available).To(BeFalse())
		Expect(n.Reason).To(ContainSubstring("dataset"))
	})

	It("stops a running job once, and answers the same operation ID with the same receipt", func() {
		rt, eng := started()
		eng.result["finetune.get"] = map[string]any{"status": "training"}
		info := changeOf(rt, "res1", "create-res1")
		Expect(info.State).To(Equal(contract.ChangeAvailable))
		Expect(info.Class).To(Equal(contract.ChangeClassSchema))
		Expect(info.Summary).To(ContainSubstring("Stop"))
		undo := undoEnv("changes-undo", "undo-1", "res1", "create-res1", "apply", "undo")
		rec, err := rt.Apply(ctx, undo, info.Current)
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.State).To(Equal(contract.ReceiptCommitted))
		Expect(eng.count("finetune.stop")).To(Equal(1))
		Expect(eng.last("finetune.stop").args).To(HaveKeyWithValue("jobId", "job-1"))
		again, err := rt.Apply(ctx, undo, info.Current)
		Expect(err).NotTo(HaveOccurred())
		Expect(again).To(Equal(rec))
		Expect(eng.count("finetune.stop")).To(Equal(1), "the engine ran once")
		Expect(changeOf(rt, "res1", "create-res1").State).To(Equal(contract.ChangeUndone))
		other, err := rt.Apply(ctx, undoEnv("changes-undo", "undo-2", "res1", "create-res1", "apply", "undo"), info.Current)
		Expect(err).NotTo(HaveOccurred())
		Expect(other.State).To(Equal(contract.ReceiptRejected))
		Expect(string(other.Result.Payload)).To(ContainSubstring(contract.UndoAlreadyUndone))
		_, err = rt.Apply(ctx, undoEnv("changes-undo", "undo-1", "res1", "other-change", "apply", "undo"), info.Current)
		Expect(errors.Is(err, protocol.ErrConflict)).To(BeTrue(), "the same ID for another change: %v", err)
	})

	It("says a finished job cannot be undone, and does not stop it", func() {
		rt, eng := started()
		for _, st := range []string{"completed", "failed", "stopped"} {
			eng.result["finetune.get"] = map[string]any{"status": st}
			info := changeOf(rt, "res1", "create-res1")
			Expect(info.State).To(Equal(contract.ChangeFinal), st)
			Expect(info.Detail).To(ContainSubstring("finished"))
		}
		rec, err := rt.Apply(ctx, undoEnv("changes-undo", "undo-1", "res1", "create-res1", "apply", "undo"), contract.Revision{Schema: 1, Data: 1})
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.State).To(Equal(contract.ReceiptRejected))
		Expect(string(rec.Result.Payload)).To(ContainSubstring(contract.UndoCannot))
		Expect(eng.count("finetune.stop")).To(Equal(0))
	})

	It("reports a job that finished between the read and the stop as cannot_undo", func() {
		rt, eng := started()
		eng.result["finetune.get"] = map[string]any{"status": "training"}
		eng.result["finetune.stop"] = &contract.HostError{Code: contract.HostConflict, Message: "the job is not running"}
		rec, err := rt.Apply(ctx, undoEnv("changes-undo", "undo-1", "res1", "create-res1", "apply", "undo"), contract.Revision{Schema: 1, Data: 1})
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.State).To(Equal(contract.ReceiptRejected))
		Expect(string(rec.Result.Payload)).To(ContainSubstring(contract.UndoCannot))
	})

	It("refuses after a later change to the job, after a week, and for a change that is not the start", func() {
		rt, eng := started()
		eng.result["finetune.get"] = map[string]any{"status": "training"}
		Expect(changeOf(rt, "res1", "nothing").State).To(Equal(contract.ChangeUnknown))
		Expect(changeOf(rt, "nores", "create-res1").State).To(Equal(contract.ChangeUnknown))
		eng.result["finetune.export"] = map[string]any{}
		_, err := rt.Apply(ctx, envelope("fine-tune-export", "export-1", "res1", ""), contract.Revision{Schema: 1, Data: 1})
		Expect(err).NotTo(HaveOccurred())
		Expect(changeOf(rt, "res1", "create-res1").State).To(Equal(contract.ChangeFinal))

		rt2, eng2 := started()
		eng2.result["finetune.get"] = map[string]any{"status": "training"}
		r := rt2.(*runtime)
		j, _ := r.j.job("res1")
		j.StartedNS = 1
		Expect(r.j.commit("res1", &j, contract.Receipt{OperationID: "bump-1", State: contract.ReceiptCommitted})).To(Succeed())
		Expect(changeOf(rt2, "res1", "create-res1").State).To(Equal(contract.ChangeExpired))
	})

	It("answers only the host: the undo calls need their own action and scope", func() {
		rt, eng := started()
		eng.result["finetune.get"] = map[string]any{"status": "training"}
		for _, c := range []struct{ scope, action string }{{"apply", "write"}, {"apply", "schema"}, {"preview", "undo"}, {"read", "undo"}} {
			_, err := rt.Apply(ctx, undoEnv("changes-undo", "undo-x", "res1", "create-res1", c.scope, c.action), contract.Revision{Schema: 1, Data: 1})
			Expect(errors.Is(err, protocol.ErrForbidden)).To(BeTrue(), "%v %v", c, err)
		}
		_, err := rt.Read(ctx, undoEnv("changes-get", "g", "res1", "create-res1", "apply", "read"))
		Expect(errors.Is(err, protocol.ErrForbidden)).To(BeTrue())
		Expect(eng.count("finetune.stop")).To(Equal(0))
		for _, tool := range Tools {
			Expect(tool).NotTo(HavePrefix("changes-"), "the undo calls are never model tools")
		}
	})
})
