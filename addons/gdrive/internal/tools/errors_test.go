// SPDX-License-Identifier: MIT
package tools_test

import (
	"context"
	"errors"

	"github.com/mudler/LocalAI/addons/gdrive/internal/tools"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("what the service answers", func() {
	var w *world
	BeforeEach(func() { w = newWorld() })

	read := func() error {
		_, _, err := w.read("get-metadata", map[string]any{"folder": "proj", "fileId": "notes"})
		return err
	}

	It("turns a 401 into credential_rejected, so the host renews the token", func() {
		w.srv.RevokeToken(token)
		err := read()
		Expect(err).To(MatchError(contract.ErrCredentialRejected))
		_, _, err = w.read("read-file", map[string]any{"folder": "proj", "fileId": "notes"})
		Expect(errors.Is(err, contract.ErrCredentialRejected)).To(BeTrue())
		_, err = w.rt.Preview(context.Background(), envFor(token, "proj", "trash-file", map[string]any{"folder": "proj", "fileId": "notes"}, bothScopes...))
		Expect(errors.Is(err, contract.ErrCredentialRejected)).To(BeTrue())
	})

	DescribeTable("a limit or an outage is retryable and keeps the wait", func(status int, reason, header string, seconds int) {
		w.srv.Inject("/drive/v3/files/notes", status, reason, header, 1)
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

	It("does not retry a refusal that waiting does not fix", func() {
		w.srv.Inject("/drive/v3/files/notes", 403, "forbidden", "", 1)
		err := read()
		Expect(err).To(MatchError(ContainSubstring("refused the request for this file")))
		var limited *contract.ErrRateLimited
		Expect(errors.As(err, &limited)).To(BeFalse())
	})

	It("names no token and no file name in a refusal", func() {
		w.srv.Inject("/drive/v3/files/notes", 400, "badRequest", "", 1)
		err := read()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).NotTo(ContainSubstring(token))
		Expect(err.Error()).NotTo(ContainSubstring("notes.txt"))
	})

	It("stops a search that would look up too many folders", func() {
		parent := "proj"
		for i := range tools.MaxLookups + 5 {
			id := "chain" + string(rune('a'+i%26)) + string(rune('a'+i/26))
			w.srv.AddFolder(id, id, parent)
			parent = id
		}
		_, _, err := w.read("get-metadata", map[string]any{"folder": "proj", "fileId": parent})
		Expect(err).To(HaveOccurred())
	})
})
