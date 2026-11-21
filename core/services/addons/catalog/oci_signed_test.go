// SPDX-License-Identifier: MIT
package catalog

import (
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/static"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"github.com/mudler/LocalAI/tests/addons/fixtures/signing"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// signLibrary publishes a Sigstore bundle for the library artifact at url,
// made by the authority for the identity and the source repository given.
func signLibrary(authority *signing.Authority, url, identity, sourceRepository string) {
	GinkgoHelper()
	ref, err := name.ParseReference(ociReference(url))
	Expect(err).NotTo(HaveOccurred())
	desc, err := remote.Head(ref)
	Expect(err).NotTo(HaveOccurred())
	digest := desc.Digest.String()
	blob, err := authority.BundleFor(digest[7:], identity, pinIssuer, sourceRepository)
	Expect(err).NotTo(HaveOccurred())
	bundleType := types.MediaType("application/vnd.dev.sigstore.bundle.v0.3+json")
	img, err := mutate.AppendLayers(empty.Image, static.NewLayer(blob, bundleType))
	Expect(err).NotTo(HaveOccurred())
	Expect(remote.Write(ref.Context().Tag("signature"), img)).To(Succeed())
	hash, err := img.Digest()
	Expect(err).NotTo(HaveOccurred())
	size, err := img.Size()
	Expect(err).NotTo(HaveOccurred())
	index := mutate.AppendManifests(empty.Index, mutate.IndexAddendum{Add: img, Descriptor: v1.Descriptor{Digest: hash, Size: size, MediaType: types.DockerManifestSchema2, ArtifactType: string(bundleType)}})
	Expect(remote.WriteIndex(ref.Context().Tag(strings.Replace(digest, ":", "-", 1)), index)).To(Succeed())
}

var _ = Describe("a signed oci:// library with a pattern and a source repository", func() {
	// The real verifier runs against host-provisioned trust, so the signing
	// certificate, its source-repository extension and the policy all count.
	const identity = "signer@registry.example"
	policy := VerificationPolicy{RequireSignature: true, Issuer: pinIssuer, IdentityRegex: `^signer@registry\.example$`, SourceRepository: pinRepo}

	DescribeTable("verifies the repository the signature was made for",
		func(repoPath, signedFor string, ok bool) {
			srv, _ := ociLibraryRegistry()
			s := setup()
			authority, err := signing.New()
			Expect(err).NotTo(HaveOccurred())
			s.signatureTrust = authority
			url := pushLibrary(srv.URL, repoPath, LibraryArtifactType, libraryIndexBody())
			signLibrary(authority, url, identity, signedFor)
			Expect(s.Save(ctx, admin, Source{ID: "lib", Name: "lib", URL: url, Enabled: true, Policy: policy})).To(Succeed())
			_, err = s.Refresh(ctx, "lib")
			if ok {
				Expect(err).NotTo(HaveOccurred())
				Expect(s.cache["lib"].releases).To(HaveLen(1))
				Expect(s.cache["lib"].releases[0].Signer).To(Equal(pinRepo))
				return
			}
			Expect(err).To(MatchError(ErrRefresh))
			Expect(s.cache["lib"].err).To(ContainSubstring("signature policy"))
			Expect(s.cache["lib"].releases).To(BeEmpty())
		},
		Entry("a library signed for the pinned repository", "libs/pinned", pinRepo, true),
		Entry("a library signed for another repository", "libs/other", "https://github.com/example-org/other-repo", false),
		Entry("a library signed with no repository in the certificate", "libs/none", "", false),
	)
})
