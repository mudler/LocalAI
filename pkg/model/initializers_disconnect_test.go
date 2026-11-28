// SPDX-License-Identifier: MIT
package model

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
)

// controllableLoadBackend is an in-process gRPC BackendServer whose LoadModel
// RPC blocks until released (or until its RPC context is canceled), letting
// tests drive client disconnects mid-load deterministically.
type controllableLoadBackend struct {
	pb.UnimplementedBackendServer

	loadStarted  chan struct{}
	releaseLoad  chan struct{}
	startOnce    sync.Once
	releaseOnce  sync.Once
	loadResult   *pb.Result
	loadErr      error
	loadCalls    atomic.Int32
	loadCanceled atomic.Bool
}

func newControllableLoadBackend(res *pb.Result, err error) *controllableLoadBackend {
	return &controllableLoadBackend{
		loadStarted: make(chan struct{}),
		releaseLoad: make(chan struct{}),
		loadResult:  res,
		loadErr:     err,
	}
}

func (b *controllableLoadBackend) release() {
	b.releaseOnce.Do(func() { close(b.releaseLoad) })
}

func (b *controllableLoadBackend) Health(context.Context, *pb.HealthMessage) (*pb.Reply, error) {
	return &pb.Reply{Message: []byte("OK")}, nil
}

func (b *controllableLoadBackend) LoadModel(ctx context.Context, _ *pb.ModelOptions) (*pb.Result, error) {
	b.loadCalls.Add(1)
	b.startOnce.Do(func() { close(b.loadStarted) })
	select {
	case <-b.releaseLoad:
		if b.loadErr != nil {
			return nil, b.loadErr
		}
		if b.loadResult != nil {
			return b.loadResult, nil
		}
		return &pb.Result{Success: true}, nil
	case <-ctx.Done():
		b.loadCanceled.Store(true)
		return nil, ctx.Err()
	}
}

func startControllableBackend(b *controllableLoadBackend) string {
	GinkgoHelper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).ToNot(HaveOccurred())
	srv := grpc.NewServer()
	pb.RegisterBackendServer(srv, b)
	go func() { _ = srv.Serve(lis) }()
	DeferCleanup(func() {
		b.release()
		srv.Stop()
	})
	return lis.Addr().String()
}

