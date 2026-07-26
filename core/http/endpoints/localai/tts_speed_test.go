package localai

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/schema"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// f32 returns a pointer to v, matching how the OpenAI `speed` field is decoded
// (a pointer so an explicit zero is distinguishable from an omitted field).
func f32(v float32) *float32 { return &v }

// Regression for #11097: /v1/audio/speech accepted the documented OpenAI
// `speed` field, dropped it before the request reached the backend, and
// returned 200 with an unchanged playback rate. The field must now be
// normalised onto the params map that is forwarded to the gRPC TTSRequest,
// and an out-of-range value must be rejected instead of silently ignored.
var _ = Describe("applyTTSSpeed", func() {
	It("forwards speed onto the backend params map", func() {
		input := &schema.TTSRequest{Speed: f32(0.8)}
		Expect(applyTTSSpeed(input)).To(Succeed())
		Expect(input.Params).To(HaveKeyWithValue("speed", "0.8"))
	})

	It("leaves params untouched when speed is unset", func() {
		input := &schema.TTSRequest{}
		Expect(applyTTSSpeed(input)).To(Succeed())
		Expect(input.Params).To(BeNil())
	})

	It("treats an omitted speed as unset but rejects an explicit zero", func() {
		omitted := &schema.TTSRequest{}
		Expect(applyTTSSpeed(omitted)).To(Succeed())
		Expect(omitted.Params).To(BeNil())

		explicitZero := &schema.TTSRequest{Speed: f32(0)}
		err := applyTTSSpeed(explicitZero)
		Expect(err).To(HaveOccurred())
		httpErr, ok := err.(*echo.HTTPError)
		Expect(ok).To(BeTrue())
		Expect(httpErr.Code).To(Equal(http.StatusBadRequest))
		Expect(explicitZero.Params).To(BeNil())
	})

	It("keeps an explicit params entry over the OpenAI field", func() {
		input := &schema.TTSRequest{Speed: f32(0.8), Params: map[string]string{"speed": "1.5"}}
		Expect(applyTTSSpeed(input)).To(Succeed())
		Expect(input.Params).To(HaveKeyWithValue("speed", "1.5"))
	})

	It("preserves unrelated params", func() {
		input := &schema.TTSRequest{Speed: f32(2), Params: map[string]string{"ref_text": "hello"}}
		Expect(applyTTSSpeed(input)).To(Succeed())
		Expect(input.Params).To(HaveKeyWithValue("ref_text", "hello"))
		Expect(input.Params).To(HaveKeyWithValue("speed", "2"))
	})

	DescribeTable("rejects values outside the OpenAI range",
		func(speed float32) {
			input := &schema.TTSRequest{Speed: f32(speed)}
			err := applyTTSSpeed(input)
			Expect(err).To(HaveOccurred())
			httpErr, ok := err.(*echo.HTTPError)
			Expect(ok).To(BeTrue())
			Expect(httpErr.Code).To(Equal(http.StatusBadRequest))
			Expect(input.Params).To(BeNil())
		},
		Entry("below the minimum", float32(0.1)),
		Entry("above the maximum", float32(4.5)),
		Entry("negative", float32(-1)),
		Entry("explicit zero (distinct from an omitted field)", float32(0)),
	)

	DescribeTable("accepts the documented bounds",
		func(speed float32, want string) {
			input := &schema.TTSRequest{Speed: f32(speed)}
			Expect(applyTTSSpeed(input)).To(Succeed())
			Expect(input.Params).To(HaveKeyWithValue("speed", want))
		},
		Entry("minimum", float32(0.25), "0.25"),
		Entry("maximum", float32(4), "4"),
	)
})
