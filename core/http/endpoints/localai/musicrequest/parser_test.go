// SPDX-License-Identifier: MIT
package musicrequest

import (
	"errors"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestMusicRequest(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Music request parsing") }

var _ = Describe("generation", func() {
	It("materializes defaults while preserving explicitly empty lyrics", func() {
		p, err := New(func(string) error { return nil }, nil)
		Expect(err).NotTo(HaveOccurred())
		r := httptest.NewRequest("POST", "/", strings.NewReader(`{"model":"m","style":"folk","lyrics":""}`))
		r.Header.Set("Content-Type", "application/json")
		got, err := p.ParseGeneration(r)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Params.COT).To(Equal("full"))
		Expect(got.Params.Steps).To(Equal(32))
		Expect(got.Lyrics).To(Equal(""))
	})
})
var _ = Describe("strict generation validation", func() {
	It("rejects malformed, ambiguous, and unsupported fields", func() {
		p, _ := New(func(string) error { return nil }, nil)
		for _, tc := range []struct {
			body   string
			status int
		}{
			{`{"model":"m","style":"s"}`, 400},
			{`{"model":"m","style":"s","lyrics":null}`, 400},
			{`{"model":"m","style":"s","lyrics":"","abc":null}`, 400},
			{`{"model":"m","style":"s","lyrics":"","model":"n"}`, 400},
			{`{"model":"m","style":"s","lyrics":"","unknown":1}`, 400},
			{`{"model":"m","style":"s","lyrics":""} {}`, 400},
			{`{"model":"m","style":"s","lyrics":"","params":{"abc_file":"secret"}}`, 422},
			{`{"model":"m","style":"s","lyrics":"","params":{"seed":1.5}}`, 400},
			{`{"model":"m","style":"s","lyrics":"","params":{"seed":-1}}`, 422},
			{`{"model":"m","style":"s","lyrics":"","params":{"cot":"bad"}}`, 422},
			{`{"model":"m","style":"s","lyrics":"","params":{"cot":"off","cot":"full"}}`, 400},
			{`{"model":"m","style":"s","lyrics":"","abc":"X:1","params":{"cot":"off"}}`, 422},
		} {
			r := httptest.NewRequest("POST", "/", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/json")
			_, err := p.ParseGeneration(r)
			Expect(err).To(HaveOccurred(), tc.body)
			Expect(err.(*Error).Code).To(Equal(tc.status), tc.body)
		}
	})
})
var _ = Describe("bounded JSON and validator integration", func() {
	It("requires a validator, preserves exact ABC, and permits full with ABC", func() {
		_, err := New(nil, nil)
		Expect(err).To(HaveOccurred())
		called := 0
		score := "X:1\r\nK:C\r\nC D |\r\n"
		p, _ := New(func(s string) error { called++; Expect(s).To(Equal(score)); return nil }, nil)
		for _, params := range []string{"", `,"params":{"cot":"full"}`} {
			body := `{"model":"m","style":"s","lyrics":"","abc":` + strconv.Quote(score) + params + `}`
			r := httptest.NewRequest("POST", "/", strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			got, err := p.ParseGeneration(r)
			Expect(err).NotTo(HaveOccurred())
			Expect(*got.ABC).To(Equal(score))
			if params == "" {
				Expect(got.Params.COT).To(Equal("melody"))
			} else {
				Expect(got.Params.COT).To(Equal("full"))
			}
		}
		Expect(called).To(Equal(2))
	})
	It("rejects invalid UTF8, wrong media, oversize text and chunked bodies", func() {
		p, _ := New(func(string) error { return errors.New("invalid") }, nil)
		for _, tc := range []struct {
			body string
			code int
		}{
			{`{"model":"m","style":"s","lyrics":"` + string([]byte{255}) + `"}`, 400},
			{`{"model":"m","style":"s","lyrics":"","abc":"X:1"}`, 422},
			{`{"model":"m","style":"` + strings.Repeat("a", 4097) + `","lyrics":""}`, 413},
			{`{"model":"m","style":"s","lyrics":"` + strings.Repeat("a", 16385) + `"}`, 413},
			{`{"model":"m","style":"s","lyrics":"","params":null}`, 400},
			{`{"model":"m","style":"s","lyrics":"","params":{"guidance_scale":"3"}}`, 400},
			{`{"model":"m","style":"s","lyrics":"","params":{"guidance_scale":1e999}}`, 422},
			{`{"model":"m","style":"s","lyrics":"","params":{"num_inference_steps":101}}`, 422},
			{`{"model":"m","style":"s","lyrics":"","params":{` + strings.Repeat(" ", 2048) + `}}`, 413},
		} {
			r := httptest.NewRequest("POST", "/", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/json")
			_, err := p.ParseGeneration(r)
			checkCode(err, tc.code)
		}
		r := httptest.NewRequest("POST", "/", strings.NewReader(`{}`))
		_, err := p.ParseGeneration(r)
		checkCode(err, 415)
		source := &repeats{left: JSONLimit + 50000}
		r = httptest.NewRequest("POST", "/", source)
		r.Header.Set("Content-Type", "application/json")
		r.ContentLength = -1
		_, err = p.ParseGeneration(r)
		checkCode(err, 413)
		Expect(source.reads).To(Equal(int64(JSONLimit + 1)))
	})
	It("rejects a known oversized Content-Length before any read", func() {
		source := &repeats{left: JSONLimit * 2}
		p, _ := New(func(string) error { return nil }, nil)
		r := httptest.NewRequest("POST", "/", source)
		r.Header.Set("Content-Type", "application/json")
		r.ContentLength = JSONLimit + 1
		_, err := p.ParseGeneration(r)
		checkCode(err, 413)
		Expect(source.reads).To(BeZero())
	})
})
var _ = Describe("native option isolation", func() {
	It("rejects every non-allowlisted option and nested params", func() {
		p, _ := New(func(string) error { return nil }, nil)
		for _, key := range []string{"abc_file", "semantic_prefix_file", "nar_noise_file", "src", "dst", "stop_after", "export_semantic", "abc", "style", "lyrics", "audio_input"} {
			r := httptest.NewRequest("POST", "/", strings.NewReader(`{"model":"m","style":"s","lyrics":"","params":{`+strconv.Quote(key)+`:"untrusted"}}`))
			r.Header.Set("Content-Type", "application/json")
			_, err := p.ParseGeneration(r)
			checkCode(err, 422)
		}
		for _, value := range []string{`{"cot":{}}`, `{"seed":[]}`, `{"guidance_scale":{}}`, `{"num_inference_steps":true}`} {
			r := httptest.NewRequest("POST", "/", strings.NewReader(`{"model":"m","style":"s","lyrics":"","params":`+value+`}`))
			r.Header.Set("Content-Type", "application/json")
			_, err := p.ParseGeneration(r)
			checkCode(err, 400)
		}
	})
})

var _ = Describe("lossless JSON Unicode", func() {
	It("rejects lone surrogate escapes before keys or values can be replaced", func() {
		p, _ := New(func(string) error { return nil }, nil)
		for _, body := range []string{
			`{"model":"m","style":"\ud800","lyrics":""}`,
			`{"model":"m","style":"\udfff","lyrics":""}`,
			`{"model":"m","style":"s","lyrics":"","abc":"X:1\ud800"}`,
			`{"model":"m","style":"s","lyrics":"","abc":"X:1\udc00"}`,
			`{"model":"m","style":"s","lyrics":"","\ud800":0}`,
			`{"model":"m","style":"s","lyrics":"","\udc00":0}`,
			`{"model":"m","style":"s","lyrics":"","params":{"\ud800":0}}`,
			`{"model":"m","style":"s","lyrics":"","params":{"\udc00":0}}`,
			`{"model":"m","style":"s","lyrics":"","params":{"cot":"\ud800"}}`,
			`{"model":"m","style":"s","lyrics":"","params":{"cot":"\udc00"}}`,
			`{"model":"m","style":"s","lyrics":"","params":{"other":{"\ud800":"v"}}}`,
			`{"model":"m","style":"s","lyrics":"","params":{"other":["\udc00"]}}`,
			`{"model":"m","style":"\ud800\u0041","lyrics":""}`,
			`{"model":"m","style":"\ud800x\udc00","lyrics":""}`,
		} {
			r := httptest.NewRequest("POST", "/", strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			_, err := p.ParseGeneration(r)
			checkCode(err, 400)
			Expect(err.(*Error).Reason).To(Equal("invalid_unicode"), body)
		}
	})
	It("accepts valid pairs, literal and escaped replacement characters without normalization", func() {
		for _, tc := range []struct{ encoded, want string }{
			{`"\ud83d\ude00"`, "😀"}, {`"\uD83D\uDE00"`, "😀"}, {`"�"`, "�"}, {`"\ufffd"`, "�"}, {`"\\ud800"`, `\ud800`},
		} {
			called := 0
			p, _ := New(func(s string) error { called++; Expect(s).To(Equal(tc.want)); return nil }, nil)
			body := `{"model":"m","style":` + tc.encoded + `,"lyrics":` + tc.encoded + `,"abc":` + tc.encoded + `}`
			r := httptest.NewRequest("POST", "/", strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			got, err := p.ParseGeneration(r)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Style).To(Equal(tc.want))
			Expect(got.Lyrics).To(Equal(tc.want))
			Expect(*got.ABC).To(Equal(tc.want))
			Expect(called).To(Equal(1))
			fields, err := object([]byte(`{` + tc.encoded + `:"v"}`))
			Expect(err).NotTo(HaveOccurred())
			Expect(fields).To(HaveKey(tc.want))
		}
	})
})

var _ = Describe("exact numeric parameter boundaries", func() {
	It("accepts inclusive endpoints and rejects immediately outside them", func() {
		p, _ := New(func(string) error { return nil }, nil)
		for _, tc := range []struct {
			params string
			code   int
		}{
			{`{"seed":0,"guidance_scale":0,"num_inference_steps":1}`, 200},
			{`{"seed":2147483647,"guidance_scale":20,"num_inference_steps":100}`, 200},
			{`{"seed":-1}`, 422}, {`{"seed":2147483648}`, 422},
			{`{"seed":9223372036854775808}`, 422}, {`{"seed":-9223372036854775809}`, 422},
			{`{"guidance_scale":-0.0000001}`, 422}, {`{"guidance_scale":20.0000001}`, 422},
			{`{"num_inference_steps":0}`, 422}, {`{"num_inference_steps":101}`, 422},
			{`{"seed":1.0}`, 400}, {`{"seed":1e0}`, 400}, {`{"num_inference_steps":1.5}`, 400},
			{`{"guidance_scale":true}`, 400}, {`{"guidance_scale":null}`, 400},
		} {
			r := httptest.NewRequest("POST", "/", strings.NewReader(`{"model":"m","style":"s","lyrics":"","params":`+tc.params+`}`))
			r.Header.Set("Content-Type", "application/json")
			got, err := p.ParseGeneration(r)
			if tc.code == 200 {
				Expect(err).NotTo(HaveOccurred())
				Expect(got.Params.COT).To(Equal("full"))
				if got.Params.Seed == 0 {
					Expect(got.Params.Guidance).To(Equal(0.0))
					Expect(got.Params.Steps).To(Equal(1))
				} else {
					Expect(got.Params.Seed).To(Equal(int64(2147483647)))
					Expect(got.Params.Guidance).To(Equal(20.0))
					Expect(got.Params.Steps).To(Equal(100))
				}
			} else {
				checkCode(err, tc.code)
			}
		}
	})
})
