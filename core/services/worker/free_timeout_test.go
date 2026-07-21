package worker

import (
	"context"
	"net"
	"os"
	"strconv"
	"syscall"
	"testing"
	"time"

	process "github.com/mudler/go-processmanager"
	gogrpc "google.golang.org/grpc"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// TestWorkerFixtureProcess turns the current test binary into a portable
// long-running child for the process-stop assertions below. Using the test
// binary avoids assuming Unix utilities live at paths such as /bin/sleep,
// which is not true in Nix environments.
func TestWorkerFixtureProcess(t *testing.T) {
	if os.Getenv("LOCALAI_WORKER_FIXTURE_PROCESS") != "1" {
		return
	}
	for {
		time.Sleep(time.Hour)
	}
}

// pidAlive probes the OS directly for a process ID. The supervisor's own
// liveness helpers all go through go-processmanager's pidfile, which Stop
// deletes as part of releasing the handle, so they report "not alive" even if
// the signal never landed. Only asking the kernel proves termination.
func pidAlive(pid string) bool {
	n, err := strconv.Atoi(pid)
	if err != nil {
		return false
	}
	proc, err := os.FindProcess(n)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

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
		procPID string
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

		// A real, long-running child so the spec can assert the process is
		// actually dead afterwards, not merely that Stop() returned. It
		// outlives every timeout below, so if it is gone at the end it is
		// because the supervisor signalled it.
		executable, err := os.Executable()
		Expect(err).ToNot(HaveOccurred())
		proc = process.New(
			process.WithTemporaryStateDir(),
			process.WithName(executable),
			process.WithArgs("-test.run=^TestWorkerFixtureProcess$"),
			process.WithEnvironment(append(os.Environ(), "LOCALAI_WORKER_FIXTURE_PROCESS=1")...),
		)
		Expect(proc.Run()).To(Succeed())

		procPID = proc.CurrentPID()
		Expect(procPID).ToNot(BeEmpty(), "the fixture process must report a PID once started")
		Expect(pidAlive(procPID)).To(BeTrue(), "the fixture process must be running before the stop")

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
		// A spec that fails before the stop would otherwise leave the sleep
		// running for its full duration.
		if pidAlive(procPID) {
			_ = proc.Stop()
		}
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

		// Reaching the stop is not enough on its own: the signal has to land.
		// Done closes only once go-processmanager has waited on the child, so
		// this is the supervisor's kill being observed, not a pidfile that
		// Stop deleted on its way out.
		Eventually(proc.Done(), "30s", "100ms").Should(BeClosed(),
			"the backend process must actually exit, not just be signalled")
		Eventually(func() bool { return pidAlive(procPID) }, "30s", "100ms").Should(BeFalse(),
			"the backend process must be gone from the OS after the stop")

		s.mu.Lock()
		defer s.mu.Unlock()
		Expect(s.processes).ToNot(HaveKey("wedged-model#0"),
			"the supervisor must release the slot so the port can be recycled")
	})
})
