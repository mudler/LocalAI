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
