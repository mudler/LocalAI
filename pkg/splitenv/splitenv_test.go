package splitenv_test

import (
	"encoding/json"
	"os"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/splitenv"
)

func TestSplitEnv(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Split engine environment test")
}

func readHostfile(path string) []string {
	GinkgoHelper()
	data, err := os.ReadFile(path)
	Expect(err).ToNot(HaveOccurred())
	var hosts []string
	Expect(json.Unmarshal(data, &hosts)).To(Succeed())
	return hosts
}

var _ = Describe("the engine environment", func() {
	BeforeEach(func() {
		for _, k := range []string{splitenv.LlamaServers, splitenv.MLXHostfile} {
			if v, ok := os.LookupEnv(k); ok {
				DeferCleanup(os.Setenv, k, v)
			} else {
				DeferCleanup(os.Unsetenv, k)
			}
		}
	})

	It("sets the llama.cpp RPC servers and unsets them with none", func() {
		Expect(splitenv.SetLlama([]string{"127.0.0.1:1", "127.0.0.1:2"})).To(Succeed())
		Expect(os.Getenv(splitenv.LlamaServers)).To(Equal("127.0.0.1:1,127.0.0.1:2"))
		Expect(splitenv.SetLlama(nil)).To(Succeed())
		_, ok := os.LookupEnv(splitenv.LlamaServers)
		Expect(ok).To(BeFalse(), "an empty list is unset, not set to empty")
	})

	It("writes the MLX hostfile and points the environment at it", func() {
		dir := GinkgoT().TempDir()
		Expect(splitenv.SetMLX(dir, []string{"127.0.0.1:5", "127.0.0.1:6"})).To(Succeed())
		path := os.Getenv(splitenv.MLXHostfile)
		Expect(readHostfile(path)).To(Equal([]string{"127.0.0.1:5", "127.0.0.1:6"}))
		Expect(splitenv.SetMLX(dir, nil)).To(Succeed())
		_, ok := os.LookupEnv(splitenv.MLXHostfile)
		Expect(ok).To(BeFalse())
	})
})
