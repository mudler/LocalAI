// SPDX-License-Identifier: MIT
package nodes

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/services/music"
	grpc "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	g "google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

type musicOwnedBackend struct {
	grpc.Backend
	ack           chan error
	called        chan struct{}
	generation    *pb.MusicGenerationRequest
	transcription *pb.MusicTranscriptionRequest
	rpcErr        error
	success       bool
	stageAcks     map[MusicStageCorrelation]chan error
}

func (b *musicOwnedBackend) MusicSettled(c MusicStageCorrelation) <-chan error {
	if b.stageAcks != nil {
		return b.stageAcks[c]
	}
	return b.ack
}
func (b *musicOwnedBackend) MusicTranscription(_ context.Context, in *pb.MusicTranscriptionRequest, _ ...g.CallOption) (*pb.MusicTranscriptionResponse, error) {
	b.transcription = in
	b.called <- struct{}{}
	return &pb.MusicTranscriptionResponse{Abc: "X:1\nK:C\nC|\n"}, b.rpcErr
}
func (b *musicOwnedBackend) MusicGeneration(_ context.Context, in *pb.MusicGenerationRequest, _ ...g.CallOption) (*pb.MusicGenerationResponse, error) {
	b.generation = in
	if in.Params != nil {
		in.Params.Seed = 99
	}
	if in.Abc != nil {
		*in.Abc = "worker-mutated"
	}
	b.called <- struct{}{}
	return &pb.MusicGenerationResponse{Success: b.success}, b.rpcErr
}

type musicOwnedStager struct {
	FileStager
	root                      string
	foreign                   bool
	fetchErr, allocErr        error
	fetches, cleans, releases atomic.Int32
	keys                      []string
	cleanupAck                chan error
	inputAck                  chan error
	cleanupErr                error
	partialAllocation         bool
	foreignOwner              bool
	missingCleanup            bool
	missingDiscard            bool
	relativeLeaf              bool
	relativeCandidate         bool
	badKey                    bool
	stageErr                  error
	discarded                 atomic.Int32
	discardAck                chan error
	partialPrivate            bool
}

func (s *musicOwnedStager) AllocateMusicOutput(_ context.Context, _ string, id string) (MusicOutputDestination, error) {
	if s.allocErr != nil && !s.partialAllocation {
		return MusicOutputDestination{}, s.allocErr
	}
	dir, err := os.MkdirTemp(s.root, "remote-")
	if err != nil {
		return MusicOutputDestination{}, err
	}
	key := "ephemeral/outputs/" + id + "/new.wav"
	if s.foreign {
		key = "ephemeral/outputs/foreign/new.wav"
	}
	dest := MusicOutputDestination{RequestID: id, RemoteLeaf: filepath.Join(dir, "new.wav"), FetchKey: key, PrivateCandidate: filepath.Join(s.root, "private.wav"), Cleanup: func(context.Context) music.CleanupTask {
		s.cleans.Add(1)
		if s.cleanupAck != nil {
			return music.CleanupTask{AttemptError: s.cleanupErr, Released: s.cleanupAck}
		}
		return nodeReleasedTask(os.RemoveAll(dir))
	}, DiscardCandidate: func(context.Context) music.CleanupTask {
		s.discarded.Add(1)
		if s.discardAck != nil {
			return music.CleanupTask{Released: s.discardAck}
		}
		err := os.Remove(filepath.Join(s.root, "private.wav"))
		if os.IsNotExist(err) {
			err = nil
		}
		return nodeReleasedTask(err)
	}}
	if s.foreignOwner {
		dest.RequestID = "foreign"
	}
	if s.missingCleanup {
		dest.Cleanup = nil
	}
	if s.missingDiscard {
		dest.DiscardCandidate = nil
	}
	if s.relativeLeaf {
		dest.RemoteLeaf = "relative.wav"
	}
	if s.relativeCandidate {
		dest.PrivateCandidate = "relative.wav"
	}
	if s.badKey {
		dest.FetchKey = "data/not-ephemeral"
	}
	if s.partialPrivate {
		if err := os.WriteFile(dest.PrivateCandidate, []byte("partial allocation"), 0600); err != nil {
			return dest, err
		}
	}
	return dest, s.allocErr
}
func (s *musicOwnedStager) FetchRemoteByKey(_ context.Context, _, key, dst string) error {
	s.fetches.Add(1)
	s.keys = append(s.keys, key)
	if s.fetchErr != nil {
		if err := os.WriteFile(dst, []byte("partial"), 0600); err != nil {
			return err
		}
		return s.fetchErr
	}
	return os.WriteFile(dst, []byte("private fixture"), 0600)
}
func (s *musicOwnedStager) EnsureRemote(_ context.Context, _, source, key string) (string, error) {
	s.keys = append(s.keys, key)
	return filepath.Join(s.root, "remote-input"), s.stageErr
}
func (s *musicOwnedStager) ReleaseRemote(_ context.Context, _, key string) error {
	s.releases.Add(1)
	s.keys = append(s.keys, key)
	return nil
}
func musicNodeFixture() (*FileStagingClient, *musicOwnedBackend, *musicOwnedStager) {
	root, err := os.MkdirTemp("", "music-node-")
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(os.RemoveAll, root)
	b := &musicOwnedBackend{ack: make(chan error, 4), called: make(chan struct{}, 1), success: true}
	s := &musicOwnedStager{root: root}
	return NewFileStagingClient(b, s, "node"), b, s
}
func nodeGenerationRequest() *pb.MusicGenerationRequest {
	abc := "X:1\nK:C\nC|\n"
	return &pb.MusicGenerationRequest{RequestId: "request", ModelIdentity: "canonical", Style: "style", Abc: &abc, Params: &pb.MusicGenerationParams{Cot: pb.MusicCOT_MUSIC_COT_FULL, Seed: 7, GuidanceScale: 3, NumInferenceSteps: 10}}
}

