package voicerecognition_test

import (
	"context"
	"sync"

	"github.com/mudler/LocalAI/core/services/voicerecognition"
	"github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ggrpc "google.golang.org/grpc"
)

// fakeStore records Set and Delete calls; everything else panics (nil embedded interface).
type fakeStore struct {
	grpc.Backend
	mu      sync.Mutex
	sets    int
	deletes int
	values  [][]byte
}

func (f *fakeStore) StoresSet(ctx context.Context, in *pb.StoresSetOptions, opts ...ggrpc.CallOption) (*pb.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sets++
	for _, v := range in.Values {
		f.values = append(f.values, v.Bytes)
	}
	return &pb.Result{Success: true}, nil
}

func (f *fakeStore) StoresDelete(ctx context.Context, in *pb.StoresDeleteOptions, opts ...ggrpc.CallOption) (*pb.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deletes++
	return &pb.Result{Success: true}, nil
}

// StoresFind returns every stored value as a perfect match.
func (f *fakeStore) StoresFind(ctx context.Context, in *pb.StoresFindOptions, opts ...ggrpc.CallOption) (*pb.StoresFindResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	res := &pb.StoresFindResult{}
	for _, v := range f.values {
		res.Keys = append(res.Keys, &pb.StoresKey{Floats: in.Key.Floats})
		res.Values = append(res.Values, &pb.StoresValue{Bytes: v})
		res.Similarities = append(res.Similarities, 1)
	}
	return res, nil
}

var _ = Describe("storeRegistry List", func() {
	var (
		fs  *fakeStore
		reg voicerecognition.Registry
		ctx = context.Background()
	)
	BeforeEach(func() {
		fs = &fakeStore{}
		reg = voicerecognition.NewStoreRegistry(func(context.Context, string) (grpc.Backend, error) { return fs, nil }, "t", 0)
	})

	It("lists what was registered, with the encoder tag and a copy of the embedding", func() {
		a, err := reg.Register(ctx, []float32{1, 0, 0}, voicerecognition.Metadata{Name: "ada", Model: "voice-detect-wespeaker-resnet34.gguf"})
		Expect(err).ToNot(HaveOccurred())
		_, err = reg.Register(ctx, []float32{0, 1, 0}, voicerecognition.Metadata{Name: "ben"})
		Expect(err).ToNot(HaveOccurred())

		got, err := reg.List(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(HaveLen(2))
		Expect(got[0].Metadata.ID).To(Equal(a.ID)) // oldest first
		Expect(got[0].Metadata.Name).To(Equal("ada"))
		Expect(got[0].Metadata.Model).To(Equal("voice-detect-wespeaker-resnet34.gguf"))
		Expect(got[0].Embedding).To(Equal([]float32{1, 0, 0}))
		Expect(got[1].Metadata.Model).To(BeEmpty()) // an untagged (legacy style) registration

		got[0].Embedding[0] = 99 // mutating the result must not touch the registry
		again, _ := reg.List(ctx)
		Expect(again[0].Embedding[0]).To(Equal(float32(1)))
	})

	It("forgets a voice from the list", func() {
		a, _ := reg.Register(ctx, []float32{1, 0}, voicerecognition.Metadata{Name: "ada"})
		_, _ = reg.Register(ctx, []float32{0, 1}, voicerecognition.Metadata{Name: "ben"})
		Expect(reg.Forget(ctx, a.ID)).To(Succeed())
		got, err := reg.List(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(HaveLen(1))
		Expect(got[0].Metadata.Name).To(Equal("ben"))
		Expect(fs.deletes).To(Equal(1))
	})

	It("is empty for a fresh registry", func() {
		got, err := reg.List(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(BeEmpty())
	})

	It("returns the encoder tag from Identify", func() {
		_, err := reg.Register(ctx, []float32{1, 0}, voicerecognition.Metadata{Name: "ada", Model: "enc.gguf"})
		Expect(err).ToNot(HaveOccurred())
		matches, err := reg.Identify(ctx, []float32{1, 0}, 1)
		Expect(err).ToNot(HaveOccurred())
		Expect(matches).To(HaveLen(1))
		Expect(matches[0].Metadata.Model).To(Equal("enc.gguf"))
	})
})
