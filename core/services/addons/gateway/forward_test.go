// SPDX-License-Identifier: MIT
package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the calls forwarded to the owner", func() {
	It("classifies the reads of every call family and fails closed for the rest", func() {
		for _, a := range []string{
			"read", "oauth-" + OAuthClientGet, "library-" + LibraryList, "hostgrants-" + HostGrantsRead,
			"hostgrants-" + HostGrantsFileChunk, "resource-list", "catalog-search", ActionUpdateCheck,
		} {
			Expect(mutation(a)).To(BeFalse(), a)
		}
		for _, a := range []string{"oauth-" + OAuthClientSet, "resource-grant", "an-action-added-later"} {
			Expect(mutation(a)).To(BeTrue(), a)
		}
	})

	It("reports a lost answer to a change as an unknown outcome and to a read as unavailable", func() {
		var unknown *OutcomeUnknown
		Expect(errors.As(lostAnswer("oauth-"+OAuthClientSet, "op1"), &unknown)).To(BeTrue())
		Expect(unknown.OperationID).To(Equal("op1"))
		Expect(lostAnswer("oauth-"+OAuthClientGet, "op2")).To(MatchError(ErrUnavailable))
	})

	It("bounds a reply", func() {
		_, ok := readBounded(strings.NewReader("abcd"), 4)
		Expect(ok).To(BeTrue())
		_, ok = readBounded(strings.NewReader("abcde"), 4)
		Expect(ok).To(BeFalse())
	})

	It("needs exactly one of data and error in an OAuth reply", func() {
		_, valid, _ := (&oauthReply{}).open()
		Expect(valid).To(BeFalse())
		_, valid, _ = (&oauthReply{Data: &OAuthReply{}, Error: &OAuthError{}}).open()
		Expect(valid).To(BeFalse())
		_, valid, refusal := (&oauthReply{Error: &OAuthError{Message: "no"}}).open()
		Expect(valid).To(BeTrue())
		Expect(refusal).To(MatchError("no"))
	})

	It("writes a decided refusal into the envelope and any other error as a typed error", func() {
		write := func(err error) *httptest.ResponseRecorder {
			rec := httptest.NewRecorder()
			writeReply(rec, err, libraryReply{Data: json.RawMessage(`{"ok":1}`)}, decided(func(le *LibraryError) any { return libraryReply{Error: le} }))
			return rec
		}
		rec := write(nil)
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Body.String()).To(ContainSubstring(`"data":{"ok":1}`))
		rec = write(&LibraryError{Type: LibraryNotFound, Message: "gone"})
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Body.String()).To(ContainSubstring(`"error"`))
		rec = write(ErrForbidden)
		Expect(rec.Code).To(Equal(http.StatusForbidden))
	})
})
