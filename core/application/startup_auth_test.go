// SPDX-License-Identifier: MIT
//go:build auth

package application

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const startupAuthCaseEnv = "LOCALAI_STARTUP_AUTH_TEST_CASE"
const startupAuthRootEnv = "LOCALAI_STARTUP_AUTH_TEST_ROOT"

// Shutdown does not join every background producer. Keep the fixture in the
// parent: child exit, not context cancellation, is the barrier before TempDir
// cleanup. Running only this helper also avoids starting the Ginkgo suite again.
func runStartupAuthCase(id string) {
	executable, err := os.Executable()
	Expect(err).NotTo(HaveOccurred())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestStartupAuthProcess$", "-test.count=1", "-test.v")
	cmd.Env = append(os.Environ(), startupAuthCaseEnv+"="+id, startupAuthRootEnv+"="+GinkgoT().TempDir())
	output := &warningOutput{}
	cmd.Stdout, cmd.Stderr = output, output
	err = cmd.Run()
	Expect(err).NotTo(HaveOccurred(), "startup auth case %s (%v):\n%s", id, ctx.Err(), output.String())
	Expect(output.String()).To(ContainSubstring("startup auth case passed: "+id), "child must execute its assertions")
}

func TestStartupAuthProcess(t *testing.T) {
	id := os.Getenv(startupAuthCaseEnv)
	if id == "" {
		return
	}
	RegisterTestingT(t)
	switch id {
	case "default":
		root := os.Getenv(startupAuthRootEnv)
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
		defer func() { cancel(); Expect(app.Shutdown()).To(Succeed()) }()
		Expect(app.AuthDB()).NotTo(BeNil())
		info, err := os.Stat(data)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()&0o027).To(BeZero(), "directory keeps the 0750 semantics")
		_, err = os.Stat(filepath.Join(data, "database.db"))
		Expect(err).NotTo(HaveOccurred())
	case "explicit":
		root := os.Getenv(startupAuthRootEnv)
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
		defer func() { cancel(); Expect(app.Shutdown()).To(Succeed()) }()
		Expect(app.AuthDB()).NotTo(BeNil())
		_, err = os.Stat(filepath.Join(root, "elsewhere", "auth", "users.db"))
		Expect(err).NotTo(HaveOccurred())
	default:
		Expect(id).To(BeEmpty(), "unknown startup auth case")
	}
	fmt.Println("startup auth case passed: " + id)
}

var _ = Describe("auth database location at first start", func() {
	It("creates a missing data directory before opening the default auth database", func() {
		runStartupAuthCase("default")
	})
	It("creates the parent of an explicit sqlite path outside the data directory", func() {
		runStartupAuthCase("explicit")
	})
})
