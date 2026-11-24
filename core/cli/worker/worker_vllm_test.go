package worker

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("VLLMDistributed", func() {
	Describe("buildVLLMArgs", func() {
		DescribeTable("produces the expected vLLM CLI argv",
			func(cmd VLLMDistributed, want []string) {
				Expect(cmd.buildVLLMArgs()).To(Equal(want))
			},
			Entry("headless follower with explicit master",
				VLLMDistributed{
					Model:                 "Qwen/Qwen3.5-1.5B",
					DataParallelSize:      4,
					DataParallelSizeLocal: 2,
					StartRank:             2,
					MasterAddr:            "10.0.0.1",
					MasterPort:            32100,
					Headless:              true,
				},
				[]string{
					"serve", "Qwen/Qwen3.5-1.5B",
					"--headless",
					"--data-parallel-size", "4",
					"--data-parallel-size-local", "2",
					"--data-parallel-start-rank", "2",
					"--data-parallel-address", "10.0.0.1",
					"--data-parallel-rpc-port", "32100",
				},
			),
			Entry("head-style invocation: rank 0, not headless",
				VLLMDistributed{
					Model:                 "moonshotai/Kimi-K2.6-Instruct",
					DataParallelSize:      8,
					DataParallelSizeLocal: 4,
					StartRank:             0,
					MasterAddr:            "127.0.0.1",
					MasterPort:            32100,
					Headless:              false,
				},
				[]string{
					"serve", "moonshotai/Kimi-K2.6-Instruct",
					"--data-parallel-size", "8",
					"--data-parallel-size-local", "4",
					"--data-parallel-start-rank", "0",
					"--data-parallel-address", "127.0.0.1",
					"--data-parallel-rpc-port", "32100",
				},
			),
			Entry("extra args appended verbatim",
				VLLMDistributed{
					Model:                 "Qwen/Qwen3.5-1.5B",
					DataParallelSize:      2,
					DataParallelSizeLocal: 1,
					StartRank:             1,
					MasterAddr:            "head.local",
					MasterPort:            32100,
					Headless:              true,
					ExtraArgs:             []string{"--tensor-parallel-size", "2", "--enable-expert-parallel"},
				},
				[]string{
					"serve", "Qwen/Qwen3.5-1.5B",
					"--headless",
					"--data-parallel-size", "2",
					"--data-parallel-size-local", "1",
					"--data-parallel-start-rank", "1",
					"--data-parallel-address", "head.local",
					"--data-parallel-rpc-port", "32100",
					"--tensor-parallel-size", "2",
					"--enable-expert-parallel",
				},
			),
		)
	})

	Describe("registrationBody", func() {
		// Followers don't host LocalAI gRPC, so node_type must be "agent"
		// to bypass the address requirement on /api/node/register, and the
		// node.role label is the contract operators rely on to scope normal
		// model placement away from these nodes.
		It("registers as agent-type with the vllm-follower role label", func() {
			cmd := VLLMDistributed{
				NodeName:              "test-follower",
				DataParallelSize:      4,
				DataParallelSizeLocal: 2,
				StartRank:             2,
				MasterAddr:            "10.0.0.1",
				NodeLabels:            "tier=fast,gpu.vendor=nvidia",
			}
			body := cmd.registrationBody()

			Expect(body).To(HaveKeyWithValue("node_type", "agent"))
			Expect(body).To(HaveKeyWithValue("name", "test-follower"))

			labels, ok := body["labels"].(map[string]string)
			Expect(ok).To(BeTrue(), "labels must be map[string]string")
			Expect(labels).To(HaveKeyWithValue("node.role", "vllm-follower"))
			Expect(labels).To(HaveKeyWithValue("tier", "fast"))
			Expect(labels).To(HaveKeyWithValue("gpu.vendor", "nvidia"))
		})
	})
})
