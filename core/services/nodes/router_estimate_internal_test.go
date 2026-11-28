package nodes

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

var _ = Describe("estimateModelVRAM", func() {
	It("sizes a model from its local weight file", func() {
		// A sparse file: the estimate comes from its size, no GGUF header needed.
		const gib = int64(1) << 30
		path := filepath.Join(GinkgoT().TempDir(), "model.gguf")
		f, err := os.Create(path)
		Expect(err).ToNot(HaveOccurred())
		Expect(f.Truncate(4 * gib)).To(Succeed())
		Expect(f.Close()).To(Succeed())

		router := NewSmartRouter(&fakeModelRouter{}, SmartRouterOptions{})
		est := router.estimateModelVRAM(context.Background(), &pb.ModelOptions{ModelFile: path, ContextSize: 2048})

		Expect(est).To(BeNumerically(">=", uint64(4*gib)))
	})
})
