// SPDX-License-Identifier: MIT
package writeflow_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/mudler/LocalAI/addons/internal/googleapi"
	"github.com/mudler/LocalAI/addons/internal/writeflow"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestWriteflow(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Writeflow suite")
}

type fake struct {
	writeflow.NoRecovery
	rev       uint64
	inspect   error
	send      error
	sent      int
	resumeOut map[string]any
	resumeErr error
}

func (f *fake) Inspect() (uint64, error) { return f.rev, f.inspect }
func (f *fake) Send() (map[string]any, error) {
	f.sent++
	return map[string]any{"id": "x"}, f.send
}
func (f *fake) Resume(bool) (map[string]any, bool, error) {
	return f.resumeOut, f.resumeOut != nil, f.resumeErr
}

var rev = contract.Revision{Schema: 1, Data: 7}

var _ = Describe("Runtime", func() {
	var rt *writeflow.Runtime
	apply := func(op string, f *fake) (contract.Receipt, error) {
		return rt.Apply(op, rev, func() (writeflow.Session, error) { return f, nil })
	}

	BeforeEach(func() {
		rt = writeflow.New(writeflow.Config{
			Source: "svc", Notice: "n", MaxPayload: 200,
			StaleNote: "changed", UnreadableNote: "unreadable",
			Describe:       func(error) string { return "described" },
			TranslateWrite: func(err error, sent bool) error { return rt.Translate(err) },
		})
		rt.Now = func() time.Time { return time.Unix(1700000000, 0) }
	})

	It("sends once and answers a repeat from the receipt", func() {
		f := &fake{rev: 7}
		rc, err := apply("op1", f)
		Expect(err).NotTo(HaveOccurred())
		Expect(rc.State).To(Equal(contract.ReceiptCommitted))
		again, err := apply("op1", f)
		Expect(err).NotTo(HaveOccurred())
		Expect(again.State).To(Equal(contract.ReceiptCommitted))
		Expect(f.sent).To(Equal(1))
		got, _ := rt.Receipt(context.Background(), "op1")
		Expect(got.State).To(Equal(contract.ReceiptCommitted))
	})

	It("rejects a change whose target moved after the preview", func() {
		f := &fake{rev: 8}
		rc, err := apply("op2", f)
		Expect(err).NotTo(HaveOccurred())
		Expect(rc.State).To(Equal(contract.ReceiptRejected))
		Expect(string(rc.Result.Payload)).To(ContainSubstring("changed"))
		Expect(f.sent).To(BeZero())
	})

	It("forgets the attempt when the service proves nothing was made", func() {
		f := &fake{rev: 7, send: googleapi.ErrNotFound}
		_, err := apply("op3", f)
		Expect(err).To(HaveOccurred())
		_, started := rt.Journal().Started("op3")
		Expect(started).To(BeFalse())
	})

	It("keeps the attempt and no receipt when the outcome is unknown", func() {
		f := &fake{rev: 7, send: googleapi.ErrUnavailable}
		_, err := apply("op4", f)
		Expect(err).To(MatchError(googleapi.ErrUnavailable))
		_, started := rt.Journal().Started("op4")
		Expect(started).To(BeTrue())
		rc, _ := rt.Receipt(context.Background(), "op4")
		Expect(rc.State).To(Equal(contract.ReceiptUnknown))
	})

	It("commits a note when the answer of a made change is unreadable", func() {
		f := &fake{rev: 7, send: googleapi.ErrUnreadable}
		rc, err := apply("op5", f)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(rc.Result.Payload)).To(ContainSubstring("unreadable"))
	})

	It("settles from the trace of an earlier attempt without sending", func() {
		f := &fake{rev: 7, resumeOut: map[string]any{"id": "found"}}
		rc, err := apply("op6", f)
		Expect(err).NotTo(HaveOccurred())
		Expect(rc.State).To(Equal(contract.ReceiptCommitted))
		Expect(f.sent).To(BeZero())
	})

	It("keeps a receipt of a part that was made when the rest is known not to be", func() {
		f := &fake{rev: 7, send: &writeflow.Partial{Out: map[string]any{"partial": true}, Err: googleapi.ErrForbidden}}
		rc, err := apply("op-partial", f)
		Expect(err).NotTo(HaveOccurred())
		Expect(rc.State).To(Equal(contract.ReceiptCommitted))
		Expect(string(rc.Result.Payload)).To(ContainSubstring(`"partial":true`))
		again, _ := rt.Receipt(context.Background(), "op-partial")
		Expect(again.State).To(Equal(contract.ReceiptCommitted))
	})

	It("leaves the outcome unknown when the failure after a part proves nothing", func() {
		f := &fake{rev: 7, send: &writeflow.Partial{Out: map[string]any{}, Err: googleapi.ErrUnavailable}}
		_, err := apply("op-partial2", f)
		Expect(err).To(HaveOccurred())
		rc, _ := rt.Receipt(context.Background(), "op-partial2")
		Expect(rc.State).To(Equal(contract.ReceiptUnknown))
	})

	It("stops when the session refuses to resume", func() {
		boom := errors.New("maybe sent")
		f := &fake{rev: 7, resumeErr: boom}
		_, err := apply("op7", f)
		Expect(err).To(MatchError(boom))
		Expect(f.sent).To(BeZero())
	})

	It("refuses a result above the bound", func() {
		_, err := rt.Finish(map[string]string{"a": string(make([]byte, 300))})
		Expect(err).To(HaveOccurred())
		res, err := rt.Finish("ok")
		Expect(err).NotTo(HaveOccurred())
		var w struct {
			Source    string
			Untrusted bool
		}
		Expect(json.Unmarshal(res.Payload, &w)).To(Succeed())
		Expect(w.Source).To(Equal("svc"))
		Expect(w.Untrusted).To(BeTrue())
	})

	It("translates a rejected token and a limit, and words a refusal", func() {
		Expect(rt.Translate(googleapi.ErrUnauthorized)).To(MatchError(contract.ErrCredentialRejected))
		var limited *contract.ErrRateLimited
		Expect(errors.As(rt.Translate(contract.RateLimitedAfter(5)), &limited)).To(BeTrue())
		Expect(rt.Translate(googleapi.ErrNotFound).Error()).To(ContainSubstring("described"))
		Expect(rt.Translate(googleapi.ErrUnavailable)).To(MatchError(googleapi.ErrUnavailable))
	})
})
