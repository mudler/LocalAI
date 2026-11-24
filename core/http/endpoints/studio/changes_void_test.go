package studio

import (
	"fmt"
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	studiosvc "github.com/mudler/LocalAI/core/services/studio"
)

var _ = Describe("the answer to a confirm of a proposal that can no longer be applied", func() {
	It("is a conflict with the text the owner reads, and no 500", func() {
		err := fmt.Errorf("%w: Bobby can no longer edit this project (it is not shared with them, or it is gone, moved or broken). Nothing was written", studiosvc.ErrProposalVoid)
		status, body := answer(failChange, err)
		Expect(status).To(Equal(http.StatusConflict))
		Expect(body.Type).To(Equal(typeConflict))
		Expect(body.Message).To(Equal("this proposal can no longer be applied: Bobby can no longer edit this project (it is not shared with them, or it is gone, moved or broken). Nothing was written"))
	})
})
