package worker

import (
	"net"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The allocator's bookkeeping records what THIS worker did with a port. The
// collision it could not see is with something this worker never did: the
// default base port, 50051, is inside Linux's default ephemeral range
// (32768-60999), so the kernel hands ports in this range to outbound
// connections and to anything that binds port 0. A port the allocator believes
// free is therefore not necessarily bindable, and the backend that is handed
// one dies on bind.
var _ = Describe("Backend gRPC port allocator, against real sockets", func() {
	// listenOnAFreePort takes a real port and holds it for the spec, the way
	// anything else on the host would. No injected seam: the point of this
	// spec is that the allocator asks the KERNEL and not its own map.
	listenOnAFreePort := func() int {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = ln.Close() })
		return ln.Addr().(*net.TCPAddr).Port
	}

	It("does not hand out a port another process already holds", func() {
		held := listenOnAFreePort()
		s := &backendSupervisor{
			processes:      map[string]*backendProcess{},
			minPort:        held,
			nextPort:       held,
			maxPort:        held + 8,
			portQuarantine: time.Hour,
		}

		port := mustAllocate(s, "model#0")
		Expect(port).NotTo(Equal(held),
			"a port the kernel has already given to something else cannot be bound by a backend")
		Expect(port).To(BeNumerically(">", held))
	})

	It("refuses the whole range rather than handing out one held port", func() {
		held := listenOnAFreePort()
		s := &backendSupervisor{
			processes:      map[string]*backendProcess{},
			minPort:        held,
			nextPort:       held,
			maxPort:        held,
			portQuarantine: time.Hour,
		}

		_, err := s.allocatePort("model#0")
		Expect(err).To(MatchError(ErrNoFreePort))
		// The operator has to be able to tell "my range is too narrow" from
		// "something else on this host is sitting in my range".
		Expect(err.Error()).To(ContainSubstring("already bound by something outside this worker"))
	})

	Describe("with the probe scripted", func() {
		// takenPorts scripts which candidates the kernel refuses, so a spec can
		// state the rule without depending on what else this host is running.
		takenPorts := func(taken ...int) func(int) bool {
			held := map[int]struct{}{}
			for _, p := range taken {
				held[p] = struct{}{}
			}
			return func(port int) bool {
				_, ok := held[port]
				return !ok
			}
		}

		It("passes over a released port the kernel took while it sat in the free pool", func() {
			// The free pool is where this bites hardest. A port this worker
			// released is exactly as available to the kernel as one it never
			// used, so probing only the freshly grown ports would still hand
			// out a stolen one.
			bp := &backendProcess{port: 50051}
			s := &backendSupervisor{
				processes: map[string]*backendProcess{"gone#0": bp},
				minPort:   50051,
				nextPort:  50052,
				maxPort:   50053,
				// Both windows lapse at once, so the released port is an
				// ORDINARY free port by the time the next allocation looks: out
				// of quarantine and no longer claimed by the key that held it.
				// Without the affinity window lapsing, step 2 skips it as owned
				// and the probe this spec is about is never reached.
				portQuarantine:     time.Nanosecond,
				portAffinityWindow: time.Nanosecond,
				portIsFree:         takenPorts(50051),
			}
			Expect(s.finishBackendStop("gone#0", bp, nil)).To(Succeed())
			Expect(s.freePortsAfterSweep()).To(ContainElement(50051),
				"the port has to reach the free pool for this spec to be about the free pool")

			Expect(mustAllocate(s, "other#0")).NotTo(Equal(50051))
		})

		It("offers a held port again once whatever held it gives it back", func() {
			// Quarantined and not blacklisted. An ephemeral outbound connection
			// releases its port; a port dropped for ever would be a permanent
			// loss of range for a collision that lasted seconds.
			free := takenPorts(50051)
			s := &backendSupervisor{
				processes:      map[string]*backendProcess{},
				minPort:        50051,
				nextPort:       50051,
				maxPort:        50052,
				portQuarantine: time.Nanosecond,
				portIsFree:     func(port int) bool { return free(port) },
			}

			Expect(mustAllocate(s, "first#0")).To(Equal(50052))

			// Whatever held 50051 has let go.
			free = takenPorts()
			Expect(mustAllocate(s, "second#0")).To(Equal(50051))
		})

		It("does not go on reserving a port it could not bind", func() {
			// Affinity is a preference for a port this worker can still bind.
			// The claim is dropped when the probe refuses it, and the case that
			// shows it is the one where no OTHER port is available either: a
			// successful allocation overwrites the claim anyway, so a spec that
			// let the range grow would pass with the drop removed.
			bp := &backendProcess{port: 50051}
			free := takenPorts()
			s := &backendSupervisor{
				processes:      map[string]*backendProcess{"model#0": bp},
				minPort:        50051,
				nextPort:       50052,
				maxPort:        50051, // the range is exactly this one port
				portQuarantine: time.Nanosecond,
				portIsFree:     func(port int) bool { return free(port) },
			}
			Expect(s.finishBackendStop("model#0", bp, nil)).To(Succeed())

			free = takenPorts(50051)
			_, err := s.allocatePort("model#0")
			Expect(err).To(MatchError(ErrNoFreePort))
			Expect(s.portAffinity).NotTo(HaveKey("model#0"),
				"a port this worker cannot bind must not stay reserved for the key that last held it")
		})
	})
})
