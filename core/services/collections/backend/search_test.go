package backend

import (
	"context"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Searching a small collection", func() {
	var b *backendInProcess

	BeforeEach(func() {
		settings := &CollectionModelSettings{}
		b, _, _ = newModelBackend(settings)
		Expect(b.CreateCollection("small")).To(Succeed())
	})

	It("answers an empty collection with no passages, whatever number is asked for", func() {
		for _, n := range []int{0, 1, 5, 20} {
			res, err := b.Search(context.Background(), "small", "anything", n)
			Expect(err).ToNot(HaveOccurred(), "max results %d", n)
			Expect(res).To(BeEmpty())
		}
	})

	It("answers with every passage when more are asked for than the collection holds", func() {
		_, err := b.Upload(context.Background(), "small", "one.txt", strings.NewReader("one short passage"))
		Expect(err).ToNot(HaveOccurred())
		for _, n := range []int{0, 1, 5, 20} {
			res, err := b.Search(context.Background(), "small", "passage", n)
			Expect(err).ToNot(HaveOccurred(), "max results %d", n)
			Expect(res).To(HaveLen(1))
		}
	})
})
