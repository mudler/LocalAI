package nodes

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// progressingStager simulates a real multi-GB upload: it reports byte-level
// progress on a steady tick for `progressFor`, then either completes (when
// `finishAfterProgress`) or goes silent while still holding the transfer open,
// standing in for a worker that died mid-stream.
//
// This reproduces the production failure shape: a 70 GB checkpoint moving
// healthily at ~26 MB/s was killed at exactly 25m00s by the cold-load ceiling,
// not by any fault of the transfer itself.
type progressingStager struct {
	fakeFileStager

	tick                time.Duration
	progressFor         time.Duration
	finishAfterProgress bool

	mu       sync.Mutex
	ctxErr   error
	finished bool
	bytes    int64
}

func (s *progressingStager) EnsureRemote(ctx context.Context, _, _, key string) (string, error) {
	stopProgress := time.Now().Add(s.progressFor)
	cb := StagingProgressFromContext(ctx)
	for {
		select {
		case <-ctx.Done():
			s.mu.Lock()
			s.ctxErr = ctx.Err()
			s.mu.Unlock()
			return "", ctx.Err()
		case <-time.After(s.tick):
		}
		if time.Now().Before(stopProgress) {
			// Bytes really moved. This is the signal a progress-based deadline
			// has to honour: the transfer is healthy, just large.
			s.mu.Lock()
			s.bytes += 4 << 20
			sent := s.bytes
			s.mu.Unlock()
			if cb != nil {
				cb("quantized_model-00003-of-00004.safetensors", sent, 70<<30)
			}
			continue
		}
		if s.finishAfterProgress {
			s.mu.Lock()
			s.finished = true
			s.mu.Unlock()
			return "/remote/" + key, nil
		}
		// Past stopProgress and not finishing: silent, wedged, still open.
	}
}

func (s *progressingStager) contextErr() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ctxErr
}

func (s *progressingStager) completed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.finished
}

var _ = Describe("cold-load staging deadline", func() {
	var (
		reg      *fakeModelRouter
		factory  *stubClientFactory
		unloader *fakeUnloader
		modelDir string
	)

	BeforeEach(func() {
		reg = &fakeModelRouter{
			findAndLockErr: errors.New("not loaded"),
			findIdleNode:   &BackendNode{ID: "n1", Name: "nvidia-thor", Address: "10.0.0.1:50051"},
		}
		factory = &stubClientFactory{client: &stubBackend{loadResult: &pb.Result{Success: true}}}
		unloader = &fakeUnloader{installReply: &messaging.BackendInstallReply{
			Success: true,
			Address: "10.0.0.1:9001",
		}}
		modelDir = GinkgoT().TempDir()
	})

	route := func(router *SmartRouter) error {
		modelFile := filepath.Join(modelDir, "big.gguf")
		Expect(os.WriteFile(modelFile, []byte("weights"), 0o644)).To(Succeed())
		_, err := router.Route(context.Background(), "longcat-video-avatar-1.5",
			filepath.Join("models", "big.gguf"), "llama-cpp",
			&pb.ModelOptions{Model: "big.gguf", ModelFile: modelFile}, false)
		return err
	}

	It("lets a slow but steadily progressing transfer run past the base ceiling", func() {
		// The transfer needs 4x the base ceiling, the same shape as 70 GB at
		// 26 MB/s against a 25m ceiling. It is healthy for its whole duration.
		stager := &progressingStager{
			tick:                20 * time.Millisecond,
			progressFor:         800 * time.Millisecond,
			finishAfterProgress: true,
		}

		router := NewSmartRouter(reg, SmartRouterOptions{
			Unloader:         unloader,
			ClientFactory:    factory,
			FileStager:       stager,
			ModelLoadCeiling: 200 * time.Millisecond,
		})

		Expect(route(router)).To(Succeed())
		Expect(stager.completed()).To(BeTrue(),
			"a transfer that is moving bytes must not be killed by the ceiling")
		Expect(stager.contextErr()).ToNot(HaveOccurred())
	})

	It("kills a transfer that progresses and then stops moving bytes", func() {
		// Healthy past the base ceiling, then the worker dies mid-stream. The
		// hold must survive the progressing phase and end shortly after the
		// bytes stop, so the per-model advisory lock is released promptly.
		stager := &progressingStager{
			tick:        20 * time.Millisecond,
			progressFor: 500 * time.Millisecond,
		}

		router := NewSmartRouter(reg, SmartRouterOptions{
			Unloader:         unloader,
			ClientFactory:    factory,
			FileStager:       stager,
			ModelLoadCeiling: 200 * time.Millisecond,
		})

		start := time.Now()
		err := route(router)
		elapsed := time.Since(start)

		Expect(err).To(HaveOccurred())
		Expect(stager.contextErr()).To(MatchError(context.DeadlineExceeded))
		Expect(elapsed).To(BeNumerically(">", 500*time.Millisecond),
			"progress must extend the hold past the base ceiling")
		Expect(elapsed).To(BeNumerically("<", 5*time.Second),
			"a wedged worker must still release the advisory lock promptly")
	})

	It("bounds a peer that trickles bytes forever with the absolute cap", func() {
		// Always progressing, never finishing. The stall window alone would let
		// this hold the per-model advisory lock for all time, so the absolute
		// cap has to be what ends it.
		stager := &progressingStager{
			tick:        20 * time.Millisecond,
			progressFor: time.Hour, // effectively forever, relative to the cap
		}

		router := NewSmartRouter(reg, SmartRouterOptions{
			Unloader:             unloader,
			ClientFactory:        factory,
			FileStager:           stager,
			ModelLoadCeiling:     100 * time.Millisecond,
			StagingStallWindow:   100 * time.Millisecond,
			ModelLoadAbsoluteMax: 700 * time.Millisecond,
		})

		start := time.Now()
		err := route(router)
		elapsed := time.Since(start)

		Expect(err).To(HaveOccurred())
		Expect(stager.contextErr()).To(MatchError(context.DeadlineExceeded))
		Expect(elapsed).To(BeNumerically(">", 400*time.Millisecond),
			"progress must have extended the hold well past the base ceiling")
		Expect(elapsed).To(BeNumerically("<", 5*time.Second),
			"the absolute cap must still fire")
	})

	It("keeps a stall window from outliving a deliberately tight ceiling", func() {
		// An operator who tightens ModelLoadCeiling wants fast failure. A
		// default 5m stall window must not silently widen that back out.
		ctx, cancel := newLoadDeadlineContext(context.Background(), 50*time.Millisecond, 0, 0)
		defer cancel()

		start := time.Now()
		<-ctx.Done()
		Expect(time.Since(start)).To(BeNumerically("<", 2*time.Second))
		Expect(ctx.Err()).To(MatchError(context.DeadlineExceeded))
	})

	It("reports the absolute cap as its deadline so children budget correctly", func() {
		// gRPC children take the earlier of (their own timeout, parent
		// deadline). Exposing the rolling expiry would hand LoadModel a few
		// milliseconds of budget instead of its configured one.
		ctx, cancel := newLoadDeadlineContext(context.Background(),
			time.Minute, time.Minute, 3*time.Hour)
		defer cancel()

		deadline, ok := ctx.Deadline()
		Expect(ok).To(BeTrue())
		Expect(time.Until(deadline)).To(BeNumerically("~", 3*time.Hour, time.Minute))
	})
})
