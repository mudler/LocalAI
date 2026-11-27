// Package storetest is the contract suite that every store.Store passes. Run
// registers Ginkgo specs, so call it inside a Describe of the implementation's
// test package.
package storetest

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/plugins/store"
)

// NewStore returns an empty store. Each spec calls it once; clean up with
// DeferCleanup.
type NewStore func() store.Store

var base = time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)

func digest(c byte) string {
	b := make([]byte, 64)
	for i := range b {
		b[i] = c
	}
	return "sha256:" + string(b)
}

func pkg(d string, created time.Time) store.Package {
	return store.Package{
		Digest: d, Name: "fmt", Version: "1.0.0", Description: "Formats code",
		MarketplaceID: "tools", Entry: "fmt", SourceSHA: "0123456789abcdef0123456789abcdef01234567",
		SizeBytes: 12, FileCount: 1,
		Files:       []store.File{{Path: "skills/fmt/SKILL.md", Size: 12, Mode: "0644", SHA256: "ab"}},
		Model:       json.RawMessage(`{"name":"fmt"}`),
		CodeReasons: []string{}, CodeFiles: []string{},
		Skipped:   []store.Skipped{{Component: "monitor", Name: "watch", Reason: "unsupported-component"}},
		CreatedAt: created,
	}
}

func install(id, pluginID, scope, owner, d string) store.Install {
	return store.Install{
		ID: id, PluginID: pluginID, Scope: scope, Owner: owner, Digest: d,
		SourceSHA: "0123456789abcdef0123456789abcdef01234567", State: store.StateInstalled,
		InstalledBy: "admin", CreatedAt: base, UpdatedAt: base,
	}
}

// create writes a package and an install in one transaction, as an install
// job does.
func create(ctx context.Context, s store.Store, in store.Install) error {
	return s.InstallTx(ctx, func(tx store.Tx) error {
		if err := tx.PutPackage(pkg(in.Digest, base)); err != nil {
			return err
		}
		return tx.CreateInstall(in)
	})
}

