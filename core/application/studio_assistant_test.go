package application

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	mcpTools "github.com/mudler/LocalAI/core/http/endpoints/mcp"
)

var _ = Describe("the studio assistant", func() {
	It("is off without the server", func() {
		cfg := config.NewApplicationConfig()
		a := &Application{applicationConfig: cfg}
		as := studioAssistant{a}
		Expect(as.Enabled()).To(BeFalse(), "Enabled with no assistant server")
		Expect(as.SystemPrompt()).To(BeEmpty(), "an absent assistant must give no prompt and no tools")
		Expect(as.Executor().HasTools()).To(BeFalse(), "an absent assistant must give no prompt and no tools")
		Expect(as.ClientExecutor().HasTools()).To(BeFalse(), "an absent assistant has no client tools")
		model, err := as.FirstModel(context.Background(), config.FLAG_IMAGE)
		Expect(err).ToNot(HaveOccurred())
		Expect(model).To(BeEmpty(), "no model is chosen without the assistant's client")

		// A holder that never initialised has no tools either, .
		a.localAIAssistant = mcpTools.NewLocalAIAssistantHolder()
		Expect(as.Enabled()).To(BeFalse(), "Enabled with a holder that has no tools")
	})
})
