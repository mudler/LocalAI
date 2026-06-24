package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/schema"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Live PII NER tier e2e. These specs run the real privacy-filter GGUF on CPU
// through the full TokenClassify path — the gap the hermetic suite cannot
// cover (it only exercises the in-process pattern tier). They Skip unless
// PII_NER_MODEL_GGUF is wired in BeforeSuite, so the default PR suite is
// unaffected; the dedicated CI job sets it.
//
// The crown-jewel invariant is byte-offset correctness: entity Start/End are
// half-open BYTE offsets into the original UTF-8 text, and the model's emitted
// text for a span must equal the corresponding byte slice. We assert that two
// ways — directly against ModelTokenClassify (raw, Threshold 0, no redactor
// merge) and against the /api/pii/analyze HTTP contract (post-merge,
// post-MinScore). The multibyte case proves offsets are bytes, not runes.
var _ = Describe("PII NER tier (live privacy-filter GGUF)", func() {
	const (
		// Reliable, unambiguous PII the multilingual NER model detects.
		emailText = "Please contact John Doe at john.doe@example.com about invoice 4421."
		// Multibyte chars BEFORE the email push its byte offset past its rune
		// offset, so a rune/byte confusion in the engine or the Go bridge would
		// surface as a mismatched slice here but not in the ASCII case above.
		multibyteText = "Müller paid at café in Zürich; reach john.doe@example.com tomorrow."
	)

	BeforeEach(func() {
		if piiNERModel == "" {
			Skip("live PII NER model not wired (set PII_NER_MODEL_GGUF + REALTIME_BACKENDS_PATH; see tests-pii-ner-e2e.yml)")
		}
	})

	Context("raw TokenClassify (byte-offset contract)", func() {
		It("returns byte-correct, rune-aligned spans for an ASCII email", func() {
			ents := tokenClassify(emailText)
			Expect(ents).NotTo(BeEmpty(), "model must detect at least one entity in an obvious-PII sentence")
			for _, e := range ents {
				assertByteCorrectSpan(emailText, e.Start, e.End, e.Text)
			}
			Expect(spanCoversSubstring(emailText, ents, "john.doe@example.com")).To(BeTrue(),
				"some detected span must cover the email address")
		})

		It("keeps byte offsets correct when multibyte runes precede the PII", func() {
			ents := tokenClassify(multibyteText)
			Expect(ents).NotTo(BeEmpty())
			for _, e := range ents {
				// This is the assertion that fails if offsets were computed in
				// runes rather than bytes: the slice would be shifted left.
				assertByteCorrectSpan(multibyteText, e.Start, e.End, e.Text)
			}
			Expect(spanCoversSubstring(multibyteText, ents, "john.doe@example.com")).To(BeTrue())
		})
	})

	Context("HTTP /api/pii/analyze", func() {
		It("reports ner-source entities with byte-correct offsets", func() {
			status, resp := analyze(schema.PIIAnalyzeRequest{
				Text:      emailText,
				Detectors: []string{piiNERModel},
			})
			Expect(status).To(Equal(http.StatusOK))
			Expect(resp.Entities).NotTo(BeEmpty())
			for _, e := range resp.Entities {
				Expect(e.Source).To(Equal("ner"), "privacy-filter detections must be tagged source=ner")
				Expect(e.Action).To(Equal("mask"), "default_action mask must propagate to each entity")
				assertByteCorrectSpan(emailText, e.Start, e.End, emailText[e.Start:e.End])
				Expect(e.Score).To(BeNumerically(">=", 0.5), "below-MinScore spans are dropped before the response")
			}
		})
	})

	Context("HTTP /api/pii/redact", func() {
		It("masks detected PII out of the returned text", func() {
			status, body := redact(schema.PIIAnalyzeRequest{
				Text:      emailText,
				Detectors: []string{piiNERModel},
			})
			Expect(status).To(Equal(http.StatusOK))
			var resp schema.PIIRedactResponse
			Expect(json.Unmarshal(body, &resp)).To(Succeed())
			Expect(resp.Masked).To(BeTrue())
			Expect(resp.RedactedText).NotTo(Equal(emailText))
			Expect(resp.RedactedText).NotTo(ContainSubstring("john.doe@example.com"),
				"the masked email must not survive in the redacted body")
		})

		It("rejects the request with pii_blocked when an entity action is block", func() {
			status, body := redact(schema.PIIAnalyzeRequest{
				Text:      emailText,
				Detectors: []string{piiNERBlockModel},
			})
			Expect(status).To(Equal(http.StatusBadRequest))
			Expect(string(body)).To(ContainSubstring("pii_blocked"))
			Expect(string(body)).NotTo(ContainSubstring("john.doe@example.com"),
				"a blocked response must never echo the raw secret")
		})
	})
})