// Run registers the specs that check the store.
func Run(newStore NewStore) {
	ctx := context.Background()

	Describe("marketplaces", func() {
		It("saves, lists and keeps the fields", func() {
			s := newStore()
			m := store.Marketplace{ID: "tools", Name: "Tools", Kind: store.KindGitHub, Location: "owner/tools",
				Ref: "main", Enabled: true, HeadSHA: "abc", LastError: "", CreatedBy: "admin",
				RefreshedAt: base, CreatedAt: base, UpdatedAt: base}
			Expect(s.SaveMarketplace(ctx, m)).To(Succeed())
			got, err := s.Marketplaces(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(HaveLen(1))
			Expect(got[0].Location).To(Equal("owner/tools"))
			Expect(got[0].Enabled).To(BeTrue())
			Expect(got[0].RefreshedAt.Equal(base)).To(BeTrue())

			m.LastError = "fetch-failed"
			m.CreatedAt = time.Time{}
			Expect(s.SaveMarketplace(ctx, m)).To(Succeed())
			got, _ = s.Marketplaces(ctx)
			Expect(got).To(HaveLen(1))
			Expect(got[0].LastError).To(Equal("fetch-failed"))
			Expect(got[0].CreatedAt.Equal(base)).To(BeTrue(), "an update must keep the creation time")
		})

		It("refuses an ID that is not a marketplace ID", func() {
			s := newStore()
			for _, id := range []string{"", "Tools", "1tools", "tools_x", "a/b"} {
				err := s.SaveMarketplace(ctx, store.Marketplace{ID: id, Kind: store.KindURL})
				Expect(errors.Is(err, store.ErrInvalid)).To(BeTrue(), "id %q: %v", id, err)
			}
		})

		It("keeps a removed default removed when the default is saved again", func() {
			s := newStore()
			def := store.Marketplace{ID: "localai", Name: "LocalAI", Kind: store.KindURL,
				Location: "https://example.test/marketplace.json", Enabled: true, Default: true}
			Expect(s.SaveMarketplace(ctx, def)).To(Succeed())
			Expect(s.DeleteMarketplace(ctx, "localai")).To(Succeed())

			got, _ := s.Marketplaces(ctx)
			Expect(got).To(HaveLen(1), "a removed default keeps a tombstone")
			Expect(got[0].Removed).To(BeTrue())
			Expect(got[0].Enabled).To(BeFalse())

			// Seeding at the next start saves the shipped default again.
			Expect(s.SaveMarketplace(ctx, def)).To(Succeed())
			got, _ = s.Marketplaces(ctx)
			Expect(got).To(HaveLen(1))
			Expect(got[0].Removed).To(BeTrue(), "the tombstone was lost")
			Expect(got[0].Enabled).To(BeFalse())
		})

		It("deletes a marketplace that is not a default, with its snapshot", func() {
			s := newStore()
			Expect(s.SaveMarketplace(ctx, store.Marketplace{ID: "mine", Kind: store.KindGit, Location: "https://example.test/x.git"})).To(Succeed())
			Expect(s.SaveSnapshot(ctx, store.Snapshot{MarketplaceID: "mine", Body: []byte("{}"), FetchedAt: base})).To(Succeed())
			Expect(s.DeleteMarketplace(ctx, "mine")).To(Succeed())
			got, _ := s.Marketplaces(ctx)
			Expect(got).To(BeEmpty())
			_, err := s.Snapshot(ctx, "mine")
			Expect(errors.Is(err, store.ErrNotFound)).To(BeTrue())
			Expect(errors.Is(s.DeleteMarketplace(ctx, "mine"), store.ErrNotFound)).To(BeTrue())
		})
	})

	Describe("snapshots", func() {
		It("round-trips and replaces", func() {
			s := newStore()
			_, err := s.Snapshot(ctx, "tools")
			Expect(errors.Is(err, store.ErrNotFound)).To(BeTrue())
			strict := false
			snap := store.Snapshot{MarketplaceID: "tools", SHA: "abc", Body: []byte(`{"name":"tools"}`), BodySHA256: "beef",
				Entries: []store.Entry{{Name: "fmt", Source: json.RawMessage(`{"source":"github","repo":"o/r"}`), Description: "d",
					Version: "1", Category: "dev", Tags: []string{"go"}, Homepage: "https://example.test", Author: "a",
					Strict: &strict, Supported: true}},
				Skipped:   []store.Skipped{{Component: "plugin", Name: "bad", Reason: "invalid"}},
				FetchedAt: base}
			Expect(s.SaveSnapshot(ctx, snap)).To(Succeed())
			got, err := s.Snapshot(ctx, "tools")
			Expect(err).NotTo(HaveOccurred())
			Expect(string(got.Body)).To(Equal(`{"name":"tools"}`))
			Expect(got.Entries).To(HaveLen(1))
			Expect(got.Entries[0].Strict).NotTo(BeNil())
			Expect(*got.Entries[0].Strict).To(BeFalse())
			Expect(string(got.Entries[0].Source)).To(MatchJSON(`{"source":"github","repo":"o/r"}`))
			Expect(got.Skipped).To(HaveLen(1))

			snap.SHA, snap.Entries = "def", nil
			Expect(s.SaveSnapshot(ctx, snap)).To(Succeed())
			got, _ = s.Snapshot(ctx, "tools")
			Expect(got.SHA).To(Equal("def"))
			Expect(got.Entries).To(BeEmpty())
		})

		It("refuses a body over 1 MiB", func() {
			s := newStore()
			err := s.SaveSnapshot(ctx, store.Snapshot{MarketplaceID: "tools", Body: make([]byte, store.MaxSnapshotBody+1)})
			Expect(errors.Is(err, store.ErrInvalid)).To(BeTrue())
		})
	})

	Describe("packages", func() {
		It("inserts once and ignores a second insert of the same digest", func() {
			s := newStore()
			p := pkg(digest('a'), base)
			Expect(s.PutPackage(ctx, p)).To(Succeed())
			p2 := p
			p2.Name = "changed"
			Expect(s.PutPackage(ctx, p2)).To(Succeed())
			got, err := s.Package(ctx, p.Digest)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Name).To(Equal("fmt"), "a package is immutable")
			Expect(got.Files).To(Equal(p.Files))
			Expect(string(got.Model)).To(MatchJSON(`{"name":"fmt"}`))
			Expect(got.Skipped).To(Equal(p.Skipped))
			_, err = s.Package(ctx, digest('b'))
			Expect(errors.Is(err, store.ErrNotFound)).To(BeTrue())
		})

		It("lists unreferenced packages and skips referenced and young ones", func() {
			s := newStore()
			old := base.Add(-48 * time.Hour)
			for _, c := range []byte{'a', 'b', 'c', 'd', 'e'} {
				Expect(s.PutPackage(ctx, pkg(digest(c), old))).To(Succeed())
			}
			Expect(s.PutPackage(ctx, pkg(digest('y'), base))).To(Succeed()) // young
			in := install("i1", "fmt@tools", store.ScopeInstance, "", digest('a'))
			in.PreviousDigest = digest('b')
			in.UpdateDigest = digest('c')
			Expect(s.InstallTx(ctx, func(tx store.Tx) error { return tx.CreateInstall(in) })).To(Succeed())

			got, err := s.UnreferencedPackages(ctx, base.Add(-24*time.Hour))
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(ConsistOf(digest('d'), digest('e')))

			ok, err := s.DeletePackage(ctx, digest('a'))
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeFalse(), "a referenced package must stay")
			ok, err = s.DeletePackage(ctx, digest('d'))
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeTrue())
			_, err = s.Package(ctx, digest('d'))
			Expect(errors.Is(err, store.ErrNotFound)).To(BeTrue())
		})
	})

	Describe("installs", func() {
		It("creates, filters, updates and deletes", func() {
			s := newStore()
			Expect(create(ctx, s, install("i1", "fmt@tools", store.ScopeInstance, "", digest('a')))).To(Succeed())
			Expect(create(ctx, s, install("i2", "lint@tools", store.ScopeUser, "u1", digest('b')))).To(Succeed())
			Expect(create(ctx, s, install("i3", "lint@tools", store.ScopeUser, "u2", digest('b')))).To(Succeed())

			all, err := s.Installs(ctx, store.InstallFilter{})
			Expect(err).NotTo(HaveOccurred())
			Expect(all).To(HaveLen(3))
			got, _ := s.Installs(ctx, store.InstallFilter{ID: "i2"})
			Expect(got).To(HaveLen(1))
			Expect(got[0].Owner).To(Equal("u1"))
			got, _ = s.Installs(ctx, store.InstallFilter{PluginID: "lint@tools"})
			Expect(got).To(HaveLen(2))
			got, _ = s.Installs(ctx, store.InstallFilter{Scope: store.ScopeUser, Owner: "u2"})
			Expect(got).To(HaveLen(1))
			Expect(got[0].ID).To(Equal("i3"))
			got, _ = s.Installs(ctx, store.InstallFilter{VisibleTo: "u1"})
			ids := []string{}
			for _, g := range got {
				ids = append(ids, g.ID)
			}
			Expect(ids).To(ConsistOf("i1", "i2"), "a user sees instance installs and their own")

			Expect(s.InstallTx(ctx, func(tx store.Tx) error {
				in, err := tx.Installs(store.InstallFilter{ID: "i1"})
				if err != nil {
					return err
				}
				in[0].State, in[0].UpdateSHA = store.StateUpdateAvailable, "fedcba9876543210fedcba9876543210fedcba98"
				return tx.UpdateInstall(in[0])
			})).To(Succeed())
			got, _ = s.Installs(ctx, store.InstallFilter{ID: "i1"})
			Expect(got[0].State).To(Equal(store.StateUpdateAvailable))
			Expect(got[0].CreatedAt.Equal(base)).To(BeTrue())

			Expect(s.SaveUserState(ctx, store.UserState{Principal: "u1", InstallID: "i1", Enabled: true})).To(Succeed())
			Expect(s.InstallTx(ctx, func(tx store.Tx) error { return tx.DeleteInstall("i1") })).To(Succeed())
			got, _ = s.Installs(ctx, store.InstallFilter{ID: "i1"})
			Expect(got).To(BeEmpty())
			_, err = s.UserState(ctx, "u1", "i1")
			Expect(errors.Is(err, store.ErrNotFound)).To(BeTrue(), "removing an install removes its user state")

			err = s.InstallTx(ctx, func(tx store.Tx) error { return tx.DeleteInstall("i1") })
			Expect(errors.Is(err, store.ErrNotFound)).To(BeTrue())
			err = s.InstallTx(ctx, func(tx store.Tx) error {
				return tx.UpdateInstall(install("nope", "x@y", store.ScopeInstance, "", digest('a')))
			})
			Expect(errors.Is(err, store.ErrNotFound)).To(BeTrue())
		})

		It("keeps (plugin_id, scope, owner) unique", func() {
			s := newStore()
			Expect(create(ctx, s, install("i1", "lint@tools", store.ScopeUser, "u1", digest('a')))).To(Succeed())
			err := create(ctx, s, install("i2", "lint@tools", store.ScopeUser, "u1", digest('b')))
			Expect(errors.Is(err, store.ErrConflict)).To(BeTrue(), "got %v", err)
			Expect(create(ctx, s, install("i3", "lint@tools", store.ScopeUser, "u2", digest('b')))).To(Succeed())

			Expect(create(ctx, s, install("i4", "fmt@tools", store.ScopeInstance, "", digest('a')))).To(Succeed())
			err = create(ctx, s, install("i5", "fmt@tools", store.ScopeInstance, "", digest('a')))
			Expect(errors.Is(err, store.ErrConflict)).To(BeTrue(), "got %v", err)
		})

		It("refuses a user install of a plugin installed for everyone and names the install", func() {
			s := newStore()
			Expect(create(ctx, s, install("inst", "fmt@tools", store.ScopeInstance, "", digest('a')))).To(Succeed())
			err := create(ctx, s, install("mine", "fmt@tools", store.ScopeUser, "u1", digest('a')))
			Expect(errors.Is(err, store.ErrConflict)).To(BeTrue(), "got %v", err)
			var ii *store.InstanceInstalledError
			Expect(errors.As(err, &ii)).To(BeTrue(), "got %v", err)
			Expect(ii.InstallID).To(Equal("inst"))
		})

		It("refuses an install with a bad scope or owner", func() {
			s := newStore()
			for _, in := range []store.Install{
				install("a", "fmt@tools", "group", "", digest('a')),
				install("b", "fmt@tools", store.ScopeUser, "", digest('a')),
				install("c", "fmt@tools", store.ScopeInstance, "u1", digest('a')),
				install("", "fmt@tools", store.ScopeInstance, "", digest('a')),
			} {
				err := s.InstallTx(ctx, func(tx store.Tx) error { return tx.CreateInstall(in) })
				Expect(errors.Is(err, store.ErrInvalid)).To(BeTrue(), "install %+v: %v", in, err)
			}
		})

		It("writes nothing when the function of a transaction fails", func() {
			s := newStore()
			boom := errors.New("boom")
			err := s.InstallTx(ctx, func(tx store.Tx) error {
				if err := tx.PutPackage(pkg(digest('a'), base)); err != nil {
					return err
				}
				if err := tx.CreateInstall(install("i1", "fmt@tools", store.ScopeInstance, "", digest('a'))); err != nil {
					return err
				}
				return boom
			})
			Expect(errors.Is(err, boom)).To(BeTrue())
			got, _ := s.Installs(ctx, store.InstallFilter{})
			Expect(got).To(BeEmpty())
			_, err = s.Package(ctx, digest('a'))
			Expect(errors.Is(err, store.ErrNotFound)).To(BeTrue())
		})

		It("gives one install when two transactions race for one plugin ID", func() {
			s := newStore()
			var wg sync.WaitGroup
			errs := make([]error, 2)
			for i := range errs {
				wg.Add(1)
				go func(i int) {
					defer GinkgoRecover()
					defer wg.Done()
					errs[i] = create(ctx, s, install([]string{"x1", "x2"}[i], "fmt@tools", store.ScopeInstance, "", digest('a')))
				}(i)
			}
			wg.Wait()
			got, _ := s.Installs(ctx, store.InstallFilter{PluginID: "fmt@tools"})
			Expect(got).To(HaveLen(1))
			failed := 0
			for _, e := range errs {
				if e != nil {
					Expect(errors.Is(e, store.ErrConflict)).To(BeTrue(), "got %v", e)
					failed++
				}
			}
			Expect(failed).To(Equal(1))
		})
	})

	Describe("user state", func() {
		It("round-trips, needs an install, and lists by principal", func() {
			s := newStore()
			err := s.SaveUserState(ctx, store.UserState{Principal: "u1", InstallID: "missing", Enabled: true})
			Expect(errors.Is(err, store.ErrNotFound)).To(BeTrue())

			Expect(create(ctx, s, install("i1", "fmt@tools", store.ScopeInstance, "", digest('a')))).To(Succeed())
			Expect(create(ctx, s, install("i2", "lint@tools", store.ScopeInstance, "", digest('b')))).To(Succeed())
			us := store.UserState{Principal: "u1", InstallID: "i1", Enabled: true,
				Config: json.RawMessage(`{"level":"high"}`), Secrets: []byte{1, 2, 3}, UpdatedAt: base}
			Expect(s.SaveUserState(ctx, us)).To(Succeed())
			got, err := s.UserState(ctx, "u1", "i1")
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Enabled).To(BeTrue())
			Expect(string(got.Config)).To(MatchJSON(`{"level":"high"}`))
			Expect(got.Secrets).To(Equal([]byte{1, 2, 3}))

			us.Enabled = false
			Expect(s.SaveUserState(ctx, us)).To(Succeed())
			got, _ = s.UserState(ctx, "u1", "i1")
			Expect(got.Enabled).To(BeFalse())

			Expect(s.SaveUserState(ctx, store.UserState{Principal: "u1", InstallID: "i2", Enabled: true})).To(Succeed())
			Expect(s.SaveUserState(ctx, store.UserState{Principal: "u2", InstallID: "i2", Enabled: true})).To(Succeed())
			list, err := s.UserStates(ctx, "u1")
			Expect(err).NotTo(HaveOccurred())
			Expect(list).To(HaveLen(2))
			_, err = s.UserState(ctx, "u3", "i1")
			Expect(errors.Is(err, store.ErrNotFound)).To(BeTrue())
		})

		It("marks every user row of an install, and only that install", func() {
			s := newStore()
			Expect(create(ctx, s, install("i1", "fmt@tools", store.ScopeInstance, "", digest('a')))).To(Succeed())
			Expect(create(ctx, s, install("i2", "lint@tools", store.ScopeInstance, "", digest('b')))).To(Succeed())
			for _, p := range []string{"u1", "u2", "u3"} {
				Expect(s.SaveUserState(ctx, store.UserState{Principal: p, InstallID: "i1", Enabled: true})).To(Succeed())
			}
			Expect(s.SaveUserState(ctx, store.UserState{Principal: "u1", InstallID: "i2", Enabled: true})).To(Succeed())

			Expect(s.MarkNeedsReenable(ctx, "i1")).To(Succeed())
			for _, p := range []string{"u1", "u2", "u3"} {
				got, err := s.UserState(ctx, p, "i1")
				Expect(err).NotTo(HaveOccurred())
				Expect(got.NeedsReenable).To(BeTrue(), "user %s", p)
				Expect(got.Enabled).To(BeTrue(), "the mark keeps the user's choice")
			}
			got, _ := s.UserState(ctx, "u1", "i2")
			Expect(got.NeedsReenable).To(BeFalse())

			// The same mark inside an update transaction.
			Expect(s.InstallTx(ctx, func(tx store.Tx) error { return tx.MarkNeedsReenable("i2") })).To(Succeed())
			got, _ = s.UserState(ctx, "u1", "i2")
			Expect(got.NeedsReenable).To(BeTrue())
		})

		It("saves a user state inside a transaction, rolled back with it", func() {
			s := newStore()
			Expect(create(ctx, s, install("i1", "fmt@tools", store.ScopeInstance, "", digest('a')))).To(Succeed())
			Expect(s.InstallTx(ctx, func(tx store.Tx) error {
				return tx.SaveUserState(store.UserState{Principal: "u1", InstallID: "i1", Enabled: true})
			})).To(Succeed())
			got, err := s.UserState(ctx, "u1", "i1")
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Enabled).To(BeTrue())

			boom := errors.New("boom")
			Expect(s.InstallTx(ctx, func(tx store.Tx) error {
				Expect(tx.SaveUserState(store.UserState{Principal: "u2", InstallID: "i1", Enabled: true})).To(Succeed())
				return boom
			})).To(MatchError(boom))
			_, err = s.UserState(ctx, "u2", "i1")
			Expect(errors.Is(err, store.ErrNotFound)).To(BeTrue())

			err = s.InstallTx(ctx, func(tx store.Tx) error {
				return tx.SaveUserState(store.UserState{Principal: "u1", InstallID: "missing"})
			})
			Expect(errors.Is(err, store.ErrNotFound)).To(BeTrue())
		})
	})

	Describe("inspections", func() {
		It("keeps the last inspection of each plugin ID", func() {
			s := newStore()
			Expect(s.SaveInspection(ctx, store.Inspection{})).To(MatchError(store.ErrInvalid))
			first := store.Inspection{PluginID: "fmt@tools", SHA: "a", Digest: digest('a'), RunsCode: false,
				CodeReasons: []string{}, Hosts: []string{"mcp.example"}, Model: []byte(`{"name":"fmt"}`), CheckedAt: base}
			Expect(s.SaveInspection(ctx, first)).To(Succeed())
			Expect(s.SaveInspection(ctx, store.Inspection{PluginID: "lint@tools", SHA: "c", CheckedAt: base})).To(Succeed())
			second := first
			second.SHA, second.RunsCode, second.CodeReasons, second.CheckedAt = "b", true, []string{"hook-command"}, base.Add(time.Hour)
			Expect(s.SaveInspection(ctx, second)).To(Succeed())
			list, err := s.Inspections(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(list).To(HaveLen(2))
			Expect(list[0].PluginID).To(Equal("fmt@tools"))
			Expect(list[0].SHA).To(Equal("b"))
			Expect(list[0].RunsCode).To(BeTrue())
			Expect(list[0].CodeReasons).To(Equal([]string{"hook-command"}))
			Expect(list[0].Hosts).To(Equal([]string{"mcp.example"}))
			Expect(string(list[0].Model)).To(MatchJSON(`{"name":"fmt"}`))
			Expect(list[0].CheckedAt).To(BeTemporally("==", base.Add(time.Hour)))
		})
	})

	Describe("preview tokens", func() {
		tok := func(id string, expires time.Time) store.Token {
			return store.Token{
				ID: id, Kind: store.TokenInstall, Principal: "u1", PluginID: "fmt@tools",
				SHA: "0123456789abcdef0123456789abcdef01234567", Digest: digest('a'), RunsCode: true,
				CodeFiles: []string{"hooks/hooks.json"}, Review: true, ExpiresAt: expires, CreatedAt: base,
			}
		}

		It("round-trips, and a take is single use", func() {
			s := newStore()
			Expect(s.PutToken(ctx, tok("t1", base.Add(15*time.Minute)))).To(Succeed())
			Expect(errors.Is(s.PutToken(ctx, tok("t1", base.Add(time.Minute))), store.ErrConflict)).To(BeTrue())
			got, err := s.Token(ctx, "t1", base)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Principal).To(Equal("u1"))
			Expect(got.RunsCode).To(BeTrue())
			Expect(got.CodeFiles).To(Equal([]string{"hooks/hooks.json"}))
			Expect(got.Review).To(BeTrue())
			Expect(got.ExpiresAt).To(BeTemporally("==", base.Add(15*time.Minute)))

			got, err = s.TakeToken(ctx, "t1", base)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Digest).To(Equal(digest('a')))
			_, err = s.TakeToken(ctx, "t1", base)
			Expect(errors.Is(err, store.ErrNotFound)).To(BeTrue())
			_, err = s.Token(ctx, "t1", base)
			Expect(errors.Is(err, store.ErrNotFound)).To(BeTrue())
		})

		It("does not give an expired token and deletes the expired ones", func() {
			s := newStore()
			Expect(s.PutToken(ctx, tok("old", base.Add(time.Minute)))).To(Succeed())
			Expect(s.PutToken(ctx, tok("new", base.Add(time.Hour)))).To(Succeed())
			later := base.Add(2 * time.Minute)
			_, err := s.Token(ctx, "old", later)
			Expect(errors.Is(err, store.ErrNotFound)).To(BeTrue())
			_, err = s.TakeToken(ctx, "old", later)
			Expect(errors.Is(err, store.ErrNotFound)).To(BeTrue())
			n, err := s.DeleteExpiredTokens(ctx, later)
			Expect(err).NotTo(HaveOccurred())
			Expect(n).To(Equal(1))
			_, err = s.Token(ctx, "new", later)
			Expect(err).NotTo(HaveOccurred())
		})

		It("refuses an invalid token", func() {
			s := newStore()
			bad := tok("t", base.Add(time.Minute))
			bad.Kind = store.TokenUpdate
			Expect(errors.Is(s.PutToken(ctx, bad), store.ErrInvalid)).To(BeTrue())
			bad = tok("", base.Add(time.Minute))
			Expect(errors.Is(s.PutToken(ctx, bad), store.ErrInvalid)).To(BeTrue())
		})

		It("gives a token to one of two concurrent takes", func() {
			s := newStore()
			Expect(s.PutToken(ctx, tok("t", base.Add(time.Hour)))).To(Succeed())
			var wg sync.WaitGroup
			var mu sync.Mutex
			won := 0
			for range 4 {
				wg.Add(1)
				go func() {
					defer GinkgoRecover()
					defer wg.Done()
					if _, err := s.TakeToken(ctx, "t", base); err == nil {
						mu.Lock()
						won++
						mu.Unlock()
					} else {
						Expect(errors.Is(err, store.ErrNotFound)).To(BeTrue(), "%v", err)
					}
				}()
			}
			wg.Wait()
			Expect(won).To(Equal(1))
		})
	})
}
