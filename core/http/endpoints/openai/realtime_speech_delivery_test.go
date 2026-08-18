// SPDX-License-Identifier: MIT

package openai

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/schema"
)

var _ = Describe("Realtime speech delivery", func() {
	It("accepts every emotion in the delivery vocabulary", func() {
		Expect(allowedSpeechEmotions).To(HaveLen(21))
		for emotion := range allowedSpeechEmotions {
			line := `{"delivery":{"emotion":"` + emotion + `"},"text":"Hello."}`
			clause, err := parseSpeechEnvelope(line)
			Expect(err).NotTo(HaveOccurred(), emotion)
			Expect(clause.Delivery.Emotion).To(Equal(emotion))
		}
	})

	It("rejects controls outside the bounded vocabulary", func() {
		_, err := parseSpeechEnvelope(`{"delivery":{"emotion":"joyful"},"text":"Hello."}`)
		Expect(err).To(MatchError(ContainSubstring(`unsupported emotion "joyful"`)))
	})

	It("parses newline-delimited clauses across arbitrary token boundaries", func() {
		parser := &speechEnvelopeParser{}
		input := "{\"delivery\":{\"emotion\":\"relief\"},\"text\":\"찾았어요.\"}\n" +
			"{\"delivery\":{\"expressiveness\":\"high\"},\"text\":\"本当です。\"}"
		var clauses []speechClause
		for _, fragment := range []string{input[:7], input[7:31], input[31:58], input[58:]} {
			parsed, err := parser.push(fragment)
			Expect(err).NotTo(HaveOccurred())
			clauses = append(clauses, parsed...)
		}
		parsed, err := parser.flush()
		Expect(err).NotTo(HaveOccurred())
		clauses = append(clauses, parsed...)

		Expect(clauses).To(HaveLen(2))
		Expect(clauses[0].Text).To(Equal("찾았어요."))
		Expect(clauses[0].Delivery.Emotion).To(Equal("relief"))
		Expect(clauses[1].Text).To(Equal("本当です。"))
	})

	It("renders only non-default controls into TTS instructions", func() {
		instructions := renderSpeechInstructions(speechDelivery{
			Emotion:        "amusement",
			Style:          "normal",
			Speed:          "slow",
			Pitch:          "normal",
			Expressiveness: "high",
		})
		Expect(instructions).To(Equal("Convey amusement. Speak slowly. Use high expressiveness."))
	})

	It("appends the private protocol without mutating application instructions", func() {
		history := schema.Messages{{Role: "system", StringContent: "Be concise.", Content: "Be concise."}}
		updated := withSpeechDeliveryProtocol(history)

		Expect(history[0].StringContent).To(Equal("Be concise."))
		Expect(updated[0].StringContent).To(HavePrefix("Be concise.\n\n"))
		Expect(updated[0].StringContent).To(ContainSubstring("helplessness"))
		Expect(updated[0].StringContent).To(ContainSubstring("overrides any request"))
	})

	It("joins Latin clauses with a space without inserting one into Japanese text", func() {
		Expect(speechTranscriptDelta("Hello.", "How are you?")).To(Equal(" How are you?"))
		Expect(speechTranscriptDelta("He replied.", `"Hello."`)).To(Equal(` "Hello."`))
		Expect(speechTranscriptDelta("見つけました。", "本当です。")).To(Equal("本当です。"))
		Expect(strings.TrimSpace(speechTranscriptDelta("", "Hello."))).To(Equal("Hello."))
	})
})
