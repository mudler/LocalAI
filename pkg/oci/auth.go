package oci

import (
	"errors"
	"net/http"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"

	"github.com/mudler/LocalAI/pkg/credentials"
)

// wrapAuthError turns a registry 401/403 into a credentials.AuthError so the
// operator is told whether a rule was sent, instead of a bare UNAUTHORIZED.
func wrapAuthError(imageRef string, err error) error {
	var terr *transport.Error
	if !errors.As(err, &terr) {
		return err
	}
	if terr.StatusCode != http.StatusUnauthorized && terr.StatusCode != http.StatusForbidden {
		return err
	}
	matchURL := ""
	if ref, perr := name.ParseReference(imageRef); perr == nil {
		matchURL = credentials.RegistryURL(ref.Context())
	}
	return credentials.NewRegistryAuthError(matchURL, imageRef, terr.StatusCode, err)
}
