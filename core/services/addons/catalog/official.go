// SPDX-License-Identifier: MIT
package catalog

const (
	// OfficialLibraryID is the source ID of the official add-on library.
	OfficialLibraryID = "official"
	// OfficialLibraryURL is the YAML index of the first-party add-ons, kept in
	// this repository at gallery/addons/index.yaml and served from the master
	// branch, like the model gallery.
	OfficialLibraryURL = "https://raw.githubusercontent.com/mudler/LocalAI/master/gallery/addons/index.yaml"
)

// OfficialLibrary is the library a standard install registers on its first
// start. It requires the signature of the add-on package workflow that runs on
// master, so a package that master did not build and sign is refused.
func OfficialLibrary() Source {
	return Source{
		ID:      OfficialLibraryID,
		Name:    "Official add-ons",
		URL:     OfficialLibraryURL,
		Enabled: true,
		Policy: VerificationPolicy{
			RequireSignature: true,
			Issuer:           "https://token.actions.githubusercontent.com",
			Identity:         "https://github.com/mudler/LocalAI/.github/workflows/addons-package.yml@refs/heads/master",
		},
	}
}

const (
	// OfficialBackendLibraryID is the source ID of the pilot backend library.
	OfficialBackendLibraryID = "official-backends"
	// OfficialBackendLibraryURL is the YAML index of the pilot backends, kept
	// in this repository at gallery/backends/index.yaml. It is rendered from
	// backend/index.yaml, which stays the source of truth.
	OfficialBackendLibraryURL = "https://raw.githubusercontent.com/mudler/LocalAI/master/gallery/backends/index.yaml"
)

// OfficialBackendLibrary is the pilot library of backends. It requires the
// signature of the backend merge workflow, from master or from a tag, with the
// same signer pattern the backend gallery uses.
func OfficialBackendLibrary() Source {
	return Source{
		ID:      OfficialBackendLibraryID,
		Name:    "Official backends (pilot)",
		URL:     OfficialBackendLibraryURL,
		Enabled: true,
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
