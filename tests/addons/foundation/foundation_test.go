// SPDX-License-Identifier: MIT
package foundation_test

import (
	"time"

	"os"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/tests/addons/foundation/cluster"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// lifecycle submits one administrator lifecycle action.
func lifecycle(cl cluster.Client, id, action string, generation uint64, version string) *cluster.Response {
	args := map[string]any{}
	if version != "" {
		args = map[string]any{"sourceId": "fixture", "version": version}
	}
	return dispatch(cl, map[string]any{"addonId": id, "action": action, "generation": generation, "arguments": args})
}

// resource registers one resource on the owner and returns its identifier.
func resource(c *cluster.Cluster, addon, owner string, grants map[string]string) string {
	GinkgoHelper()
	r, err := c.Control("resource", map[string]any{"addonId": addon, "owner": owner, "grants": grants})
	Expect(err).NotTo(HaveOccurred())
	Expect(r.Status).To(Equal(200), r.String())
	var out struct {
		ResourceID string `json:"resourceId"`
	}
	Expect(r.JSON(&out)).To(Succeed())
	return out.ResourceID
}

type result struct {
	Payload struct {
		Actor     string         `json:"actor"`
		Arguments map[string]any `json:"arguments"`
	} `json:"payload"`
	Outputs []struct {
		Handle    string `json:"handle"`
		Name      string `json:"name"`
		MediaType string `json:"mediaType"`
		Size      int64  `json:"size"`
	} `json:"outputs"`
}

// read is a typed read of a resource through one client.
func read(cl cluster.Client, addon, rid string, generation uint64, args map[string]any) *cluster.Response {
	return dispatch(cl, map[string]any{"addonId": addon, "resourceId": rid, "generation": generation, "action": "read", "arguments": args})
}

func actorOf(r *cluster.Response) string {
	GinkgoHelper()
	Expect(r.Status).To(Equal(200), r.String())
	var out result
	Expect(r.JSON(&out)).To(Succeed())
	return out.Payload.Actor
}

func errorType(r *cluster.Response) string {
	var e apiError
	_ = r.JSON(&e)
	return e.Error.Type
}

// lines counts the non-empty lines of a durable fixture log.
func lines(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n := 0
	for _, l := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(l) != "" {
			n++
		}
	}
	return n
}

// fixtureChildren counts the live fixture processes that are children of pid.
func fixtureChildren(pid int) int {
	n := 0
	for _, cmdline := range cluster.ChildrenOf(pid) {
		if strings.Contains(cmdline, "bin/fixture") {
			n++
		}
	}
	return n
}

func dataDir(c *cluster.Cluster, id string) string {
	return filepath.Join(c.Config.Nodes[cluster.Owner].Root, "data", id)
}

// retainData writes a marker the add-on's retained data must keep.
func retainData(c *cluster.Cluster, id string) string {
	GinkgoHelper()
	dir := dataDir(c, id)
	Expect(os.MkdirAll(dir, 0o700)).To(Succeed())
	marker := filepath.Join(dir, "rows.db")
	Expect(os.WriteFile(marker, []byte("retained rows"), 0o600)).To(Succeed())
	return marker
}

func expectRetained(marker string) {
	GinkgoHelper()
	b, err := os.ReadFile(marker)
	Expect(err).NotTo(HaveOccurred(), "retained data must survive")
	Expect(string(b)).To(Equal("retained rows"))
}

var forged = map[string]string{
	"X-Forwarded-Actor": "fnd-admin", "X-LocalAI-Actor": "fnd-admin", "X-LocalAI-User": "fnd-admin",
	"X-Actor-Id": "fnd-admin", "X-Forwarded-User": "fnd-admin", "X-Remote-User": "fnd-admin",
	"X-LocalAI-Credential": "key:forged", "X-LocalAI-Authority": `{"actor":{"userId":"fnd-admin"},"scope":"read"}`,
}

