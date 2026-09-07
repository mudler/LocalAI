package main

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

var _ = Describe("managed DFlash companion options", func() {
	It("replaces only the draft model in an existing speculative configuration", func() {
		managedPath := ".artifacts/huggingface/0123456789abcdef/snapshot"
		lo := parseOptions(&pb.ModelOptions{
			Options: []string{"draft_model:" + managedPath},
			EngineArgs: `{
				"speculative_config": {
					"method": "dflash",
					"model": "Mia-AiLab/Qwen3.8-27B-DFlash2-EXL3-5.0bpw",
					"num_speculative_tokens": 7
				}
			}`,
		})

		Expect(lo.speculativeConfig).To(MatchJSON(`{
			"method": "dflash",
			"model": ".artifacts/huggingface/0123456789abcdef/snapshot",
			"num_speculative_tokens": 7
		}`))
	})

	It("ignores a draft companion when speculative decoding is not configured", func() {
		lo := parseOptions(&pb.ModelOptions{
			Options: []string{"draft_model:.artifacts/huggingface/0123456789abcdef/snapshot"},
		})

		Expect(lo.speculativeConfig).To(BeEmpty())
	})
})
