package worker

import (
	"context"
	"net"
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/workerctl"
)

// The install reply gives the frontend a backend's advertised address (the
// worker's advertised host with the backend's port), while the worker records
// the process under its listen address. Requests that name a process carry
// the advertised form back, so both must name the same process.
var _ = Describe("Advertised backend addresses", func() {
	const advertiseHost = "192.0.2.10"

	DescribeTable("advertisedBackendAddr",
		func(listen, advertise, want string, wantErr bool) {
			got, err := advertisedBackendAddr(listen, advertise)
			Expect(got).To(Equal(want))
			if wantErr {
				Expect(err).To(HaveOccurred())
			} else {
				Expect(err).NotTo(HaveOccurred())
			}
		},
		Entry("advertised host with the backend's port", "127.0.0.1:50052", "192.0.2.10:50051", "192.0.2.10:50052", false),
		Entry("unchanged when the advertise address is the listen address", "127.0.0.1:50051", "127.0.0.1:50051", "127.0.0.1:50051", false),
		Entry("bracketed IPv6 advertise host", "127.0.0.1:50052", "[2001:db8::1]:50051", "[2001:db8::1]:50052", false),
		Entry("listen address on a parse error", "127.0.0.1:50052", "no-port", "127.0.0.1:50052", true),
	)

	advertisedSupervisor := func(backend *modelStopBackend) (*backendSupervisor, string, int) {
		addr, port, stopServer := startModelStopBackend(backend)
		DeferCleanup(stopServer)
		proc := startModelStopProcess()
		DeferCleanup(func() {
			if pidAlive(proc.CurrentPID()) {
				_ = proc.Stop()
			}
		})
		s := &backendSupervisor{
			cfg: &Config{AdvertiseAddr: net.JoinHostPort(advertiseHost, "50051")},
			processes: map[string]*backendProcess{
				"model#0": {proc: proc, addr: addr, port: port, instance: "i"},
			},
		}
		return s, addr, port
	}
	advertised := func(port int) string { return net.JoinHostPort(advertiseHost, strconv.Itoa(port)) }

	It("model.unload frees the process named by its advertised address", func() {
		backend := &modelStopBackend{}
		s, addr, port := advertisedSupervisor(backend)

		reply := s.unloadModel(context.Background(), workerctl.ModelUnloadRequest{ModelName: "model", Address: advertised(port)})

		Expect(reply.Success).To(BeTrue())
		Expect(backend.freeCalls.Load()).To(Equal(int32(1)))
		Expect(s.getAddr("model#0")).To(Equal(addr))
	})

	It("model.unload still frees the process named by its listen address", func() {
		backend := &modelStopBackend{}
		s, addr, _ := advertisedSupervisor(backend)

		Expect(s.unloadModel(context.Background(), workerctl.ModelUnloadRequest{ModelName: "model", Address: addr}).Success).To(BeTrue())
		Expect(backend.freeCalls.Load()).To(Equal(int32(1)))
	})

	It("model.unload frees nothing for the same port on another host", func() {
		backend := &modelStopBackend{}
		s, _, port := advertisedSupervisor(backend)

		other := net.JoinHostPort("192.0.2.99", strconv.Itoa(port))
		Expect(s.unloadModel(context.Background(), workerctl.ModelUnloadRequest{ModelName: "model", Address: other}).Success).To(BeTrue())
		Expect(backend.freeCalls.Load()).To(BeZero())
	})

	It("model.stop accepts the advertised address as the expected address", func() {
		backend := &modelStopBackend{}
		s, _, port := advertisedSupervisor(backend)

		reply := requestModelStop(s, workerctl.ModelStopRequest{ModelName: "model", ProcessKey: "model#0", ExpectedAddress: advertised(port)})

		Expect(reply.Error).To(BeEmpty())
		Expect(reply.Terminated).To(BeTrue())
		Expect(s.processes).NotTo(HaveKey("model#0"))
	})

	It("model.stop rejects the same port on another host", func() {
		s, _, port := advertisedSupervisor(&modelStopBackend{})

		reply := requestModelStop(s, workerctl.ModelStopRequest{ProcessKey: "model#0", ExpectedAddress: net.JoinHostPort("192.0.2.99", strconv.Itoa(port))})

		Expect(reply.Error).To(ContainSubstring("address mismatch"))
		Expect(s.processes).To(HaveKey("model#0"))
	})
})
