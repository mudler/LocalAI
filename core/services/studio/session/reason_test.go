package session

import (
	"errors"
	"fmt"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("the reason of a failed turn", func() {
	ginkgo.It("gives a refused model request a reason the user can act on", func() {
		for _, tc := range []struct {
			err  error
			want string
		}{
			{fmt.Errorf("call failed: %w", errors.New("Post x: "+MarkNotAllowed)), ReasonNotAllowed},
			{errors.New("agent: " + MarkQuota), ReasonQuota},
			{errors.New("connection reset"), reasonAgent},
		} {
			Expect(reasonFor(tc.err)).To(Equal(tc.want), "reasonFor(%v)", tc.err)
			Expect(fixedReason(reasonFor(tc.err))).To(Equal(reasonFor(tc.err)), "a reason must survive the transcript filter")
		}
	})

	ginkgo.DescribeTable("gives a model error a reason that says what to do",
		func(err error, contains []string) {
			got := reasonFor(err)
			for _, want := range contains {
				Expect(got).To(ContainSubstring(want))
			}
			Expect(got).NotTo(ContainSubstring("upstream.internal"), "the reason leaks an internal address")
			Expect(fixedReason(got)).To(Equal(got), "the transcript filter rewrote the reason")
		},
		ginkgo.Entry("context too small with numbers", errors.New("the request (9100 tokens) exceeds the available context size (8192)"),
			[]string{"context window is too small", "908 tokens"}),
		ginkgo.Entry("context too small without numbers", errors.New("prompt is too long for the context"),
			[]string{"context window is too small"}),
		ginkgo.Entry("the model calls a tool that is not offered", errors.New("failed to select tool: selected tool get-time not found in available tools"),
			[]string{"tool that is not available", "get-time", "<connector>__<tool>"}),
		ginkgo.Entry("model keeps failing", errors.New("the model failed 4 times in a row (last: HTTP 502 bad gateway upstream.internal:8080)"),
			[]string{"The model failed", "HTTP 502"}),
	)
})
