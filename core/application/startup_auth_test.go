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

var _ = Describe("auth database location at first start", func() {
	It("creates a missing data directory before opening the default auth database", func() {
		root := GinkgoT().TempDir()
		models, backends := filepath.Join(root, "models"), filepath.Join(root, "backends")
		Expect(os.Mkdir(models, 0750)).To(Succeed())
		Expect(os.Mkdir(backends, 0750)).To(Succeed())
		state, err := system.GetSystemState(system.WithBackendPath(backends), system.WithModelPath(models))
		Expect(err).NotTo(HaveOccurred())
		data := filepath.Join(root, "not", "yet", "created")
		ctx, cancel := context.WithCancel(context.Background())
		var app *Application
		app, err = New(config.WithContext(ctx), config.WithSystemState(state), config.WithDataPath(data),
			config.WithAuthEnabled(true), config.WithAuthDatabaseURL(filepath.Join(data, "database.db")),
			config.WithAuthAPIKeyHMACSecret("secret"))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { cancel(); _ = app.Shutdown() })
		Expect(app.AuthDB()).NotTo(BeNil())
		info, err := os.Stat(data)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()&0o027).To(BeZero(), "directory keeps the 0750 semantics")
		_, err = os.Stat(filepath.Join(data, "database.db"))
		Expect(err).NotTo(HaveOccurred())
	})
	It("creates the parent of an explicit sqlite path outside the data directory", func() {
		root := GinkgoT().TempDir()
		models, backends := filepath.Join(root, "models"), filepath.Join(root, "backends")
		Expect(os.Mkdir(models, 0750)).To(Succeed())
		Expect(os.Mkdir(backends, 0750)).To(Succeed())
		state, err := system.GetSystemState(system.WithBackendPath(backends), system.WithModelPath(models))
		Expect(err).NotTo(HaveOccurred())
		ctx, cancel := context.WithCancel(context.Background())
		var app *Application
		db := filepath.Join(root, "elsewhere", "auth", "users.db?_busy_timeout=2000")
		app, err = New(config.WithContext(ctx), config.WithSystemState(state), config.WithDataPath(filepath.Join(root, "data")),
			config.WithAuthEnabled(true), config.WithAuthDatabaseURL(db), config.WithAuthAPIKeyHMACSecret("secret"))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { cancel(); _ = app.Shutdown() })
		Expect(app.AuthDB()).NotTo(BeNil())
		_, err = os.Stat(filepath.Join(root, "elsewhere", "auth", "users.db"))
		Expect(err).NotTo(HaveOccurred())
	})
})
