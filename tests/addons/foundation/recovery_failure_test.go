// SPDX-License-Identifier: MIT
package foundation_test

import (
	"strings"
	"time"

	"github.com/mudler/LocalAI/tests/addons/foundation/cluster"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// A new generation or an enabled add-on whose process cannot start, and an
// owner that dies while one is starting, must leave the last good code, the
// retained data and a usable lifecycle.
var _ = Describe("failed starts and interrupted lifecycle", func() {
	It("keeps the last good generation running and its data when an upgrade fails to start", func() {
		c, res := serving([]cluster.Package{
			cluster.ToolPackage("1.0.0", nil),
			cluster.ToolPackage("2.0.0", map[string]any{"mode": "exit-start"}),
			cluster.ToolPackage("3.0.0", nil),
		}, map[string]string{"found-tool": "1.0.0"})
		admin := c.Token(cluster.FrontB, "admin")
		marker := retainData(c, "found-tool")
		r := lifecycle(admin, "found-tool", "upgrade", 0, "2.0.0")
		Expect(r.Status).NotTo(Equal(200), "an upgrade whose process cannot start must not report success: "+r.String())
		a := appOf(admin, "found-tool")
		Expect(a).NotTo(BeNil())
		Expect(a.State).To(Equal("running"))
		Expect(a.Generation).To(Equal(uint64(1)))
		Expect(fixtureChildren(c.Nodes[cluster.Owner].PID())).To(Equal(1))
		Expect(actorOf(read(c.Token(cluster.FrontA, "member"), "found-tool", res["found-tool"], 1, map[string]any{"tool": "echo"}))).To(Equal("fnd-member"))
		expectRetained(marker)
		// The failure left nothing behind that blocks a later good upgrade.
		r = lifecycle(admin, "found-tool", "upgrade", 0, "3.0.0")
		Expect(r.Status).To(Equal(200), r.String())
		settle("the third release running", 30*time.Second, func() bool {
			cur := appOf(admin, "found-tool")
			return cur != nil && cur.State == "running" && cur.Generation > 1
		})
		expectRetained(marker)
	})

	It("keeps the last good code and data when the owner is killed in the middle of an upgrade and recovers on restart", func() {
		c, res := serving([]cluster.Package{
			cluster.ToolPackage("1.0.0", nil),
			cluster.ToolPackage("2.0.0", map[string]any{"mode": "never-ready"}),
			cluster.ToolPackage("3.0.0", nil),
		}, map[string]string{"found-tool": "1.0.0"})
		admin := c.Token(cluster.FrontA, "admin")
		marker := retainData(c, "found-tool")
		owner := c.Nodes[cluster.Owner]
		ownerPID := owner.PID()
		done := make(chan struct{})
		go func() {
			defer GinkgoRecover()
			defer close(done)
			// The connection dies with the owner; the result is not the point.
			_, _ = admin.Do("POST", "/api/addons/dispatch", map[string]any{"operationId": nextID("mid"), "addonId": "found-tool", "action": "upgrade", "generation": 0, "arguments": map[string]any{"sourceId": "fixture", "version": "2.0.0"}}, nil)
		}()
		var lastGood int
		for pid := range cluster.ChildrenOf(ownerPID) {
			lastGood = pid
		}
		// The last good process is replaced by the candidate, which never
		// becomes ready: the owner is killed while that start is in flight.
		settle("the candidate generation being started", 30*time.Second, func() bool {
			for pid, cmdline := range cluster.ChildrenOf(ownerPID) {
				if pid != lastGood && strings.Contains(cmdline, "bin/fixture") {
					return true
				}
			}
			return false
		})
		owner.Kill()
		Eventually(done, 30*time.Second).Should(BeClosed())
		settle("every add-on process ending", 20*time.Second, func() bool { return fixtureChildren(ownerPID) == 0 })

		restartOwner(c)
		settle("the last good generation running", 60*time.Second, func() bool {
			a := appOf(c.Token(cluster.FrontB, "admin"), "found-tool")
			return a != nil && a.State == "running"
		})
		a := appOf(c.Token(cluster.FrontB, "admin"), "found-tool")
		Expect(a.Generation).To(Equal(uint64(1)), "the interrupted upgrade must not have replaced the last good code")
		Expect(a.Version).To(Equal("1.0.0"))
		Expect(actorOf(read(c.Token(cluster.FrontB, "member"), "found-tool", res["found-tool"], 1, map[string]any{"tool": "echo"}))).To(Equal("fnd-member"))
		expectRetained(marker)
		Expect(fixtureChildren(c.Nodes[cluster.Owner].PID())).To(Equal(1))
		// A later good upgrade is not blocked by the interrupted one.
		r := lifecycle(c.Token(cluster.FrontB, "admin"), "found-tool", "upgrade", 0, "3.0.0")
		Expect(r.Status).To(Equal(200), r.String())
		expectRetained(marker)
	})

	It("keeps retained data and a consistent lifecycle when the owner is killed in the middle of an enable", func() {
		c := start()
		Expect(c.Library.Add(cluster.ServicePackage("1.0.0", map[string]any{"mode": "never-ready"}), c.Effects)).To(Succeed())
		Expect(c.Up()).To(Succeed())
		admin := c.Token(cluster.FrontA, "admin")
		Expect(lifecycle(admin, "found-svc", "install", 0, "1.0.0").Status).To(Equal(200))
		marker := retainData(c, "found-svc")
		owner := c.Nodes[cluster.Owner]
		ownerPID := owner.PID()
		done := make(chan struct{})
		go func() {
			defer GinkgoRecover()
			defer close(done)
			_, _ = admin.Do("POST", "/api/addons/dispatch", map[string]any{"operationId": nextID("enable"), "addonId": "found-svc", "action": "enable", "generation": 1, "arguments": map[string]any{}}, nil)
		}()
		settle("the process being started", 30*time.Second, func() bool { return fixtureChildren(ownerPID) == 1 })
		owner.Kill()
		Eventually(done, 30*time.Second).Should(BeClosed())
		settle("the process ending", 20*time.Second, func() bool { return fixtureChildren(ownerPID) == 0 })

		restartOwner(c)
		a := appOf(c.Token(cluster.FrontB, "admin"), "found-svc")
		Expect(a).NotTo(BeNil(), "the installed add-on is not lost")
		Expect(a.State).NotTo(Equal("running"), "a process that never became ready is never reported as running")
		expectRetained(marker)
		// The lifecycle is not wedged: the add-on can be disabled and removed.
		for _, action := range []string{"disable", "uninstall"} {
			r := lifecycle(c.Token(cluster.FrontB, "admin"), "found-svc", action, a.Generation, "")
			Expect(r.Status).To(Equal(200), action+": "+r.String())
		}
		expectRetained(marker)
		Expect(fixtureChildren(c.Nodes[cluster.Owner].PID())).To(BeZero())
	})
})
