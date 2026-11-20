// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"strings"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("image markers in chat messages", func() {
	DescribeTable("maps image placeholders in conversation order",
		func(marker string) {
			opts := &pb.PredictOptions{
				Images: []string{"first", "second", "third"},
				Messages: []*pb.Message{
					{Role: "user", Content: strings.Repeat(marker, 2) + "describe"},
					{Role: "assistant", Content: "two pictures"},
					{Role: "user", Content: marker + "compare"},
				},
			}
			req, err := buildPromptRequest(opts, marker, nil, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(req.Messages).To(HaveLen(3))
			Expect(req.Messages[1]["content"]).To(Equal("two pictures"))
			var urls []string
			for _, i := range []int{0, 2} {
				parts := req.Messages[i]["content"].([]map[string]any)
				for _, part := range parts {
					if part["type"] == "image_url" {
						urls = append(urls, part["image_url"].(map[string]any)["url"].(string))
					}
				}
			}
			Expect(urls).To(Equal([]string{
				fmt.Sprintf("%s0", imagePlaceholder),
				fmt.Sprintf("%s1", imagePlaceholder),
				fmt.Sprintf("%s2", imagePlaceholder),
			}))
		},
		Entry("default marker", defaultMediaMarker),
		Entry("custom marker", "<image:here>"),
	)
})
