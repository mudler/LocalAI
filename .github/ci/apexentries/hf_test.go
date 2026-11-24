package main

import (
	"bytes"
	"io"
	"net/http"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestApexEntries(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "apexentries")
}

// stubTransport answers every request with one canned status and body, so the
// status handling of the fetchers can be exercised without reaching the real
// HuggingFace API.
type stubTransport struct {
	status int
	body   string
}

func (t stubTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: t.status,
		Body:       io.NopCloser(bytes.NewBufferString(t.body)),
		Header:     make(http.Header),
		Request:    req,
	}, nil
}

func stubClient(status int, body string) *http.Client {
	return &http.Client{Transport: stubTransport{status: status, body: body}}
}

const oneGGUFBody = `{"siblings":[{"rfilename":"Model-APEX-I-Quality.gguf","size":10,"lfs":{"sha256":"aa","size":10}}]}`

var _ = Describe("FetchOptionalRepoFiles", func() {
	// HuggingFace answers 401 rather than 404 for a repo that does not exist
	// when the client carries no credentials, so an optional probe cannot tell
	// "absent" from "unauthorized" and must treat both as "no counterpart".
	It("treats a 401 as an absent repo and flags it as unavailable", func() {
		files, unavailable, err := FetchOptionalRepoFiles(stubClient(http.StatusUnauthorized, ""), "unsloth/Nope-GGUF")

		Expect(err).ToNot(HaveOccurred())
		Expect(files).To(BeEmpty())
		Expect(unavailable).To(BeTrue())
	})

	It("treats a 403 as an absent repo and flags it as unavailable", func() {
		files, unavailable, err := FetchOptionalRepoFiles(stubClient(http.StatusForbidden, ""), "unsloth/Gated-GGUF")

		Expect(err).ToNot(HaveOccurred())
		Expect(files).To(BeEmpty())
		Expect(unavailable).To(BeTrue())
	})

	// A clean 404 is an unambiguous absence, so it must NOT be reported as
	// unavailable: the whole point of the flag is to separate the ambiguous
	// case a human may need to look at from the settled one.
	It("treats a 404 as an absent repo without flagging it as unavailable", func() {
		files, unavailable, err := FetchOptionalRepoFiles(stubClient(http.StatusNotFound, ""), "unsloth/Nope-GGUF")

		Expect(err).ToNot(HaveOccurred())
		Expect(files).To(BeEmpty())
		Expect(unavailable).To(BeFalse())
	})

	It("parses a 200 body as usual", func() {
		files, unavailable, err := FetchOptionalRepoFiles(stubClient(http.StatusOK, oneGGUFBody), "unsloth/Real-GGUF")

		Expect(err).ToNot(HaveOccurred())
		Expect(unavailable).To(BeFalse())
		Expect(files).To(HaveLen(1))
		Expect(files[0].Name).To(Equal("Model-APEX-I-Quality.gguf"))
		Expect(files[0].SHA256).To(Equal("aa"))
	})

	// Tolerating 401/403 must not widen into tolerating everything: a 500 is a
	// broken API, not evidence about whether the repo exists.
	It("still errors on a 500", func() {
		_, _, err := FetchOptionalRepoFiles(stubClient(http.StatusInternalServerError, ""), "unsloth/Real-GGUF")

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unexpected status 500"))
	})
})

var _ = Describe("FetchRepoFiles", func() {
	// The APEX repo itself is not optional. A 401 there means the repo the run
	// was asked to publish cannot be read, which is a real failure and must not
	// be quietly downgraded to "no files".
	It("errors on a 401 for a required repo", func() {
		_, err := FetchRepoFiles(stubClient(http.StatusUnauthorized, ""), "mudler/Model-APEX-GGUF")

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unexpected status 401"))
	})

	It("errors on a 403 for a required repo", func() {
		_, err := FetchRepoFiles(stubClient(http.StatusForbidden, ""), "mudler/Model-APEX-GGUF")

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unexpected status 403"))
	})

	It("still treats a 404 as an absent repo", func() {
		files, err := FetchRepoFiles(stubClient(http.StatusNotFound, ""), "mudler/Model-APEX-GGUF")

		Expect(err).ToNot(HaveOccurred())
		Expect(files).To(BeEmpty())
	})
})

var _ = Describe("ParseRepoFiles", func() {
	It("returns gguf siblings with their lfs sha256", func() {
		body := []byte(`{"siblings":[
			{"rfilename":"Model-APEX-I-Quality.gguf","size":10,"lfs":{"sha256":"aa","size":10}},
			{"rfilename":"README.md"},
			{"rfilename":"mmproj.gguf","size":5,"lfs":{"sha256":"bb","size":5}}
		]}`)

		files, err := ParseRepoFiles(body)

		Expect(err).ToNot(HaveOccurred())
		Expect(files).To(HaveLen(2))
		Expect(files[0].Name).To(Equal("Model-APEX-I-Quality.gguf"))
		Expect(files[0].SHA256).To(Equal("aa"))
		Expect(files[1].Name).To(Equal("mmproj.gguf"))
	})

	It("reports a gguf that carries no lfs sha256", func() {
		body := []byte(`{"siblings":[{"rfilename":"mmproj.gguf","size":5}]}`)

		_, err := ParseRepoFiles(body)

		Expect(err).To(MatchError(ErrNoSHA256))
	})
})
