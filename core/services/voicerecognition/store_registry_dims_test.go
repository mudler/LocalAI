package voicerecognition_test

import (
	"context"
	"fmt"
	"math"
	"sync"

	"github.com/mudler/LocalAI/core/services/voicerecognition"
	"github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ggrpc "google.golang.org/grpc"
)

// strictStore mimics local-store: one dimension per store, an error on a
// mixed-dimension Set or Find, cosine similarity for Find.
type strictStore struct {
	grpc.Backend
	mu   sync.Mutex
	keys [][]float32
	vals [][]byte
}

func (s *strictStore) dimErr(n int) error {
	if len(s.keys) > 0 && len(s.keys[0]) != n {
		return fmt.Errorf("Try to add key with length %d when existing length is %d", n, len(s.keys[0]))
	}
	return nil
}

func (s *strictStore) StoresSet(_ context.Context, in *pb.StoresSetOptions, _ ...ggrpc.CallOption) (*pb.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, k := range in.Keys {
		if err := s.dimErr(len(k.Floats)); err != nil {
			return nil, err
		}
		s.keys = append(s.keys, k.Floats)
		s.vals = append(s.vals, in.Values[i].Bytes)
	}
	return &pb.Result{Success: true}, nil
}

func (s *strictStore) StoresDelete(_ context.Context, in *pb.StoresDeleteOptions, _ ...ggrpc.CallOption) (*pb.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range in.Keys {
		for i, k := range s.keys {
			if fmt.Sprint(k) == fmt.Sprint(d.Floats) {
				s.keys = append(s.keys[:i], s.keys[i+1:]...)
				s.vals = append(s.vals[:i], s.vals[i+1:]...)
				break
			}
		}
	}
	return &pb.Result{Success: true}, nil
}

func (s *strictStore) StoresFind(_ context.Context, in *pb.StoresFindOptions, _ ...ggrpc.CallOption) (*pb.StoresFindResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.dimErr(len(in.Key.Floats)); err != nil {
		return nil, err
	}
	res := &pb.StoresFindResult{}
	for i, k := range s.keys {
		var dot, na, nb float64
		for j := range k {
			dot += float64(k[j] * in.Key.Floats[j])
			na += float64(k[j] * k[j])
			nb += float64(in.Key.Floats[j] * in.Key.Floats[j])
		}
		res.Keys = append(res.Keys, &pb.StoresKey{Floats: k})
		res.Values = append(res.Values, &pb.StoresValue{Bytes: s.vals[i]})
		res.Similarities = append(res.Similarities, float32(dot/math.Sqrt(na*nb)))
	}
	return res, nil
}

func unit(dim, hot int) []float32 {
	v := make([]float32, dim)
	v[hot] = 1
	return v
}

