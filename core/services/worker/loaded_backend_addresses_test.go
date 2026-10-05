package worker

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// LoadedBackendAddresses feeds the worker's /readyz data-path probe, so every
// address it returns gets dialled. It must therefore report only processes
// that are actually listening: addr is recorded when the process is spawned,
// well before its gRPC server binds, and a cold start that took 10 to 15
// seconds would otherwise pull the worker out of rotation every time.
var _ = Describe("loaded backend addresses", func() {
	It("includes a backend that has passed the health-check gate", func() {
		s := &backendSupervisor{processes: map[string]*backendProcess{
			"model#0": {addr: "127.0.0.1:50051", port: 50051, serving: true},
		}}

		Expect(s.LoadedBackendAddresses()).To(ConsistOf("127.0.0.1:50051"))
	})

	It("excludes a backend that is still starting", func() {
		// The supervisor inserts the entry with addr already set, then polls
		// for up to 30s waiting for the gRPC server to bind. Dialling during
		// that window gets connection refused from a perfectly healthy start.
		s := &backendSupervisor{processes: map[string]*backendProcess{
			"model#0": {addr: "127.0.0.1:50051", port: 50051},
		}}

		Expect(s.LoadedBackendAddresses()).To(BeEmpty())
	})

	It("excludes a backend that is stopping", func() {
		s := &backendSupervisor{processes: map[string]*backendProcess{
			"model#0": {addr: "127.0.0.1:50051", port: 50051, serving: true, stopping: true},
		}}

		Expect(s.LoadedBackendAddresses()).To(BeEmpty())
	})

	It("reports only the serving members of a mixed set", func() {
		s := &backendSupervisor{processes: map[string]*backendProcess{
			"serving#0":  {addr: "127.0.0.1:50051", port: 50051, serving: true},
			"serving#1":  {addr: "127.0.0.1:50052", port: 50052, serving: true},
			"starting#0": {addr: "127.0.0.1:50053", port: 50053},
			"stopping#0": {addr: "127.0.0.1:50054", port: 50054, serving: true, stopping: true},
		}}

		Expect(s.LoadedBackendAddresses()).To(ConsistOf("127.0.0.1:50051", "127.0.0.1:50052"))
	})

	It("holds no addresses when the worker holds no processes", func() {
		s := &backendSupervisor{processes: map[string]*backendProcess{}}

		Expect(s.LoadedBackendAddresses()).To(BeEmpty())
	})

	It("marks a backend serving only once it has answered a health check", func() {
		bp := &backendProcess{addr: "127.0.0.1:50051", port: 50051}
		s := &backendSupervisor{processes: map[string]*backendProcess{"model#0": bp}}

		Expect(s.LoadedBackendAddresses()).To(BeEmpty())
		Expect(s.markBackendServing("model#0", bp)).To(BeTrue())
		Expect(s.LoadedBackendAddresses()).To(ConsistOf("127.0.0.1:50051"))
	})
})
