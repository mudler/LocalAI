package localai_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/routing/pii"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// PIIDecideEndpoint exposes the redactor as a decision oracle. These
// specs pin the validation surface and the suggested_action mapping
// across all four actions (allow/mask/route_local/block). The redactor
// itself is covered in core/services/routing/pii/redactor_test.go.

var _ = Describe("PIIDecideEndpoint", func() {
	var redactor *pii.Redactor

	BeforeEach(func() {
		patterns, err := pii.Compile(pii.DefaultPatterns())
		Expect(err).NotTo(HaveOccurred())
		redactor = pii.NewRedactor(patterns)
	})

	It("rejects requests with no text field", func() {
		rec, _ := invokePIIDecide(redactor, `{}`)
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("text is required"))
	})

	It("rejects malformed JSON", func() {
		rec, _ := invokePIIDecide(redactor, `not json`)
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
	})

	It("returns allow for clean text", func() {
		rec, body := invokePIIDecide(redactor, `{"text":"hello world"}`)
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(body.SuggestedAction).To(Equal("allow"))
		Expect(body.Findings).To(BeEmpty())
		Expect(body.RedactedPreview).To(Equal("hello world"))
	})

	It("returns mask for text containing email (default action)", func() {
		rec, body := invokePIIDecide(redactor, `{"text":"reach me at alice@example.com please"}`)
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(body.SuggestedAction).To(Equal("mask"))
		Expect(body.Findings).To(HaveLen(1))
		Expect(body.Findings[0].Pattern).To(Equal("email"))
		Expect(body.Findings[0].HashPrefix).NotTo(BeEmpty())
		Expect(body.RedactedPreview).To(ContainSubstring("[REDACTED:email]"))
		Expect(body.RedactedPreview).NotTo(ContainSubstring("alice@example.com"))
	})

	It("returns block when an api_key_prefix is present (block beats mask)", func() {
		// api_key_prefix defaults to ActionBlock per DefaultPatterns.
		// Mix in an email so we also confirm the block-action wins
		// over the mask-action via actionRank.
		rec, body := invokePIIDecide(redactor, `{"text":"my key is sk-1234567890abcdefghij and email alice@example.com"}`)
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(body.SuggestedAction).To(Equal("block"))
		Expect(len(body.Findings)).To(BeNumerically(">=", 1))
	})

	It("returns route_local when an override sets that action", func() {
		// Promote the email pattern to route_local for this test —
		// exercises the route_local branch of suggestedAction without
		// needing a custom pattern set.
		Expect(redactor.SetAction("email", pii.ActionRouteLocal)).To(Succeed())
		rec, body := invokePIIDecide(redactor, `{"text":"contact alice@example.com"}`)
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(body.SuggestedAction).To(Equal("route_local"))
		// route_local leaves the original text intact — caller decides
		// whether to forward it to a local-only backend.
		Expect(body.RedactedPreview).To(ContainSubstring("alice@example.com"))
	})

	It("never leaks the matched value via HashPrefix", func() {
		rec, body := invokePIIDecide(redactor, `{"text":"alice@example.com"}`)
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(body.Findings).To(HaveLen(1))
		// HashPrefix is 8 hex chars of sha256 — definitely not the
		// matched value, but stable so admins can correlate leaks.
		Expect(body.Findings[0].HashPrefix).To(HaveLen(8))
		Expect(body.Findings[0].HashPrefix).NotTo(ContainSubstring("alice"))
	})
})

func invokePIIDecide(redactor *pii.Redactor, body string) (*httptest.ResponseRecorder, schema.PIIDecideResponse) {
	e := echo.New()
	e.POST("/api/pii/decide", localai.PIIDecideEndpoint(redactor))
	req := httptest.NewRequest(http.MethodPost, "/api/pii/decide", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	var parsed schema.PIIDecideResponse
	if rec.Code == http.StatusOK {
		Expect(json.Unmarshal(rec.Body.Bytes(), &parsed)).To(Succeed())
	}
	return rec, parsed
}