var _ = Describe("storeRegistry with several embedding dimensions", func() {
	var (
		mu     sync.Mutex
		stores map[string]*strictStore
		reg    voicerecognition.Registry
		ctx    = context.Background()
	)
	BeforeEach(func() {
		stores = map[string]*strictStore{}
		reg = voicerecognition.NewStoreRegistry(func(_ context.Context, name string) (grpc.Backend, error) {
			mu.Lock()
			defer mu.Unlock()
			if stores[name] == nil {
				stores[name] = &strictStore{}
			}
			return stores[name], nil
		}, "voices", 0)
	})

	It("keeps the configured store name for the first dimension and suffixes later ones", func() {
		_, err := reg.Register(ctx, unit(192, 0), voicerecognition.Metadata{Name: "ada"})
		Expect(err).ToNot(HaveOccurred())
		_, err = reg.Register(ctx, unit(256, 0), voicerecognition.Metadata{Name: "ada"})
		Expect(err).ToNot(HaveOccurred())
		_, err = reg.Register(ctx, unit(192, 1), voicerecognition.Metadata{Name: "ben"})
		Expect(err).ToNot(HaveOccurred())
		names := []string{}
		for n := range stores {
			names = append(names, n)
		}
		Expect(names).To(ConsistOf("voices", "voices-256"))
		Expect(stores["voices"].keys).To(HaveLen(2))
		Expect(stores["voices-256"].keys).To(HaveLen(1))
	})

	It("identifies within the probe dimension only", func() {
		a192, _ := reg.Register(ctx, unit(192, 0), voicerecognition.Metadata{Name: "ada-192"})
		a256, _ := reg.Register(ctx, unit(256, 0), voicerecognition.Metadata{Name: "ada-256"})

		m, err := reg.Identify(ctx, unit(192, 0), 5)
		Expect(err).ToNot(HaveOccurred())
		Expect(m).To(HaveLen(1))
		Expect(m[0].ID).To(Equal(a192.ID))

		m, err = reg.Identify(ctx, unit(256, 0), 5)
		Expect(err).ToNot(HaveOccurred())
		Expect(m).To(HaveLen(1))
		Expect(m[0].ID).To(Equal(a256.ID))
		Expect(m[0].Distance).To(BeNumerically("~", 0, 1e-6))
	})

	It("returns no match, not an error, for a dimension without voices", func() {
		_, err := reg.Register(ctx, unit(192, 0), voicerecognition.Metadata{Name: "ada"})
		Expect(err).ToNot(HaveOccurred())
		m, err := reg.Identify(ctx, unit(512, 0), 5)
		Expect(err).ToNot(HaveOccurred())
		Expect(m).To(BeEmpty())
		Expect(stores).To(HaveLen(1)) // the probe created no store
	})

	It("returns no match on an empty registry", func() {
		m, err := reg.Identify(ctx, unit(192, 0), 5)
		Expect(err).ToNot(HaveOccurred())
		Expect(m).To(BeEmpty())
	})

	It("lets one name hold voices from several encoders and forgets each by ID", func() {
		a192, _ := reg.Register(ctx, unit(192, 0), voicerecognition.Metadata{Name: "ada", Model: "ecapa.gguf"})
		a256, _ := reg.Register(ctx, unit(256, 0), voicerecognition.Metadata{Name: "ada", Model: "wespeaker.gguf"})

		got, err := reg.List(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(HaveLen(2))
		Expect(got[0].Embedding).To(HaveLen(192))
		Expect(got[1].Embedding).To(HaveLen(256))

		Expect(reg.Forget(ctx, a256.ID)).To(Succeed())
		Expect(stores["voices-256"].keys).To(BeEmpty())
		Expect(stores["voices"].keys).To(HaveLen(1))
		m, err := reg.Identify(ctx, unit(256, 0), 5)
		Expect(err).ToNot(HaveOccurred())
		Expect(m).To(BeEmpty())

		Expect(reg.Forget(ctx, a192.ID)).To(Succeed())
		Expect(reg.Forget(ctx, a192.ID)).To(MatchError(voicerecognition.ErrNotFound))
	})

	It("still enforces a fixed dimension when one is configured", func() {
		fixed := voicerecognition.NewStoreRegistry(func(context.Context, string) (grpc.Backend, error) { return &strictStore{}, nil }, "voices", 192)
		_, err := fixed.Register(ctx, unit(256, 0), voicerecognition.Metadata{Name: "ada"})
		Expect(err).To(MatchError(voicerecognition.ErrDimensionMismatch))
		_, err = fixed.Identify(ctx, unit(256, 0), 1)
		Expect(err).To(MatchError(voicerecognition.ErrDimensionMismatch))
	})

	It("is safe for concurrent registration and identification across dimensions", func() {
		dims := []int{192, 256, 512}
		var wg sync.WaitGroup
		for i := 0; i < 30; i++ {
			wg.Add(1)
			go func(i int) {
				defer GinkgoRecover()
				defer wg.Done()
				d := dims[i%len(dims)]
				_, err := reg.Register(ctx, unit(d, i%d), voicerecognition.Metadata{Name: fmt.Sprintf("v%d", i)})
				Expect(err).ToNot(HaveOccurred())
				_, err = reg.Identify(ctx, unit(d, 0), 3)
				Expect(err).ToNot(HaveOccurred())
			}(i)
		}
		wg.Wait()
		got, _ := reg.List(ctx)
		Expect(got).To(HaveLen(30))
		Expect(stores).To(HaveLen(3))
	})
})
