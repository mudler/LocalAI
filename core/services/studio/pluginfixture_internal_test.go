package studio

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	. "github.com/onsi/gomega"

	"github.com/mudler/nib/plugin/claudeplugin"

	"github.com/mudler/LocalAI/core/services/plugins"
	"github.com/mudler/LocalAI/core/services/plugins/fetch"
	"github.com/mudler/LocalAI/core/services/plugins/store"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// noFetcher is a plugin fetcher that the Studio specs never reach: they write
// the plugin records and files directly.
type noFetcher struct{}

func (noFetcher) FetchMarketplace(context.Context, string, string, string) ([]byte, string, error) {
	return nil, "", errors.New("no fetch in these specs")
}

func (noFetcher) FetchPlugin(context.Context, fetch.MarketplaceSource, *claudeplugin.Marketplace, *claudeplugin.Entry, string) (*fetch.Fetched, error) {
	return nil, errors.New("no fetch in these specs")
}

func (noFetcher) ResolveRef(context.Context, string, string) (string, error) {
	return "", errors.New("no fetch in these specs")
}

// pluginFixture is a Plugins service on a file store, with a helper that
// installs a plugin without a marketplace.
type pluginFixture struct {
	svc *plugins.Service
	st  store.Store
	f   *files.Service
}

// newPluginFixture gives svc the Plugins service of a fresh file store. The
// package files go into the workspace of f.
func newPluginFixture(svc *Service, f *files.Service) *pluginFixture {
	st, err := store.NewFileStore("")
	Expect(err).NotTo(HaveOccurred())
	ps, err := plugins.New(plugins.Options{Store: st, Fetcher: noFetcher{}, Root: files.PluginsPath})
	Expect(err).NotTo(HaveOccurred())
	svc.d.Plugins = ps
	Expect(f.EnsureShared(context.Background())).To(Succeed())
	return &pluginFixture{svc: ps, st: st, f: f}
}

// install stores a package of model with fileset (paths relative to the
// package) and an install of it for everyone, or for owner when owner is not
// "". It returns the install ID and the digest. The plugin is not enabled for
// anyone yet.
func (x *pluginFixture) install(id string, m plugins.Model, owner string, fileset map[string]string) (string, string) {
	ctx := context.Background()
	sum := sha256.Sum256([]byte(id + "|" + m.Name + "|" + m.Version))
	digest := "sha256:" + hex.EncodeToString(sum[:])
	admin := workspace.Actor{Principal: files.AdminPrincipal}
	for p, body := range fileset {
		_, err := x.f.Store().Put(ctx, admin, files.PluginsPath+"/"+hex.EncodeToString(sum[:])+"/"+p, strings.NewReader(body), workspace.PutOptions{Overwrite: true})
		Expect(err).NotTo(HaveOccurred())
	}
	raw, err := json.Marshal(m)
	Expect(err).NotTo(HaveOccurred())
	Expect(x.st.PutPackage(ctx, store.Package{Digest: digest, Name: m.Name, Version: m.Version, Model: raw, CreatedAt: time.Now()})).To(Succeed())
	in := store.Install{ID: id, PluginID: m.Name + "@test", Scope: store.ScopeInstance, Digest: digest, State: store.StateInstalled}
	if owner != "" {
		in.Scope, in.Owner = store.ScopeUser, owner
	}
	Expect(x.st.InstallTx(ctx, func(tx store.Tx) error { return tx.CreateInstall(in) })).To(Succeed())
	return id, digest
}

// enable turns the install on for principal.
func (x *pluginFixture) enable(principal, installID string, on bool) {
	Expect(x.st.SaveUserState(context.Background(), store.UserState{Principal: principal, InstallID: installID, Enabled: on})).To(Succeed())
}
