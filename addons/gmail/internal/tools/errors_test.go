// SPDX-License-Identifier: MIT
package tools_test

import (
	"errors"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("what the service answers", func() {
	var w *world
	BeforeEach(func() { w = newWorld() })

	read := func() error {
		_, _, err := w.read("get-message", map[string]any{"messageId": "plain"})
		return err
	}

	It("turns a 401 into credential_rejected, so the host renews the token", func() {
		w.srv.RevokeToken(token)
		Expect(read()).To(MatchError(contract.ErrCredentialRejected))
		_, _, err := w.read("search-messages", map[string]any{})
		Expect(errors.Is(err, contract.ErrCredentialRejected)).To(BeTrue())
		_, err = w.rt.Preview(ctx(), envFor(token, "Work", "send-draft", map[string]any{"label": "Work", "draftId": "r1"}, bothScopes...))
		Expect(errors.Is(err, contract.ErrCredentialRejected)).To(BeTrue())
	})

	DescribeTable("a limit or an outage is retryable and keeps the wait", func(status int, reason, header string, seconds int) {
		w.srv.Inject("/gmail/v1/users/me/messages/plain", status, reason, header, 1)
		err := read()
		var limited *contract.ErrRateLimited
		Expect(errors.As(err, &limited)).To(BeTrue(), "%v", err)
		Expect(limited.Seconds()).To(Equal(seconds))
		Expect(read()).To(Succeed(), "the next call works")
	},
		Entry("403 rateLimitExceeded with Retry-After", 403, "rateLimitExceeded", "7", 7),
		Entry("403 userRateLimitExceeded without a wait", 403, "userRateLimitExceeded", "", 30),
		Entry("429 with Retry-After", 429, "rateLimitExceeded", "12", 12),
		Entry("429 without a wait", 429, "", "", 30),
		Entry("503 with Retry-After", 503, "backendError", "9", 9),
		Entry("500 without a wait", 500, "internalError", "", 5),
		Entry("429 with a wait above the bound", 429, "", "999999", contract.MaxRetryAfter),
	)

	It("says clearly that a message was not found, with no ID or text", func() {
		_, _, err := w.read("get-message", map[string]any{"messageId": "gone-id"})
		Expect(err).To(MatchError(ContainSubstring("not found")))
		Expect(err.Error()).NotTo(ContainSubstring("gone-id"))
	})

	It("does not retry a refusal that waiting does not fix", func() {
		w.srv.Inject("/gmail/v1/users/me/messages/plain", 403, "forbidden", "", 1)
		err := read()
		Expect(err).To(MatchError(ContainSubstring("refused the request")))
		var limited *contract.ErrRateLimited
		Expect(errors.As(err, &limited)).To(BeFalse())
	})

	It("names no token, address or subject in a refusal", func() {
		w.srv.Inject("/gmail/v1/users/me/messages/plain", 400, "badRequest", "", 1)
		err := read()
		Expect(err).To(HaveOccurred())
		for _, s := range []string{token, "alice@example.test", "Quarterly"} {
			Expect(err.Error()).NotTo(ContainSubstring(s))
		}
	})

	It("passes a limit on during a search whose metadata calls are limited", func() {
		w.srv.Inject("/gmail/v1/users/me/messages/", 429, "rateLimitExceeded", "3", 1)
		_, _, err := w.read("search-messages", map[string]any{})
		var limited *contract.ErrRateLimited
		Expect(errors.As(err, &limited)).To(BeTrue(), "%v", err)
		Expect(limited.Seconds()).To(Equal(3))
	})
})