var _ = Describe("Client disconnect during local model cold load", func() {
	var ml *ModelLoader

	BeforeEach(func() {
		systemState, err := system.GetSystemState(system.WithModelPath(GinkgoT().TempDir()))
		Expect(err).ToNot(HaveOccurred())
		ml = NewModelLoader(systemState)
		ml.SetLoadFailureCooldown(5*time.Minute, 5*time.Minute)
	})

	It("keeps loading in the background without triggering cooldown when the initiating client disconnects", func() {
		backend := newControllableLoadBackend(&pb.Result{Success: true}, nil)
		addr := startControllableBackend(backend)
		ml.SetExternalBackend("slow", addr)

		reqCtx1, cancel1 := context.WithCancel(context.Background())
		defer cancel1()

		req1Done := make(chan error, 1)
		go func() {
			_, err := ml.Load(
				WithContext(reqCtx1),
				WithModelID("slow-model"),
				WithModel("slow-model.gguf"),
				WithBackendString("slow"),
				WithGRPCAttempts(5),
				WithGRPCAttemptsDelay(0),
			)
			req1Done <- err
		}()

		Eventually(backend.loadStarted, "2s").Should(BeClosed())

		// Client 1 disconnects while LoadModel is still blocked.
		cancel1()

		// Client 1 must return promptly with context.Canceled without waiting
		// for LoadModel to finish.
		var err1 error
		Eventually(req1Done, "2s").Should(Receive(&err1))
		Expect(errors.Is(err1, context.Canceled)).To(BeTrue(), "expected context.Canceled, got: %v", err1)

		// The gRPC LoadModel RPC must remain alive rather than being canceled.
		Consistently(backend.loadCanceled.Load, "80ms", "10ms").Should(BeFalse())

		// Client 2 arrives while the detached cold load is still in flight and
		// coalesces onto it rather than hitting a cooldown error.
		req2Done := make(chan error, 1)
		go func() {
			client, err := ml.Load(
				WithContext(context.Background()),
				WithModelID("slow-model"),
				WithModel("slow-model.gguf"),
				WithBackendString("slow"),
				WithGRPCAttempts(5),
				WithGRPCAttemptsDelay(0),
			)
			if err == nil && client == nil {
				err = errors.New("nil client returned")
			}
			req2Done <- err
		}()

		Consistently(req2Done, "60ms", "10ms").ShouldNot(Receive())
		Expect(backend.loadCalls.Load()).To(Equal(int32(1)))

		// Finish the cold load: Client 2 succeeds and subsequent requests hit
		// the loaded model with no cooldown.
		backend.release()

		var err2 error
		Eventually(req2Done, "2s").Should(Receive(&err2))
		Expect(err2).ToNot(HaveOccurred())
		Expect(backend.loadCalls.Load()).To(Equal(int32(1)))

		client3, err3 := ml.Load(
			WithContext(context.Background()),
			WithModelID("slow-model"),
			WithModel("slow-model.gguf"),
			WithBackendString("slow"),
			WithGRPCAttempts(5),
			WithGRPCAttemptsDelay(0),
		)
		Expect(err3).ToNot(HaveOccurred())
		Expect(client3).ToNot(BeNil())
		Expect(backend.loadCalls.Load()).To(Equal(int32(1)))
	})

	It("registers the model for later requests when the sole client disconnects mid-load", func() {
		backend := newControllableLoadBackend(&pb.Result{Success: true}, nil)
		addr := startControllableBackend(backend)
		ml.SetExternalBackend("slow", addr)

		reqCtx, cancel := context.WithCancel(context.Background())
		defer cancel()

		req1Done := make(chan error, 1)
		go func() {
			_, err := ml.Load(
				WithContext(reqCtx),
				WithModelID("solo-model"),
				WithModel("solo-model.gguf"),
				WithBackendString("slow"),
				WithGRPCAttempts(5),
				WithGRPCAttemptsDelay(0),
			)
			req1Done <- err
		}()

		Eventually(backend.loadStarted, "2s").Should(BeClosed())
		cancel()

		var err1 error
		Eventually(req1Done, "2s").Should(Receive(&err1))
		Expect(errors.Is(err1, context.Canceled)).To(BeTrue())

		backend.release()

		Eventually(ml.GetLoadingCount, "2s", "10ms").Should(Equal(0))
		Expect(ml.CheckIsLoaded("solo-model")).ToNot(BeNil())

		client2, err2 := ml.Load(
			WithContext(context.Background()),
			WithModelID("solo-model"),
			WithModel("solo-model.gguf"),
			WithBackendString("slow"),
			WithGRPCAttempts(5),
			WithGRPCAttemptsDelay(0),
		)
		Expect(err2).ToNot(HaveOccurred())
		Expect(client2).ToNot(BeNil())
		Expect(backend.loadCalls.Load()).To(Equal(int32(1)))
	})

	It("also decouples client request deadlines from the backend load", func() {
		backend := newControllableLoadBackend(&pb.Result{Success: true}, nil)
		addr := startControllableBackend(backend)
		ml.SetExternalBackend("slow", addr)

		reqCtx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
		defer cancel()

		_, err1 := ml.Load(
			WithContext(reqCtx),
			WithModelID("deadline-model"),
			WithModel("deadline-model.gguf"),
			WithBackendString("slow"),
			WithGRPCAttempts(5),
			WithGRPCAttemptsDelay(0),
		)
		Expect(errors.Is(err1, context.DeadlineExceeded)).To(BeTrue(), "expected context.DeadlineExceeded, got: %v", err1)
		Consistently(backend.loadCanceled.Load, "60ms", "10ms").Should(BeFalse())

		backend.release()
		Eventually(ml.GetLoadingCount, "2s", "10ms").Should(Equal(0))

		client2, err2 := ml.Load(
			WithContext(context.Background()),
			WithModelID("deadline-model"),
			WithModel("deadline-model.gguf"),
			WithBackendString("slow"),
			WithGRPCAttempts(5),
			WithGRPCAttemptsDelay(0),
		)
		Expect(err2).ToNot(HaveOccurred())
		Expect(client2).ToNot(BeNil())
		Expect(backend.loadCalls.Load()).To(Equal(int32(1)))
	})

	It("does not tear down the model when a follower disconnects mid-load", func() {
		backend := newControllableLoadBackend(&pb.Result{Success: true}, nil)
		addr := startControllableBackend(backend)
		ml.SetExternalBackend("slow", addr)

		leaderDone := make(chan error, 1)
		go func() {
			_, err := ml.Load(
				WithContext(context.Background()),
				WithModelID("shared-model"),
				WithModel("shared-model.gguf"),
				WithBackendString("slow"),
				WithGRPCAttempts(5),
				WithGRPCAttemptsDelay(0),
			)
			leaderDone <- err
		}()
		Eventually(backend.loadStarted, "2s").Should(BeClosed())

		followerCtx, cancelFollower := context.WithCancel(context.Background())
		defer cancelFollower()
		followerDone := make(chan error, 1)
		go func() {
			_, err := ml.Load(
				WithContext(followerCtx),
				WithModelID("shared-model"),
				WithModel("shared-model.gguf"),
				WithBackendString("slow"),
				WithGRPCAttempts(5),
				WithGRPCAttemptsDelay(0),
			)
			followerDone <- err
		}()

		Consistently(followerDone, "40ms", "10ms").ShouldNot(Receive())
		cancelFollower()

		var followerErr error
		Eventually(followerDone, "2s").Should(Receive(&followerErr))
		Expect(errors.Is(followerErr, context.Canceled)).To(BeTrue())

		backend.release()

		var leaderErr error
		Eventually(leaderDone, "2s").Should(Receive(&leaderErr))
		Expect(leaderErr).ToNot(HaveOccurred())
		Expect(ml.CheckIsLoaded("shared-model")).ToNot(BeNil())
		Expect(backend.loadCalls.Load()).To(Equal(int32(1)))
	})

	It("still arms failure cooldown when the backend itself fails to load", func() {
		backend := newControllableLoadBackend(&pb.Result{Success: false, Message: "out of memory"}, nil)
		backend.release()
		addr := startControllableBackend(backend)
		ml.SetExternalBackend("failing", addr)

		_, err1 := ml.Load(
			WithContext(context.Background()),
			WithModelID("broken-model"),
			WithModel("broken-model.gguf"),
			WithBackendString("failing"),
			WithGRPCAttempts(5),
			WithGRPCAttemptsDelay(0),
		)
		Expect(err1).To(HaveOccurred())
		var coolErr *ModelLoadCooldownError
		Expect(errors.As(err1, &coolErr)).To(BeFalse())

		_, err2 := ml.Load(
			WithContext(context.Background()),
			WithModelID("broken-model"),
			WithModel("broken-model.gguf"),
			WithBackendString("failing"),
			WithGRPCAttempts(5),
			WithGRPCAttemptsDelay(0),
		)
		Expect(errors.As(err2, &coolErr)).To(BeTrue(), "expected cooldown error after genuine load failure, got: %v", err2)
		Expect(backend.loadCalls.Load()).To(Equal(int32(1)))
	})

	It("does not start a backend or arm cooldown for an already-canceled request", func() {
		backend := newControllableLoadBackend(&pb.Result{Success: true}, nil)
		backend.release()
		addr := startControllableBackend(backend)
		ml.SetExternalBackend("slow", addr)

		canceledCtx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err1 := ml.Load(
			WithContext(canceledCtx),
			WithModelID("pre-canceled"),
			WithModel("pre-canceled.gguf"),
			WithBackendString("slow"),
			WithGRPCAttempts(5),
			WithGRPCAttemptsDelay(0),
		)
		Expect(errors.Is(err1, context.Canceled)).To(BeTrue())
		Expect(backend.loadCalls.Load()).To(Equal(int32(0)))

		client2, err2 := ml.Load(
			WithContext(context.Background()),
			WithModelID("pre-canceled"),
			WithModel("pre-canceled.gguf"),
			WithBackendString("slow"),
			WithGRPCAttempts(5),
			WithGRPCAttemptsDelay(0),
		)
		Expect(err2).ToNot(HaveOccurred())
		Expect(client2).ToNot(BeNil())
		Expect(backend.loadCalls.Load()).To(Equal(int32(1)))
	})

	It("stops auto-load backend iteration when the caller disconnects mid-load", func() {
		first := newControllableLoadBackend(&pb.Result{Success: true}, nil)
		second := newControllableLoadBackend(&pb.Result{Success: true}, nil)
		second.release()

		ml.SetExternalBackend("alpha-backend", startControllableBackend(first))
		ml.SetExternalBackend("beta-backend", startControllableBackend(second))

		reqCtx, cancel := context.WithCancel(context.Background())
		defer cancel()

		done := make(chan error, 1)
		go func() {
			_, err := ml.Load(
				WithContext(reqCtx),
				WithModelID("auto-model"),
				WithModel("auto-model.bin"),
				WithGRPCAttempts(5),
				WithGRPCAttemptsDelay(0),
			)
			done <- err
		}()

		Eventually(first.loadStarted, "2s").Should(BeClosed())
		cancel()

		var err error
		Eventually(done, "2s").Should(Receive(&err))
		Expect(errors.Is(err, context.Canceled)).To(BeTrue())
		Expect(second.loadCalls.Load()).To(Equal(int32(0)),
			"auto-load must not probe additional backends after the caller canceled")

		first.release()
		Eventually(ml.GetLoadingCount, "2s", "10ms").Should(Equal(0))
		Expect(ml.CheckIsLoaded("auto-model")).ToNot(BeNil())
	})

	It("keeps the per-model lifecycle lock until a detached load finishes so ShutdownModel sees the model", func() {
		backend := newControllableLoadBackend(&pb.Result{Success: true}, nil)
		addr := startControllableBackend(backend)
		ml.SetExternalBackend("slow", addr)

		reqCtx, cancel := context.WithCancel(context.Background())
		defer cancel()

		reqDone := make(chan error, 1)
		go func() {
			_, err := ml.Load(
				WithContext(reqCtx),
				WithModelID("shutdown-after-cancel"),
				WithModel("shutdown-after-cancel.gguf"),
				WithBackendString("slow"),
				WithGRPCAttempts(5),
				WithGRPCAttemptsDelay(0),
			)
			reqDone <- err
		}()

		Eventually(backend.loadStarted, "2s").Should(BeClosed())
		cancel()

		var reqErr error
		Eventually(reqDone, "2s").Should(Receive(&reqErr))
		Expect(errors.Is(reqErr, context.Canceled)).To(BeTrue())

		shutdownDone := make(chan error, 1)
		go func() {
			shutdownDone <- ml.ShutdownModel("shutdown-after-cancel")
		}()

		Consistently(shutdownDone, "60ms", "10ms").ShouldNot(Receive(),
			"ShutdownModel must wait for the detached in-flight load to finish")

		backend.release()

		var shutdownErr error
		Eventually(shutdownDone, "2s").Should(Receive(&shutdownErr))
		Expect(shutdownErr).ToNot(HaveOccurred())
		Expect(ml.ListLoadedModels()).To(BeEmpty())
	})

	It("continues propagating request context cancellation to distributed ModelRouter", func() {
		routerStarted := make(chan struct{})
		routerCanceled := make(chan error, 1)

		ml.SetModelRouter(func(ctx context.Context, _, _, _, _, _ string, _ *pb.ModelOptions, _ bool) (*Model, error) {
			close(routerStarted)
			<-ctx.Done()
			routerCanceled <- ctx.Err()
			return nil, ctx.Err()
		})

		reqCtx, cancel := context.WithCancel(context.Background())
		defer cancel()

		done := make(chan error, 1)
		go func() {
			_, err := ml.Load(
				WithContext(reqCtx),
				WithModelID("distributed-model"),
				WithModel("distributed-model.gguf"),
				WithBackendString("llama-cpp"),
			)
			done <- err
		}()

		Eventually(routerStarted, "2s").Should(BeClosed())
		cancel()

		var routerErr, loadErr error
		Eventually(routerCanceled, "2s").Should(Receive(&routerErr))
		Expect(errors.Is(routerErr, context.Canceled)).To(BeTrue())
		Eventually(done, "2s").Should(Receive(&loadErr))
		Expect(errors.Is(loadErr, context.Canceled)).To(BeTrue())
	})
})
