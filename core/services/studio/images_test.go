package studio_test

import (
	"strings"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/media"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// imagesChat is a chat service with files whose chat model sees images when
// sees is true, and whose describe_image model is describer ("" for none).
func imagesChat(sees bool, describer string) (*studio.Service, string, func() string) {
	f := newFiles()
	m := media.New(provider.New(), nil, func(media.Capability) string { return "" }).
		WithVisionChooser(func(string) (string, bool) { return describer, false })
	say := func(workspace.Actor, string) []studiotest.ToolStep { return []studiotest.ToolStep{{Say: "ok"}} }
	svc, h := newToolService(say, func(d *studio.Deps) {
		d.Files = f
		d.Media = m
		d.Modalities = func(string) studio.Modalities { return studio.Modalities{Vision: sees} }
	})
	id := create(svc, alice)
	return svc, id, func() string { return turnPrompt(svc, h, id) }
}

// imagesLine is the block of the prompt that starts with "Images:".
func imagesLine(prompt string) string {
	for _, part := range strings.Split(prompt, "\n\n") {
		if strings.HasPrefix(part, "Images:") {
			return part
		}
	}
	return ""
}

var _ = Describe("what the model is told about images", func() {
	It("tells a model that sees images to answer about them directly", func() {
		_, _, prompt := imagesChat(true, "the-chat-model")
		Expect(imagesLine(prompt())).To(Equal("Images: you see the images attached to the user's message, with the message. " +
			"Answer about them directly. Use describe_image only for an image in the user's files that is not in the message."))
	})

	It("tells a model that cannot see images to use describe_image", func() {
		_, _, prompt := imagesChat(false, "helper-vl")
		Expect(imagesLine(prompt())).To(Equal("Images: you cannot see images. " +
			"To look at an image the user attached or has in their files, call describe_image with its path."))
	})

	It("tells a model with no way to see images to say so and suggest another model", func() {
		_, _, prompt := imagesChat(false, "")
		Expect(imagesLine(prompt())).To(Equal("Images: you cannot see images, and no model here can describe them. " +
			"When the user shares an image, tell them plainly and suggest that they choose a model that can look at images for this chat."))
	})
})
