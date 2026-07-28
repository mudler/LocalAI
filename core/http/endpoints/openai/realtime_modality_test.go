package openai

import (
	"encoding/json"

	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("resolveOutputModalities", func() {
	It("defaults to audio when neither session nor response specify", func() {
		got := resolveOutputModalities(nil, nil)
		Expect(got).To(ConsistOf(types.ModalityAudio))
	})

	It("uses session modalities when response omits them", func() {
		sess := []types.Modality{types.ModalityText}
		got := resolveOutputModalities(sess, nil)
		Expect(got).To(ConsistOf(types.ModalityText))
	})

	It("response modalities override session", func() {
		sess := []types.Modality{types.ModalityAudio}
		resp := []types.Modality{types.ModalityText}
		got := resolveOutputModalities(sess, resp)
		Expect(got).To(ConsistOf(types.ModalityText))
	})

	It("returns false from modalitiesContainAudio for text-only", func() {
		Expect(modalitiesContainAudio([]types.Modality{types.ModalityText})).To(BeFalse())
	})

	It("returns true from modalitiesContainAudio for audio (default)", func() {
		Expect(modalitiesContainAudio([]types.Modality{types.ModalityAudio})).To(BeTrue())
	})

	It("returns true when both audio and text are present", func() {
		Expect(modalitiesContainAudio([]types.Modality{types.ModalityText, types.ModalityAudio})).To(BeTrue())
	})
})

var _ = Describe("modalitiesWithAlias", func() {
	It("uses output_modalities when set", func() {
		got := modalitiesWithAlias([]types.Modality{types.ModalityText}, nil)
		Expect(got).To(ConsistOf(types.ModalityText))
	})

	It("falls back to the legacy modalities alias when output is empty", func() {
		got := modalitiesWithAlias(nil, []types.Modality{types.ModalityText})
		Expect(got).To(ConsistOf(types.ModalityText))
	})

	It("prefers output_modalities over the alias when both are present", func() {
		got := modalitiesWithAlias([]types.Modality{types.ModalityText}, []types.Modality{types.ModalityAudio})
		Expect(got).To(ConsistOf(types.ModalityText))
	})

	It("returns empty when neither is set", func() {
		Expect(modalitiesWithAlias(nil, nil)).To(BeEmpty())
	})
})

// These tests exercise the actual JSON wire boundary rather than calling the
// helper directly: they decode representative session.update / response.create
// payloads and reproduce the exact expressions used in updateSession and
// triggerResponseAtTurn. This guards against a regression where a wrong JSON
// tag or a missed call site would let encoding/json silently drop the legacy
// `modalities` key and fall the session back to audio.
var _ = Describe("modalities JSON boundary", func() {
	It("keeps a session.update text-only when only the legacy modalities alias is sent", func() {
		var ev types.SessionUpdateEvent
		Expect(json.Unmarshal([]byte(`{"type":"session.update","session":{"modalities":["text"]}}`), &ev)).To(Succeed())
		Expect(ev.Session.Realtime).NotTo(BeNil())

		// Mirrors updateSession: session.OutputModalities = modalitiesWithAlias(rt.OutputModalities, rt.Modalities)
		rt := ev.Session.Realtime
		effective := resolveOutputModalities(modalitiesWithAlias(rt.OutputModalities, rt.Modalities), nil)
		Expect(effective).To(ConsistOf(types.ModalityText))
		Expect(modalitiesContainAudio(effective)).To(BeFalse())
	})

	It("still honors the GA output_modalities field on session.update", func() {
		var ev types.SessionUpdateEvent
		Expect(json.Unmarshal([]byte(`{"type":"session.update","session":{"output_modalities":["text"]}}`), &ev)).To(Succeed())
		rt := ev.Session.Realtime
		effective := resolveOutputModalities(modalitiesWithAlias(rt.OutputModalities, rt.Modalities), nil)
		Expect(effective).To(ConsistOf(types.ModalityText))
	})

	It("defaults a session.update with no modalities to audio", func() {
		var ev types.SessionUpdateEvent
		Expect(json.Unmarshal([]byte(`{"type":"session.update","session":{}}`), &ev)).To(Succeed())
		rt := ev.Session.Realtime
		effective := resolveOutputModalities(modalitiesWithAlias(rt.OutputModalities, rt.Modalities), nil)
		Expect(modalitiesContainAudio(effective)).To(BeTrue())
	})

	It("keeps a response.create text-only when only the legacy modalities alias is sent", func() {
		var ev types.ResponseCreateEvent
		Expect(json.Unmarshal([]byte(`{"type":"response.create","response":{"modalities":["text"]}}`), &ev)).To(Succeed())

		// Mirrors triggerResponseAtTurn: respMods = modalitiesWithAlias(overrides.OutputModalities, overrides.Modalities)
		respMods := modalitiesWithAlias(ev.Response.OutputModalities, ev.Response.Modalities)
		effective := resolveOutputModalities(nil, respMods)
		Expect(effective).To(ConsistOf(types.ModalityText))
		Expect(modalitiesContainAudio(effective)).To(BeFalse())
	})

	It("lets a response.create alias override an audio session", func() {
		var ev types.ResponseCreateEvent
		Expect(json.Unmarshal([]byte(`{"type":"response.create","response":{"modalities":["text"]}}`), &ev)).To(Succeed())
		respMods := modalitiesWithAlias(ev.Response.OutputModalities, ev.Response.Modalities)
		effective := resolveOutputModalities([]types.Modality{types.ModalityAudio}, respMods)
		Expect(effective).To(ConsistOf(types.ModalityText))
	})
})
