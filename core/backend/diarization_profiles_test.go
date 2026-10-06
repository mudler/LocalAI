// SPDX-License-Identifier: MIT
package backend

import (
	"context"
	"encoding/json"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/voicerecognition"
	grpcPkg "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"strings"
)

var _ = Describe("speaker profile transport", func() {
	It("preserves opt-in and registration IDs in offline and live transport", func() {
		v := voicerecognition.KnownVoice{ID: "id", Name: "Ada", Embedding: []float32{1, 0}}
		r := (&DiarizationRequest{IncludeSpeakerProfiles: true, KnownVoices: []voicerecognition.KnownVoice{v}}).toProto(2, "model")
		Expect(r.IncludeSpeakerProfiles).To(BeTrue())
		Expect(r.KnownVoices[0].Id).To(Equal("id"))
		var o liveOptions
		WithKnownVoices([]voicerecognition.KnownVoice{v})(&o)
		Expect(liveConfigProto("", o).KnownVoices[0].Id).To(Equal("id"))
		Expect((&DiarizationRequest{}).toProto(2, "").IncludeSpeakerProfiles).To(BeFalse())
	})
	It("validates response data against separate trusted metadata and omits defaults", func() {
		trusted := schema.SpeakerEncoder{Identity: "sha256:" + strings.Repeat("a", 64), Dimension: 2}
		raw, _ := json.Marshal(schema.SpeakerProfiles{Version: 1, Encoder: trusted})
		p, err := decodeSpeakerProfiles(string(raw), trusted)
		Expect(err).NotTo(HaveOccurred())
		Expect(p.Encoder).To(Equal(trusted))
		trusted.Dimension = 3
		_, err = decodeSpeakerProfiles(string(raw), trusted)
		Expect(err).To(HaveOccurred())
		_, err = decodeSpeakerProfiles("", trusted)
		Expect(status.Code(err)).To(Equal(codes.Unimplemented))
		raw, err = json.Marshal(diarizationResultFromProto(nil))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).NotTo(ContainSubstring("speaker_profiles"))
	})
})

var _ = Describe("profile raw slot association", func() {
	It("keeps sparse out-of-order slots separate from normalized IDs and names", func() {
		out := diarizationResultFromProto(&pb.DiarizeResponse{Segments: []*pb.DiarizeSegment{
			{Speaker: "7", Text: "Hello", Name: "Ada", Start: 0, End: 1},
			{Speaker: "2", Text: "there", Name: "Ada", Start: 1, End: 2},
		}})
		Expect(out.Segments[0].Speaker).To(Equal("SPEAKER_00"))
		Expect(out.Segments[0].Label).To(Equal("7"))
		Expect(out.Segments[1].Speaker).To(Equal("SPEAKER_01"))
		Expect(out.Segments[1].Label).To(Equal("2"))
		Expect(out.Speakers[0].Label).To(Equal("7"))
		Expect(out.Speakers[1].Label).To(Equal("2"))
		raw, err := json.Marshal(out)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).To(ContainSubstring(`"label":"7"`))
		Expect(string(raw)).To(ContainSubstring(`"label":"2"`))
	})
})

var _ = Describe("portable voice compatibility", func() {
	It("rejects same-dimension incompatible encoders and unknown identity without changing legacy selection", func() {
		identity := "sha256:" + strings.Repeat("a", 64)
		m := &portableStatusBackend{identity: identity}
		voices := []voicerecognition.KnownVoice{{ID: "match", Model: identity, Embedding: []float32{1, 0}}, {ID: "other", Model: "sha256:" + strings.Repeat("b", 64), Embedding: []float32{0, 1}}, {ID: "legacy", Model: "speaker.gguf", Embedding: []float32{1, 0}}}
		got := compatiblePortableVoices(context.Background(), m, voices)
		Expect(got).To(HaveLen(2))
		Expect(got[0].ID).To(Equal("match"))
		Expect(got[1].ID).To(Equal("legacy"))
		m.identity = ""
		got = compatiblePortableVoices(context.Background(), m, voices)
		Expect(got).To(HaveLen(1))
		Expect(got[0].ID).To(Equal("legacy"))
	})
})