var _ = Describe("Music staging ownership", func() {
	It("denies unsupported unary settlement before any staging", func() {
		c := NewFileStagingClient(nil, nil, "node")
		_, err := c.MusicGeneration(context.Background(), nodeGenerationRequest())
		Expect(err).To(HaveOccurred())
		c.Backend = &musicUnaryOnly{}
		_, err = c.MusicGeneration(context.Background(), nodeGenerationRequest())
		Expect(err).To(MatchError(music.ErrSettlementUnsupported))
	})
	It("requires the optional owned-directory allocator, with no precreated-file fallback", func() {
		c, b, _ := musicNodeFixture()
		c.stager = &fakeFileStager{}
		_, err := c.MusicGeneration(context.Background(), nodeGenerationRequest())
		Expect(err).To(MatchError(ContainSubstring("owned directory")))
		Expect(b.called).NotTo(Receive())
	})
	It("denies a missing request settlement capability before allocation", func() {
		c, b, s := musicNodeFixture()
		b.ack = nil
		_, err := c.MusicGeneration(context.Background(), nodeGenerationRequest())
		Expect(err).To(MatchError(music.ErrSettlementUnsupported))
		Expect(s.cleans.Load()).To(BeZero())
		Expect(b.called).NotTo(Receive())
	})

	It("clones ABC and params, fetches exactly once and cleans exact owned output", func() {
		c, b, s := musicNodeFixture()
		in := nodeGenerationRequest()
		before := proto.Clone(in)
		b.ack <- nil
		out, err := c.MusicGeneration(context.Background(), in)
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Success).To(BeTrue())
		Expect(proto.Equal(in, before)).To(BeTrue())
		Expect(b.generation.TrustedOutputDestination).NotTo(Equal(in.TrustedOutputDestination))
		Expect(s.fetches.Load()).To(Equal(int32(1)))
		Expect(s.cleans.Load()).To(Equal(int32(1)))
		Expect(s.keys).To(Equal([]string{"ephemeral/outputs/request/new.wav"}))
		_, err = os.Stat(filepath.Join(s.root, "private.wav"))
		Expect(err).NotTo(HaveOccurred())
	})
	for _, failure := range []string{"fetch", "false", "rpc", "alloc", "foreign"} {
		failure := failure
		It("fails closed on "+failure, func() {
			c, b, s := musicNodeFixture()
			b.ack <- nil
			switch failure {
			case "fetch":
				s.fetchErr = errors.New("fetch fatal")
			case "false":
				b.success = false
			case "rpc":
				b.rpcErr = errors.New("rpc failed")
			case "alloc":
				s.allocErr = errors.New("allocate failed")
			case "foreign":
				s.foreign = true
			}
			out, err := c.MusicGeneration(context.Background(), nodeGenerationRequest())
			Expect(err).To(HaveOccurred())
			Expect(out).To(BeNil())
			if failure != "alloc" {
				Expect(s.cleans.Load()).To(Equal(int32(1)))
			}
			if failure != "fetch" {
				Expect(s.fetches.Load()).To(BeZero())
			} else {
				_, statErr := os.Stat(filepath.Join(s.root, "private.wav"))
				Expect(os.IsNotExist(statErr)).To(BeTrue())
			}
		})
	}
	It("retains the call stack and directory after cancellation or negative acknowledgement", func() {
		c, b, s := musicNodeFixture()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() { _, err := c.MusicGeneration(ctx, nodeGenerationRequest()); done <- err }()
		Eventually(b.called, time.Second).Should(Receive())
		cancel()
		b.ack <- errors.New("not settled")
		Expect(done).NotTo(Receive())
		Expect(s.cleans.Load()).To(BeZero())
		Expect(s.fetches.Load()).To(BeZero())
		b.ack <- nil
		var err error
		Eventually(done, time.Second).Should(Receive(&err))
		Expect(err).To(MatchError(context.Canceled))
		Expect(s.cleans.Load()).To(Equal(int32(1)))
	})
	It("stages a transcription clone and releases only its actual request key after acknowledgement", func() {
		c, b, s := musicNodeFixture()
		source := filepath.Join(s.root, "borrowed.wav")
		Expect(os.WriteFile(source, []byte("fixture"), 0400)).To(Succeed())
		in := &pb.MusicTranscriptionRequest{RequestId: "request", ModelIdentity: "canonical", TrustedSource: source}
		before := proto.Clone(in)
		done := make(chan error, 1)
		go func() { _, err := c.MusicTranscription(context.Background(), in); done <- err }()
		Eventually(b.called, time.Second).Should(Receive())
		Expect(s.releases.Load()).To(BeZero())
		Expect(done).NotTo(Receive())
		b.ack <- nil
		Eventually(done, time.Second).Should(Receive(BeNil()))
		Expect(proto.Equal(in, before)).To(BeTrue())
		Expect(b.transcription.TrustedSource).NotTo(Equal(source))
		Expect(s.releases.Load()).To(Equal(int32(1)))
		Expect(s.keys).To(Equal([]string{"ephemeral/inputs/request/borrowed.wav", "ephemeral/inputs/request/borrowed.wav"}))
		data, err := os.ReadFile(source)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(Equal("fixture"))
	})
	It("keeps a partial allocation capability until cleanup acknowledgement", func() {
		c, _, s := musicNodeFixture()
		s.allocErr = errors.New("partial allocation")
		s.partialAllocation = true
		s.cleanupErr = errors.New("delete attempt")
		s.cleanupAck = make(chan error, 1)
		done := make(chan error, 1)
		go func() { _, err := c.MusicGeneration(context.Background(), nodeGenerationRequest()); done <- err }()
		Eventually(s.cleans.Load, time.Second).Should(Equal(int32(1)))
		Expect(done).NotTo(Receive())
		s.cleanupAck <- nil
		Eventually(done, time.Second).Should(Receive(HaveOccurred()))
	})
	It("does not return after failed remote output cleanup until explicit release", func() {
		c, b, s := musicNodeFixture()
		b.ack <- nil
		s.cleanupAck = make(chan error, 1)
		s.cleanupErr = errors.New("cleanup unresolved")
		done := make(chan error, 1)
		go func() { _, err := c.MusicGeneration(context.Background(), nodeGenerationRequest()); done <- err }()
		Eventually(s.cleans.Load, time.Second).Should(Equal(int32(1)))
		Expect(done).NotTo(Receive())
		s.cleanupAck <- errors.New("still unresolved")
		Expect(done).NotTo(Receive())
		s.cleanupAck <- nil
		Eventually(done, time.Second).Should(Receive(HaveOccurred()))
		Expect(s.discarded.Load()).To(Equal(int32(1)))
	})
	It("retains failed staged-input release until its definitive ack", func() {
		c, b, s := musicNodeFixture()
		s.inputAck = make(chan error, 1)
		b.ack <- nil
		path := filepath.Join(s.root, "source.wav")
		Expect(os.WriteFile(path, []byte("borrowed"), 0400)).To(Succeed())
		done := make(chan error, 1)
		go func() {
			_, err := c.MusicTranscription(context.Background(), &pb.MusicTranscriptionRequest{RequestId: "request", ModelIdentity: "canonical", TrustedSource: path})
			done <- err
		}()
		Eventually(s.releases.Load, time.Second).Should(Equal(int32(1)))
		Expect(done).NotTo(Receive())
		s.inputAck <- errors.New("delete failed")
		Expect(done).NotTo(Receive())
		s.inputAck <- nil
		Eventually(done, time.Second).Should(Receive(HaveOccurred()))
	})
	It("cannot consume transcription acknowledgement for generation of the same cover", func() {
		c, b, s := musicNodeFixture()
		a := make(chan error, 1)
		g := make(chan error, 1)
		b.stageAcks = map[MusicStageCorrelation]chan error{{"request", music.TranscriptionStage, "run"}: a, {"request", music.GenerationStage, "run"}: g}
		a <- nil
		done := make(chan error, 1)
		go func() { _, err := c.MusicGeneration(context.Background(), nodeGenerationRequest()); done <- err }()
		Eventually(b.called, time.Second).Should(Receive())
		Expect(done).NotTo(Receive())
		Expect(s.cleans.Load()).To(BeZero())
		g <- nil
		Eventually(done, time.Second).Should(Receive(BeNil()))
	})
	for _, kind := range []string{"foreign-owner", "missing-cleanup", "missing-discard", "relative-leaf", "relative-candidate", "bad-key"} {
		kind := kind
		It("rejects invalid output capability "+kind+" before native I/O", func() {
			c, b, s := musicNodeFixture()
			switch kind {
			case "foreign-owner":
				s.foreignOwner = true
			case "missing-cleanup":
				s.missingCleanup = true
			case "missing-discard":
				s.missingDiscard = true
			case "relative-leaf":
				s.relativeLeaf = true
			case "relative-candidate":
				s.relativeCandidate = true
			case "bad-key":
				s.badKey = true
			}
			_, err := c.MusicGeneration(context.Background(), nodeGenerationRequest())
			Expect(err).To(HaveOccurred())
			Expect(b.called).NotTo(Receive())
			Expect(s.fetches.Load()).To(BeZero())
			if kind == "foreign-owner" || kind == "missing-cleanup" {
				Expect(s.cleans.Load()).To(BeZero())
			} else {
				Expect(s.cleans.Load()).To(Equal(int32(1)))
			}
		})
	}
	for _, kind := range []string{"nil-request", "nil-params", "invalid-request-id", "empty-model"} {
		kind := kind
		It("rejects invalid generation envelope "+kind, func() {
			c, b, s := musicNodeFixture()
			in := nodeGenerationRequest()
			switch kind {
			case "nil-request":
				in = nil
			case "nil-params":
				in.Params = nil
			case "invalid-request-id":
				in.RequestId = "../foreign"
			case "empty-model":
				in.ModelIdentity = ""
			}
			_, err := c.MusicGeneration(context.Background(), in)
			Expect(err).To(HaveOccurred())
			Expect(b.called).NotTo(Receive())
			Expect(s.cleans.Load()).To(BeZero())
		})
	}
	It("cleans staged-input ownership on stage failure without starting native execution", func() {
		c, b, s := musicNodeFixture()
		s.stageErr = errors.New("stage failed")
		path := filepath.Join(s.root, "source.wav")
		Expect(os.WriteFile(path, []byte("borrowed"), 0400)).To(Succeed())
		_, err := c.MusicTranscription(context.Background(), &pb.MusicTranscriptionRequest{RequestId: "request", ModelIdentity: "canonical", TrustedSource: path})
		Expect(err).To(HaveOccurred())
		Expect(b.called).NotTo(Receive())
		Expect(s.releases.Load()).To(Equal(int32(1)))
	})

	It("joins exact partial allocation private candidate discard acknowledgement", func() {
		c, _, s := musicNodeFixture()
		s.partialAllocation = true
		s.partialPrivate = true
		s.allocErr = errors.New("partial allocate")
		s.discardAck = make(chan error, 1)
		done := make(chan error, 1)
		go func() { _, err := c.MusicGeneration(context.Background(), nodeGenerationRequest()); done <- err }()
		Eventually(s.discarded.Load, time.Second).Should(Equal(int32(1)))
		Expect(done).NotTo(Receive())
		Expect(os.Remove(filepath.Join(s.root, "private.wav"))).To(Succeed())
		s.discardAck <- nil
		Eventually(done, time.Second).Should(Receive(HaveOccurred()))
		Expect(s.cleans.Load()).To(Equal(int32(1)))
		Expect(s.discarded.Load()).To(Equal(int32(1)))
	})

})

type musicUnaryOnly struct{ grpc.Backend }

func (*musicUnaryOnly) MusicGeneration(context.Context, *pb.MusicGenerationRequest, ...g.CallOption) (*pb.MusicGenerationResponse, error) {
	panic("must not call")
}
func (*musicUnaryOnly) MusicTranscription(context.Context, *pb.MusicTranscriptionRequest, ...g.CallOption) (*pb.MusicTranscriptionResponse, error) {
	panic("must not call")
}

func nodeReleasedTask(err error) music.CleanupTask {
	ch := make(chan error, 1)
	ch <- nil
	return music.CleanupTask{AttemptError: err, Released: ch}
}
func (s *musicOwnedStager) ReleaseMusicInputs(ctx context.Context, node, id string, keys []string) music.CleanupTask {
	var err error
	for _, key := range keys {
		err = errors.Join(err, s.ReleaseRemote(ctx, node, key))
	}
	if s.inputAck != nil {
		return music.CleanupTask{AttemptError: err, Released: s.inputAck}
	}
	return nodeReleasedTask(err)
}
