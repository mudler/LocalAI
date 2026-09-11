package worker

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync/atomic"

	"github.com/mudler/LocalAI/core/services/messaging"
	process "github.com/mudler/go-processmanager"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	gogrpc "google.golang.org/grpc"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

type modelStopBackend struct {
	pb.UnimplementedBackendServer
	freeCalls atomic.Int32
	freeErr   error
}

func (b *modelStopBackend) Free(context.Context, *pb.HealthMessage) (*pb.Result, error) {
	b.freeCalls.Add(1)
	return &pb.Result{Success: b.freeErr == nil}, b.freeErr
}

func startModelStopBackend(backend *modelStopBackend) (string, int, func()) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).NotTo(HaveOccurred())
	server := gogrpc.NewServer()
	pb.RegisterBackendServer(server, backend)
	go func() { _ = server.Serve(lis) }()
	return lis.Addr().String(), lis.Addr().(*net.TCPAddr).Port, server.Stop
}

func startModelStopProcess() *process.Process {
	proc := process.New(process.WithTemporaryStateDir(), process.WithName("/bin/sleep"), process.WithArgs("300"))
	Expect(proc.Run()).To(Succeed())
	return proc
}

func requestModelStop(s *backendSupervisor, req messaging.ModelStopRequest) messaging.ModelStopReply {
	data, err := json.Marshal(req)
	Expect(err).NotTo(HaveOccurred())
	var response []byte
	s.handleModelStop(data, func(data []byte) { response = append([]byte(nil), data...) })
	var reply messaging.ModelStopReply
	Expect(json.Unmarshal(response, &reply)).To(Succeed())
	return reply
}

var _ = Describe("Acknowledged exact model stop", func() {
	It("stops only the exact process key and releases its port before replying", func() {
		backend := &modelStopBackend{}
		addr, port, stopServer := startModelStopBackend(backend)
		defer stopServer()
		proc := startModelStopProcess()
		other := &backendProcess{addr: "127.0.0.1:59999", port: 59999}
		s := &backendSupervisor{cfg: &Config{}, processes: map[string]*backendProcess{
			"model#0": {proc: proc, addr: addr, port: port},
			"model#1": other,
		}}

		reply := requestModelStop(s, messaging.ModelStopRequest{ModelName: "model", ProcessKey: "model#0", ExpectedAddress: addr})

		Expect(reply).To(Equal(messaging.ModelStopReply{Matched: true, Freed: true, Terminated: true, ProcessKey: "model#0", Address: addr}))
		Expect(backend.freeCalls.Load()).To(Equal(int32(1)))
		Expect(s.processes).To(HaveKeyWithValue("model#1", other))
		Expect(s.processes).NotTo(HaveKey("model#0"))
		Expect(quarantinedPortNumbers(s)).To(ConsistOf(port))
		Expect(proc.Done()).To(BeClosed())
	})

	It("rejects an address mismatch without stopping anything", func() {
		proc := startModelStopProcess()
		defer func() {
			if pidAlive(proc.CurrentPID()) {
				_ = proc.Stop()
			}
		}()
		s := &backendSupervisor{cfg: &Config{}, processes: map[string]*backendProcess{"model#0": {proc: proc, addr: "127.0.0.1:50051", port: 50051}}}

		reply := requestModelStop(s, messaging.ModelStopRequest{ProcessKey: "model#0", ExpectedAddress: "127.0.0.1:50052"})

		Expect(reply.Matched).To(BeTrue())
		Expect(reply.Terminated).To(BeFalse())
		Expect(reply.Error).To(ContainSubstring("address mismatch"))
		Expect(s.processes).To(HaveKey("model#0"))
		Expect(pidAlive(proc.CurrentPID())).To(BeTrue())
	})

	It("treats an absent exact process key as idempotently terminated", func() {
		s := &backendSupervisor{cfg: &Config{}, processes: map[string]*backendProcess{}}
		reply := requestModelStop(s, messaging.ModelStopRequest{ProcessKey: "missing#0", ExpectedAddress: "127.0.0.1:50051"})
		Expect(reply).To(Equal(messaging.ModelStopReply{Matched: false, Terminated: true, ProcessKey: "missing#0"}))
	})

	It("reports Free failure but still terminates the process", func() {
		backend := &modelStopBackend{freeErr: errors.New("free failed")}
		addr, port, stopServer := startModelStopBackend(backend)
		defer stopServer()
		proc := startModelStopProcess()
		s := &backendSupervisor{cfg: &Config{}, processes: map[string]*backendProcess{"model#0": {proc: proc, addr: addr, port: port}}}

		reply := requestModelStop(s, messaging.ModelStopRequest{ProcessKey: "model#0", ExpectedAddress: addr})

		Expect(reply.Matched).To(BeTrue())
		Expect(reply.Freed).To(BeFalse())
		Expect(reply.Terminated).To(BeTrue())
		Expect(reply.Error).To(ContainSubstring("free failed"))
		Expect(s.processes).NotTo(HaveKey("model#0"))
	})

	It("skips Free when forced", func() {
		backend := &modelStopBackend{}
		addr, port, stopServer := startModelStopBackend(backend)
		defer stopServer()
		proc := startModelStopProcess()
		s := &backendSupervisor{cfg: &Config{}, processes: map[string]*backendProcess{"model#0": {proc: proc, addr: addr, port: port}}}

		reply := requestModelStop(s, messaging.ModelStopRequest{ProcessKey: "model#0", ExpectedAddress: addr, Force: true})

		Expect(reply.Matched).To(BeTrue())
		Expect(reply.Freed).To(BeFalse())
		Expect(reply.Terminated).To(BeTrue())
		Expect(backend.freeCalls.Load()).To(BeZero())
	})
})
