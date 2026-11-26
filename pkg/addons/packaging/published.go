// SPDX-License-Identifier: MIT
package packaging

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/mudler/LocalAI/pkg/oci/cosignverify"
	"github.com/sigstore/sigstore-go/pkg/root"
)

// signatureTrust replaces the public Sigstore trust root when it is set. Only
// the tests set it, with a fixture authority.
var signatureTrust root.TrustedMaterial

// verifySignature checks that ref carries a keyless signature made by the
// exact identity, issued by the GitHub Actions OIDC issuer. It is the check
// a host runs before it installs a package.
func verifySignature(ctx context.Context, ref, identity string) error {
	p := cosignverify.Policy{Issuer: OIDCIssuer, Identity: identity}
	var (
		v   *cosignverify.Verifier
		err error
	)
	if signatureTrust != nil {
		v, err = cosignverify.NewVerifierWithTrustedMaterial(p, nil, nil, signatureTrust)
	} else {
		v, err = cosignverify.NewVerifier(p, nil, nil)
	}
	if err != nil {
		return err
	}
	return v.VerifyImage(ctx, ref)
}

// cmdVerifyPublished finds the index that the tag push of a release
// published under <id>-<version> and prints its digest. The release event
// promotes that index and builds nothing, so it must not trust a digest it
// did not make: the index must be a complete package index of this add-on,
// version and commit, and the index and every platform image must be signed
// by the add-on package workflow of the release tag.
func cmdVerifyPublished(args []string) error {
	fs := flag.NewFlagSet("verify-published", flag.ContinueOnError)
	repository := fs.String("repository", "", "shared add-on registry and repository")
	config := fs.String("config", "", "platform matrix file of the add-on")
	version := fs.String("version", "", "release version; the index is <id>-<version> and the signer ref is refs/tags/v<version>")
	revision := fs.String("revision", "", "commit of the release tag")
	ownerRepo := fs.String("owner-repo", "", "GitHub owner/repository that ran the workflow")
	if err := fs.Parse(args); err != nil {
		return err
	}
	repo, err := parseRepository(*repository)
	if err != nil {
		return err
	}
	cfg, err := loadPlatforms(*config)
	if err != nil {
		return err
	}
	if !versionRE.MatchString(*version) {
		return fmt.Errorf("--version %q is not a release version", *version)
	}
	if !shaRE.MatchString(*revision) {
		return errors.New("--revision must be the 40 hex characters of the release commit")
	}
	identity, err := signerIdentity(*ownerRepo, "refs/tags/v"+*version)
	if err != nil {
		return err
	}
	regTag, err := addonTag(cfg.Addon, *version)
	if err != nil {
		return err
	}
	tag := repo.Tag(regTag)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	digest, im, err := readPublishedIndex(ctx, tag, &merged{Version: *version, Revision: *revision}, cfg)
	if err != nil {
		if isNotFound(err) {
			return fail("E_NOT_PUBLISHED", "%s is not published: the tag push run of v%s has not published this add-on", tag, *version)
		}
		return fmt.Errorf("%s: %w", tag, err)
	}
	refs := []string{repo.Digest(digest.String()).String()}
	var children []string
	for _, c := range im.Manifests {
		children = append(children, c.Digest.String())
		refs = append(refs, repo.Digest(c.Digest.String()).String())
	}
	for _, ref := range refs {
		if err := verifySignature(ctx, ref, identity); err != nil {
			return fail("E_SIGNATURE", "%s (%s) is not signed by %s: %v", ref, tag, identity, err)
		}
	}
	_, _ = fmt.Fprintf(stdout, "digest=%s\nreference=%s@%s\nchildren=%s\nidentity=%s\n", digest, repo.Name(), digest, strings.Join(children, ","), identity)
	return nil
}
