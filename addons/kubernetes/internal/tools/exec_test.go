// SPDX-License-Identifier: MIT
package tools_test

import (
	"context"
	"strings"
	"time"

	"github.com/mudler/LocalAI/addons/kubernetes/internal/kubeapi"
	"github.com/mudler/LocalAI/addons/kubernetes/internal/policy"
	"github.com/mudler/LocalAI/addons/kubernetes/internal/tools"
	"github.com/mudler/LocalAI/addons/kubernetes/kubetest"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func execRig() *rig {
	g := newRig(pod("team-a", "web-1", "node-1", time.Hour))
	g.setPolicy(policy.Policy{Namespaces: []string{"*"}, WriteTools: policy.WriteTools})
	return g
}

var execArgs = map[string]any{"namespace": "team-a", "pod": "web-1", "command": []string{"sh", "-c", "echo 'a b'; ls /data"}}

var _ = Describe("exec", func() {
	It("is disabled by the default policy", func() {
		g := newRig(pod("team-a", "web-1", "node-1", time.Hour))
		_, err := g.preview("op", tools.ToolExec, execArgs)
		Expect(err).To(MatchError(ContainSubstring("(writeTools): exec is disabled")))
		Expect(g.k.Execs()).To(BeEmpty())
	})

	It("previews the exact argv", func() {
		g := execRig()
		p, err := g.preview("op", tools.ToolExec, execArgs)
		Expect(err).NotTo(HaveOccurred())
		s := sampleOf(p)
		Expect(s["command"]).To(Equal([]any{"sh", "-c", "echo 'a b'; ls /data"}))
		Expect(s["container"]).To(Equal("app"))
		Expect(s["timeoutSeconds"]).To(BeNumerically("==", 30))
		Expect(g.k.Execs()).To(BeEmpty())
	})

	It("returns exit code and capped output", func() {
		g := execRig()
		g.k.SetExec(kubeapi.ExecResult{ExitCode: 3, Stdout: strings.Repeat("o", 40<<10), Stderr: "boom"}, nil)
		rc, data := g.run("op", tools.ToolExec, execArgs)
		Expect(rc.State).To(Equal(contract.ReceiptCommitted), "a non-zero exit is a result, not an error")
		Expect(data["exitCode"]).To(BeNumerically("==", 3))
		Expect(len(data["stdout"].(string))).To(BeNumerically("<=", kubeapi.MaxText))
		Expect(data["stdoutCut"]).To(BeTrue())
		Expect(data["stderr"]).To(Equal("boom"))
		Expect(g.k.Execs()).To(Equal([]kubetest.ExecCall{{Namespace: "team-a", Pod: "web-1", Container: "app", Command: []string{"sh", "-c", "echo 'a b'; ls /data"}}}))
	})

	It("stops at the timeout and says so", func() {
		g := execRig()
		g.k.SetExecHook(func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() })
		args := map[string]any{"namespace": "team-a", "pod": "web-1", "command": []string{"sleep", "100"}, "timeoutSeconds": 1}
		start := time.Now()
		_, data := g.run("op", tools.ToolExec, args)
		Expect(time.Since(start)).To(BeNumerically("<", 5*time.Second))
		Expect(data["timedOut"]).To(BeTrue())
	})

	It("refuses a second call on the same pod while one runs", func() {
		g := execRig()
		release := make(chan struct{})
		started := make(chan struct{}, 1)
		g.k.SetExecHook(func(ctx context.Context) error { started <- struct{}{}; <-release; return nil })
		p1, _ := g.preview("op-1", tools.ToolExec, execArgs)
		p2, _ := g.preview("op-2", tools.ToolExec, execArgs)
		done := make(chan error, 1)
		go func() { _, err := g.apply("op-1", tools.ToolExec, execArgs, p1); done <- err }()
		Eventually(started).Should(Receive())
		_, err := g.apply("op-2", tools.ToolExec, execArgs, p2)
		Expect(err).To(MatchError(ContainSubstring("another exec runs in this pod")))
		close(release)
		Eventually(done).Should(Receive(BeNil()))
		rc, err := g.rt.Receipt(ctx, "op-2")
		Expect(err).NotTo(HaveOccurred())
		Expect(rc.State).To(Equal(contract.ReceiptUnknown), "the refused call left no receipt")
	})

	It("refuses an unknown container and asks for one when the pod has several", func() {
		g := execRig()
		_, err := g.preview("op", tools.ToolExec, map[string]any{"namespace": "team-a", "pod": "web-1", "container": "nope", "command": []string{"ls"}})
		Expect(err).To(MatchError(ContainSubstring("no container nope")))
	})
})
