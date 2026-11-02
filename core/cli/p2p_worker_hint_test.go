package cli

import (
	"strings"

	"github.com/alecthomas/kong"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

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
