//go:build linux || darwin

// SPDX-License-Identifier: MIT
package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/packages"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// These specs check the cause that a failed launch carries for the owner log.
// The text of the error stays the generic ErrRuntime text; Cause is the
// specific one.
var _ = Describe("The cause of a failed launch", func() {
	// scriptFixture is a package whose executable is a shell script.
	scriptFixture := func(body string) packages.PackageReceipt {
		p := fixture("normal", service)
		Expect(os.Remove(filepath.Join(p.Directory, "fixture"))).To(Succeed())
		Expect(os.WriteFile(filepath.Join(p.Directory, "fixture"), []byte("#!/bin/sh\n"+body+"\n"), 0o500)).To(Succeed())
		return p
	}
	launcher := func() *Supervisor {
		if os.Getenv("ADDON_RUNTIME_WSL") == "1" {
			Skip("the launch of an add-on in WSL goes through wsl.exe")
		}
		s := supervisor()
		s.config.ReadyTimeout = 1500 * time.Millisecond
		return s
	}
	// causeOf starts the package and checks what every failure has in common.
	causeOf := func(s *Supervisor, p packages.PackageReceipt) string {
		_, e := s.Start(context.Background(), p, 1)
		Expect(e).To(HaveOccurred())
		Expect(errors.Is(e, ErrRuntime)).To(BeTrue(), "not an ErrRuntime: %v", e)
		Expect(e.Error()).To(Equal(ErrRuntime.Error()), "the error text must stay generic")
		return Cause(e)
	}

	It("names a receipt on disk that differs from the journal", func() {
		p := fixture("normal", service)
		other := p
		other.Digest = "sha256:another"
		Expect(causeOf(launcher(), other)).To(ContainSubstring(StageReceiptMismatch + ": receipt.json in " + filepath.Base(p.Directory) + " differs from the receipt in the journal"))
	})
	It("names a missing receipt", func() {
		p := fixture("normal", service)
		Expect(os.Remove(filepath.Join(p.Directory, "receipt.json"))).To(Succeed())
		Expect(causeOf(launcher(), p)).To(ContainSubstring(StageReceiptUnreadable + ": receipt.json in " + filepath.Base(p.Directory) + " cannot be read: no such file"))
	})
	It("names a manifest that does not fit the host", func() {
		p := fixture("normal", service)
		p.Manifest.Arch = "no-such-arch"
		Expect(causeOf(launcher(), p)).To(ContainSubstring(StageManifestInvalid + ": the manifest does not fit this host"))
	})
	It("names a missing executable and keeps the host path out", func() {
		p := fixture("normal", service)
		Expect(os.Remove(filepath.Join(p.Directory, "fixture"))).To(Succeed())
		cause := causeOf(launcher(), p)
		Expect(cause).To(ContainSubstring(StageExecutableMissing + ": executable fixture in " + filepath.Base(p.Directory)))
		Expect(cause).NotTo(ContainSubstring(filepath.Dir(p.Directory) + "/"))
	})
	It("names a data directory that cannot be made", func() {
		root := filepath.Join(realTempDir(), "data")
		Expect(os.MkdirAll(root, 0o700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(root, "sample"), []byte("a file"), 0o600)).To(Succeed())
		s := launcher()
		s.config.DataRoot = root
		cause := causeOf(s, fixture("normal", service))
		Expect(cause).To(ContainSubstring(StageDataDir + ": the data directory cannot be created"))
		Expect(cause).NotTo(ContainSubstring(root))
	})
	It("names a process that cannot start", func() {
		p := fixture("normal", service)
		Expect(os.Remove(filepath.Join(p.Directory, "fixture"))).To(Succeed())
		Expect(os.WriteFile(filepath.Join(p.Directory, "fixture"), []byte("#!/no/such/interpreter\n"), 0o500)).To(Succeed())
		cause := causeOf(launcher(), p)
		Expect(cause).To(ContainSubstring(StageProcessStart + ": the process did not start: no such file or directory"))
		Expect(cause).NotTo(ContainSubstring(p.Directory))
	})
	It("gives the exit status and the last line of a process that exits at once", func() {
		cause := causeOf(launcher(), scriptFixture("echo first line >&2\necho candidate-boom >&2\nexit 3"))
		Expect(cause).To(ContainSubstring(StageProcessExited))
		Expect(cause).To(MatchRegexp(`exited 3 after \d+\.\d\ds; last output: "candidate-boom"`))
	})
	It("takes the last line of the standard output too", func() {
		cause := causeOf(launcher(), scriptFixture("echo on stdout\nexit 4"))
		Expect(cause).To(ContainSubstring(`exited 4`))
		Expect(cause).To(ContainSubstring(`last output: "on stdout"`))
	})
	It("says so when the process wrote nothing", func() {
		cause := causeOf(launcher(), scriptFixture("exit 5"))
		Expect(cause).To(ContainSubstring(StageProcessExited))
		Expect(cause).To(ContainSubstring("exited 5"))
		Expect(cause).To(HaveSuffix("no output"))
	})
	It("bounds a huge last line", func() {
		cause := causeOf(launcher(), scriptFixture("head -c 100000 /dev/zero | tr '\\0' x\necho\nexit 1"))
		Expect(len(cause)).To(BeNumerically("<", 400))
		Expect(cause).To(ContainSubstring(strings.Repeat("x", maxOutputCause) + `..."`))
		Expect(cause).NotTo(ContainSubstring(strings.Repeat("x", maxOutputCause+1)))
	})
	It("keeps credentials and control characters out of the last line", func() {
		cause := causeOf(launcher(), scriptFixture(`printf 'dial https://user:hunter2@example.test/x API_TOKEN=abc123 password: s3cret \001\033[31mred\n' >&2`+"\nexit 1"))
		Expect(cause).To(ContainSubstring("last output:"))
		for _, secret := range []string{"hunter2", "abc123", "s3cret", "\x01", "\x1b"} {
			Expect(cause).NotTo(ContainSubstring(secret))
		}
	})
	It("names a process that never became ready, with the last output", func() {
		s := launcher()
		cause := causeOf(s, scriptFixture("echo warming up >&2\nexec sleep 30"))
		Expect(cause).To(ContainSubstring(StageReadinessTimeout + ": no good health answer within 1.5s"))
		Expect(cause).To(ContainSubstring(`still running after`))
		Expect(cause).To(ContainSubstring(`last output: "warming up"`))
	})
	It("names a process whose health answer does not repeat the registration", func() {
		s := launcher()
		// The forged process must start and answer before the readiness
		// timeout ends. Unlike the scripts, it is a new executable: macOS takes
		// seconds to admit it, and a loaded Linux runner can take more than the
		// 1.5 s of the script specs (startBudget is only 300 ms there). The
		// spec ends at the first rejected answer, so a long timeout costs
		// nothing when the process starts at once.
		s.config.ReadyTimeout = max(s.config.ReadyTimeout, startBudget, 15*time.Second)
		cause := causeOf(s, fixture("forged", service))
		Expect(cause).To(ContainSubstring(StageRegistrationRejected + ": the health answer does not repeat the registration"))
	})
	It("names a start that is refused before a process is made", func() {
		s := launcher()
		_, e := s.Start(context.Background(), fixture("normal", service), 0)
		Expect(e).To(HaveOccurred())
		Expect(errors.Is(e, ErrRuntime)).To(BeTrue())
		Expect(Cause(e)).To(ContainSubstring(StageStartRefused + ": generation 0"))
	})
	It("is the text of any other error", func() {
		Expect(Cause(errors.New("plain"))).To(Equal("plain"))
	})

	Describe("in the way that Windows passes the channels", func() {
		reportingTo := func(line string) string {
			return `eval "printf '` + line + `\n' >&$` + contract.EndpointHandleEnv + `; exec $` + contract.EndpointHandleEnv + `>&-"` + "\nexec sleep 30"
		}
		reported := func() *Supervisor {
			s := launcher()
			s.reportEndpoint = true
			return s
		}
		It("names a process that never reports its endpoint", func() {
			cause := causeOf(reported(), scriptFixture("echo silent >&2\nexec sleep 30"))
			Expect(cause).To(ContainSubstring(StageHandshakeTimeout + ": the process did not report its endpoint in time"))
			Expect(cause).To(ContainSubstring(`last output: "silent"`))
		})
		It("names a process that reports an endpoint that is not loopback", func() {
			cause := causeOf(reported(), scriptFixture(reportingTo("192.0.2.1:80")))
			Expect(cause).To(ContainSubstring(StageHandshakeProtocol + ": the process reported an endpoint that is not 127.0.0.1"))
		})
		It("names a process that exits before it reports", func() {
			cause := causeOf(reported(), scriptFixture("echo gone >&2\nexit 7"))
			Expect(cause).To(ContainSubstring(StageProcessExited))
			Expect(cause).To(ContainSubstring(`exited 7`))
			Expect(cause).To(ContainSubstring(`last output: "gone"`))
		})
	})
})