var _ = Describe("foundation fixture through the owner and two frontends", Ordered, func() {
	var (
		c        *cluster.Cluster
		admin    cluster.Client
		hostBin  string
		hostUI   string
		binSum   string
		uiSum    string
		toolRes  string
		uiRes    string
		svcRes   string
		exportID string
	)
	front := []string{cluster.FrontA, cluster.FrontB}

	BeforeAll(func() {
		hostBin = paths.Server
		hostUI = filepath.Join(repoRoot, "core/http/react-ui/dist")
		if _, err := os.Stat(hostUI); err != nil {
			hostUI = ""
		}
		binSum = hashTree(hostBin)
		if hostUI != "" {
			uiSum = hashTree(hostUI)
		}
		c = start()
		Expect(c.Library.Add(cluster.UIPackage("1.0.0", nil), c.Effects)).To(Succeed())
		Expect(c.Library.Add(cluster.UIPackage("2.0.0", nil), c.Effects)).To(Succeed())
		Expect(c.Library.Add(cluster.ToolPackage("1.0.0", nil), c.Effects)).To(Succeed())
		Expect(c.Library.Add(cluster.ServicePackage("1.0.0", map[string]any{"mode": "lose-reply"}), c.Effects)).To(Succeed())
		Expect(c.Up()).To(Succeed())
		admin = c.Token(cluster.FrontA, "admin")
	})

	It("installs the UI, tool and service-only packages from the catalog through a frontend without starting them and leaves the host binary and UI build unchanged", func() {
		for _, id := range []string{"found-ui", "found-tool", "found-svc"} {
			r := lifecycle(admin, id, "install", 0, "1.0.0")
			Expect(r.Status).To(Equal(200), r.String())
		}
		status, list := apps(admin)
		Expect(status).To(Equal(200))
		Expect(list).To(HaveLen(3))
		for _, a := range list {
			Expect(a.Enabled).To(BeFalse(), a.ID)
			Expect(a.State).To(Equal("stopped"), a.ID)
		}
		Expect(fixtureChildren(c.Nodes[cluster.Owner].PID())).To(BeZero(), "installing must not start any process")
		Expect(hashTree(hostBin)).To(Equal(binSum), "the host binary must not change")
		if hostUI != "" {
			Expect(hashTree(hostUI)).To(Equal(uiSum), "the shipped UI build must not change")
		}
	})

	It("activates each package through the other frontend and runs one process per add-on under the owner only", func() {
		viaB := c.Token(cluster.FrontB, "admin")
		for _, id := range []string{"found-ui", "found-tool", "found-svc"} {
			r := lifecycle(viaB, id, "enable", 1, "")
			Expect(r.Status).To(Equal(200), r.String())
		}
		settle("three running add-ons", 30*time.Second, func() bool {
			_, list := apps(admin)
			n := 0
			for _, a := range list {
				if a.State == "running" {
					n++
				}
			}
			return n == 3
		})
		Expect(fixtureChildren(c.Nodes[cluster.Owner].PID())).To(Equal(3))
		for _, f := range front {
			Expect(fixtureChildren(c.Nodes[f].PID())).To(BeZero(), f+" must never start an add-on")
			Expect(filepath.Join(c.Config.Nodes[f].Root)).NotTo(BeADirectory(), f+" must not open add-on storage")
		}
		toolRes = resource(c, "found-tool", "member", map[string]string{"friend": "viewer"})
		uiRes = resource(c, "found-ui", "member", map[string]string{"friend": "viewer"})
		svcRes = resource(c, "found-svc", "member", nil)
	})

	It("lists the same apps from the owner and both frontends and shows only what each declared", func() {
		var seen [][]string
		for _, n := range []string{cluster.Owner, cluster.FrontA, cluster.FrontB} {
			status, list := apps(c.Token(n, "member"))
			Expect(status).To(Equal(200), n)
			byID := map[string]app{}
			ids := []string{}
			for _, a := range list {
				byID[a.ID] = a
				ids = append(ids, a.ID+":"+a.State)
			}
			seen = append(seen, ids)
			Expect(byID["found-ui"].HasUI).To(BeTrue())
			Expect(byID["found-tool"].HasUI).To(BeFalse(), "the tool package declares no UI")
			Expect(byID["found-tool"].UI).To(BeNil())
			Expect(byID["found-tool"].Tools).To(ConsistOf("echo", "export"))
			Expect(byID["found-svc"].HasUI).To(BeFalse())
			Expect(byID["found-svc"].Tools).To(BeEmpty(), "the service-only package declares no tool")
		}
		Expect(seen[1]).To(ConsistOf(seen[0]))
		Expect(seen[2]).To(ConsistOf(seen[0]))
	})

	It("serves reads of the UI, tool and service-only packages alternately through the owner and both frontends", func() {
		nodes := []string{cluster.FrontA, cluster.FrontB, cluster.Owner, cluster.FrontB, cluster.FrontA, cluster.Owner}
		for i, n := range nodes {
			Expect(actorOf(read(c.Token(n, "member"), "found-ui", uiRes, 1, map[string]any{"tool": "echo", "i": i}))).To(Equal("fnd-member"), n)
			Expect(actorOf(read(c.Token(n, "friend"), "found-tool", toolRes, 1, map[string]any{"tool": "echo"}))).To(Equal("fnd-friend"), n)
			Expect(actorOf(read(c.Token(n, "member"), "found-svc", svcRes, 1, map[string]any{"q": "plain"}))).To(Equal("fnd-member"), n)
		}
	})

	It("denies an account without the feature, a stranger to the resource and an anonymous caller the same way on every node", func() {
		for _, n := range []string{cluster.Owner, cluster.FrontA, cluster.FrontB} {
			r := read(c.Token(n, "outsider"), "found-tool", toolRes, 1, map[string]any{"tool": "echo"})
			Expect(r.Status).To(Equal(403), n+" outsider: "+r.String())
			// The friend has a grant on the tool resource only.
			r = read(c.Token(n, "friend"), "found-svc", svcRes, 1, map[string]any{})
			Expect(r.Status).To(Equal(403), n+" friend on a foreign resource: "+r.String())
			anon, err := cluster.Client{Base: c.Nodes[n].URL()}.Do("POST", "/api/addons/dispatch", `{"addonId":"found-tool","action":"read"}`, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(anon.Status).To(BeElementOf(401, 403), n)
		}
	})

	It("takes the actor from the authenticated credential only and ignores forged actor and authority headers", func() {
		for _, n := range []string{cluster.FrontA, cluster.FrontB, cluster.Owner} {
			cl := c.Token(n, "member")
			cl.Headers = forged
			Expect(actorOf(read(cl, "found-tool", toolRes, 1, map[string]any{"tool": "echo"}))).To(Equal("fnd-member"), n)
			// A forged administrator cannot reach lifecycle either.
			r := lifecycle(cl, "found-tool", "disable", 1, "")
			Expect(r.Status).To(Equal(403), n)
		}
		// A body that claims another actor is not part of the request type.
		r := dispatch(c.Token(cluster.FrontA, "member"), map[string]any{"addonId": "found-tool", "resourceId": toolRes, "generation": 1, "action": "read", "actor": "fnd-admin", "arguments": map[string]any{"tool": "echo"}})
		Expect(r.Status).To(Equal(400), r.String())
	})

	It("denies a tool the add-on did not declare and a generation that is not current on every node", func() {
		for _, n := range []string{cluster.FrontA, cluster.FrontB, cluster.Owner} {
			m := c.Token(n, "member")
			r := read(m, "found-tool", toolRes, 1, map[string]any{"tool": "drop-everything"})
			Expect(r.Status).To(Equal(403), n+": "+r.String())
			r = read(m, "found-tool", toolRes, 7, map[string]any{"tool": "echo"})
			Expect(r.Status).To(Equal(403), n+": "+r.String())
			// Without a tool name only the generation of the running add-on
			// can refuse the call.
			r = read(m, "found-tool", toolRes, 7, map[string]any{"q": "plain"})
			Expect(r.Status).To(Equal(403), n+" plain read of a stale generation: "+r.String())
		}
	})

	It("relays JSON and a private file result and downloads the file through a different frontend than the one that produced it", func() {
		m := c.Token(cluster.FrontA, "member")
		r := read(m, "found-tool", toolRes, 1, map[string]any{"tool": "export"})
		Expect(r.Status).To(Equal(200), r.String())
		var out result
		Expect(r.JSON(&out)).To(Succeed())
		Expect(out.Outputs).To(HaveLen(1))
		exportID = out.Outputs[0].Handle
		Expect(exportID).NotTo(Equal("report"), "the host never hands out the add-on's own handle")
		Expect(out.Outputs[0].Name).To(Equal("report.txt"))

		viaB := c.Token(cluster.FrontB, "member")
		// The file result is transferred in the background: the read returns
		// the artifact as transferring, and a download answers 403 until it
		// is complete. Wait on the documented status lookup, bounded, so a
		// loaded machine cannot turn the transfer time into a failure.
		Eventually(func(g Gomega) string {
			st, err := viaB.Do("GET", "/api/addons/artifacts/"+exportID, nil, nil)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(st.Status).To(Equal(200), st.String())
			var a struct {
				State string `json:"state"`
			}
			g.Expect(st.JSON(&a)).To(Succeed())
			return a.State
		}, 30*time.Second, 100*time.Millisecond).Should(Equal("complete"))
		dl, err := viaB.Do("GET", "/api/addons/artifacts/"+exportID+"/download", nil, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(dl.Status).To(Equal(200))
		Expect(dl.String()).To(Equal("fixture\n"))
		Expect(dl.Header.Get("Content-Disposition")).To(ContainSubstring("attachment"))
		// Another account, even one with a grant on the resource, is denied
		// the bytes of someone else's result.
		other, err := c.Token(cluster.FrontB, "friend").Do("GET", "/api/addons/artifacts/"+exportID+"/download", nil, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(other.Status).To(Equal(403))

		j, err := viaB.Do("POST", "/api/addons/relay", map[string]any{"kind": "json", "request": map[string]any{
			"addonId": "found-tool", "resourceId": toolRes, "generation": 1, "operationId": nextID("json"), "action": "read", "arguments": map[string]any{"tool": "echo", "shape": "json"}}}, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(j.Status).To(Equal(200), j.String())
		Expect(j.String()).To(ContainSubstring("fnd-member"))
	})

	It("relays lifecycle status and progress of an operation to the administrator who made it and to nobody else", func() {
		r := lifecycle(admin, "found-tool", "enable", 1, "")
		// The add-on is already enabled: an identical enable is an idempotent
		// no-op or a conflict, but never a server error.
		Expect(r.Status).To(BeNumerically("<", 500), r.String())
		op := nextID("progress")
		r = dispatch(admin, map[string]any{"operationId": op, "addonId": "found-svc", "action": "enable", "generation": 1})
		Expect(r.Status).To(BeNumerically("<", 500), r.String())
		for _, kind := range []string{"status", "progress"} {
			rel, err := c.Token(cluster.FrontB, "admin").Do("POST", "/api/addons/relay", map[string]any{"kind": kind, "request": map[string]any{
				"addonId": "found-svc", "generation": 1, "operationId": op, "action": "operation-status", "arguments": map[string]any{}}}, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(rel.Status).To(Equal(200), kind+": "+rel.String())
			Expect(rel.String()).To(ContainSubstring("completed"), kind)
			// Another account never sees the operation.
			no, err := c.Token(cluster.FrontB, "member").Do("POST", "/api/addons/relay", map[string]any{"kind": kind, "request": map[string]any{
				"addonId": "found-svc", "generation": 1, "operationId": op, "action": "operation-status", "arguments": map[string]any{}}}, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(no.Status).To(Equal(403), kind)
		}
	})

	It("serves the immutable UI assets by package digest through a non-owner frontend and refuses everything else", func() {
		a := appOf(c.Token(cluster.FrontA, "member"), "found-ui")
		Expect(a).NotTo(BeNil())
		Expect(a.UI).NotTo(BeNil())
		for _, n := range front {
			r, err := c.Token(n, "member").Do("GET", a.UI.Entry, nil, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(r.Status).To(Equal(200), n)
			Expect(r.String()).To(ContainSubstring("createAddon"))
			Expect(r.Header.Get("Content-Type")).To(ContainSubstring("javascript"))
			Expect(r.Header.Get("Cache-Control")).To(ContainSubstring("immutable"))
			// The same asset without a credential is refused.
			anon, err := cluster.Client{Base: c.Nodes[n].URL()}.Do("GET", a.UI.Entry, nil, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(anon.Status).To(BeElementOf(401, 403))
			out, err := c.Token(n, "outsider").Do("GET", a.UI.Entry, nil, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(out.Status).To(Equal(403))
		}
		base := strings.TrimSuffix(a.UI.Entry, "ui.js")
		for _, bad := range []string{base + "private.txt", base + "../manifest.json", base + "bin/fixture", strings.Replace(base, a.UI.Entry[len("/api/addons/found-ui/assets/"):len("/api/addons/found-ui/assets/")+64], strings.Repeat("0", 64), 1) + "ui.js"} {
			r, err := c.Token(cluster.FrontB, "member").Do("GET", bad, nil, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(r.Status).To(BeElementOf(400, 403, 404), bad)
			Expect(r.String()).NotTo(ContainSubstring("not declared"))
		}
		// The tool package has no UI: there is nothing to serve.
		r, err := c.Token(cluster.FrontB, "member").Do("GET", "/api/addons/found-tool/assets/"+strings.Repeat("a", 64)+"/ui.js", nil, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Status).To(BeElementOf(403, 404))
	})

	It("applies one confirmed change exactly once through a different frontend than the one that proposed it", func() {
		pid := nextID("write")
		r, err := c.Token(cluster.FrontA, "member").Do("POST", "/api/addons/proposals", map[string]any{
			"addonId": "found-tool", "resourceId": toolRes, "generation": 1, "operationId": pid, "action": "write", "arguments": map[string]any{"note": "one"}}, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Status).To(Equal(200), r.String())
		var p struct {
			ID, Digest, State string
		}
		Expect(r.JSON(&p)).To(Succeed())
		Expect(p.State).To(Equal("pending"))
		// A bearer key can never confirm.
		bad, err := c.Token(cluster.FrontB, "member").Do("POST", "/api/addons/proposals/"+pid+"/confirm", map[string]any{"digest": p.Digest}, map[string]string{"X-LocalAI-Confirm": pid})
		Expect(err).NotTo(HaveOccurred())
		Expect(bad.Status).To(Equal(403))
		// Another account cannot confirm it either.
		other, err := c.Session(cluster.FrontB, "friend").Do("POST", "/api/addons/proposals/"+pid+"/confirm", map[string]any{"digest": p.Digest}, map[string]string{"X-LocalAI-Confirm": pid})
		Expect(err).NotTo(HaveOccurred())
		Expect(other.Status).To(Equal(403))
		Expect(lines(filepath.Join(c.Effects, "found-tool.log"))).To(BeZero())

		confirm := func(node string) *cluster.Response {
			resp, err := c.Session(node, "member").Do("POST", "/api/addons/proposals/"+pid+"/confirm", map[string]any{"digest": p.Digest}, map[string]string{"X-LocalAI-Confirm": pid})
			Expect(err).NotTo(HaveOccurred())
			return resp
		}
		first := confirm(cluster.FrontB)
		Expect(first.Status).To(Equal(200), first.String())
		Expect(first.String()).To(ContainSubstring(`"state":"applied"`))
		again := confirm(cluster.FrontA)
		Expect(again.Status).To(Equal(200), again.String())
		Expect(again.String()).To(ContainSubstring(`"state":"applied"`))
		Expect(lines(filepath.Join(c.Effects, "found-tool.log"))).To(Equal(1), "one committed effect")
		Expect(lines(filepath.Join(c.Effects, "found-tool.log.applies"))).To(Equal(1), "one apply request reached the add-on")
	})

	It("reports a lost reply as outcome unknown with the original operation ID and settles it by receipt lookup without a second apply", func() {
		pid := nextID("lost")
		r, err := c.Token(cluster.FrontA, "member").Do("POST", "/api/addons/proposals", map[string]any{
			"addonId": "found-svc", "resourceId": svcRes, "generation": 1, "operationId": pid, "action": "write", "arguments": map[string]any{"note": "lost"}}, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Status).To(Equal(200), r.String())
		var p struct{ Digest string }
		Expect(r.JSON(&p)).To(Succeed())
		confirm := func(node string) *cluster.Response {
			resp, err := c.Session(node, "member").Do("POST", "/api/addons/proposals/"+pid+"/confirm", map[string]any{"digest": p.Digest}, map[string]string{"X-LocalAI-Confirm": pid})
			Expect(err).NotTo(HaveOccurred())
			return resp
		}
		// The owner and each frontend report the same original operation.
		first := confirm(cluster.FrontB)
		Expect(first.Status).To(Equal(503), first.String())
		var e apiError
		Expect(first.JSON(&e)).To(Succeed())
		Expect(e.Error.Type).To(Equal("outcome_unknown"))
		Expect(e.Error.OperationID).To(Equal(pid))
		Expect(lines(filepath.Join(c.Effects, "found-svc.log"))).To(Equal(1), "the change did commit")
		// Repeating the confirmation from every node never submits it again.
		for _, n := range []string{cluster.FrontA, cluster.Owner, cluster.FrontB} {
			rep := confirm(n)
			if rep.Status == 503 {
				Expect(errorType(rep)).To(Equal("outcome_unknown"), n)
				var again apiError
				Expect(rep.JSON(&again)).To(Succeed())
				Expect(again.Error.OperationID).To(Equal(pid), n)
			}
		}
		Expect(lines(filepath.Join(c.Effects, "found-svc.log.applies"))).To(Equal(1), "no second apply may reach the add-on")
		// A status lookup is read-only and settles by receipt.
		rec, err := c.Session(cluster.FrontA, "member").Do("POST", "/api/addons/proposals/"+pid+"/reconcile", nil, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.Status).To(Equal(200), rec.String())
		Expect(rec.String()).To(ContainSubstring(`"state":"applied"`))
		Expect(lines(filepath.Join(c.Effects, "found-svc.log"))).To(Equal(1))
		Expect(lines(filepath.Join(c.Effects, "found-svc.log.applies"))).To(Equal(1))
	})

	It("upgrades to a new generation, denies the previous generation and its assets, and keeps the retained data", func() {
		marker := retainData(c, "found-ui")
		before := appOf(admin, "found-ui")
		Expect(before.Generation).To(Equal(uint64(1)))
		r := lifecycle(admin, "found-ui", "upgrade", 0, "2.0.0")
		Expect(r.Status).To(Equal(200), r.String())
		settle("the upgraded add-on running", 30*time.Second, func() bool {
			a := appOf(admin, "found-ui")
			return a != nil && a.Generation == 2 && a.State == "running"
		})
		expectRetained(marker)
		for _, n := range []string{cluster.FrontA, cluster.FrontB, cluster.Owner} {
			stale := read(c.Token(n, "member"), "found-ui", uiRes, 1, map[string]any{"q": "plain"})
			Expect(stale.Status).To(Equal(403), n+" stale generation: "+stale.String())
			Expect(actorOf(read(c.Token(n, "member"), "found-ui", uiRes, 2, map[string]any{"tool": "echo"}))).To(Equal("fnd-member"), n)
		}
		old, err := c.Token(cluster.FrontB, "member").Do("GET", before.UI.Entry, nil, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(old.Status).To(BeElementOf(403, 404), "the previous package digest is no longer served")
		now := appOf(c.Token(cluster.FrontB, "member"), "found-ui")
		Expect(now.UI.Entry).NotTo(Equal(before.UI.Entry))
		cur, err := c.Token(cluster.FrontB, "member").Do("GET", now.UI.Entry, nil, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(cur.Status).To(Equal(200))
	})

	It("disables, removes and reinstalls an add-on while its data and grants remain", func() {
		marker := retainData(c, "found-tool")
		Expect(lifecycle(admin, "found-tool", "disable", 1, "").Status).To(Equal(200))
		for _, n := range []string{cluster.FrontA, cluster.FrontB, cluster.Owner} {
			r := read(c.Token(n, "member"), "found-tool", toolRes, 1, map[string]any{"tool": "echo"})
			Expect(r.Status).To(BeElementOf(403, 503), n+" disabled: "+r.String())
		}
		Expect(fixtureChildren(c.Nodes[cluster.Owner].PID())).To(Equal(2))
		Expect(lifecycle(admin, "found-tool", "uninstall", 0, "").Status).To(Equal(200))
		expectRetained(marker)
		Expect(lifecycle(admin, "found-tool", "install", 0, "1.0.0").Status).To(Equal(200))
		cur := appOf(admin, "found-tool")
		Expect(cur).NotTo(BeNil())
		Expect(lifecycle(admin, "found-tool", "enable", cur.Generation, "").Status).To(Equal(200))
		settle("the reinstalled add-on running", 30*time.Second, func() bool {
			a := appOf(admin, "found-tool")
			return a != nil && a.State == "running"
		})
		expectRetained(marker)
		g := appOf(admin, "found-tool").Generation
		// The grants of the resource outlived the removal: the friend reads it.
		Expect(actorOf(read(c.Token(cluster.FrontB, "friend"), "found-tool", toolRes, g, map[string]any{"tool": "echo"}))).To(Equal("fnd-friend"))
	})

	It("never changes the host binary or the shipped UI build over the whole lifecycle", func() {
		Expect(hashTree(hostBin)).To(Equal(binSum))
		if hostUI != "" {
			Expect(hashTree(hostUI)).To(Equal(uiSum))
		}
	})
})
