// SPDX-License-Identifier: MIT
//go:build auth

// Package oauth_test is the conformance suite of the OAuth logins of add-ons.
// It runs the real application (accounts, the add-on owner, the catalog, the
// supervisor, the connectors and the HTTP routes) against fake OAuth services,
// with one test-only add-on executable packaged once per service. Every spec
// runs the same steps for every service, so a host that needed code of its
// own for one service fails for the others.
package oauth_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mudler/LocalAI/addons/gdrive/gdrivetest"
	"github.com/mudler/LocalAI/addons/gmail/gmailtest"
	"github.com/mudler/xlog"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fixtureExecutable is the test-only add-on, built once for the suite.
var fixtureExecutable []byte

// drive is the fake Drive API and the shipped Google Drive add-on built to
// talk to it. The address of the API is fixed in the executable when it is
// linked, so the fake starts before the build and serves every spec of the
// suite; a spec resets it.
var (
	drive           *gdrivetest.Server
	driveExecutable []byte
)

// mailbox is the fake Gmail API and the shipped Gmail add-on built to talk
// to it, set up in the same way.
var (
	mailbox         *gmailtest.Server
	gmailExecutable []byte
)

func TestOAuthConformance(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Add-on OAuth conformance suite")
}

var _ = BeforeSuite(func() {
	xlog.SetLogger(xlog.NewLoggerWithHandler(suiteLogHandler, xlog.LogLevelDebug))
	dir, err := os.MkdirTemp("", "oauth-fixture-")
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = os.RemoveAll(dir) })
	bin := filepath.Join(dir, "oauthfixture")
	out, err := exec.Command("go", "build", "-o", bin, "../../../addons/internal/oauthfixture").CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), string(out))
	fixtureExecutable, err = os.ReadFile(bin) // #nosec G304 -- the suite reads the file it just built
	Expect(err).NotTo(HaveOccurred())

	drive = gdrivetest.New()
	DeferCleanup(drive.Close)
	driveBin := filepath.Join(dir, "gdrive")
	out, err = exec.Command("go", "build", "-ldflags", "-X github.com/mudler/LocalAI/addons/gdrive/internal/driveapi.DefaultBase="+drive.URL,
		"-o", driveBin, "../../../addons/gdrive/cmd/gdrive").CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), string(out))
	driveExecutable, err = os.ReadFile(driveBin) // #nosec G304 -- the suite reads the file it just built
	Expect(err).NotTo(HaveOccurred())

	mailbox = gmailtest.New()
	DeferCleanup(mailbox.Close)
	gmailBin := filepath.Join(dir, "gmail")
	out, err = exec.Command("go", "build", "-ldflags", "-X github.com/mudler/LocalAI/addons/gmail/internal/gmailapi.DefaultBase="+mailbox.URL,
		"-o", gmailBin, "../../../addons/gmail/cmd/gmail").CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), string(out))
	gmailExecutable, err = os.ReadFile(gmailBin) // #nosec G304 -- the suite reads the file it just built
	Expect(err).NotTo(HaveOccurred())
})
