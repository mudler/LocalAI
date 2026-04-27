package cli

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Worker per-replica process keying", func() {
	Describe("buildProcessKey", func() {
		// Pin the supervisor's keying contract: distinct replica indexes for
		// the same modelID produce distinct process keys, so the supervisor
		// map can hold multiple processes for one model. Dropping the suffix
		// would re-introduce the original flap (one model, one slot, churn).
		DescribeTable("produces stable, distinct keys",
			func(modelID, backend string, replica int, want string) {
				Expect(buildProcessKey(modelID, backend, replica)).To(Equal(want))
			},
			Entry("modelID present, replica 0", "Qwen3-35B", "llama-cpp", 0, "Qwen3-35B#0"),
			Entry("modelID present, replica 1", "Qwen3-35B", "llama-cpp", 1, "Qwen3-35B#1"),
			Entry("falls back to backend when modelID empty", "", "llama-cpp", 0, "llama-cpp#0"),
			Entry("backend fallback with replica 2", "", "llama-cpp", 2, "llama-cpp#2"),
		)

		It("makes replicas distinguishable", func() {
			r0 := buildProcessKey("model-a", "llama-cpp", 0)
			r1 := buildProcessKey("model-a", "llama-cpp", 1)
			Expect(r0).ToNot(Equal(r1), "replicas of the same model must produce distinct keys")
		})
	})

	Describe("registrationBody", func() {
		It("includes max_replicas_per_model and the auto-label", func() {
			cmd := &WorkerCMD{
				Addr:                "worker.example.com:50051",
				MaxReplicasPerModel: 4,
			}
			body := cmd.registrationBody()

			Expect(body).To(HaveKey("max_replicas_per_model"))
			Expect(body["max_replicas_per_model"]).To(Equal(4))

			labels, ok := body["labels"].(map[string]string)
			Expect(ok).To(BeTrue(), "labels must be present so selectors can target the slot count")
			Expect(labels).To(HaveKeyWithValue("node.replica-slots", "4"))
		})

		It("coerces zero/unset MaxReplicasPerModel to 1", func() {
			cmd := &WorkerCMD{Addr: "worker.example.com:50051"}
			body := cmd.registrationBody()
			Expect(body["max_replicas_per_model"]).To(Equal(1),
				"unset must default to single-replica behavior, not capacity 0")

			labels := body["labels"].(map[string]string)
			Expect(labels).To(HaveKeyWithValue("node.replica-slots", "1"))
		})

		It("preserves user-provided labels alongside the auto-label", func() {
			cmd := &WorkerCMD{
				Addr:                "worker.example.com:50051",
				MaxReplicasPerModel: 2,
				NodeLabels:          "tier=fast,gpu=a100",
			}
			body := cmd.registrationBody()
			labels := body["labels"].(map[string]string)
			Expect(labels).To(HaveKeyWithValue("tier", "fast"))
			Expect(labels).To(HaveKeyWithValue("gpu", "a100"))
			Expect(labels).To(HaveKeyWithValue("node.replica-slots", "2"))
		})
	})
})
