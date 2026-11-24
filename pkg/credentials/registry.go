package credentials

import (
	"context"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/mudler/xlog"
	"oras.land/oras-go/v2/registry"
	"oras.land/oras-go/v2/registry/remote/auth"
	orascreds "oras.land/oras-go/v2/registry/remote/credentials"
)

// RegistryURL is the URL a registry repository is matched as. Keychain lookups
// and auth errors both use it, so a rule that authenticates a pull is also the
// rule an error names.
func RegistryURL(repo name.Repository) string {
	return repo.Registry.Scheme() + "://" + repo.RegistryStr() + "/" + repo.RepositoryStr()
}

type storeKeychain struct{}

func (storeKeychain) Resolve(target authn.Resource) (authn.Authenticator, error) {
	var matchURL string
	switch r := target.(type) {
	case name.Repository:
		matchURL = RegistryURL(r)
	case name.Registry:
		matchURL = r.Scheme() + "://" + r.RegistryStr()
	default:
		matchURL = "https://" + target.RegistryStr()
	}
	c, ok := Default().Match(matchURL)
	if !ok {
		return authn.Anonymous, nil
	}
	secret, err := c.resolve()
	if err != nil {
		return nil, err
	}
	switch c.Kind {
	case KindBasic:
		return &authn.Basic{Username: secret.username, Password: secret.password}, nil
	case KindBearer:
		return &authn.Bearer{Token: secret.bearer}, nil
	}
	xlog.Warn("Ignoring header credential for a container registry; registries accept basic or bearer auth only", "credential", c)
	return authn.Anonymous, nil
}

// Keychain consults the credentials store first and docker config second, so
// hosts that already rely on `docker login` keep working unchanged.
func Keychain() authn.Keychain {
	return authn.NewMultiKeychain(storeKeychain{}, authn.DefaultKeychain)
}

// orasMatchURL binds repository to the host oras asks about. oras passes
// Reference.Host(), which rewrites docker.io to registry-1.docker.io, so hosts
// are compared after the store's folding rather than as string prefixes.
func orasMatchURL(repository, hostport string) string {
	matchURL := "https://" + hostport
	if ref, err := registry.ParseReference(repository); err == nil {
		if normalizeHost("https", ref.Host()) == normalizeHost("https", hostport) {
			return matchURL + "/" + ref.Repository
		}
		return matchURL
	}
	if rest, ok := strings.CutPrefix(repository, hostport+"/"); ok {
		matchURL += "/" + rest
	}
	return matchURL
}

// OrasCredential returns an oras credential func for one repository. oras only
// passes the registry host to the func, so the repository is bound here to
// let repository-scoped rules match.
func OrasCredential(repository string) auth.CredentialFunc {
	return func(ctx context.Context, hostport string) (auth.Credential, error) {
		if c, ok := Default().Match(orasMatchURL(repository, hostport)); ok {
			secret, err := c.resolve()
			if err != nil {
				return auth.EmptyCredential, err
			}
			switch c.Kind {
			case KindBasic:
				return auth.Credential{Username: secret.username, Password: secret.password}, nil
			case KindBearer:
				return auth.Credential{AccessToken: secret.bearer}, nil
			}
		}
		docker, err := orascreds.NewStoreFromDocker(orascreds.StoreOptions{})
		if err != nil {
			// A missing or unreadable docker config means anonymous, the
			// same outcome as before this adapter existed.
			return auth.EmptyCredential, nil
		}
		cred, err := orascreds.Credential(docker)(ctx, hostport)
		if err != nil {
			// A broken credsStore helper would otherwise fail every pull, even
			// of public artifacts that never needed docker config.
			xlog.Debug("Ignoring docker config credentials that cannot be read", "registry", hostport, "error", err)
			return auth.EmptyCredential, nil
		}
		return cred, nil
	}
}
