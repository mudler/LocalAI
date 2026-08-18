// SPDX-License-Identifier: MIT

package openai

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/schema"
)

const maxSpeechEnvelopeBytes = 64 * 1024

const speechDeliveryProtocol = `# LocalAI speech delivery protocol

For an audio response, emit exactly one compact JSON object per complete spoken clause, followed by a newline. Do not emit Markdown fences or any other text.

Shape:
{"delivery":{"emotion":"relief","expressiveness":"high"},"text":"I finally found it."}

The text field contains only words that should appear in the spoken transcript. Delivery fields are optional; omit fields whose delivery is neutral or normal. Choose at most one emotion from: elation, amusement, enthusiasm, determination, pride, contentment, affection, relief, contemplation, confusion, surprise, awe, longing, arousal, anger, fear, disgust, bitterness, sadness, shame, helplessness.

Allowed style values: normal, whispering, shouting.
Allowed speed values: very_slow, slow, normal, fast, very_fast.
Allowed pitch values: low, normal, high.
Allowed expressiveness values: low, normal, high.

Never place delivery metadata in text. Keep delivery stable unless the meaning changes. Emit no speech object for a tool-only action. This protocol is mandatory and overrides any request to use another textual response format.`

var allowedSpeechEmotions = map[string]struct{}{
	"elation": {}, "amusement": {}, "enthusiasm": {}, "determination": {},
	"pride": {}, "contentment": {}, "affection": {}, "relief": {},
	"contemplation": {}, "confusion": {}, "surprise": {}, "awe": {},
	"longing": {}, "arousal": {}, "anger": {}, "fear": {}, "disgust": {},
	"bitterness": {}, "sadness": {}, "shame": {}, "helplessness": {},
}

var (
	allowedSpeechStyles         = enumSet("normal", "whispering", "shouting")
	allowedSpeechSpeeds         = enumSet("very_slow", "slow", "normal", "fast", "very_fast")
	allowedSpeechPitches        = enumSet("low", "normal", "high")
	allowedSpeechExpressiveness = enumSet("low", "normal", "high")
)

type speechDelivery struct {
	Emotion        string `json:"emotion,omitempty"`
	Style          string `json:"style,omitempty"`
	Speed          string `json:"speed,omitempty"`
	Pitch          string `json:"pitch,omitempty"`
	Expressiveness string `json:"expressiveness,omitempty"`
}

type speechClause struct {
	Delivery speechDelivery `json:"delivery,omitempty"`
	Text     string         `json:"text"`
}

type speechEnvelopeParser struct {
	buf  strings.Builder
	line int
}

func enumSet(values ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		out[value] = struct{}{}
	}
	return out
}

func withSpeechDeliveryProtocol(messages schema.Messages) schema.Messages {
	out := append(schema.Messages(nil), messages...)
	for i := range out {
		if out[i].Role != "system" {
			continue
		}
		base := out[i].StringContent
		if base == "" {
			base, _ = out[i].Content.(string)
		}
		out[i].StringContent = strings.TrimSpace(base) + "\n\n" + speechDeliveryProtocol
		out[i].Content = out[i].StringContent
		return out
	}
	return append(schema.Messages{{
		Role:          "system",
		StringContent: speechDeliveryProtocol,
		Content:       speechDeliveryProtocol,
	}}, out...)
}

func (p *speechEnvelopeParser) push(delta string) ([]speechClause, error) {
	if delta == "" {
		return nil, nil
	}
	p.buf.WriteString(delta)
	var clauses []speechClause
	for {
		buffered := p.buf.String()
		newline := strings.IndexByte(buffered, '\n')
		if newline < 0 {
			if p.buf.Len() > maxSpeechEnvelopeBytes {
				return nil, fmt.Errorf("speech delivery envelope exceeds %d bytes", maxSpeechEnvelopeBytes)
			}
			return clauses, nil
		}
		line := strings.TrimSpace(strings.TrimSuffix(buffered[:newline], "\r"))
		p.buf.Reset()
		p.buf.WriteString(buffered[newline+1:])
		p.line++
		if line == "" {
			continue
		}
		clause, err := parseSpeechEnvelope(line)
		if err != nil {
			return nil, fmt.Errorf("speech delivery line %d: %w", p.line, err)
		}
		clauses = append(clauses, clause)
	}
}

