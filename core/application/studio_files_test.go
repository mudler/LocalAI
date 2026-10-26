// SPDX-License-Identifier: MIT
//go:build auth

package application

import (
	"context"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Files need a database for the tree and a place for the bytes. Without a data
// path there is neither, and the chats say so.
var _ = Describe("studio files at startup", func() {
	newApp := func(opts ...config.AppOption) *Application {
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		state, err := system.GetSystemState(system.WithModelPath(GinkgoT().TempDir()), system.WithBackendPath(GinkgoT().TempDir()))
		Expect(err).NotTo(HaveOccurred())
		base := []config.AppOption{config.WithContext(ctx), config.WithSystemState(state), config.DisableMetricsEndpoint,
			config.WithDisableLocalAIAssistant(true), config.WithDisableStats(true)}
		app, err := New(append(base, opts...)...)
		if app != nil {
			DeferCleanup(func() { Expect(app.Shutdown()).To(Succeed()) })
		}
		Expect(err).NotTo(HaveOccurred())
		return app
	}

	It("keeps files in a private SQLite file under the data path when there is no database", func() {
		dir := GinkgoT().TempDir()
		app := newApp(config.WithDataPath(dir))
		Expect(app.StudioFiles().Available()).To(BeTrue(), app.StudioFiles().Unavailable())
		Expect(filepath.Join(dir, "chats", "workspace.db")).To(BeAnExistingFile())
		Expect(filepath.Join(dir, "chats", "blobs")).To(BeADirectory())
	})

	It("shares the auth database when there is one", func() {
		dir := GinkgoT().TempDir()
		app := newApp(config.WithDataPath(dir), config.WithAuthEnabled(true),
			config.WithAuthDatabaseURL(filepath.Join(GinkgoT().TempDir(), "auth.db")))
		Expect(app.StudioFiles().Available()).To(BeTrue(), app.StudioFiles().Unavailable())
		Expect(filepath.Join(dir, "chats", "workspace.db")).NotTo(BeAnExistingFile())
		Expect(app.authDB.Migrator().HasTable("studio_ws_nodes")).To(BeTrue())
	})

	It("turns files off, with a reason, when there is no data path", func() {
		app := newApp(config.WithDataPath(""))
		Expect(app.StudioFiles().Available()).To(BeFalse())
		Expect(app.StudioFiles().Unavailable()).To(ContainSubstring("data path"))
		_, err := os.Stat("chats")
		Expect(err).To(HaveOccurred()) // nothing was created in the working directory
	})
})
