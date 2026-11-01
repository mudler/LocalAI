package application

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	mcpTools "github.com/mudler/LocalAI/core/http/endpoints/mcp"
)

var _ = Describe("the studio assistant", func() {
	It("is off without the server or with the setting off", func() {
		cfg := config.NewApplicationConfig()
		a := &Application{applicationConfig: cfg}
		as := studioAssistant{a}
		Expect(as.Enabled()).To(BeFalse(), "Enabled with no assistant server")
		Expect(as.SystemPrompt()).To(BeEmpty(), "an absent assistant must give no prompt and no tools")
		Expect(as.Executor().HasTools()).To(BeFalse(), "an absent assistant must give no prompt and no tools")

		// A holder that never initialised has no tools either, setting on or off.
		a.localAIAssistant = mcpTools.NewLocalAIAssistantHolder()
		Expect(as.Enabled()).To(BeFalse(), "Enabled with a holder that has no tools")
		cfg.DisableLocalAIAssistant = true
		Expect(as.Enabled()).To(BeFalse(), "Enabled while the setting is off")
	})
})
