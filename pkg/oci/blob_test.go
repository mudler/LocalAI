package oci

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("OCI", func() {
	Context("pulling images", func() {
		It("should fetch blobs correctly", func() {
			payload := []byte("local OCI blob fixture")
			digest := fmt.Sprintf("sha256:%x", sha256.Sum256(payload))
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
				w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
				if r.Method != http.MethodHead {
					_, _ = w.Write(payload)
				}
			}))
			defer server.Close()
			f, err := os.CreateTemp("", "ollama")
			Expect(err).NotTo(HaveOccurred())
			defer os.RemoveAll(f.Name())
			err = fetchImageBlob(context.Background(), strings.TrimPrefix(server.URL, "http://")+"/library/gemma", digest, f.Name(), nil, true)
			Expect(err).NotTo(HaveOccurred())
			Expect(os.ReadFile(f.Name())).To(Equal(payload))
		})
	})
})