// tokenClassify drives core/backend.ModelTokenClassify against the live model
// with the loader/config the running server uses — the same path the NER
// detector takes, but at Threshold 0 so we see the raw, unmerged spans.
func tokenClassify(text string) []backend.TokenEntity {
	GinkgoHelper()
	cfg, ok := localAIApp.ModelConfigLoader().GetModelConfig(piiNERModel)
	Expect(ok).To(BeTrue(), "model config %q must be loaded", piiNERModel)
	fn, err := backend.ModelTokenClassify(text, backend.TokenClassifyOptions{},
		localAIApp.ModelLoader(), cfg, localAIApp.ApplicationConfig())
	Expect(err).NotTo(HaveOccurred())
	ents, err := fn(context.TODO())
	Expect(err).NotTo(HaveOccurred())
	return ents
}

// assertByteCorrectSpan is the shared byte-offset invariant: a half-open byte
// range within text, aligned to UTF-8 rune boundaries, whose slice equals the
// entity's own reported text.
func assertByteCorrectSpan(text string, start, end int, got string) {
	GinkgoHelper()
	Expect(start).To(BeNumerically(">=", 0))
	Expect(end).To(BeNumerically(">", start))
	Expect(end).To(BeNumerically("<=", len(text)))
	Expect(utf8.RuneStart(text[start])).To(BeTrue(), "start %d is mid-rune in %q", start, text)
	if end < len(text) {
		Expect(utf8.RuneStart(text[end])).To(BeTrue(), "end %d is mid-rune in %q", end, text)
	}
	slice := text[start:end]
	Expect(utf8.ValidString(slice)).To(BeTrue(), "span %q is not valid UTF-8", slice)
	Expect(slice).To(Equal(got), "entity text must equal text[start:end]")
}

func spanCoversSubstring(text string, ents []backend.TokenEntity, sub string) bool {
	lo := bytes.Index([]byte(text), []byte(sub))
	if lo < 0 {
		return false
	}
	hi := lo + len(sub)
	for _, e := range ents {
		// any overlap with [lo,hi)
		if e.Start < hi && e.End > lo {
			return true
		}
	}
	return false
}

func analyze(req schema.PIIAnalyzeRequest) (int, schema.PIIAnalyzeResponse) {
	GinkgoHelper()
	status, body := postJSON("/api/pii/analyze", req)
	var resp schema.PIIAnalyzeResponse
	if status == http.StatusOK {
		Expect(json.Unmarshal(body, &resp)).To(Succeed())
	}
	return status, resp
}

func redact(req schema.PIIAnalyzeRequest) (int, []byte) {
	GinkgoHelper()
	return postJSON("/api/pii/redact", req)
}

func postJSON(path string, payload any) (int, []byte) {
	GinkgoHelper()
	data, err := json.Marshal(payload)
	Expect(err).NotTo(HaveOccurred())
	httpResp, err := http.Post(anthropicBaseURL+path, "application/json", bytes.NewReader(data))
	Expect(err).NotTo(HaveOccurred())
	defer func() { _ = httpResp.Body.Close() }()
	body, err := io.ReadAll(httpResp.Body)
	Expect(err).NotTo(HaveOccurred())
	return httpResp.StatusCode, body
}
