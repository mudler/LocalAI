// SPDX-License-Identifier: MIT
package foundation_test

import (
	"os"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/tests/addons/foundation/cluster"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// serving brings up a cluster with the given packages published, the named
// add-ons installed and enabled, and one resource of each registered.
func serving(pkgs []cluster.Package, install map[string]string) (*cluster.Cluster, map[string]string) {
	GinkgoHelper()
	c := start()
	for _, p := range pkgs {
		Expect(c.Library.Add(p, c.Effects)).To(Succeed())
	}
	Expect(c.Up()).To(Succeed())
	admin := c.Token(cluster.FrontA, "admin")
	resources := map[string]string{}
	for id, version := range install {
		Expect(lifecycle(admin, id, "install", 0, version).Status).To(Equal(200))
		Expect(lifecycle(admin, id, "enable", 1, "").Status).To(Equal(200))
		resources[id] = resource(c, id, "member", map[string]string{"friend": "viewer"})
	}
	settle("every add-on running", 30*time.Second, func() bool {
		_, list := apps(admin)
		n := 0
		for _, a := range list {
			if a.State == "running" {
				n++
			}
		}
		return n == len(install)
	})
	return c, resources
}

func restartOwner(c *cluster.Cluster) {
	GinkgoHelper()
	Expect(c.Nodes[cluster.Owner].Start()).To(Succeed())
	Expect(c.Nodes[cluster.Owner].Ready(120 * time.Second)).To(Succeed())
}

var _ = Describe("owner failure and recovery", func() {
	It("fails closed on every frontend while the owner is down, executes nothing locally and recovers the last good package and data on restart", func() {
		c, res := serving([]cluster.Package{cluster.ToolPackage("1.0.0", nil), cluster.UIPackage("1.0.0", nil)}, map[string]string{"found-tool": "1.0.0", "found-ui": "1.0.0"})
		marker := retainData(c, "found-tool")
		pkgDir := filepath.Join(c.Config.Nodes[cluster.Owner].Root, "packages")
		packageSum := hashTree(pkgDir)
		before := lines(filepath.Join(c.Effects, "found-tool.log"))
		ui := appOf(c.Token(cluster.FrontA, "member"), "found-ui")

		owner := c.Nodes[cluster.Owner]
		ownerPID := owner.PID()
		owner.Kill()
		settle("the add-on processes ending with the owner", 20*time.Second, func() bool { return fixtureChildren(ownerPID) == 0 })

		var bodies []string
		for _, n := range []string{cluster.FrontA, cluster.FrontB} {
			cl := c.Token(n, "member")
			r := read(cl, "found-tool", res["found-tool"], 1, map[string]any{"tool": "echo"})
			Expect(r.Status).To(Equal(503), n+": "+r.String())
			Expect(errorType(r)).To(Equal("addon_error"))
			bodies = append(bodies, r.String())
			status, _ := apps(cl)
			Expect(status).To(Equal(503), n+" apps")
			asset, err := cl.Do("GET", ui.UI.Entry, nil, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(asset.Status).To(Equal(503), n+" asset")
			life := lifecycle(c.Token(n, "admin"), "found-tool", "disable", 1, "")
			Expect(life.Status).To(Equal(503), n+" lifecycle: "+life.String())
			prop, err := cl.Do("POST", "/api/addons/proposals", map[string]any{"addonId": "found-tool", "resourceId": res["found-tool"], "generation": 1, "operationId": nextID("down"), "action": "write", "arguments": map[string]any{}}, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(prop.Status).To(Equal(503), n+" propose: "+prop.String())
			Expect(fixtureChildren(c.Nodes[n].PID())).To(BeZero(), n+" must execute nothing locally")
			Expect(c.Config.Nodes[n].Root).NotTo(BeADirectory(), n+" must not create add-on storage")
		}
		Expect(bodies[1]).To(Equal(bodies[0]), "the same typed error from every frontend")
		Expect(fixtureChildren(ownerPID)).To(BeZero())

		restartOwner(c)
		settle("the add-ons running again", 30*time.Second, func() bool {
			a := appOf(c.Token(cluster.FrontB, "admin"), "found-tool")
			b := appOf(c.Token(cluster.FrontB, "admin"), "found-ui")
			return a != nil && b != nil && a.State == "running" && b.State == "running"
		})
		a := appOf(c.Token(cluster.FrontA, "admin"), "found-tool")
		Expect(a.Generation).To(Equal(uint64(1)), "the last good generation is the one that runs")
		for _, n := range []string{cluster.FrontA, cluster.FrontB} {
			Expect(actorOf(read(c.Token(n, "friend"), "found-tool", res["found-tool"], 1, map[string]any{"tool": "echo"}))).To(Equal("fnd-friend"))
		}
		Expect(fixtureChildren(c.Nodes[cluster.Owner].PID())).To(Equal(2), "exactly one process per add-on")
		Expect(hashTree(pkgDir)).To(Equal(packageSum), "package code is unchanged")
		expectRetained(marker)
		Expect(lines(filepath.Join(c.Effects, "found-tool.log"))).To(Equal(before))
	})

	It("refuses a second owner on the same storage and keeps one process per add-on", func() {
		c, res := serving([]cluster.Package{cluster.ToolPackage("1.0.0", nil)}, map[string]string{"found-tool": "1.0.0"})
		owner := c.Nodes[cluster.Owner]
		Expect(fixtureChildren(owner.PID())).To(Equal(1))

		dup, err := c.Config.Derive("dup")
		Expect(err).NotTo(HaveOccurred())
		second := &cluster.Node{Name: cluster.Owner, Config: dup, ServerPath: paths.Server, MockPath: paths.Mock}
		Expect(second.Start()).To(Succeed())
		DeferCleanup(second.Stop)
		Expect(second.Wait(60*time.Second)).To(BeTrue(), "a second owner on the same storage must not keep running: "+second.Tail(8))
		Expect(fixtureChildren(second.PID())).To(BeZero())
		// The first owner keeps its single process and keeps serving.
		Expect(fixtureChildren(owner.PID())).To(Equal(1))
		Expect(actorOf(read(c.Token(cluster.FrontB, "member"), "found-tool", res["found-tool"], 1, map[string]any{"tool": "echo"}))).To(Equal("fnd-member"))
	})

	It("stops an add-on process orphaned by an owner crash before it starts the next one", func() {
		c, res := serving([]cluster.Package{cluster.ToolPackage("1.0.0", map[string]any{"mode": "orphan"})}, map[string]string{"found-tool": "1.0.0"})
		owner := c.Nodes[cluster.Owner]
		var orphan int
		for pid := range cluster.ChildrenOf(owner.PID()) {
			orphan = pid
		}
		Expect(orphan).NotTo(BeZero())
		owner.Kill()
		// An add-on that ignores the closed startup channel outlives its owner.
		Consistently(func() bool { return cluster.Alive(orphan) }, 1500*time.Millisecond, 100*time.Millisecond).Should(BeTrue(), "the orphan mode must really orphan the process")

		restartOwner(c)
		settle("the orphan being stopped", 20*time.Second, func() bool { return !cluster.Alive(orphan) })
		settle("the add-on serving again", 30*time.Second, func() bool {
			a := appOf(c.Token(cluster.FrontA, "admin"), "found-tool")
			return a != nil && a.State == "running"
		})
		children := cluster.ChildrenOf(c.Nodes[cluster.Owner].PID())
		Expect(children).To(HaveLen(1))
		for pid := range children {
			Expect(pid).NotTo(Equal(orphan))
		}
		Expect(actorOf(read(c.Token(cluster.FrontA, "member"), "found-tool", res["found-tool"], 1, map[string]any{"tool": "echo"}))).To(Equal("fnd-member"))
	})

	It("leaves no process behind when a cluster with an orphaned add-on is taken down", func() {
		c, _ := serving([]cluster.Package{cluster.ToolPackage("1.0.0", map[string]any{"mode": "orphan"})}, map[string]string{"found-tool": "1.0.0"})
		owner := c.Nodes[cluster.Owner]
		first := owner.PID()
		var orphan int
		for pid := range cluster.ChildrenOf(first) {
			orphan = pid
		}
		Expect(orphan).NotTo(BeZero())
		owner.Kill()
		restartOwner(c)
		var current int
		for pid := range cluster.ChildrenOf(owner.PID()) {
			current = pid
		}
		Expect(current).NotTo(BeZero())
		// The teardown a failing spec reaches: every process must be gone.
		c.Down()
		for _, pid := range []int{first, owner.PID(), orphan, current, c.Nodes[cluster.FrontA].PID(), c.Nodes[cluster.FrontB].PID()} {
			Expect(cluster.Alive(pid)).To(BeFalse(), "process %d survived Down", pid)
		}
		c.Down()
	})

	It("keeps the files of the package of an installed add-on read only", func() {
		c, _ := serving([]cluster.Package{cluster.ToolPackage("1.0.0", nil)}, map[string]string{"found-tool": "1.0.0"})
		root := filepath.Join(c.Config.Nodes[cluster.Owner].Root, "packages")
		var files int
		Expect(filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
			if e != nil || d.IsDir() {
				return e
			}
			info, err := d.Info()
			Expect(err).NotTo(HaveOccurred())
			files++
			Expect(info.Mode().Perm()&0o222).To(BeZero(), p+" must not be writable")
			return nil
		})).To(Succeed())
		Expect(files).To(BeNumerically(">", 0))
	})
})
