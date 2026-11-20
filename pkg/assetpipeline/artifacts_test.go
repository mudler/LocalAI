// SPDX-License-Identifier: MIT
package assetpipeline_test

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	. "github.com/mudler/LocalAI/pkg/assetpipeline"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Artifact MIME types", func() {
	It("preserves the string representation in JSON and artifact storage", func() {
		s, err := Open(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())

		DeferCleanup(s.Close)
		ctx := context.Background()
		_, err = s.Update(ctx, "resource", "create", 0, "create", func(w *Workspace) error {
			*w = New("asset", DefaultGraph(), time.Now())
			return nil
		})
		Expect(err).NotTo(HaveOccurred())

		for _, mime := range []ArtifactMIME{MIMEPNG, MIMEJPEG, MIMEGLB} {
			Expect(mime.Valid()).To(BeTrue())

			a, err := s.PutArtifact(ctx, "resource", "fixture", mime, strings.NewReader("fixture"))
			Expect(err).NotTo(HaveOccurred())

			stored, err := s.Artifact(ctx, "resource", a.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(stored.MIME).To(Equal(mime))

			encoded, err := json.Marshal(stored)
			Expect(err).NotTo(HaveOccurred())

			var decoded Artifact
			Expect(json.Unmarshal(encoded, &decoded)).To(Succeed())
			Expect(decoded).To(Equal(stored))
			Expect(string(encoded)).To(ContainSubstring(`"mime":"` + string(mime) + `"`))
		}

		for _, mime := range []ArtifactMIME{"", "text/plain", "image/webp"} {
			Expect(mime.Valid()).To(BeFalse())

			_, err := s.PutArtifact(ctx, "resource", "fixture", mime, strings.NewReader("fixture"))
			Expect(err).To(MatchError("unsupported artifact type"))
		}
	})
})
