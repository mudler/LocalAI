package worker

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/workerctl"
)

// The frontend's control client against the REAL worker control plane: the real
// supervisor's handlers, mounted through the real nodes.AuthenticatedRoutes so
// the real bearer check runs first, reached by a real nodes.ControlClient over
// a real HTTP transport. The only thing standing in for production is the
// transport's dial, which is the seam the tunnel occupies.
//
// It lives in this package because the dependency runs worker -> nodes: the
// frontend's client cannot be exercised against the real handler from the other
// side without an import cycle. Both halves of the contract are written by
// different packages, so a spec on either side alone can only prove that side
// agrees with itself; the paths, the envelope shape and the status codes are
// only pinned together here.
var _ = Describe("the frontend's control client against the real worker", func() {
	const (
		token  = "s3cret-registration-token"
		nodeID = "worker-under-test"
	)

	var (
		sup     *backendSupervisor
		client  *nodes.ControlClient
		srvAddr string
		sigCh   chan os.Signal
	)

	// newClient builds a control client whose transport dials srvAddr,
	// authenticating with the token given.
	newClient := func(srvAddr, tok string) *nodes.ControlClient {
		return nodes.NewControlClient(func(string) func(context.Context, string, string) (net.Conn, error) {
			return func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "tcp", srvAddr)
			}
		}, tok)
	}

	BeforeEach(func() {
		sigCh = make(chan os.Signal, 1)
		sup = &backendSupervisor{
			cfg:       &Config{},
			nodeID:    nodeID,
			sigCh:     sigCh,
			processes: map[string]*backendProcess{},
		}

		dir := GinkgoT().TempDir()
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		srv, err := nodes.StartFileTransferServerWithRoutes(lis,
			filepath.Join(dir, "staging"), filepath.Join(dir, "models"), filepath.Join(dir, "data"),
			token, config.DefaultMaxUploadSize, nil,
			&nodes.AuthenticatedRoutes{Prefix: workerctl.Prefix, Register: sup.RegisterControlRoutes})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = srv.Close() })

		srvAddr = lis.Addr().String()
		client = newClient(srvAddr, token)
	})

	It("round-trips models.running end to end", func() {
		var reply messaging.ModelsRunningReply
		Expect(client.Call(context.Background(), nodeID, workerctl.PathModelsRunning,
			messaging.ModelsRunningRequest{}, &reply)).To(Succeed())
		// Nothing is running, and an empty list is the worker's real answer
		// rather than a decode that quietly produced nothing: the client
		// reports a body it cannot read as unroutable instead.
		Expect(reply.Models).To(BeEmpty())
		Expect(reply.Error).To(BeEmpty())
	})

	It("streams install progress in order and returns the terminal reply", func() {
		sup.installFn = func(_ context.Context, req messaging.BackendInstallRequest, _ bool,
			onProgress func(messaging.BackendInstallProgressEvent)) (string, error) {
			onProgress(messaging.BackendInstallProgressEvent{OpID: req.OpID, Percentage: 50})
			onProgress(messaging.BackendInstallProgressEvent{OpID: req.OpID, Percentage: 100})
			return "127.0.0.1:41234", nil
		}

		var seen []float64
		var reply messaging.BackendInstallReply
		err := client.CallStreaming(context.Background(), nodeID, workerctl.PathBackendInstall,
			messaging.BackendInstallRequest{Backend: "mock", OpID: "op-1"}, &reply,
			func(ev messaging.BackendInstallProgressEvent) { seen = append(seen, ev.Percentage) })
		Expect(err).NotTo(HaveOccurred())
		Expect(seen).To(Equal([]float64{50, 100}))
		Expect(reply.Success).To(BeTrue())
		Expect(reply.WorkerLocalAddress).To(Equal("127.0.0.1:41234"))
	})

	It("reports a FAILED install as the worker's own answer, not as a transport failure", func() {
		// The distinction the two sides exist to preserve: the worker answers
		// 200 with Error set, so the frontend reads a verdict a caller may act
		// on rather than a route it must not act on.
		sup.installFn = func(context.Context, messaging.BackendInstallRequest, bool,
			func(messaging.BackendInstallProgressEvent)) (string, error) {
			return "", errors.New("no child with platform linux/arm64")
		}

		var reply messaging.BackendInstallReply
		err := client.CallStreaming(context.Background(), nodeID, workerctl.PathBackendInstall,
			messaging.BackendInstallRequest{Backend: "mock"}, &reply, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(reply.Success).To(BeFalse())
		Expect(reply.Error).To(ContainSubstring("linux/arm64"))
	})

	It("round-trips a 204 verb, which carries no body at all", func() {
		Expect(client.Call(context.Background(), nodeID, workerctl.PathBackendStop,
			messaging.BackendStopRequest{Backend: "no-such-backend"}, nil)).To(Succeed())
	})

	It("reports an unknown control verb as unsupported, and not as absence", func() {
		err := client.Call(context.Background(), nodeID, workerctl.Prefix+"invented", struct{}{}, &struct{}{})
		Expect(err).To(MatchError(nodes.ErrWorkerControlUnsupported))
		Expect(errors.Is(err, cluster.ErrNoRoute)).To(BeFalse())
		Expect(cluster.IsWorkerAnswer(err)).To(BeFalse())
	})

	It("reports a rejected token as unroutable, never as a worker verdict about a backend", func() {
		// The bearer check runs before routing, so a wrong token is a 401 for
		// every verb. It says nothing about any backend and nothing may reap on
		// it; it also must not be read as the verb being unsupported, which
		// would send the upgrade path into its destructive legacy fallback.
		wrong := newClient(srvAddr, "not-the-token")
		var reply messaging.BackendListReply
		err := wrong.Call(context.Background(), nodeID, workerctl.PathBackendList,
			messaging.BackendListRequest{}, &reply)
		Expect(errors.Is(err, nodes.ErrWorkerUnroutable)).To(BeTrue())
		Expect(errors.Is(err, nodes.ErrWorkerControlUnsupported)).To(BeFalse())
		Expect(cluster.IsWorkerAnswer(err)).To(BeFalse())
	})
})
