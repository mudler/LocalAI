package main

import (
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Strata v0.2.15 removed its DeepSeek V4.1 code, so the helper refuses that
// family with a clear load error instead of failing later on a missing module.
// The spec needs a Strata source checkout (STRATA_ROOT) and a Python with
// regex and jinja2 (STRATA_TEST_PYTHON, default python3); it is skipped
// otherwise.
var _ = Describe("deepseek41 helper", func() {
	It("refuses the family with a clear error", func() {
		root, python := os.Getenv("STRATA_ROOT"), os.Getenv("STRATA_TEST_PYTHON")
		if root == "" {
			Skip("set STRATA_ROOT (a Strata checkout) to run")
		}
		_, err := newHelperTokenizer(python, root, GinkgoT().TempDir(), familyDeepSeek41, time.Minute)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("DeepSeek V4.1 family is not supported"))
	})
})
