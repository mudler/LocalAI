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
	parse := func(args ...string) error {
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
			return err
		}
		_, err = p.Parse(args)
		return err
	}

	It("starts with LOCALAI_DISABLE_ASSISTANT in the environment, which is no longer read", func() {
		GinkgoT().Setenv("LOCALAI_DISABLE_ASSISTANT", "true")
		Expect(parse()).To(Succeed())
	})

	It("no longer has the --disable-local-ai-assistant flag", func() {
		Expect(parse("--disable-local-ai-assistant")).To(HaveOccurred())
	})
})
