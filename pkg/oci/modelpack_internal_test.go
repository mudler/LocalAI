package oci

import (
	"encoding/json"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fakeManifestImage serves a hand-written manifest, so artifactType and the
// config descriptor can be varied without a registry.
type fakeManifestImage struct {
	v1.Image
	manifest []byte
}

func (i *fakeManifestImage) RawManifest() ([]byte, error) { return i.manifest, nil }

func manifestJSON(fields map[string]any) []byte {
	fields["schemaVersion"] = 2
	raw, _ := json.Marshal(fields)
	return raw
}

var _ = Describe("ModelPack detection", func() {
	It("recognizes a manifest by its artifactType", func() {
		img := &fakeManifestImage{manifest: manifestJSON(map[string]any{
			"artifactType": ModelPackArtifactType,
		})}
		Expect(IsModelPackImage(img)).To(BeTrue())
	})

	It("recognizes a manifest identified only by its config mediaType", func() {
		// Some registries and older clients drop artifactType.
		img := &fakeManifestImage{manifest: manifestJSON(map[string]any{
			"config": map[string]any{"mediaType": ModelPackConfigMediaType},
		})}
		Expect(IsModelPackImage(img)).To(BeTrue())
	})

	It("does not claim a plain container image", func() {
		img := &fakeManifestImage{manifest: manifestJSON(map[string]any{
			"config": map[string]any{"mediaType": "application/vnd.oci.image.config.v1+json"},
			"layers": []any{},
		})}
		Expect(IsModelPackImage(img)).To(BeFalse())
	})

	It("does not claim a manifest with neither discriminator", func() {
		img := &fakeManifestImage{manifest: manifestJSON(map[string]any{})}
		Expect(IsModelPackImage(img)).To(BeFalse())
	})

	It("errors on an unparseable manifest rather than guessing", func() {
		_, err := IsModelPackImage(&fakeManifestImage{manifest: []byte("{not json")})
		Expect(err).To(HaveOccurred())
	})
})
