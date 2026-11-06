// SPDX-License-Identifier: MIT
package music

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("value contract", Label("coverage"), func() {
	It("rejects either forbidden cover model before a state exists", func() {
		r := fixture(cover)
		r.Generation.Authorized = false
		s, code := Preflight(r)
		Expect(s).To(BeNil())
		Expect(code).To(Equal("preflight"))
		r = fixture(cover)
		r.Transcription.Authorized = false
		s, code = Preflight(r)
		Expect(s).To(BeNil())
		Expect(code).To(Equal("preflight"))
	})
})

func fixture(op operation) request {
	return request{ID: "request-a", Principal: "owner-a", Operation: op, Transcription: facts{true, true, true, true, "score-model"}, Generation: facts{true, true, true, true, "audio-model"}, Feature: true, Quota: true, InputsValid: true, StorageReady: true, Deadline: 100, Now: 1}
}

var _ = Describe("preflight guards", Label("coverage"), func() {
	It("fails closed for every untrusted prerequisite before effects exist", func() {
		mutations := []func(*request){
			func(r *request) { r.ID = "" }, func(r *request) { r.Principal = "" }, func(r *request) { r.Feature = false }, func(r *request) { r.Quota = false }, func(r *request) { r.InputsValid = false }, func(r *request) { r.StorageReady = false }, func(r *request) { r.Now = r.Deadline }, func(r *request) { r.Deadline = r.Now + 901 },
			func(r *request) { r.Transcription.Authorized = false }, func(r *request) { r.Generation.Authorized = false }, func(r *request) { r.Generation.Capable = false }, func(r *request) { r.Generation.IdentityMatches = false }, func(r *request) { r.Generation.ContextFits = false }, func(r *request) { r.Generation.Model = "" }, func(r *request) { r.Transcription.Capable = false }, func(r *request) { r.Transcription.IdentityMatches = false }, func(r *request) { r.Transcription.ContextFits = false }, func(r *request) { r.Transcription.Model = "" },
		}
		for i, mutate := range mutations {
			r := fixture(cover)
			mutate(&r)
			s, code := Preflight(r)
			Expect(s).To(BeNil(), i)
			Expect(code).To(Equal("preflight"))
		}
	})
})
