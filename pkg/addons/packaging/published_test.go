// SPDX-License-Identifier: MIT
package packaging

import (
	"context"

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

// The release event builds nothing. It promotes the index that the tag push
// run published, so it reads that index from the registry and checks it and
// its signatures the way a host does before an install.
var _ = Describe("the index a release promotes", func() {
	const (
		ownerRepo = "example/localai"
		identity  = "https://github.com/example/localai/.github/workflows/addons-package.yml@refs/tags/v1.2.3"
	)
	var (
		reg       *countingRegistry
		p         *project
		repoName  string
		authority *signing.Authority
	)
	BeforeEach(func() {
		reg = newRegistry()
		p = newProject(both)
		repoName = reg.repo("addons/tables")
		var err error
		authority, err = signing.New()
		Expect(err).NotTo(HaveOccurred())
		previous := signatureTrust
		signatureTrust = authority
		DeferCleanup(func() { signatureTrust = previous })
	})

	// signAs attaches to subjectDigest a Sigstore bundle over signedDigest,
	// made for signer and issuer, as an OCI 1.1 referrer: what cosign
	// --new-bundle-format pushes. A real signature has both digests equal.
	signAs := func(subjectDigest, signedDigest, signer, issuer string) {
		GinkgoHelper()
		repo, err := name.NewRepository(repoName)
		Expect(err).NotTo(HaveOccurred())
		h, err := v1.NewHash(signedDigest)
		Expect(err).NotTo(HaveOccurred())
		subject, err := remote.Head(repo.Digest(subjectDigest))
		Expect(err).NotTo(HaveOccurred())
		blob, err := authority.Bundle(h.Hex, signer, issuer)
		Expect(err).NotTo(HaveOccurred())
		bundleType := types.MediaType("application/vnd.dev.sigstore.bundle.v0.3+json")
		img, err := mutate.AppendLayers(empty.Image, static.NewLayer(blob, bundleType))
		Expect(err).NotTo(HaveOccurred())
		img = mutate.ConfigMediaType(img, bundleType)
		img = mutate.Subject(img, v1.Descriptor{MediaType: subject.MediaType, Digest: subject.Digest, Size: subject.Size}).(v1.Image)
		d, err := img.Digest()
		Expect(err).NotTo(HaveOccurred())
		Expect(remote.Write(repo.Digest(d.String()), img)).To(Succeed())
	}
	sign := func(digest, signer string) {
		GinkgoHelper()
		signAs(digest, digest, signer, OIDCIssuer)
	}
	// publish pushes a release index of rev and returns its digest and the
	// digests of its platform images.
	publish := func(revision string) (string, []string) {
		GinkgoHelper()
		m := p.merged("1.2.3", revision, nil)
		_, err := run("publish", "--config", p.config, "--layout", m, "--repository", repoName, "--tag", "1.2.3")
		Expect(err).NotTo(HaveOccurred())
		repo, _ := name.NewRepository(repoName)
		d, err := remote.Head(repo.Tag("tables-1.2.3"))
		Expect(err).NotTo(HaveOccurred())
		children, err := childDigests(context.Background(), repo, d.Digest)
		Expect(err).NotTo(HaveOccurred())
		return d.Digest.String(), children
	}
	verify := func(revision string) (string, error) {
		return run("verify-published", "--config", p.config, "--repository", repoName, "--version", "1.2.3", "--revision", revision, "--owner-repo", ownerRepo)
	}

	It("prints the digest of a complete index of the release commit signed by the workflow of the tag", func() {
		index, children := publish(rev)
		for _, d := range append([]string{index}, children...) {
			sign(d, identity)
		}
		out, err := verify(rev)
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("digest=" + index + "\n"))
		Expect(out).To(ContainSubstring("identity=" + identity + "\n"))
		// The digest is the one promote names; promote takes it as it is.
		_, err = run("promote", "--repository", repoName, "--addon", "tables", "--alias", "latest", "--digest", index, "--version", "1.2.3", "--revision", rev, "--git-dir", "/nonexistent")
		Expect(err).NotTo(HaveOccurred())
	})

	It("fails when the tag push has not published the add-on yet", func() {
		_, err := verify(rev)
		expectCode(err, "E_NOT_PUBLISHED")
	})

	It("refuses an index of another commit", func() {
		index, children := publish("fedcba9876543210fedcba9876543210fedcba98")
		for _, d := range append([]string{index}, children...) {
			sign(d, identity)
		}
		_, err := verify(rev)
		Expect(err).To(MatchError(ContainSubstring("not built from the same add-on, version and commit")))
	})

	It("refuses an index or a platform image without the signature of the workflow of the tag", func() {
		index, children := publish(rev)
		// Unsigned.
		_, err := verify(rev)
		expectCode(err, "E_SIGNATURE")
		// The index is signed, one platform image is not.
		sign(index, identity)
		sign(children[0], identity)
		_, err = verify(rev)
		expectCode(err, "E_SIGNATURE")
		Expect(err).To(MatchError(ContainSubstring(children[1])))
		// Signed by the workflow of master, not of the tag.
		sign(children[1], "https://github.com/example/localai/.github/workflows/addons-package.yml@refs/heads/master")
		_, err = verify(rev)
		expectCode(err, "E_SIGNATURE")
	})

	DescribeTable("refuses a signature that is not exactly the one of the workflow of the tag",
		func(signIndex func(index, child string)) {
			index, children := publish(rev)
			for _, d := range children {
				sign(d, identity)
			}
			signIndex(index, children[0])
			_, err := verify(rev)
			expectCode(err, "E_SIGNATURE")
			Expect(err).To(MatchError(ContainSubstring(index)))
		},
		Entry("another OIDC issuer", func(index, _ string) {
			signAs(index, index, identity, "https://issuer.example")
		}),
		Entry("the workflow of another repository", func(index, _ string) {
			signAs(index, index, "https://github.com/example/fork/.github/workflows/addons-package.yml@refs/tags/v1.2.3", OIDCIssuer)
		}),
		Entry("a valid bundle of another digest attached to the index", func(index, child string) {
			signAs(index, child, identity, OIDCIssuer)
		}),
	)

	It("refuses bad inputs before any request", func() {
		for _, args := range [][]string{
			{"--version", "latest", "--revision", rev, "--owner-repo", ownerRepo},
			{"--version", "1.2.3", "--revision", "sha-0123", "--owner-repo", ownerRepo},
			{"--version", "1.2.3", "--revision", rev, "--owner-repo", "not a repo"},
		} {
			_, err := run(append([]string{"verify-published", "--config", p.config, "--repository", repoName}, args...)...)
			Expect(err).To(HaveOccurred(), "%v", args)
		}
		_, err := run("verify-published", "--config", p.config, "--repository", "quay.io/go-skynet/local-ai-backends", "--version", "1.2.3", "--revision", rev, "--owner-repo", ownerRepo)
		Expect(err).To(HaveOccurred())
		Expect(reg.writeCount()).To(BeZero())
	})
})
