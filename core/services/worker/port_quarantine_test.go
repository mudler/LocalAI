package worker

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// quarantinedPortNumbers exposes the held ports for assertions without leaking
// the quarantine deadlines, which are wall-clock and not reproducible.
func quarantinedPortNumbers(s *backendSupervisor) []int {
	ports := make([]int, 0, len(s.quarantinedPorts))
	for _, q := range s.quarantinedPorts {
		ports = append(ports, q.port)
	}
	return ports
}

var _ = Describe("Backend port quarantine", func() {
	Describe("finishBackendStop", func() {
		It("does not offer a just-stopped port to the next backend", func() {
			// The controller still holds a NodeModel row pointing at this
			// address. Handing the port straight to the next backend lets that
			// row pass probeHealth against a different process and dispatch to
			// it silently. The quarantine holds the port for the NATS
			// round-trip the controller needs to drop the row.
			bp := &backendProcess{port: 50051}
			s := &backendSupervisor{
				processes:      map[string]*backendProcess{"model#0": bp},
				nextPort:       50060,
				portQuarantine: time.Hour,
			}

			Expect(s.finishBackendStop("model#0", bp, nil)).To(Succeed())

			Expect(s.freePorts).To(BeEmpty())
			Expect(s.allocatePort()).NotTo(Equal(50051))
		})

		It("returns the port to the free pool once the quarantine expires", func() {
			// The quarantine must not leak ports: nextPort only ever grows, so a
			// port that never comes back is a permanent loss of allocator range.
			bp := &backendProcess{port: 50051}
			s := &backendSupervisor{
				processes:      map[string]*backendProcess{"model#0": bp},
				nextPort:       50060,
				portQuarantine: time.Millisecond,
			}

			Expect(s.finishBackendStop("model#0", bp, nil)).To(Succeed())
			time.Sleep(5 * time.Millisecond)

			Expect(s.allocatePort()).To(Equal(50051))
		})
	})

	Describe("releaseBackendStart", func() {
		It("quarantines the port of a startup that failed after binding", func() {
			// A backend that died during startup may have bound and served on
			// the port for a moment, which is long enough for a load to have
			// recorded the address.
			bp := &backendProcess{port: 50051}
			s := &backendSupervisor{
				processes:      map[string]*backendProcess{"model#0": bp},
				nextPort:       50060,
				portQuarantine: time.Hour,
			}

			s.releaseBackendStart("model#0", bp)

			Expect(s.freePorts).To(BeEmpty())
			Expect(s.allocatePort()).To(Equal(50060))
		})
	})

	Describe("allocatePort", func() {
		It("prefers a released port over growing the range", func() {
			s := &backendSupervisor{
				processes:      map[string]*backendProcess{},
				nextPort:       50060,
				portQuarantine: time.Millisecond,
			}

			s.releasePort(50051)
			time.Sleep(5 * time.Millisecond)

			Expect(s.allocatePort()).To(Equal(50051))
			Expect(s.allocatePort()).To(Equal(50060))
			Expect(s.nextPort).To(Equal(50061))
		})

		It("does not sweep a port whose quarantine is still running", func() {
			s := &backendSupervisor{
				processes:      map[string]*backendProcess{},
				nextPort:       50060,
				portQuarantine: time.Hour,
			}

			s.releasePort(50051)
			s.releasePort(50052)

			Expect(s.allocatePort()).To(Equal(50060))
			Expect(s.allocatePort()).To(Equal(50061))
		})

		It("applies the default quarantine when none is configured", func() {
			// The zero value must not degrade to "no quarantine": every
			// supervisor built outside the tests leaves the field unset.
			s := &backendSupervisor{
				processes: map[string]*backendProcess{},
				nextPort:  50060,
			}

			s.releasePort(50051)

			Expect(s.allocatePort()).To(Equal(50060))
		})
	})
})
