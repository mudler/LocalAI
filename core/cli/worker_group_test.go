package cli

import (
	"strings"

	"github.com/alecthomas/kong"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/config"
)

// parseCLI parses args against a copy of the real command tree, with the
// variables cmd/local-ai/main.go supplies.
func parseCLI(args ...string) (*kong.Context, error) {
	app := CLI
	k, err := kong.New(&app, kong.Vars{
		"backendssystempath":   "/var/lib/local-ai/backends",
		"basepath":             kong.ExpandPath("."),
		"generatedcontentpath": DefaultGeneratedContentPath(),
		"uploadpath":           DefaultUploadPath(),
		"galleries":            config.DefaultGalleriesJSON,
		"backends":             config.DefaultBackendGalleriesJSON,
		"version":              "test",
	})
	Expect(err).ToNot(HaveOccurred())
	return k.Parse(args)
}

var _ = Describe("worker command group", func() {
	// `local-ai worker --flags` is the distributed-mode worker it always was; the
	// old p2p-worker name reaches the same group.
	DescribeTable("selects the command",
		func(args []string, want string) {
			ctx, err := parseCLI(args...)
			Expect(err).ToNot(HaveOccurred())
			Expect(ctx.Command()).To(HavePrefix(want))
		},
		Entry("worker with flags is the default child", []string{"worker", "--register-to=http://x", "--nats-url=nats://x"}, "worker run"),
		Entry("worker run", []string{"worker", "run", "--register-to=http://x", "--nats-url=nats://x"}, "worker run"),
		Entry("llama-cpp-rpc", []string{"worker", "llama-cpp-rpc"}, "worker llama-cpp-rpc"),
		Entry("mlx-distributed", []string{"worker", "mlx-distributed", "--hostfile=h", "--rank=0"}, "worker mlx-distributed"),
		Entry("p2p-worker llama-cpp-rpc", []string{"p2p-worker", "llama-cpp-rpc"}, "worker llama-cpp-rpc"),
		Entry("llama-cpp-rpc joining over P2P", []string{"worker", "llama-cpp-rpc", "--join", "localai://pair?v=1"}, "worker llama-cpp-rpc"),
		Entry("mlx-distributed joining over P2P, with no hostfile or rank", []string{"worker", "mlx-distributed", "--join", "localai://pair?v=1"}, "worker mlx-distributed"),
		Entry("p2p-worker vllm", []string{"p2p-worker", "vllm", "some/model", "--data-parallel-size=2", "--data-parallel-size-local=1", "--master-addr=h", "--master-port=1", "--start-rank=1"}, "worker vllm"),
		Entry("p2p-worker with flags is the default child", []string{"p2p-worker", "--register-to=http://x", "--nats-url=nats://x"}, "worker run"),
		Entry("worker that joins over P2P", []string{"worker", "--join=localai://pair?v=1", "--p2p-identity=/k", "--p2p-bootstrap=/ip4/127.0.0.1/tcp/1/p2p/x", "--allow-open-relay"}, "worker run"),
	)

	It("keeps the removed P2P workers parseable so that they can explain themselves", func() {
		for _, args := range [][]string{{"worker", "p2p-llama-cpp-rpc", "--token", "x"}, {"p2p-worker", "p2p-mlx", "--token=x", "--p2p-enable-limits=true"}} {
			ctx, err := parseCLI(args...)
			Expect(err).ToNot(HaveOccurred(), "%v", args)
			Expect(ctx.Run(&cliContext.Context{})).To(MatchError(ContainSubstring("was removed")))
		}
	})

	It("lists the new p2p commands", func() {
		for _, cmd := range []string{"relay", "relays add", "relays remove", "relays list", "relays check", "relays sync x", "relays claim x y", "id", "status", "pair --invite=x", "forward --peer=x --listen=127.0.0.1:1"} {
			ctx, err := parseCLI(append([]string{"p2p"}, strings.Fields(cmd)...)...)
			if cmd == "relays add" || cmd == "relays remove" {
				Expect(err).To(HaveOccurred(), "needs an argument")
				continue
			}
			Expect(err).ToNot(HaveOccurred(), cmd)
			Expect(ctx.Command()).To(HavePrefix("p2p "))
		}
	})

})
