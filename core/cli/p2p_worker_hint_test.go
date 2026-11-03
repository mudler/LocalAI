package cli

import (
	"strings"

	"github.com/alecthomas/kong"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/cli/worker"
	"github.com/mudler/LocalAI/core/config"
)

var _ = Describe("p2p worker hint", func() {
	It("names a command the CLI accepts", func() {
		// Parse a copy so the package-level CLI is left untouched. The vars
		// are the ones cmd/local-ai/main.go supplies; the tags cannot be
		// resolved without them.
		app := CLI
		k, err := kong.New(&app, kong.Vars{
			"basepath":             kong.ExpandPath("."),
			"generatedcontentpath": DefaultGeneratedContentPath(),
			"uploadpath":           DefaultUploadPath(),
			"galleries":            config.DefaultGalleriesJSON,
			"backends":             config.DefaultBackendGalleriesJSON,
			"version":              "test",
		})
		Expect(err).ToNot(HaveOccurred())

		args := strings.Fields(workerJoinCommand)
		Expect(args[0]).To(Equal("local-ai"))

		ctx, err := k.Parse(args[1:])
		Expect(err).ToNot(HaveOccurred())
		Expect(ctx.Command()).To(Equal("p2p-worker p2p-llama-cpp-rpc"))
	})
})

var _ = Describe("P2P limits CLI boundary", func() {
	It("preserves exact true semantics at every entry point", func() {
		for _, value := range []string{"", "true", "false", "1", "yes"} {
			GinkgoT().Setenv("LOCALAI_P2P_ENABLE_LIMITS", value)
			run := RunCMD{}
			rpc := worker.P2P{}
			mlx := worker.P2PMLX{}
			explorer := ExplorerCMD{}
			for _, entry := range []any{&run, &rpc, &mlx, &explorer} {
				parser, err := kong.New(entry, kong.Vars{"basepath": ".", "generatedcontentpath": ".", "uploadpath": ".", "galleries": config.DefaultGalleriesJSON, "backends": config.DefaultBackendGalleriesJSON, "version": "test"})
				Expect(err).NotTo(HaveOccurred())
				_, err = parser.Parse(nil)
				Expect(err).NotTo(HaveOccurred())
			}
			Expect(run.P2PEnableLimits == "true").To(Equal(value == "true"))
			Expect(rpc.P2PEnableLimits == "true").To(Equal(value == "true"))
			Expect(mlx.P2PEnableLimits == "true").To(Equal(value == "true"))
			Expect(explorer.P2PEnableLimits == "true").To(Equal(value == "true"))
		}
	})

	It("accepts the documented --p2p-enable-limits flag at every entry point", func() {
		GinkgoT().Setenv("LOCALAI_P2P_ENABLE_LIMITS", "")
		run := RunCMD{}
		rpc := worker.P2P{}
		mlx := worker.P2PMLX{}
		explorer := ExplorerCMD{}
		entries := map[string]*string{
			"run": &run.P2PEnableLimits, "rpc": &rpc.P2PEnableLimits,
			"mlx": &mlx.P2PEnableLimits, "explorer": &explorer.P2PEnableLimits,
		}
		for name, entry := range map[string]any{"run": &run, "rpc": &rpc, "mlx": &mlx, "explorer": &explorer} {
			parser, err := kong.New(entry, kong.Vars{"basepath": ".", "generatedcontentpath": ".", "uploadpath": ".", "galleries": config.DefaultGalleriesJSON, "backends": config.DefaultBackendGalleriesJSON, "version": "test"})
			Expect(err).NotTo(HaveOccurred())
			_, err = parser.Parse([]string{"--p2p-enable-limits=true"})
			Expect(err).NotTo(HaveOccurred(), name)
			Expect(*entries[name]).To(Equal("true"), name)
		}
	})
})
