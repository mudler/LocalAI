package cosignverify

import (
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/mudler/LocalAI/internal"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

var _ = Describe("registry User-Agent", func() {
	It("identifies LocalAI during signature verification requests", func() {
		originalVersion := internal.Version
		internal.Version = "v-test"
		DeferCleanup(func() {
			internal.Version = originalVersion
		})

		var userAgent string
		transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
			userAgent = req.Header.Get("User-Agent")
			return &http.Response{
				StatusCode: http.StatusOK,
				Header: http.Header{
					"Content-Type":          []string{"application/vnd.oci.image.manifest.v1+json"},
					"Docker-Content-Digest": []string{"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
				},
				Body:    io.NopCloser(strings.NewReader("")),
				Request: req,
			}, nil
		})

		verifier, err := NewVerifier(Policy{
			Issuer:        "https://token.actions.githubusercontent.com",
			IdentityRegex: `^https://github\.com/example/.*`,
		}, nil, transport)
		Expect(err).NotTo(HaveOccurred())

		ref, err := name.ParseReference("registry.example.com/localai/backend:latest")
		Expect(err).NotTo(HaveOccurred())
		_, err = remote.Head(ref, verifier.remoteOptions(context.Background())...)
		Expect(err).NotTo(HaveOccurred())
		Expect(userAgent).To(ContainSubstring("LocalAI/v-test"))
	})
})
