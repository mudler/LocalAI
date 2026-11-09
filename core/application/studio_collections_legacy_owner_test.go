// SPDX-License-Identifier: MIT
//go:build auth

package application

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/collections"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The legacy owner (LOCALAI_AUTH_LEGACY_OWNER) keeps the root storage area over
// /api/collections; the chats must list and search the same collections for
// that account, and only for that account.
var _ = Describe("the studio collections adapter for the legacy owner", func() {
	var (
		ctx     = context.Background()
		app     *Application
		svc     *collections.Service
		col     studioCollections
		owner   *auth.User
		mallory *auth.User
	)

	BeforeEach(func() {
		appCtx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		state, err := system.GetSystemState(system.WithModelPath(GinkgoT().TempDir()), system.WithBackendPath(GinkgoT().TempDir()))
		Expect(err).NotTo(HaveOccurred())
		app, err = New(config.WithContext(appCtx), config.WithSystemState(state), config.DisableMetricsEndpoint,
			config.WithDisableStats(true))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(app.Shutdown()).To(Succeed()) })

		db, err := auth.InitDB(filepath.Join(GinkgoT().TempDir(), "auth.db"))
		Expect(err).NotTo(HaveOccurred())
		app.authDB = db
		app.applicationConfig.Auth.LegacyOwnerEmail = "owner@example.test"

		owner = &auth.User{ID: "33333333-3333-4333-8333-333333333333", Email: "Owner@Example.test", Name: "owner", Role: auth.RoleAdmin,
			Status: auth.StatusActive, Provider: auth.ProviderLocal}
		// An OIDC identity that reports the owner's address without verification:
		// LocalAI stores the e-mail but grants only the user role.
		mallory = &auth.User{ID: "44444444-4444-4444-8444-444444444444", Email: "owner@example.test", Name: "mallory", Role: auth.RoleUser,
			Status: auth.StatusActive, Provider: auth.ProviderOIDC}
		Expect(db.Create(owner).Error).To(Succeed())
		Expect(db.Create(mallory).Error).To(Succeed())
		Expect(auth.UpdateUserPermissions(db, mallory.ID, auth.PermissionMap{auth.FeatureCollections: true})).To(Succeed())

		cfg := config.NewApplicationConfig()
		cfg.DataPath = GinkgoT().TempDir()
		cfg.Collections.VectorEngine = "chromem"
		cfg.Collections.EmbeddingModel = "embed"
		svc, err = collections.New(cfg, nil, collections.WithHTTPClient(embeddingClient()))
		Expect(err).NotTo(HaveOccurred())
		app.collectionsService.Store(svc)
		col = studioCollections{a: app}

		Expect(svc.CreateForUser("", "before-auth")).To(Succeed())
		_, err = svc.UploadForUser(ctx, "", "before-auth", "notes.md", strings.NewReader("Hold the button for ten seconds."))
		Expect(err).NotTo(HaveOccurred())
	})

	It("lists and searches the root collections in the owner's chats", func() {
		names, err := col.List(ctx, owner.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(names).To(ContainElement("before-auth"))
		hits, err := col.Search(ctx, owner.ID, "before-auth", "how do I reset it", 3)
		Expect(err).NotTo(HaveOccurred())
		Expect(hits).NotTo(BeEmpty())
		Expect(hits[0].Content).To(ContainSubstring("Hold the button"))
	})

	It("keeps an account with the owner's e-mail but no admin role out of the root area", func() {
		names, err := col.List(ctx, mallory.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(names).NotTo(ContainElement("before-auth"))
		_, err = col.Search(ctx, mallory.ID, "before-auth", "reset", 3)
		Expect(err).To(HaveOccurred())
	})

	It("keeps the owner in their own area when no legacy owner is configured", func() {
		app.applicationConfig.Auth.LegacyOwnerEmail = ""
		names, err := col.List(ctx, owner.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(names).NotTo(ContainElement("before-auth"))
	})
})
