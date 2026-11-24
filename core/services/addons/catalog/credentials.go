// SPDX-License-Identifier: MIT
package catalog

import (
	"context"
	"github.com/mudler/LocalAI/pkg/credentials"
	"net/url"
)

type Credential = credentials.Credential

// CredentialBinding is trusted owner configuration, never a request DTO.
// HTTP catalog references are independent of the installer's OCI credential store.
type CredentialBinding struct {
	Origin string
	Store  *credentials.Store
}
type CredentialResolver struct {
	References map[string]CredentialBinding `json:"-"`
}

func (r CredentialResolver) Resolve(ctx context.Context, reference, origin string) (Credential, error) {
	if err := ctx.Err(); err != nil {
		return Credential{}, err
	}
	b, ok := r.References[reference]
	if !ok || b.Store == nil {
		return Credential{}, ErrCredential
	}
	requested, e := safeURL(origin)
	if e != nil {
		return Credential{}, ErrCredential
	}
	bound, e := safeURL(b.Origin)
	if e != nil || requested.Scheme != bound.Scheme || requested.Host != bound.Host {
		return Credential{}, ErrCredential
	}
	c, ok := b.Store.Match(origin)
	if !ok {
		return Credential{}, ErrCredential
	}
	return c, nil
}
func safeURL(raw string) (*url.URL, error) {
	u, e := url.Parse(raw)
	if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, ErrInvalid
	}
	return u, nil
}
