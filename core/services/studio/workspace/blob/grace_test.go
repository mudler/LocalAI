package blob_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
)

var _ = Describe("Grace", func() {
	DescribeTable("turns the BlobGrace option into the wait of RemoveLater",
		func(in, want time.Duration) {
			Expect(blob.Grace(in)).To(Equal(want))
		},
		Entry("unset uses the default", time.Duration(0), blob.DefaultGrace),
		Entry("negative removes at once", -time.Second, time.Duration(0)),
		Entry("positive is kept", 5*time.Second, 5*time.Second),
	)
})
