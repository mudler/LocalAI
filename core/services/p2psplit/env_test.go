package p2psplit_test

import (
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/p2psplit"
)

var _ = Describe("the engine environment", func() {
	BeforeEach(func() {
		for _, k := range []string{p2psplit.LlamaServersEnv, p2psplit.MLXHostfileEnv} {
			if v, ok := os.LookupEnv(k); ok {
				DeferCleanup(os.Setenv, k, v)
			} else {
				DeferCleanup(os.Unsetenv, k)
			}
		}
	})

	It("sets the llama.cpp RPC servers and unsets them with none", func() {
		Expect(p2psplit.SetLlamaEnv([]string{"127.0.0.1:1", "127.0.0.1:2"})).To(Succeed())
		Expect(os.Getenv(p2psplit.LlamaServersEnv)).To(Equal("127.0.0.1:1,127.0.0.1:2"))
		Expect(p2psplit.SetLlamaEnv(nil)).To(Succeed())
		_, ok := os.LookupEnv(p2psplit.LlamaServersEnv)
		Expect(ok).To(BeFalse(), "an empty list is unset, not set to empty")
	})

	It("writes the MLX hostfile and points the environment at it", func() {
		dir := GinkgoT().TempDir()
		Expect(p2psplit.SetMLXEnv(dir, []string{"127.0.0.1:5", "127.0.0.1:6"})).To(Succeed())
		path := os.Getenv(p2psplit.MLXHostfileEnv)
		Expect(readHostfile(path)).To(Equal([]string{"127.0.0.1:5", "127.0.0.1:6"}))
		Expect(p2psplit.SetMLXEnv(dir, nil)).To(Succeed())
		_, ok := os.LookupEnv(p2psplit.MLXHostfileEnv)
		Expect(ok).To(BeFalse())
	})
})
