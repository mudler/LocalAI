// SPDX-License-Identifier: MIT
package nodes

import (
	"context"
	"github.com/mudler/LocalAI/core/services/workerctl"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"time"
)

var _ = Describe("Router install environment", func() {
	It("keeps changed snapshots out of an in-flight identical slot", func() {
		started := make(chan struct{}, 2)
		release := make(chan struct{})
		defer close(release)
		unloader := &fakeUnloader{installReply: &workerctl.BackendInstallReply{Success: true, Address: "127.0.0.1:1234"}, installHook: func() { started <- struct{}{}; <-release }}
		router := NewSmartRouter(&fakeModelRouter{}, SmartRouterOptions{Unloader: unloader})
		done := make(chan error, 2)
		for _, value := range []string{"first", "second"} {
			go func(value string) {
				_, err := router.installBackendOnNode(context.Background(), &BackendNode{ID: "node"}, "backend", "model", 0, map[string]string{"TOKEN": value})
				done <- err
			}(value)
		}
		Eventually(started).Should(Receive())
		Eventually(started).Should(Receive())
		// Both leaders entered before either could finish, so they cannot have shared
		// one install reply (and therefore one child's credentials).
		go func() { release <- struct{}{}; release <- struct{}{} }()
		Eventually(done).Should(Receive(BeNil()))
		Eventually(done).Should(Receive(BeNil()))
		Expect(unloader.installCalls).To(HaveLen(2))
		Expect([]string{unloader.installCalls[0].env["TOKEN"], unloader.installCalls[1].env["TOKEN"]}).To(ConsistOf("first", "second"))
	})
	It("coalesces identical snapshots without placing credentials in the identity", func() {
		a := map[string]string{"TOKEN": "test-value", "EMPTY": ""}
		b := map[string]string{"EMPTY": "", "TOKEN": "test-value"}
		Expect(installEnvIdentity(a)).To(Equal(installEnvIdentity(b)))
		Expect(installEnvIdentity(a)).NotTo(ContainSubstring("test-value"))
		Expect(installEnvIdentity(nil)).NotTo(Equal(installEnvIdentity(map[string]string{})))
	})
	It("forwards complete and explicitly empty snapshots through the operation-aware fake", func() {
		fake := &fakeOpWorker{fakeUnloader: &fakeUnloader{}}
		for _, env := range []map[string]string{{"TOKEN": "test-value"}, {}} {
			_, err := fake.InstallBackendOp("node", "backend", "model", "", 0, "", "operation", time.Minute, nil, env)
			Expect(err).NotTo(HaveOccurred())
			Expect(fake.installs[len(fake.installs)-1].EnvVars).To(Equal(env))
			Expect(fake.installCalls[len(fake.installCalls)-1].env).To(Equal(env))
		}
	})
	It("copies snapshots and preserves absence separately from removal", func() {
		Expect(installEnvSnapshot(nil)).To(BeNil())
		Expect(installEnvSnapshot([]map[string]string{nil})).NotTo(BeNil())
		env := map[string]string{"TOKEN": "test-value"}
		snapshot := installEnvSnapshot([]map[string]string{env})
		env["TOKEN"] = "changed"
		Expect(snapshot["TOKEN"]).To(Equal("test-value"))
	})
})
