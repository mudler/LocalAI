package openai

import (
	"encoding/json"

	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ConversationItemSpeakerEvent", func() {
	It("marshals with the conversation.item.speaker type and nested speaker", func() {
		ev := types.ConversationItemSpeakerEvent{
			ItemID:  "item_123",
			Speaker: types.Speaker{Name: "Jeremy", ID: "spk_1", Labels: map[string]string{"family": "yes"}, Confidence: 92, Distance: 0.1, Matched: true},
		}
		b, err := json.Marshal(ev)
		Expect(err).ToNot(HaveOccurred())

		var got map[string]any
		Expect(json.Unmarshal(b, &got)).To(Succeed())
		Expect(got["type"]).To(Equal("conversation.item.speaker"))
		Expect(got["item_id"]).To(Equal("item_123"))

		spk := got["speaker"].(map[string]any)
		Expect(spk["name"]).To(Equal("Jeremy"))
		Expect(spk["id"]).To(Equal("spk_1"))
		Expect(spk["matched"]).To(Equal(true))
		Expect(spk["labels"]).To(HaveKeyWithValue("family", "yes"))
	})

	It("omits labels when the speaker has none", func() {
		ev := types.ConversationItemSpeakerEvent{ItemID: "i", Speaker: types.Speaker{Name: "Jeremy", Matched: true}}
		b, err := json.Marshal(ev)
		Expect(err).ToNot(HaveOccurred())
		var got map[string]any
		Expect(json.Unmarshal(b, &got)).To(Succeed())
		spk := got["speaker"].(map[string]any)
		_, hasLabels := spk["labels"]
		Expect(hasLabels).To(BeFalse())
	})

	It("omits the name for an unknown speaker but keeps matched=false", func() {
		ev := types.ConversationItemSpeakerEvent{ItemID: "i", Speaker: types.Speaker{Matched: false}}
		b, err := json.Marshal(ev)
		Expect(err).ToNot(HaveOccurred())
		var got map[string]any
		Expect(json.Unmarshal(b, &got)).To(Succeed())
		spk := got["speaker"].(map[string]any)
		_, hasName := spk["name"]
		Expect(hasName).To(BeFalse())
		Expect(spk["matched"]).To(Equal(false))
	})
})
