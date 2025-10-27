package http_test

import (
	"encoding/json"

	openai "github.com/mudler/LocalAI/core/http/endpoints/openai"
	"github.com/mudler/LocalAI/core/schema"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("MapOpenAIToVideo", func() {
	It("maps size and seconds correctly", func() {
		cases := []struct {
			name     string
			input    *schema.OpenAIRequest
			raw      map[string]interface{}
			expectsW int32
			expectsH int32
			expectsF int32
			expectsN int32
		}{
			{
				name: "size in input",
				input: &schema.OpenAIRequest{
					PredictionOptions: schema.PredictionOptions{
						BasicModelRequest: schema.BasicModelRequest{Model: "m"},
					},
					Size: "256x128",
				},
				expectsW: 256,
				expectsH: 128,
			},
			{
				name:  "size in raw and seconds as string",
				input: &schema.OpenAIRequest{PredictionOptions: schema.PredictionOptions{BasicModelRequest: schema.BasicModelRequest{Model: "m"}}},
				raw:   map[string]interface{}{"size": "720x480", "seconds": "2"},
				expectsW: 720,
				expectsH: 480,
				expectsF: 30,
				expectsN: 60,
			},
			{
				name:  "seconds as number and fps override",
				input: &schema.OpenAIRequest{PredictionOptions: schema.PredictionOptions{BasicModelRequest: schema.BasicModelRequest{Model: "m"}}},
				raw:   map[string]interface{}{"seconds": 3.0, "fps": 24.0},
				expectsF: 24,
				expectsN: 72,
			},
		}

		for _, c := range cases {
			By(c.name)
			vr := openai.MapOpenAIToVideo(c.input, c.raw)
			if c.expectsW != 0 {
				Expect(vr.Width).To(Equal(c.expectsW))
			}
			if c.expectsH != 0 {
				Expect(vr.Height).To(Equal(c.expectsH))
			}
			if c.expectsF != 0 {
				Expect(vr.FPS).To(Equal(c.expectsF))
			}
			if c.expectsN != 0 {
				Expect(vr.NumFrames).To(Equal(c.expectsN))
			}

			b, err := json.Marshal(vr)
			Expect(err).ToNot(HaveOccurred())
			_ = b
		}
	})
})

