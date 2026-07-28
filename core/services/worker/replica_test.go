package worker

import (
	"encoding/json"

	"github.com/mudler/LocalAI/core/services/messaging"
	process "github.com/mudler/go-processmanager"
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
			cfg := &Config{
				Addr:                "worker.example.com:50051",
				MaxReplicasPerModel: 4,
			}
			body := cfg.registrationBody()

			Expect(body).To(HaveKey("max_replicas_per_model"))
			Expect(body["max_replicas_per_model"]).To(Equal(4))

			labels, ok := body["labels"].(map[string]string)
			Expect(ok).To(BeTrue(), "labels must be present so selectors can target the slot count")
			Expect(labels).To(HaveKeyWithValue("node.replica-slots", "4"))
		})

		It("coerces zero/unset MaxReplicasPerModel to 1", func() {
			cfg := &Config{Addr: "worker.example.com:50051"}
			body := cfg.registrationBody()
			Expect(body["max_replicas_per_model"]).To(Equal(1),
				"unset must default to single-replica behavior, not capacity 0")

			labels := body["labels"].(map[string]string)
			Expect(labels).To(HaveKeyWithValue("node.replica-slots", "1"))
		})

		It("preserves user-provided labels alongside the auto-label", func() {
			cfg := &Config{
				Addr:                "worker.example.com:50051",
				MaxReplicasPerModel: 2,
				NodeLabels:          "tier=fast,gpu=a100",
			}
			body := cfg.registrationBody()
			labels := body["labels"].(map[string]string)
			Expect(labels).To(HaveKeyWithValue("tier", "fast"))
			Expect(labels).To(HaveKeyWithValue("gpu", "a100"))
			Expect(labels).To(HaveKeyWithValue("node.replica-slots", "2"))
		})
	})

	Describe("Process map lookup by bare model name", func() {
		// Regression: PR #9583 changed the supervisor's map key from
		// `modelID` to `modelID#replicaIndex`. The NATS backend.stop
		// handler kept passing the bare modelID, so the lookup silently
		// no-op'd — the worker process stayed alive after an admin
		// "Unload model" click, and subsequent chats kept being served
		// by the leftover process. The registry rows were gone, so the
		// UI reported "no models loaded" while the model kept
		// responding. resolveProcessKeys must turn a bare modelID into
		// the actual replica process keys so stop/isRunning find the
		// running processes.
		It("resolves a bare modelID to its replica process keys", func() {
			s := &backendSupervisor{
				processes: map[string]*backendProcess{
					"qwen3.6-35B#0": {addr: "127.0.0.1:50051"},
					"qwen3.6-35B#1": {addr: "127.0.0.1:50052"},
					"other-model#0": {addr: "127.0.0.1:50053"},
				},
			}
			keys := s.resolveProcessKeys("qwen3.6-35B")
			Expect(keys).To(ConsistOf("qwen3.6-35B#0", "qwen3.6-35B#1"),
				"bare modelID must match all replica process keys")

			// Bare modelID for a model with no live processes returns nothing.
			Expect(s.resolveProcessKeys("not-loaded")).To(BeEmpty())

			// Full processKey resolves to itself (per-replica callers stay precise).
			Expect(s.resolveProcessKeys("qwen3.6-35B#0")).To(ConsistOf("qwen3.6-35B#0"))

			// A processKey that doesn't exist returns nothing — no spurious
			// prefix fallback when the caller was explicit.
			Expect(s.resolveProcessKeys("qwen3.6-35B#9")).To(BeEmpty())
		})

		It("isRunning returns false when no replica matches", func() {
			// We can only test the not-found path without a real *process.Process
			// (IsAlive() requires PID introspection). That's enough to pin the
			// regression — pre-fix, isRunning("qwen3.6-35B") would always
			// return false because the map was keyed by "qwen3.6-35B#0".
			// Post-fix, isRunning calls resolveProcessKeys first, so the
			// per-replica lookup is exercised before the IsAlive probe.
			s := &backendSupervisor{processes: map[string]*backendProcess{}}
			Expect(s.isRunning("qwen3.6-35B")).To(BeFalse())
			// resolveProcessKeys finds the replica entries (the lookup contract
			// is what the backend.delete handler relies on); the IsAlive probe
			// itself is exercised by the integration path in distributed mode.
			s.processes["qwen3.6-35B#0"] = &backendProcess{addr: "127.0.0.1:50051"}
			Expect(s.resolveProcessKeys("qwen3.6-35B")).To(ConsistOf("qwen3.6-35B#0"))
		})
	})

	Describe("Backend stop sequencing", func() {
		It("keeps the process and port reserved until termination completes", func() {
			proc := process.New()
			bp := &backendProcess{proc: proc, addr: "not-reparsed-during-cleanup", port: 50051}
			s := &backendSupervisor{
				processes: map[string]*backendProcess{"model#0": bp},
			}

			claimed := s.beginBackendStop("model#0")
			Expect(claimed).To(BeIdenticalTo(bp))
			Expect(bp.stopping).To(BeTrue())
			Expect(s.processes).To(HaveKeyWithValue("model#0", bp))
			Expect(s.freePorts).To(BeEmpty())
			Expect(quarantinedPortNumbers(s)).To(BeEmpty())

			s.finishBackendStop("model#0", bp, nil)
			Expect(s.processes).NotTo(HaveKey("model#0"))
			// Released into quarantine rather than straight into the free pool:
			// a controller row may still name this address.
			Expect(quarantinedPortNumbers(s)).To(ConsistOf(50051))
		})

		It("does not report a starting backend as ready after stop begins", func() {
			bp := &backendProcess{port: 50051}
			s := &backendSupervisor{
				processes: map[string]*backendProcess{"model#0": bp},
			}

			Expect(s.backendStartStillValid("model#0", bp)).To(BeTrue())
			bp.stopping = true
			Expect(s.backendStartStillValid("model#0", bp)).To(BeFalse())
		})

		It("recycles a failed startup port at most once", func() {
			bp := &backendProcess{port: 50051}
			s := &backendSupervisor{
				processes: map[string]*backendProcess{"model#0": bp},
			}

			s.releaseBackendStart("model#0", bp)
			s.releaseBackendStart("model#0", bp)

			Expect(s.processes).NotTo(HaveKey("model#0"))
			Expect(quarantinedPortNumbers(s)).To(ConsistOf(50051))
		})

		It("does not recycle the port owned by a replacement startup", func() {
			failed := &backendProcess{port: 50051}
			replacement := &backendProcess{port: 50052}
			s := &backendSupervisor{
				processes: map[string]*backendProcess{"model#0": replacement},
			}

			s.releaseBackendStart("model#0", failed)

			Expect(s.processes).To(HaveKeyWithValue("model#0", replacement))
			Expect(s.freePorts).To(BeEmpty())
			Expect(quarantinedPortNumbers(s)).To(BeEmpty())
		})
	})

	Describe("backend.stop request decoding", func() {
		It("preserves the legacy empty-payload stop-all command", func() {
			req, stopAll, err := decodeBackendStopRequest(nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(stopAll).To(BeTrue())
			Expect(req).To(Equal(messaging.BackendStopRequest{}))
		})

		It("preserves force for a structured stop-all command", func() {
			data, err := json.Marshal(messaging.BackendStopRequest{Force: true})
			Expect(err).NotTo(HaveOccurred())

			req, stopAll, err := decodeBackendStopRequest(data)
			Expect(err).NotTo(HaveOccurred())
			Expect(stopAll).To(BeTrue())
			Expect(req.Force).To(BeTrue())
		})

		It("rejects malformed JSON instead of treating it as stop-all", func() {
			_, stopAll, err := decodeBackendStopRequest([]byte(`{"backend":`))
			Expect(err).To(MatchError(ContainSubstring("decoding backend stop request")))
			Expect(stopAll).To(BeFalse())
		})
	})
})
