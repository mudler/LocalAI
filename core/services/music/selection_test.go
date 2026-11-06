// SPDX-License-Identifier: MIT
package music

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("named workflow gate selection", Label("coverage"), func() {
	It("uses Go's top-level regexp separators without truncating expressions", func() {
		for _, tc := range []struct {
			pattern string
			labels  []string
		}{
			{"", []string{"traces", "invariants", "coverage", "determinism"}},
			{`^TestMusicWorkflow[^/]*Coverage$`, []string{"coverage"}},
			{`^TestMusicWorkflow[^/|]*Coverage$`, []string{"coverage"}},
			{`^TestMusicWorkflowCoverage$/child`, []string{"coverage"}},
			{`^TestMusicWorkflowTraces$/child|^TestMusicWorkflowCoverage$/other`, []string{"traces", "coverage"}},
			{`^TestMusicWorkflow(Traces|Coverage)$`, []string{"traces", "coverage"}},
			{`^TestMusicWorkflow(Coverage|Traces/child)$`, []string{"coverage"}},
			{`^TestMusicWorkflowCoverage$|^TestMusicWorkflowCoverage$/child`, []string{"coverage"}},
			{`^TestMusicWorkflowInvariants$`, []string{"invariants"}},
			{`^TestMusicWorkflowDeterminism$`, []string{"determinism"}},
			{`^TestMusicWorkflowCoverage\/$`, []string{}},
			{`^TestMusicWorkflowCoverage\|$`, []string{}},
			{`^TestMusicWorkflowCoverage\[$`, []string{}},
			{`^TestMusicWorkflowCoverage]$/child`, []string{}},
			{`^Nothing$`, []string{}},
		} {
			labels, err := workflowLabels(tc.pattern)
			Expect(err).NotTo(HaveOccurred(), tc.pattern)
			Expect(labels).To(Equal(tc.labels), tc.pattern)
		}
		Expect(workflowRunPatterns(`a\/b/c|d(e/f|g)/h`)).To(Equal([][]string{{`a\/b`, "c"}, {"d(e/f|g)", "h"}}))
		Expect(workflowRunPatterns(`a\\/b`)).To(Equal([][]string{{`a\\`, "b"}}))
		Expect(workflowRunPatterns(`a[[:alpha:]/]b/c`)).To(Equal([][]string{{`a[[:alpha:]/]b`, "c"}}))
		for _, pattern := range []string{`[`, `abc\`, `TestMusicWorkflowCoverage/[`, `TestMusicWorkflowCoverage|(`} {
			_, err := workflowLabels(pattern)
			Expect(err).To(HaveOccurred(), pattern)
		}
	})
})
