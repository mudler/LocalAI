//go:build linux

// SPDX-License-Identifier: MIT
package egress_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/addons/sandbox/internal/egress"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/chv"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/gvisor"
	"github.com/mudler/LocalAI/core/services/studio/sandbox"
	"github.com/mudler/LocalAI/core/services/studio/sandbox/sbtest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// The proxy in a real guest. These specs need the setup of the backend
// integration suites and skip, saying why, without it:
//
//	SANDBOX_TEST_IMAGE  the guest image (name@sha256:... or oci-layout:<dir>@sha256:...)
//	SANDBOX_TEST_RUNSC  runsc with gvisor-bin next to it, for gVisor
//	SANDBOX_TEST_CACHE  unpacked images; default under the temporary folder
//
// KVM also needs /dev/kvm for the user that runs the test.

func cacheDir(kind string) string {
	if d := os.Getenv("SANDBOX_TEST_CACHE"); d != "" {
		return filepath.Join(d, kind)
	}
	return filepath.Join(os.TempDir(), "sandbox-egress-test-cache-"+kind)
}

func stateDir() string {
	d, err := os.MkdirTemp("", "eg")
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = os.RemoveAll(d) })
	return d
}

func gvisorBackend() sandbox.Backend {
	runsc, img := os.Getenv("SANDBOX_TEST_RUNSC"), os.Getenv("SANDBOX_TEST_IMAGE")
	if runsc == "" || img == "" {
		Skip("set SANDBOX_TEST_RUNSC and SANDBOX_TEST_IMAGE to run the proxy in a gVisor guest")
	}
	b, err := gvisor.New(gvisor.Options{StateDir: stateDir(), CacheDir: cacheDir("gvisor"), Runsc: runsc, DefaultImage: img})
	Expect(err).NotTo(HaveOccurred(), "this host cannot run gVisor")
	return b
}

func kvmBackend() sandbox.Backend {
	img := os.Getenv("SANDBOX_TEST_IMAGE")
	if img == "" {
		Skip("set SANDBOX_TEST_IMAGE to run the proxy in a KVM guest")
	}
	b, err := chv.New(chv.Options{StateDir: stateDir(), CacheDir: cacheDir("kvm"), DefaultImage: img, NoBwrap: os.Getenv("SANDBOX_NO_BWRAP") == "1"})
	if errors.Is(err, sandbox.ErrUnsupported) {
		Skip("this host cannot run KVM: " + err.Error())
	}
	Expect(err).NotTo(HaveOccurred())
	return b
}

type guest struct {
	sb sandbox.Sandbox
}

func newGuest(b sandbox.Backend, ports map[int]sandbox.PortHandler) guest {
	ctx := context.Background()
	st := sbtest.OpenStore()
	_, err := st.Mkdir(ctx, sbtest.Admin, "/proj")
	Expect(err).NotTo(HaveOccurred())
	v, err := st.View(ctx, sbtest.Admin, []workspace.MountRequest{{Path: "/proj", At: "proj", Mode: workspace.ModeWrite}})
	Expect(err).NotTo(HaveOccurred())
	sb, err := b.Create(ctx, sandbox.Spec{Env: sandbox.Env{Store: st, Actor: sbtest.Admin, Ports: ports}, View: v, Memory: 1 << 30, Disk: 1 << 30})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = sb.Destroy(context.Background()) })
	return guest{sb: sb}
}

func (g guest) run(script string) (int, string, string) {
	var out, errb bytes.Buffer
	p, err := g.sb.Exec(context.Background(), sandbox.Cmd{Args: []string{"bash", "-c", script}, Stdout: &out, Stderr: &errb})
	Expect(err).NotTo(HaveOccurred())
	code, err := p.Wait()
	Expect(err).NotTo(HaveOccurred())
	return code, out.String(), errb.String()
}

func proxyInGuest(newBackend func() sandbox.Backend) {
	It("reaches an allowed host only through the proxy, gets the 403 line when blocked or idle, and has no other way out", func() {
		b := newBackend()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "hello from the host") }))
		DeferCleanup(srv.Close)
		port := portOf(srv.URL)
		var mu = make(chan struct{}, 1)
		mu <- struct{}{}
		decision := "allow"
		f := newFixture(0, func(a egress.CheckArgs) egress.Verdict {
			<-mu
			defer func() { mu <- struct{}{} }()
			if decision == "allow" && a.Host == "public.test" {
				return egress.Verdict{Action: "allow", Reason: "preset"}
			}
			return egress.Verdict{Action: "block", Reason: "off"}
		}, egress.Limits{})
		g := newGuest(b, f.proxy.Ports("alice", "c1"))
		url := fmt.Sprintf("http://public.test:%d/", port)

		// No call runs: the proxy is closed.
		code, out, _ := g.run("curl -sS --max-time 20 " + url)
		Expect(out).To(ContainSubstring("no command of this chat is running"), "exit %d", code)

		end := f.proxy.Begin(context.Background(), "alice", "c1", "op1")
		code, out, errOut := g.run("curl -sS --max-time 20 " + url)
		Expect(code).To(BeZero(), "%s%s", out, errOut)
		Expect(out).To(Equal("hello from the host"))

		// The preset goes to off. The allow above is cached for its call
		// only, so the next call asks again.
		end()
		<-mu
		decision = "off"
		mu <- struct{}{}
		end = f.proxy.Begin(context.Background(), "alice", "c1", "op2")
		_, out, _ = g.run("curl -sS --max-time 20 " + url)
		Expect(out).To(ContainSubstring("Blocked by LocalAI: public.test:"), "the model reads why")
		code, _, errOut = g.run("curl -sS --max-time 20 https://example.com/")
		Expect(code).NotTo(BeZero())
		Expect(errOut).To(ContainSubstring("403"))

		// Around the proxy: no network device, no route, no DNS.
		code, out, errOut = g.run(fmt.Sprintf("curl -sS --max-time 10 --noproxy '*' http://10.0.2.2:%d/; echo; cat /proc/net/dev", port))
		Expect(out).NotTo(ContainSubstring("hello from the host"), "%s", errOut)
		ifaces := []string{}
		for _, l := range strings.Split(out, "\n") {
			if name, _, ok := strings.Cut(strings.TrimSpace(l), ":"); ok && !strings.Contains(name, "|") {
				ifaces = append(ifaces, name)
			}
		}
		Expect(ifaces).To(ConsistOf("lo"), "the guest has only loopback")
		_ = code
		end()
	})
}

var _ = Describe("the proxy in a gVisor guest", func() { proxyInGuest(gvisorBackend) })
var _ = Describe("the proxy in a KVM guest", func() { proxyInGuest(kvmBackend) })
