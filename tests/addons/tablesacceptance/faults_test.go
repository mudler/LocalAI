// SPDX-License-Identifier: MIT
package tablesacceptance_test

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/mudler/LocalAI/tests/addons/foundation/cluster"
	"github.com/mudler/LocalAI/tests/addons/tablesacceptance/tablespkg"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// snapshot lists a directory tree with the size and time of every file, so a
// spec can prove that something did not touch it.
func snapshot(root string) []string {
	GinkgoHelper()
	var out []string
	err := filepath.WalkDir(root, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		out = append(out, fmt.Sprintf("%s %d %d", p, info.Size(), info.ModTime().UnixNano()))
		return nil
	})
	Expect(err).NotTo(HaveOccurred())
	sort.Strings(out)
	return out
}

// Faults that the real executable cannot show on its own: an artifact that
// expires, a transfer that is withdrawn and a change that committed and lost
// its answer. They run against a test-only build of the same executable
// (addons/tables/cmd/tables-faults) that a spec arms by creating a file in the
// "fault" directory of the add-on's data, and a short artifact lifetime that
// only the fixture's launcher sets. The product executable has neither.
var _ = Describe("Tables with faults armed in the test-only build", Ordered, func() {
	var (
		d    *deployment
		res  string
		data string
	)
	row := map[string]any{"Item": "ink", "Qty": 2}

	arm := func(name string) {
		GinkgoHelper()
		Expect(os.MkdirAll(filepath.Join(data, "fault"), 0o700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(data, "fault", name), nil, 0o600)).To(Succeed())
	}
	disarm := func(name string) { _ = os.Remove(filepath.Join(data, "fault", name)) }

	BeforeAll(func() {
		scratch, err := os.MkdirTemp("", "tables-faults-*")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			_ = filepath.WalkDir(scratch, func(p string, e fs.DirEntry, err error) error {
				if err == nil && e.IsDir() {
					_ = os.Chmod(p, 0o700)
				}
				return nil
			})
			_ = os.RemoveAll(scratch)
		})
		exe := filepath.Join(scratch, "tables-faults")
		Expect(tablespkg.BuildExecutableFrom(repoRoot, exe, "./addons/tables/cmd/tables-faults", func(argv ...string) { record(argv...) })).To(Succeed())
		built, err := tablespkg.Build(repoRoot, scratch, exe, func(argv ...string) { record(argv...) }, "0.1.0")
		Expect(err).NotTo(HaveOccurred())
		c := startFrom(func(cfg *cluster.Config) { cfg.ArtifactExpiryMillis = 12000 }, built, "0.1.0")
		d = deploy(c, "0.1.0")
		data = filepath.Join(c.Config.Nodes[cluster.Owner].Root, "data", "tables")
		res = d.seedLedger(cluster.FrontA, "member", "Faults", map[string]any{"Item": "pen", "Qty": 3})
		Expect(d.grant(cluster.Owner, "member", res, "editor", "editor").Status).To(Equal(200))
	})

	It("denies an expired file identically from every node and leaves the data and the backups untouched", func() {
		op := opID("backup")
		backup := d.tab(cluster.FrontB, "admin", "POST", "/api/addons/tables/recovery/backup", map[string]any{"operationId": op}, op)
		Expect(backup.Status).To(Equal(200), backup.String())
		id := d.export(cluster.FrontA, "member", res)
		for _, n := range nodes {
			Expect(d.download(n, "member", id).Status).To(Equal(200), n)
		}
		db, err := os.Stat(d.databaseFile(res))
		Expect(err).NotTo(HaveOccurred())
		receipts := d.receipts(res)
		backups := snapshot(filepath.Join(d.c.Config.Nodes[cluster.Owner].Root, "backups"))
		Expect(backups).NotTo(BeEmpty())

		// The lifetime ends by itself; the first node to see it is not special.
		Eventually(func() int { return d.download(cluster.Owner, "member", id).Status }, 60*time.Second, 200*time.Millisecond).ShouldNot(Equal(200))
		first := d.download(cluster.Owner, "member", id)
		Expect(first.Status).To(Equal(403), first.String())
		Expect(first.String()).NotTo(ContainSubstring("pen"))
		for _, n := range nodes {
			r := d.download(n, "member", id)
			Expect(r.Status).To(Equal(first.Status), n)
			Expect(r.String()).To(Equal(first.String()), n+": the same answer from every node")
			st, err := d.c.Token(n, "member").Do("GET", "/api/addons/artifacts/"+id, nil, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(st.Status).To(Equal(403), n+": "+st.String())
		}
		after, err := os.Stat(d.databaseFile(res))
		Expect(err).NotTo(HaveOccurred())
		Expect(after.ModTime()).To(Equal(db.ModTime()))
		Expect(d.receipts(res)).To(Equal(receipts))
		Expect(d.rowCount(cluster.Owner, "member", res)).To(Equal(1))
		Expect(snapshot(filepath.Join(d.c.Config.Nodes[cluster.Owner].Root, "backups"))).To(Equal(backups))
		// A new export is a new file with a new ID.
		fresh := d.export(cluster.FrontB, "member", res)
		Expect(fresh).NotTo(Equal(id))
		Expect(d.download(cluster.FrontA, "member", fresh).Status).To(Equal(200))
	})

	It("withdraws a transfer held in flight when the add-on is disabled and leaves no downloadable file", func() {
		arm("hold-output")
		DeferCleanup(func() { disarm("hold-output") })
		r := d.read(d.c.Token(cluster.FrontA, "member"), res, map[string]any{"tool": "rows-export", "table": "Entries"})
		Expect(r.Status).To(Equal(200), r.String())
		outs, _ := body(r)["outputs"].([]any)
		Expect(outs).To(HaveLen(1))
		id, _ := outs[0].(map[string]any)["handle"].(string)
		Expect(id).NotTo(BeEmpty())
		status := func(n string) (int, string) {
			st, err := d.c.Token(n, "member").Do("GET", "/api/addons/artifacts/"+id, nil, nil)
			Expect(err).NotTo(HaveOccurred())
			s, _ := body(st)["state"].(string)
			return st.Status, s
		}
		// The held stream cannot finish: the file is not downloadable on any node.
		for _, n := range nodes {
			code, state := status(n)
			Expect(code).To(Equal(200), n)
			Expect(state).To(Equal("transferring"), n)
			Expect(d.download(n, "member", id).Status).NotTo(Equal(200), n)
		}
		gen := d.gen
		Expect(d.lifecycle(cluster.FrontB, "disable", gen, "").Status).To(Equal(200))
		disarm("hold-output")
		Expect(d.lifecycle(cluster.FrontA, "enable", gen, "").Status).To(Equal(200))
		d.waitState("running")
		var final string
		Eventually(func() string {
			_, final = status(cluster.Owner)
			return final
		}, 30*time.Second, 200*time.Millisecond).ShouldNot(Equal("transferring"))
		Expect(final).To(BeElementOf("cancelled", "failed"))
		for _, n := range nodes {
			code, state := status(n)
			Expect(code).To(Equal(200), n)
			Expect(state).To(Equal(final), n)
			Expect(d.download(n, "member", id).Status).NotTo(Equal(200), n)
		}
		// The withdrawn bytes are gone and the quota is free: a new file works.
		Expect(filepath.Join(d.c.Config.Nodes[cluster.Owner].Root, "artifacts")).To(BeADirectory())
		var left []string
		_ = filepath.WalkDir(filepath.Join(d.c.Config.Nodes[cluster.Owner].Root, "artifacts"), func(p string, e fs.DirEntry, err error) error {
			if err == nil && !e.IsDir() && filepath.Base(p) == id {
				left = append(left, p)
			}
			return nil
		})
		Expect(left).To(BeEmpty(), "no byte of a withdrawn transfer stays on disk")
		fresh := d.export(cluster.FrontB, "member", res)
		Expect(d.download(cluster.Owner, "member", fresh).Status).To(Equal(200))
	})

	It("reports a change that committed and lost its answer as unknown, finds it by receipt and replays the same receipt once", func() {
		rows := d.rowCount(cluster.Owner, "member", res)
		receipts := len(d.receipts(res))
		pid := d.tablesPID()
		Expect(pid).NotTo(BeZero())
		op := opID("lost")
		rev := d.revisionOf(res)
		args := map[string]any{"table": "Entries", "rows": []any{row}}
		arm("crash-after-commit")
		lost := d.apply(cluster.FrontA, "member", res, op, "rows-insert", rev, args)
		Expect(lost.Status).To(Equal(503), lost.String())
		Expect(errType(lost)).To(Equal("outcome_unknown"))
		Expect(body(lost)["error"].(map[string]any)["operationId"]).To(Equal(op), "the original operation ID, from the frontend")
		d.waitTables(pid)
		// The lookup is read-only and finds the committed receipt.
		for _, n := range nodes {
			st := d.operation(n, "member", res, op)
			Expect(st["state"]).To(Equal("committed"), n)
		}
		Expect(d.rowCount(cluster.Owner, "member", res)).To(Equal(rows + 1))
		// The same operation again is the same receipt, not a second change.
		var first any
		for i, n := range []string{cluster.FrontB, cluster.Owner, cluster.FrontA} {
			again := d.apply(n, "member", res, op, "rows-insert", rev, args)
			Expect(again.Status).To(Equal(200), n+": "+again.String())
			if i == 0 {
				first = body(again)["revision"]
			}
			Expect(body(again)["revision"]).To(Equal(first), n)
		}
		Expect(d.rowCount(cluster.Owner, "member", res)).To(Equal(rows+1), "exactly one row")
		Expect(len(d.receipts(res))).To(Equal(receipts+1), "exactly one receipt")
	})
})
