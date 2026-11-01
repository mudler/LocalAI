// SPDX-License-Identifier: MIT
package catalog

import (
	"regexp"
	"strings"

	"github.com/mudler/LocalAI/internal"
)

const (
	// OfficialLibraryID is the source ID of the official add-on library.
	OfficialLibraryID = "official"
	// OfficialLibraryURL is the YAML index of the first-party add-ons that a
	// build without a release version reads. It is kept in this repository at
	// gallery/addons/index.yaml and served from the master branch, like the
	// model gallery. Its entries are the mutable development builds.
	OfficialLibraryURL = "https://raw.githubusercontent.com/mudler/LocalAI/master/gallery/addons/index.yaml"
	// officialReleaseURLPrefix and OfficialReleaseAsset name the index of a
	// release: the add-on package workflow attaches it to the GitHub release of
	// each v<semver> tag. Its entries are pinned by digest to the packages that
	// the run of the tag built and signed.
	officialReleaseURLPrefix = "https://github.com/mudler/LocalAI/releases/download/"
	// OfficialReleaseAsset is the file name of the add-on index of a release.
	OfficialReleaseAsset = "addons-index.yaml"
	officialSignerPrefix = "https://github.com/mudler/LocalAI/.github/workflows/addons-package.yml@"
)

// releaseVersionRE matches the version of a tagged build: v<semver>. The
// output of git describe for a commit after a tag (v1.2.3-14-gabc1234) does not
// match, so such a build reads the development library.
var releaseVersionRE = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z]+(\.[0-9A-Za-z-]+)*)?$`)

// OfficialLibrary is the library a standard install registers on its first
// start. A release build (internal.Version is a v<semver> tag) reads the index
// attached to its own release and requires the signature of the add-on package
// workflow run of that tag, so it installs the immutable packages that release
// built. Any other build reads the index on master and requires the signature
// of the run on master. A package that the named run did not build and sign is
// refused.
func OfficialLibrary() Source { return officialLibraryFor(internal.Version) }

func officialLibraryFor(version string) Source {
	url, ref := OfficialLibraryURL, "refs/heads/master"
	if releaseVersionRE.MatchString(version) {
		url, ref = officialReleaseURLPrefix+version+"/"+OfficialReleaseAsset, "refs/tags/"+version
	}
	return Source{
		ID:      OfficialLibraryID,
		Name:    "Official add-ons",
		URL:     url,
		Enabled: true,
		Policy: VerificationPolicy{
			RequireSignature: true,
			Issuer:           "https://token.actions.githubusercontent.com",
			Identity:         officialSignerPrefix + ref,
		},
	}
}

// isOfficialAddonURL reports whether a stored URL is one the shipped library
// has used: the master index or the index of a release. A library whose URL is
// anything else was edited by the admin.
func isOfficialAddonURL(url string) bool {
	if url == OfficialLibraryURL {
		return true
	}
	rest, ok := strings.CutPrefix(url, officialReleaseURLPrefix)
	if !ok {
		return false
	}
	version, asset, ok := strings.Cut(rest, "/")
	return ok && asset == OfficialReleaseAsset && releaseVersionRE.MatchString(version)
}

const (
	// OfficialBackendLibraryID is the source ID of the backend library.
	OfficialBackendLibraryID = "official-backends"
	// OfficialBackendLibraryURL is the YAML index of the backends, kept in this
	// repository at gallery/backends/index.yaml. It is the source of truth;
	// backend/index.yaml is generated from it.
	OfficialBackendLibraryURL = "https://raw.githubusercontent.com/mudler/LocalAI/master/gallery/backends/index.yaml"
)

// OfficialBackendLibrary is the library of the backends of the backend gallery.
// A backend installed through the library route (the catalog and the backend
// library installer) must carry the signature of the backend merge workflow,
// from master or from a tag. A build reads either this library or the backend
// gallery by default, as the build says (see core/library.BackendSource).
func OfficialBackendLibrary() Source {
	return Source{
		ID:      OfficialBackendLibraryID,
		Name:    "Official backend library",
		URL:     OfficialBackendLibraryURL,
		Enabled: true,
		Kind:    KindBackend,
		Policy: VerificationPolicy{
			RequireSignature: true,
			Issuer:           "https://token.actions.githubusercontent.com",
			IdentityRegex:    `^https://github\.com/mudler/LocalAI/\.github/workflows/backend_merge\.yml@refs/(heads/master|tags/.+)$`,
		},
	}
}

const (
	// OfficialModelLibraryID is the source ID of the model library.
	OfficialModelLibraryID = "official-models"
	// OfficialModelLibraryURL is the YAML index of the models, kept in this
	// repository at gallery/models/index.yaml. It is the source of truth;
	// gallery/index.yaml is generated from it.
	OfficialModelLibraryURL = "https://raw.githubusercontent.com/mudler/LocalAI/master/gallery/models/index.yaml"
)

// OfficialModelLibrary is the library of the models of the model gallery. It
// has no signature policy of its own; the sha256 of each model file is checked
// on install.
func OfficialModelLibrary() Source {
	return Source{ID: OfficialModelLibraryID, Name: "Official model library", URL: OfficialModelLibraryURL, Enabled: true, Kind: KindModel}
}
