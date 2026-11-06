// SPDX-License-Identifier: MIT
package cli

import (
	"github.com/alecthomas/kong"
	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the removed LocalAI Assistant switch", Serial, func() {
	// parse returns the removed option as parsed, and the parse error.
	parse := func(args ...string) (RemovedAssistantOptions, error) {
		command := struct {
			cliContext.Context `embed:""`
			Run                RunCMD `cmd:"" default:"withargs"`
		}{}
		p, err := kong.New(&command, kong.Vars{
			"basepath":             kong.ExpandPath("."),
			"generatedcontentpath": DefaultGeneratedContentPath(),
			"uploadpath":           DefaultUploadPath(),
			"galleries":            config.DefaultGalleriesJSON,
			"backends":             config.DefaultBackendGalleriesJSON,
		})
		if err != nil {
			return RemovedAssistantOptions{}, err
		}
		_, err = p.Parse(args)
		return command.Run.RemovedAssistantOptions, err
	}

	It("starts with LOCALAI_DISABLE_ASSISTANT in the environment, maps it and warns about it", func() {
		GinkgoT().Setenv("LOCALAI_DISABLE_ASSISTANT", "true")
		o, err := parse()
		Expect(err).NotTo(HaveOccurred())
		Expect(o.set()).To(Equal([]string{"LOCALAI_DISABLE_ASSISTANT"}))
	})

	It("accepts the --disable-local-ai-assistant flag, maps it and warns about it", func() {
		o, err := parse("--disable-local-ai-assistant")
		Expect(err).NotTo(HaveOccurred())
		Expect(o.set()).To(Equal([]string{"LOCALAI_DISABLE_ASSISTANT"}))
		Expect(func() { o.warn() }).NotTo(Panic())
	})

	It("warns about nothing when the option is not set", func() {
		o, err := parse()
		Expect(err).NotTo(HaveOccurred())
		Expect(o.set()).To(BeEmpty())
	})
})
