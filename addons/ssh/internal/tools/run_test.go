// SPDX-License-Identifier: MIT
package tools_test

import (
	"io"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/addons/ssh/internal/sshclient"
	"github.com/mudler/LocalAI/addons/ssh/internal/tools"
	"github.com/mudler/LocalAI/addons/ssh/sshtest"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("run", func() {
	var f *fixture
	AfterEach(func() { f.srv.Close() })

	Context("with a plain server", func() {
		BeforeEach(func() { f = newFixture(sshtest.Options{}) })

		It("previews the host, the user and the exact command, and connects to nothing", func() {
			p, err := f.preview("op1", map[string]any{"command": "df -h /"})
			Expect(err).NotTo(HaveOccurred())
			s := sampleOf(p)
			Expect(s).To(HaveKeyWithValue("host", user+"@"+f.srv.Address()))
			Expect(s).To(HaveKeyWithValue("user", user))
			Expect(s).To(HaveKeyWithValue("command", "df -h /"))
			Expect(s).To(HaveKeyWithValue("timeoutSeconds", BeNumerically("==", tools.DefaultTimeout)))
			Expect(s).To(HaveKeyWithValue("hostKey", sshclient.FingerprintOfPin(sshclient.PinHex(f.srv.HostKey()))))
			Expect(f.srv.Logins()).To(Equal(0))
			noLeak(p.Summary)
		})

		It("runs the command once and returns the exit code, stdout and stderr", func() {
			rc, r := f.run("op1", map[string]any{"command": "echo hello"})
			Expect(rc.State).To(Equal(contract.ReceiptCommitted))
			Expect(r.Untrusted).To(BeTrue())
			Expect(r.Data).To(HaveKeyWithValue("exitCode", BeNumerically("==", 0)))
			Expect(r.Data).To(HaveKeyWithValue("stdout", "ran: echo hello\n"))
			Expect(r.Data).To(HaveKeyWithValue("timedOut", false))
			Expect(r.Data).To(HaveKeyWithValue("stdoutTruncated", false))
			Expect(f.srv.Commands()).To(Equal([]string{"echo hello"}))
			Expect(f.srv.Requests()).To(Equal([]string{"exec"}))
			// The same operation again answers from the journal and runs nothing.
			p, _ := f.preview("op1", map[string]any{"command": "echo hello"})
			rc, err := f.apply("op1", map[string]any{"command": "echo hello"}, p)
			Expect(err).NotTo(HaveOccurred())
			Expect(rc.State).To(Equal(contract.ReceiptCommitted))
			Expect(f.srv.Commands()).To(HaveLen(1))
			// The journal keeps the fact, not the output.
			Expect(string(rc.Result.Payload)).NotTo(ContainSubstring("hello"))
		})

		It("runs with a private key", func() {
			pub, pemKey := sshtest.NewClientKey()
			f.srv.Close()
			f = newFixture(sshtest.Options{Key: pub})
			f.cred = pemKey
			rc, r := f.run("op1", map[string]any{"command": "whoami"})
			Expect(rc.State).To(Equal(contract.ReceiptCommitted))
			Expect(r.Data).To(HaveKeyWithValue("stdout", "ran: whoami\n"))
		})

		It("reports a non-zero exit code as a result, not as an error", func() {
			f.srv.Close()
			f = newFixture(sshtest.Options{Handler: func(cmd string, s sshtest.Streams) int {
				_, _ = io.WriteString(s.Stderr, "no such file")
				return 2
			}})
			rc, r := f.run("op1", map[string]any{"command": "ls /nope"})
			Expect(rc.State).To(Equal(contract.ReceiptCommitted))
			Expect(r.Data).To(HaveKeyWithValue("exitCode", BeNumerically("==", 2)))
			Expect(r.Data).To(HaveKeyWithValue("stderr", "no such file"))
		})

		DescribeTable("refuses a command that cannot be shown to a person",
			func(args map[string]any, want string) {
				_, err := f.preview("op", args)
				Expect(err).To(MatchError(ContainSubstring(want)))
				Expect(f.srv.Logins()).To(Equal(0))
			},
			Entry("empty", map[string]any{"command": "  "}, "command is empty"),
			Entry("with a control character", map[string]any{"command": "echo \x07"}, "control character"),
			Entry("too long", map[string]any{"command": strings.Repeat("a", tools.MaxCommandLength+1)}, "longer than"),
			Entry("with a timeout below one", map[string]any{"command": "id", "timeoutSeconds": 0}, "timeoutSeconds is from 1 to"),
			Entry("with a timeout above the maximum", map[string]any{"command": "id", "timeoutSeconds": tools.MaxTimeout + 1}, "timeoutSeconds is from 1 to"),
			Entry("with an environment the model chose", map[string]any{"command": "id", "env": map[string]any{"A": "b"}}, "arguments do not match"),
			Entry("with input for the command", map[string]any{"command": "cat", "stdin": "x"}, "arguments do not match"),
			Entry("with a terminal request", map[string]any{"command": "top", "pty": true}, "arguments do not match"),
		)

		It("allows a multi-line command and the longest timeout", func() {
			_, err := f.preview("op", map[string]any{"command": "set -e\nls\n\tpwd", "timeoutSeconds": tools.MaxTimeout})
			Expect(err).NotTo(HaveOccurred())
		})

		It("refuses a command that holds the credential", func() {
			_, err := f.preview("op", map[string]any{"command": "echo " + pass + " | sudo -S id"})
			Expect(err).To(MatchError(ContainSubstring("never put a secret in a command")))
			Expect(err.Error()).NotTo(ContainSubstring("S3CRET"))
		})

		It("refuses a host that is not the one the host allowed, at preview and at apply", func() {
			_, err := f.preview("op", map[string]any{"command": "id", "host": user + "@other.example"})
			Expect(err).To(MatchError(ContainSubstring("host is not the one the host allowed")))
		})

		It("refuses a connection with no pin", func() {
			f.host = f.addr("")
			_, err := f.preview("op", map[string]any{"command": "id"})
			Expect(err).To(MatchError(ContainSubstring("no host key is pinned")))
		})

		It("rejects the operation as stale when the pin changed after the preview", func() {
			args := map[string]any{"command": "id"}
			p, err := f.preview("op1", args)
			Expect(err).NotTo(HaveOccurred())
			f.host = f.addr(strings.Repeat("cd", 32))
			args["host"] = f.host
			rc, err := f.apply("op1", args, p)
			Expect(err).NotTo(HaveOccurred())
			Expect(rc.State).To(Equal(contract.ReceiptRejected))
			Expect(string(rc.Result.Payload)).To(ContainSubstring("stale"))
			Expect(f.srv.Commands()).To(BeEmpty())
		})

		It("refuses a changed host key at apply, sends no password and runs nothing", func() {
			args := map[string]any{"command": "id"}
			p, _ := f.preview("op1", args)
			f.srv.RotateHostKey()
			_, err := f.apply("op1", args, p)
			Expect(err).To(MatchError(ContainSubstring("the host key changed")))
			Expect(f.srv.Commands()).To(BeEmpty())
			Expect(f.srv.Refused()).To(Equal(0))
			// Nothing started, so the same operation may be tried again.
			rc, err := f.rt.Receipt(ctx, "op1")
			Expect(err).NotTo(HaveOccurred())
			Expect(rc.State).To(Equal(contract.ReceiptUnknown))
		})

		It("says a login failure in its own words and leaves the operation retryable", func() {
			f.cred = user + ":wrong-password"
			args := map[string]any{"command": "id"}
			p, _ := f.preview("op1", args)
			_, err := f.apply("op1", args, p)
			Expect(err).To(MatchError(ContainSubstring("refused the login")))
			f.cred = user + ":" + pass
			rc, err := f.apply("op1", args, p)
			Expect(err).NotTo(HaveOccurred())
			Expect(rc.State).To(Equal(contract.ReceiptCommitted))
		})

		It("says the host is unreachable when it is down", func() {
			args := map[string]any{"command": "id"}
			p, _ := f.preview("op1", args)
			f.srv.Close()
			_, err := f.apply("op1", args, p)
			Expect(err).To(MatchError(ContainSubstring("could not be reached")))
		})
	})

	It("returns the output so far when the timeout passes, and flags it", func() {
		f = newFixture(sshtest.Options{Handler: func(cmd string, s sshtest.Streams) int {
			_, _ = io.WriteString(s.Stdout, "working\n")
			select {
			case <-s.Done:
			case <-time.After(20 * time.Second):
			}
			return 0
		}})
		begin := time.Now()
		rc, r := f.run("op1", map[string]any{"command": "sleep 600", "timeoutSeconds": 1})
		Expect(rc.State).To(Equal(contract.ReceiptCommitted))
		Expect(r.Data).To(HaveKeyWithValue("timedOut", true))
		Expect(r.Data).To(HaveKeyWithValue("exitCode", BeNumerically("==", -1)))
		Expect(r.Data).To(HaveKeyWithValue("stdout", "working\n"))
		Expect(r.Data["note"]).To(ContainSubstring("may still be running"))
		Expect(time.Since(begin)).To(BeNumerically("<", 8*time.Second))
	})

	It("caps stdout and stderr and says so", func() {
		big := strings.Repeat("0123456789abcdef", 8<<10) // 128 KiB
		f = newFixture(sshtest.Options{Handler: func(cmd string, s sshtest.Streams) int {
			_, _ = io.WriteString(s.Stdout, big)
			_, _ = io.WriteString(s.Stderr, big)
			return 0
		}})
		rc, r := f.run("op1", map[string]any{"command": "dump"})
		Expect(rc.State).To(Equal(contract.ReceiptCommitted))
		Expect(r.Data["stdout"]).To(HaveLen(sshclient.MaxOutput))
		Expect(r.Data["stderr"]).To(HaveLen(sshclient.MaxOutput))
		Expect(r.Data).To(HaveKeyWithValue("stdoutTruncated", true))
		Expect(r.Data).To(HaveKeyWithValue("stderrTruncated", true))
		Expect(len(rc.Result.Payload)).To(BeNumerically("<=", tools.MaxPayload))
	})

	It("removes the password from output and from errors", func() {
		f = newFixture(sshtest.Options{Handler: func(cmd string, s sshtest.Streams) int {
			_, _ = io.WriteString(s.Stdout, "password is "+pass+"\n")
			_, _ = io.WriteString(s.Stderr, "user:pass="+user+":"+pass+"\n")
			return 1
		}})
		rc, r := f.run("op1", map[string]any{"command": "cat /etc/app.conf"})
		Expect(r.Data["stdout"]).To(Equal("password is [redacted]\n"))
		Expect(r.Data["stderr"]).To(Equal("user:pass=[redacted]\n"))
		noLeak(rc.Result.Payload)
		Expect(rc.Result.Payload).NotTo(ContainSubstring("S3CRET"))
	})

	It("removes a private key from output in whole and in pieces", func() {
		pub, pemKey := sshtest.NewClientKey()
		lines := strings.Split(strings.TrimSpace(pemKey), "\n")
		f = newFixture(sshtest.Options{Key: pub, Handler: func(cmd string, s sshtest.Streams) int {
			_, _ = io.WriteString(s.Stdout, pemKey+"\n")
			_, _ = io.WriteString(s.Stderr, strings.Join(lines[1:len(lines)-1], "")+"\n")
			return 0
		}})
		f.cred = pemKey
		rc, r := f.run("op1", map[string]any{"command": "cat ~/.ssh/id"})
		for _, out := range []any{r.Data["stdout"], r.Data["stderr"]} {
			Expect(out).NotTo(ContainSubstring(lines[1]))
			Expect(out).To(ContainSubstring("[redacted]"))
		}
		Expect(string(rc.Result.Payload)).NotTo(ContainSubstring(lines[1]))
	})

	It("answers unknown, and runs nothing twice, when the connection drops after the command started", func() {
		var once sync.Once
		f = newFixture(sshtest.Options{})
		f.srv.Close()
		var drop func()
		f = newFixture(sshtest.Options{Handler: func(cmd string, s sshtest.Streams) int {
			once.Do(func() { drop() })
			time.Sleep(200 * time.Millisecond)
			return 0
		}})
		drop = f.srv.DropConnections
		args := map[string]any{"command": "reboot"}
		p, _ := f.preview("op1", args)
		rc, err := f.apply("op1", args, p)
		Expect(err).NotTo(HaveOccurred())
		Expect(rc.State).To(Equal(contract.ReceiptUnknown))
		// A second apply of the same operation does not send it again.
		rc, err = f.apply("op1", args, p)
		Expect(err).NotTo(HaveOccurred())
		Expect(rc.State).To(Equal(contract.ReceiptUnknown))
		Expect(f.srv.Commands()).To(HaveLen(1))
		got, _ := f.rt.Receipt(ctx, "op1")
		Expect(got.State).To(Equal(contract.ReceiptUnknown))
	})

	It("runs one command at a time on a connection", func() {
		release := make(chan struct{})
		started := make(chan struct{})
		f = newFixture(sshtest.Options{Handler: func(cmd string, s sshtest.Streams) int {
			if cmd == "slow" {
				close(started)
				<-release
			}
			return 0
		}})
		args := map[string]any{"command": "slow"}
		p, _ := f.preview("op1", args)
		done := make(chan error, 1)
		go func() { _, err := f.apply("op1", args, p); done <- err }()
		Eventually(started).Should(BeClosed())
		// Another command, and a read, are refused while the first runs.
		args2 := map[string]any{"command": "fast"}
		p2, _ := f.preview("op2", args2)
		_, err := f.apply("op2", args2, p2)
		Expect(err).To(MatchError(ContainSubstring("another command is still running")))
		_, err = f.read(tools.ToolGetInfo, nil)
		Expect(err).To(MatchError(ContainSubstring("another command is still running")))
		close(release)
		Eventually(done).Should(Receive(BeNil()))
		// And once it ended, the next one runs.
		rc, err := f.apply("op2", args2, p2)
		Expect(err).NotTo(HaveOccurred())
		Expect(rc.State).To(Equal(contract.ReceiptCommitted))
	})
})
