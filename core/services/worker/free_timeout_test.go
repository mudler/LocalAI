package worker

import (
	"context"
	"net"

	process "github.com/mudler/go-processmanager"
	gogrpc "google.golang.org/grpc"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// hangingBackend is a real gRPC backend server whose Free handler never
// returns. It models the production failure mode: 37 Python backends default to
// PYTHON_GRPC_MAX_WORKERS=1, so while a LoadModel handler is wedged (e.g.
// inside torch.load) the single gRPC worker thread is occupied and a Free RPC
// queues behind it forever.
//
// The server must be real rather than a socket that merely accepts: without a
// completed HTTP/2 handshake the call is bounded by gRPC's own ~20s connect
// timeout, which would mask an unbounded Free instead of exposing it. Here the
// connection reaches READY, so nothing but the caller's own deadline can end
// the call.
type hangingBackend struct {
	pb.UnimplementedBackendServer
	entered chan struct{}
	release chan struct{}
}

func (h *hangingBackend) Free(_ context.Context, _ *pb.HealthMessage) (*pb.Result, error) {
	close(h.entered)
	// Deliberately ignores the server-side context: a backend whose only
	// worker thread is blocked cannot observe cancellation either.
	<-h.release
	return &pb.Result{Success: true}, nil
}

var _ = Describe("Stopping a backend whose Free never returns", func() {
	var (
		proc    *process.Process
		backend *hangingBackend
		server  *gogrpc.Server
		s       *backendSupervisor
	)

	BeforeEach(func() {
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())

		backend = &hangingBackend{
			entered: make(chan struct{}),
			release: make(chan struct{}),
		}
		server = gogrpc.NewServer()
		pb.RegisterBackendServer(server, backend)
		go func() { _ = server.Serve(lis) }()

		// The process is deliberately never Run(): go-processmanager v0.1.1
		// writes Process.pid from readPID() with no synchronization, so a live
		// process races its own monitor goroutine under -race (reproducible
		// with a bare Run()+Stop(), independent of this spec). Since
		// scripts/model-lifecycle-conformance.sh runs this package with -race
		// and is fail-closed, starting one here would turn that gate red on an
		// upstream defect. An unstarted process still drives the branch this
		// spec is about: Stop() returns promptly, which is all that is needed
		// to prove the stop was reached at all. Whether SIGTERM/SIGKILL then
		// lands is go-processmanager's contract, not this supervisor's.
		proc = process.New(
			process.WithTemporaryStateDir(),
			process.WithName("/bin/sleep"),
			process.WithArgs("300"),
		)

		s = &backendSupervisor{
			cfg: &Config{},
			processes: map[string]*backendProcess{
				"wedged-model#0": {
					proc:        proc,
					addr:        lis.Addr().String(),
					port:        lis.Addr().(*net.TCPAddr).Port,
					backendName: "longcat-video",
				},
			},
		}
	})

	AfterEach(func() {
		close(backend.release)
		server.Stop()
	})

	It("reaches the stop even though Free never returns", func() {
		done := make(chan error, 1)
		go func() { done <- s.stopBackendExact("wedged-model#0", false) }()

		Eventually(backend.entered, "20s", "100ms").Should(BeClosed(),
			"Free must be attempted — it is a courtesy call we still want to make")

		// The whole point: a Free that never answers must not hold the stop
		// hostage. Unbounded, this never receives, the process is never
		// signalled, and the router's abandoned-load reap (#10948) is inert
		// against a wedged single-worker Python backend.
		Eventually(done, "60s", "200ms").Should(Receive(BeNil()),
			"stopBackendExact must fall through to the process stop despite a hung Free")

		s.mu.Lock()
		defer s.mu.Unlock()
		Expect(s.processes).ToNot(HaveKey("wedged-model#0"),
			"the supervisor must release the slot so the port can be recycled")
	})
})