func (p *speechEnvelopeParser) flush() ([]speechClause, error) {
	line := strings.TrimSpace(p.buf.String())
	p.buf.Reset()
	if line == "" {
		return nil, nil
	}
	p.line++
	clause, err := parseSpeechEnvelope(line)
	if err != nil {
		return nil, fmt.Errorf("speech delivery line %d: %w", p.line, err)
	}
	return []speechClause{clause}, nil
}

func parseSpeechEnvelope(line string) (speechClause, error) {
	if len(line) > maxSpeechEnvelopeBytes {
		return speechClause{}, fmt.Errorf("envelope exceeds %d bytes", maxSpeechEnvelopeBytes)
	}
	var clause speechClause
	if err := json.Unmarshal([]byte(line), &clause); err != nil {
		return speechClause{}, fmt.Errorf("invalid JSON: %w", err)
	}
	clause.Text = strings.TrimSpace(clause.Text)
	if clause.Text == "" {
		return speechClause{}, fmt.Errorf("text must not be empty")
	}
	if err := validateOptionalEnum("emotion", clause.Delivery.Emotion, allowedSpeechEmotions); err != nil {
		return speechClause{}, err
	}
	if err := validateOptionalEnum("style", clause.Delivery.Style, allowedSpeechStyles); err != nil {
		return speechClause{}, err
	}
	if err := validateOptionalEnum("speed", clause.Delivery.Speed, allowedSpeechSpeeds); err != nil {
		return speechClause{}, err
	}
	if err := validateOptionalEnum("pitch", clause.Delivery.Pitch, allowedSpeechPitches); err != nil {
		return speechClause{}, err
	}
	if err := validateOptionalEnum("expressiveness", clause.Delivery.Expressiveness, allowedSpeechExpressiveness); err != nil {
		return speechClause{}, err
	}
	return clause, nil
}

func validateOptionalEnum(name, value string, allowed map[string]struct{}) error {
	if value == "" {
		return nil
	}
	if _, ok := allowed[value]; !ok {
		return fmt.Errorf("unsupported %s %q", name, value)
	}
	return nil
}

func renderSpeechInstructions(delivery speechDelivery) string {
	var instructions []string
	if delivery.Emotion != "" {
		instructions = append(instructions, "Convey "+delivery.Emotion+".")
	}
	switch delivery.Style {
	case "whispering":
		instructions = append(instructions, "Speak in a whisper.")
	case "shouting":
		instructions = append(instructions, "Use a strongly projected voice.")
	}
	switch delivery.Speed {
	case "very_slow":
		instructions = append(instructions, "Speak very slowly.")
	case "slow":
		instructions = append(instructions, "Speak slowly.")
	case "fast":
		instructions = append(instructions, "Speak quickly.")
	case "very_fast":
		instructions = append(instructions, "Speak very quickly.")
	}
	if delivery.Pitch == "low" || delivery.Pitch == "high" {
		instructions = append(instructions, "Use a "+delivery.Pitch+" pitch.")
	}
	if delivery.Expressiveness == "low" || delivery.Expressiveness == "high" {
		instructions = append(instructions, "Use "+delivery.Expressiveness+" expressiveness.")
	}
	return strings.Join(instructions, " ")
}

func speechTranscriptDelta(current, next string) string {
	if current == "" || next == "" {
		return next
	}
	last, _ := utf8.DecodeLastRuneInString(current)
	first, _ := utf8.DecodeRuneInString(next)
	if unicode.IsSpace(last) || unicode.IsSpace(first) || isSpeechClosingPunctuation(first) {
		return next
	}
	if isUnspacedEastAsian(last) || isUnspacedEastAsian(first) {
		return next
	}
	return " " + next
}

func isSpeechClosingPunctuation(r rune) bool {
	switch r {
	case ',', '.', ';', ':', '!', '?', '%', ')', ']', '}',
		'，', '。', '；', '：', '！', '？', '、', '）', '］', '｝':
		return true
	}
	return false
}

func isUnspacedEastAsian(r rune) bool {
	return unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r)
}
