// SPDX-License-Identifier: MIT
package application

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/core/services/addons/recovery"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type forgetPort struct {
	recovery.PolicyPort
	forgotten []string
	err       error
}

func (f *forgetPort) Forget(_ context.Context, id string) (int, error) {
	f.forgotten = append(f.forgotten, id)
	return 3, f.err
}

var _ = Describe("the purge of the OAuth client of an add-on", func() {
	var (
		ctx   = context.Background()
		store connectors.Store
		inner *forgetPort
		port  oauthPurgePort
	)

	BeforeEach(func() {
		var err error
		store, err = connectors.NewJSON(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		for _, id := range []string{"provider-a", "provider-b"} {
			Expect(store.PutOAuthClient(ctx, connectors.OAuthClient{AddonID: id, ClientID: "client-" + id})).To(Succeed())
		}
		inner = &forgetPort{}
		port = oauthPurgePort{PolicyPort: inner, store: func() connectors.Store { return store }}
	})

	It("removes the approved client of the purged add-on and no other", func() {
		n, err := port.Forget(ctx, "provider-a")
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(3), "the count of the policy records")
		Expect(inner.forgotten).To(Equal([]string{"provider-a"}))
		_, err = store.GetOAuthClient(ctx, "provider-a")
		Expect(err).To(MatchError(connectors.ErrNotFound))
		_, err = store.GetOAuthClient(ctx, "provider-b")
		Expect(err).NotTo(HaveOccurred())
	})

	It("keeps the client when the records could not be forgotten", func() {
		inner.err = errors.New("store down")
		_, err := port.Forget(ctx, "provider-a")
		Expect(err).To(HaveOccurred())
		_, err = store.GetOAuthClient(ctx, "provider-a")
		Expect(err).NotTo(HaveOccurred(), "the purge repeats from its journal")
	})

	It("purges when the studio store is not open", func() {
		port.store = func() connectors.Store { return nil }
		_, err := port.Forget(ctx, "provider-a")
		Expect(err).NotTo(HaveOccurred())
	})

	It("is deleted only by the purge: no uninstall path removes an approved client", func() {
		// An uninstall keeps retained data, the client included, so a
		// reinstall needs no new approval. The stores implement the delete and
		// the purge port calls it; nothing else may.
		allowed := map[string]bool{
			"application/addon_oauth_purge.go":                   true,
			"services/studio/connectors/oauth.go":                true,
			"services/studio/connectors/json_oauth.go":           true,
			"services/studio/connectors/gorm_oauth.go":           true,
			"services/studio/connectors/connectorstest/oauth.go": true,
		}
		err := filepath.WalkDir("..", func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			rel := filepath.ToSlash(strings.TrimPrefix(path, "../"))
			if strings.Contains(string(b), "DeleteOAuthClient") {
				Expect(allowed).To(HaveKey(rel), "%s deletes an approved OAuth client", rel)
			}
			return nil
		})
		Expect(err).NotTo(HaveOccurred())
	})
})
