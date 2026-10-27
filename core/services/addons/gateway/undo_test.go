// SPDX-License-Identifier: MIT
package gateway

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the typed refusal of an undo", func() {
	It("crosses the owner's wire as the same typed error, whatever the frontend", func() {
		for _, code := range []string{contract.UndoStale, contract.UndoNotFound, contract.UndoExpired, contract.UndoAlreadyUndone, contract.UndoCannot} {
			w := httptest.NewRecorder()
			resourceFailure(w, ResourceCall{Op: resourceUndo}, "op-1", &UndoRefused{Code: code, Detail: "the rows changed"})
			Expect(w.Code).To(Equal(http.StatusConflict), code)
			got := resourceReplyError(strings.NewReader(w.Body.String()), w.Code, "op-1")
			u, ok := asUndoRefused(got)
			Expect(ok).To(BeTrue(), code)
			Expect(u.Code).To(Equal(code))
			Expect(u.Detail).To(Equal("the rows changed"))
		}
	})

	It("does not trust a code the host does not know, and cleans the detail the add-on wrote", func() {
		got := resourceReplyError(strings.NewReader(`{"type":"undo_refused","code":"run-this","detail":"x"}`), 409, "op-1")
		_, ok := asUndoRefused(got)
		Expect(ok).To(BeFalse())
		got = resourceReplyError(strings.NewReader(`{"type":"undo_refused","code":"stale","detail":"a\nb\u0000c"}`), 409, "op-1")
		u, _ := asUndoRefused(got)
		Expect(u.Detail).NotTo(ContainSubstring("\n"))
		Expect(u.Detail).NotTo(ContainSubstring("\x00"))
	})

	It("is a stale view for a caller that only knows ErrStale, and a denial otherwise", func() {
		Expect(errors.Is(&UndoRefused{Code: contract.UndoStale}, ErrStale)).To(BeTrue())
		Expect(errors.Is(&UndoRefused{Code: contract.UndoExpired}, ErrStale)).To(BeFalse())
		Expect(StatusFor(&UndoRefused{Code: contract.UndoExpired})).To(Equal(http.StatusForbidden))
	})

	It("is a mutation on the carrier: a lost answer is an unknown outcome, never a plain outage", func() {
		Expect(ResourceCall{Op: resourceUndo}.mutating()).To(BeTrue())
		e := resourceOutcome(ResourceCall{Op: resourceUndo}, "op-9", ErrUnavailable)
		var unknown *OutcomeUnknown
		Expect(errors.As(e, &unknown)).To(BeTrue())
		Expect(unknown.OperationID).To(Equal("op-9"))
	})
})
