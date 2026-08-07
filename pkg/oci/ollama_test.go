package oci

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("OCI", func() {
	Context("ollama", func() {
		It("pulls model files", func() {
			payload := []byte("local Ollama model fixture")
			digest := fmt.Sprintf("sha256:%x", sha256.Sum256(payload))
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
				if strings.Contains(r.URL.Path, "/manifests/") {
					_ = json.NewEncoder(w).Encode(Manifest{SchemaVersion: 2, Layers: []LayerDetail{{Digest: digest, MediaType: "application/vnd.ollama.image.model", Size: len(payload)}}})
					return
				}
				w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
				if r.Method != http.MethodHead {
					_, _ = w.Write(payload)
				}
			}))
			defer server.Close()
			f, err := os.CreateTemp("", "ollama")
			Expect(err).NotTo(HaveOccurred())
			defer os.RemoveAll(f.Name())
			err = ollamaFetchModel(context.Background(), "http", strings.TrimPrefix(server.URL, "http://"), "gemma:2b", f.Name(), nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(os.ReadFile(f.Name())).To(Equal(payload))
		})
	})
})
