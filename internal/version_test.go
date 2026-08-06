package internal

import (
	"fmt"
	"runtime"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("UserAgent", func() {
	platform := fmt.Sprintf("(%s; %s)", runtime.GOOS, runtime.GOARCH)

	BeforeEach(func() {
		saved := Version
		DeferCleanup(func() { Version = saved })
	})

	DescribeTable("identifies the build",
		func(version, want string) {
			Version = version
			Expect(UserAgent()).To(Equal(want))
		},
		Entry("source build without a stamped version", "", "LocalAI "+platform),
		Entry("released build", "v3.2.1", "LocalAI/v3.2.1 "+platform),
	)

	// The platform suffix is what distinguishes a real build from the bare
	// fallback, so assert it is genuinely present rather than trusting only the
	// composed strings above — those would still pass if the format string and
	// the expectation drifted together.
	DescribeTable("always carries the platform",
		func(version string) {
			Version = version
			Expect(UserAgent()).To(And(
				ContainSubstring(runtime.GOOS),
				ContainSubstring(runtime.GOARCH),
			))
		},
		Entry("source build", ""),
		Entry("released build", "v9.9.9"),
	)
})
