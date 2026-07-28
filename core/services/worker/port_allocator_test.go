package worker

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// mustAllocate fails the spec if allocation errors, so the ordinary "give me a
// port" specs below stay readable while still refusing to assert on a zero
// value that the allocator never intended to hand out.
func mustAllocate(s *backendSupervisor, key string) int {
	port, err := s.allocatePort(key)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	return port
}

var _ = Describe("Backend gRPC port allocator", func() {
	Describe("range exhaustion", func() {
		It("reports an explicit error instead of handing out an unbindable port", func() {
			// The allocator used to increment without an upper bound. Past
			// 65535 it kept returning larger integers, which cannot be bound,
			// so the operator saw "backend won't start" with nothing pointing
			// at the allocator. Exhaustion must be named as exhaustion.
			s := &backendSupervisor{
				processes:      map[string]*backendProcess{},
				minPort:        50051,
				nextPort:       50051,
				maxPort:        50052,
				portQuarantine: time.Hour,
			}

			first := mustAllocate(s, "a#0")
			second := mustAllocate(s, "b#0")
			Expect([]int{first, second}).To(ConsistOf(50051, 50052))

			_, err := s.allocatePort("c#0")
			Expect(err).To(MatchError(ErrNoFreePort))
			// The operator has to learn which knob to turn from the message
			// alone; a bare "no free port" sends them reading source.
			Expect(err.Error()).To(ContainSubstring("50051"))
			Expect(err.Error()).To(ContainSubstring("50052"))
			Expect(err.Error()).To(ContainSubstring("LOCALAI_GRPC_MAX_PORT"))
		})

		It("does not hand out a port beyond the end of the range", func() {
			s := &backendSupervisor{
				processes:      map[string]*backendProcess{},
				minPort:        50051,
				nextPort:       50051,
				maxPort:        50053,
				portQuarantine: time.Hour,
			}

			for i := 0; i < 3; i++ {
				port := mustAllocate(s, "model#0")
				Expect(port).To(BeNumerically(">=", 50051))
				Expect(port).To(BeNumerically("<=", 50053))
			}
		})
	})

	Describe("unexpectedly dead processes", func() {
		It("returns the dead process's port to the allocator", func() {
			// The death path deleted the map entry without releasing the port,
			// so every unexpected exit permanently consumed one port. A
			// crash-looping backend leaks one per restart, which is the real
			// route to exhausting the range — not the concurrent-peak growth
			// the issue assumed.
			bp := &backendProcess{port: 50051}
			s := &backendSupervisor{
				processes:      map[string]*backendProcess{"model#0": bp},
				minPort:        50051,
				nextPort:       50052,
				maxPort:        50060,
				portQuarantine: time.Millisecond,
			}

			s.reapDeadProcess("model#0", bp)

			Expect(s.processes).NotTo(HaveKey("model#0"))
			// Quarantined, not lost: the port must come back.
			Expect(quarantinedPortNumbers(s)).To(ContainElement(50051))
			time.Sleep(5 * time.Millisecond)
			Expect(mustAllocate(s, "model#0")).To(Equal(50051))
		})

		It("does not exhaust the range when one backend crash-loops", func() {
			// Restarting the same key repeatedly must not walk the allocator up
			// the range. Before the fix this consumed a fresh port per restart.
			s := &backendSupervisor{
				processes:      map[string]*backendProcess{},
				minPort:        50051,
				nextPort:       50051,
				maxPort:        50053,
				portQuarantine: time.Millisecond,
			}

			for i := 0; i < 10; i++ {
				port, err := s.allocatePort("crashy#0")
				Expect(err).NotTo(HaveOccurred(), "restart %d exhausted the range", i)
				bp := &backendProcess{port: port}
				s.processes["crashy#0"] = bp
				s.reapDeadProcess("crashy#0", bp)
				time.Sleep(2 * time.Millisecond)
			}
		})
	})

	Describe("effectiveMaxPort", func() {
		It("uses the full range when unset", func() {
			Expect((&Config{}).effectiveMaxPort(50051)).To(Equal(65535))
		})

		It("honours an operator-set ceiling", func() {
			Expect((&Config{GRPCMaxPort: 51000}).effectiveMaxPort(50051)).To(Equal(51000))
		})

		It("clamps a ceiling above the highest TCP port", func() {
			Expect((&Config{GRPCMaxPort: 70000}).effectiveMaxPort(50051)).To(Equal(65535))
		})

		It("ignores a ceiling below the base port instead of wedging every start", func() {
			// An inverted range would leave the allocator with nothing to hand
			// out, so a typo here would take the whole worker down rather than
			// degrade it.
			Expect((&Config{GRPCMaxPort: 40000}).effectiveMaxPort(50051)).To(Equal(65535))
		})
	})

	Describe("per-key port affinity", func() {
		It("never hands one key's released port to a different key", func() {
			// This is what makes a stale NodeModel row harmless. Process keys
			// (modelID#replica) and NodeModel rows (nodeID, modelName,
			// replicaIndex) are isomorphic, so if a port can only ever be
			// re-bound by the key that last held it, the only row that can name
			// that port is that key's own row — which its re-registration
			// overwrites. Misrouting to a *different* model becomes impossible
			// by construction rather than by racing a quarantine timer.
			s := &backendSupervisor{
				processes:      map[string]*backendProcess{},
				minPort:        50051,
				nextPort:       50051,
				maxPort:        50060,
				portQuarantine: time.Millisecond,
			}

			portA := mustAllocate(s, "alpha#0")
			s.releasePortForKey("alpha#0", portA)
			time.Sleep(5 * time.Millisecond)

			// beta must NOT be given alpha's port while unused ports remain.
			portB := mustAllocate(s, "beta#0")
			Expect(portB).NotTo(Equal(portA))

			// alpha coming back gets its own port again.
			Expect(mustAllocate(s, "alpha#0")).To(Equal(portA))
		})

		It("reuses an unowned port before growing the range", func() {
			s := &backendSupervisor{
				processes:      map[string]*backendProcess{},
				minPort:        50051,
				nextPort:       50051,
				maxPort:        50060,
				portQuarantine: time.Millisecond,
			}

			// A port released without an owning key (legacy/unknown) is free
			// for anyone: no row can be attributed to it.
			s.releasePort(50051)
			time.Sleep(5 * time.Millisecond)

			Expect(mustAllocate(s, "gamma#0")).To(Equal(50051))
		})

		It("steals another key's port rather than failing an exhausted range", func() {
			// Affinity is a preference, not a reservation. Refusing to start a
			// backend because a long-gone key still owns the last port trades a
			// vanishingly rare misroute for a guaranteed outage.
			s := &backendSupervisor{
				processes:      map[string]*backendProcess{},
				minPort:        50051,
				nextPort:       50051,
				maxPort:        50051,
				portQuarantine: time.Millisecond,
			}

			portA := mustAllocate(s, "alpha#0")
			s.releasePortForKey("alpha#0", portA)
			time.Sleep(5 * time.Millisecond)

			port, err := s.allocatePort("beta#0")
			Expect(err).NotTo(HaveOccurred())
			Expect(port).To(Equal(portA))
		})

		It("stops sequestering the port once the stale-row window has passed", func() {
			// Affinity exists to outlive any controller row that could still
			// name the port. Past that, holding it for a key that may never
			// come back is pure cost: every distinct model the worker has ever
			// served would consume a port permanently, so a long-lived worker
			// would climb to the end of its range on distinct-key count rather
			// than on concurrency, then steal on every subsequent allocation
			// while advising the operator to raise a ceiling that is not the
			// problem.
			s := &backendSupervisor{
				processes:          map[string]*backendProcess{},
				minPort:            50051,
				nextPort:           50051,
				maxPort:            50060,
				portQuarantine:     time.Millisecond,
				portAffinityWindow: 20 * time.Millisecond,
			}

			portA := mustAllocate(s, "alpha#0")
			s.releasePortForKey("alpha#0", portA)
			time.Sleep(60 * time.Millisecond)

			// A brand-new key reuses it rather than growing the range.
			Expect(mustAllocate(s, "beta#0")).To(Equal(portA))
		})

		It("applies the default affinity window when none is configured", func() {
			// The zero value must not degrade to "no affinity": every
			// supervisor built outside the tests leaves the field unset, and
			// zero would hand a just-released port straight to another model.
			s := &backendSupervisor{
				processes:      map[string]*backendProcess{},
				minPort:        50051,
				nextPort:       50051,
				maxPort:        50060,
				portQuarantine: time.Millisecond,
			}

			portA := mustAllocate(s, "alpha#0")
			s.releasePortForKey("alpha#0", portA)
			time.Sleep(5 * time.Millisecond)

			Expect(mustAllocate(s, "beta#0")).NotTo(Equal(portA))
		})

		It("keeps the affinity map bounded by the size of the port range", func() {
			// The affinity map must not become the port leak's twin. Handing a
			// port to a new key drops the previous owner's entry, so the map is
			// injective over ports and can never hold more entries than the
			// range has ports, however many distinct keys the worker sees.
			s := &backendSupervisor{
				processes:      map[string]*backendProcess{},
				minPort:        50051,
				nextPort:       50051,
				maxPort:        50052,
				portQuarantine: time.Nanosecond,
			}

			for i := 0; i < 50; i++ {
				key := "model" + string(rune('a'+i%26)) + "#0"
				port, err := s.allocatePort(key)
				Expect(err).NotTo(HaveOccurred())
				s.releasePortForKey(key, port)
				time.Sleep(time.Millisecond)
			}

			Expect(len(s.portAffinity)).To(BeNumerically("<=", 2))
		})
	})
})
