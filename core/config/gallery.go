package config

// GalleryVerification declares the keyless-cosign signature policy that
// every OCI backend image fetched from this gallery must satisfy.
//
// Verification is opt-in: galleries without a Verification block install
// backends with no signature check (the downloader logs a warning when
// LOCALAI_REQUIRE_BACKEND_INTEGRITY is unset; that flag turns the warning
// into a hard error).
//
// Identity matching: set Issuer (exact) or IssuerRegex, AND Identity
// (exact) or IdentityRegex. For GitHub Actions keyless signing the
// typical shape is:
//
//	verification:
//	  issuer: "https://token.actions.githubusercontent.com"
//	  identity_regex: "^https://github\\.com/mudler/local-ai-backends/\\.github/workflows/build\\.yaml@refs/heads/master$"
//	  not_before: "2026-05-01T00:00:00Z"
//
// NotBefore is the revocation lever: advance it to invalidate every
// signature produced before a known compromise window. Keyless cosign
// certs are ephemeral so there is no CA-side revocation.
type GalleryVerification struct {
	Issuer        string `json:"issuer,omitempty" yaml:"issuer,omitempty"`
	IssuerRegex   string `json:"issuer_regex,omitempty" yaml:"issuer_regex,omitempty"`
	Identity      string `json:"identity,omitempty" yaml:"identity,omitempty"`
	IdentityRegex string `json:"identity_regex,omitempty" yaml:"identity_regex,omitempty"`

	// NotBefore is an RFC3339 timestamp. Empty disables the time check.
	NotBefore string `json:"not_before,omitempty" yaml:"not_before,omitempty"`
}

type Gallery struct {
	URL          string               `json:"url" yaml:"url"`
	Name         string               `json:"name" yaml:"name"`
	Verification *GalleryVerification `json:"verification,omitempty" yaml:"verification,omitempty"`
}