var _ = Describe("portable voice encoder family", func() {
	const fam, other = "voicedetect:ecapa_tdnn:ecapa:192", "voicedetect:campplus:campplus:192"
	It("keeps a voice with another weights hash when it has a family, for the backend to judge", func() {
		identity := "sha256:" + strings.Repeat("a", 64)
		otherHash := "sha256:" + strings.Repeat("b", 64)
		m := &portableStatusBackend{identity: identity}
		voices := []voicerecognition.KnownVoice{
			{ID: "same-family", Model: otherHash, Weights: otherHash, Family: fam, Embedding: []float32{1, 0}},
			{ID: "other-family", Model: otherHash, Weights: otherHash, Family: other, Embedding: []float32{1, 0}},
			{ID: "no-family", Model: otherHash, Weights: otherHash, Embedding: []float32{1, 0}},
			{ID: "wrong-size", Model: otherHash, Weights: otherHash, Family: fam, Embedding: []float32{1, 0, 0}},
		}
		got := compatiblePortableVoices(context.Background(), m, voices)
		Expect(got).To(HaveLen(2))
		Expect(got[0].ID).To(Equal("same-family"))
		Expect(got[1].ID).To(Equal("other-family"))
	})
	It("sends the fingerprint to the backend, offline and live", func() {
		v := voicerecognition.KnownVoice{ID: "a", Name: "Ada", Embedding: []float32{1, 0}, Model: "sha256:x", Family: "f", Weights: "sha256:x"}
		offline := (&DiarizationRequest{KnownVoices: []voicerecognition.KnownVoice{v}}).toProto(2, "model")
		Expect(offline.KnownVoices[0].EncoderFamily).To(Equal("f"))
		Expect(offline.KnownVoices[0].EncoderWeights).To(Equal("sha256:x"))
		var live liveOptions
		WithKnownVoices([]voicerecognition.KnownVoice{v})(&live)
		cfg := liveConfigProto("en", live)
		Expect(cfg.KnownVoices[0].EncoderFamily).To(Equal("f"))
		Expect(cfg.KnownVoices[0].EncoderWeights).To(Equal("sha256:x"))
	})
	It("reads the family of the loaded encoder from the backend status", func() {
		m := &portableStatusBackend{identity: "sha256:" + strings.Repeat("a", 64), family: fam}
		trusted, err := speakerEncoderFromBackend(context.Background(), m)
		Expect(err).NotTo(HaveOccurred())
		Expect(trusted.Family).To(Equal(fam))
	})
})

type portableStatusBackend struct {
	grpcPkg.Backend
	identity  string
	dimension int32
	family    string
}

func (m *portableStatusBackend) Status(context.Context) (*pb.StatusResponse, error) {
	dim := m.dimension
	if dim == 0 {
		dim = 2
	}
	return &pb.StatusResponse{SpeakerEncoder: &pb.SpeakerEncoder{Identity: m.identity, Dimension: dim, Family: m.family}}, nil
}

var _ = Describe("selection before portable compatibility", func() {
	It("keeps legacy 192 candidates regardless of unrelated portable 256 order", func() {
		identity := "sha256:" + strings.Repeat("a", 64)
		makeEntry := func(id, tag string, dim int) voicerecognition.Entry {
			v := make([]float32, dim)
			v[0] = 1
			return voicerecognition.Entry{Metadata: voicerecognition.Metadata{ID: id, Name: id, Model: tag}, Embedding: v}
		}
		entries := []voicerecognition.Entry{
			makeEntry("portable", "sha256:"+strings.Repeat("b", 64), 256),
			makeEntry("legacy", "", 192),
			makeEntry("tagged", "speaker.gguf", 192),
			makeEntry("wrong-size", "speaker.gguf", 256),
		}
		m := &portableStatusBackend{identity: identity, dimension: 192}
		for _, pair := range [][]voicerecognition.Entry{{entries[0], entries[1]}, {entries[1], entries[0]}} {
			selected := voicerecognition.SelectKnownVoices(pair, "speaker.gguf")
			got := compatiblePortableVoices(context.Background(), m, selected.Voices)
			Expect(got).To(HaveLen(1))
			Expect(got[0].ID).To(Equal("legacy"))
		}
		for i := 0; i < len(entries); i++ {
			entries = append(entries[1:], entries[0])
			selected := voicerecognition.SelectKnownVoices(entries, "speaker.gguf")
			got := compatiblePortableVoices(context.Background(), m, selected.Voices)
			Expect(got).To(HaveLen(2))
			Expect(got[0].ID).To(Equal("tagged"))
			Expect(got[1].ID).To(Equal("legacy"))
			offline := (&DiarizationRequest{KnownVoices: got}).toProto(2, "model")
			var live liveOptions
			WithKnownVoices(got)(&live)
			Expect(liveConfigProto("", live).KnownVoices).To(Equal(offline.KnownVoices))
		}
	})
})
