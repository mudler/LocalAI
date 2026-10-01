package localai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/schema"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("validateSystemOneRequest", func() {
	req := func(state string, questions string) *schema.SystemOneRequest {
		r := &schema.SystemOneRequest{Model: "m", State: json.RawMessage(state)}
		Expect(json.Unmarshal([]byte(questions), &r.Questions)).To(Succeed())
		return r
	}

	It("accepts the three question types", func() {
		r := req(`"ticket text"`, `{
			"team": {"type":"choice","instructions":"which","criteria":{"a":"A","b":null}},
			"refund": {"type":"noul","instructions":"refund?","criteria":{"false":"No refund","true":"Refund asked"}},
			"urgency": {"type":"score","instructions":"how urgent","criteria":["low","high"]}
		}`)
		Expect(validateSystemOneRequest(r)).To(Succeed())
	})

	It("accepts a noul question with no criteria", func() {
		Expect(validateSystemOneRequest(req(`"x"`, `{"q":{"type":"noul","instructions":"i"}}`))).To(Succeed())
	})

	DescribeTable("refuses a malformed request with a message that names the problem",
		func(state, questions, want string) {
			Expect(validateSystemOneRequest(req(state, questions))).To(MatchError(ContainSubstring(want)))
		},
		Entry("missing state", ``, `{"q":{"type":"noul","instructions":"i"}}`, "state is required"),
		Entry("null state", `null`, `{"q":{"type":"noul","instructions":"i"}}`, "state is required"),
		Entry("blank string state", `"   "`, `{"q":{"type":"noul","instructions":"i"}}`, "state is required"),
		Entry("no questions", `"x"`, `{}`, "at least one question"),
		Entry("blank question id", `"x"`, `{" ":{"type":"noul","instructions":"i"}}`, "blank"),
		Entry("unknown type", `"x"`, `{"q":{"type":"rank","instructions":"i"}}`, "unknown type"),
		Entry("choice with one option", `"x"`, `{"q":{"type":"choice","instructions":"i","criteria":{"a":"A"}}}`, "at least 2"),
		Entry("choice with a blank option key", `"x"`, `{"q":{"type":"choice","instructions":"i","criteria":{"a":"A"," ":"B"}}}`, "blank"),
		Entry("score with one level", `"x"`, `{"q":{"type":"score","instructions":"i","criteria":["only"]}}`, "at least 2"),
		Entry("noul criteria with a stray key", `"x"`, `{"q":{"type":"noul","instructions":"i","criteria":{"maybe":"M"}}}`, `"false" and "true"`),
	)

	It("refuses more than 64 questions", func() {
		var b strings.Builder
		b.WriteString("{")
		for i := 0; i < 65; i++ {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString(`"q` + strings.Repeat("x", i) + `":{"type":"noul","instructions":"i"}`)
		}
		b.WriteString("}")
		Expect(validateSystemOneRequest(req(`"x"`, b.String()))).To(MatchError(ContainSubstring("at most 64")))
	})
})

var _ = Describe("systemOneBind", func() {
	bind := func(body string) (int, error) {
		e := echo.New()
		r := httptest.NewRequest(http.MethodPost, "/v1/systemone", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		c := e.NewContext(r, httptest.NewRecorder())
		var out schema.SystemOneRequest
		if err := systemOneBind(c, &out); err != nil {
			return systemOneBindStatus(err), err
		}
		return http.StatusOK, nil
	}

	It("binds a normal body", func() {
		status, err := bind(`{"model":"m","state":"x","questions":{}}`)
		Expect(err).ToNot(HaveOccurred())
		Expect(status).To(Equal(http.StatusOK))
	})

	It("answers 413 for a body over 64 KiB", func() {
		status, err := bind(`{"model":"m","state":"` + strings.Repeat("a", 65*1024) + `"}`)
		Expect(err).To(HaveOccurred())
		Expect(status).To(Equal(http.StatusRequestEntityTooLarge))
	})

	It("answers 400 for malformed JSON", func() {
		status, err := bind(`{not json`)
		Expect(err).To(HaveOccurred())
		Expect(status).To(Equal(http.StatusBadRequest))
	})
})
