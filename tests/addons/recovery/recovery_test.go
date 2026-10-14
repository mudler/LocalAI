// SPDX-License-Identifier: MIT
package recovery_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/auth"
	addonsvc "github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/core/services/addons/policy"
	"github.com/mudler/LocalAI/core/services/addons/tools"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// admin sends one recovery mutation as the administrator's own browser tab.
func (h *host) admin(path, op string, body map[string]any) reply {
	GinkgoHelper()
	body["operationId"] = op
	b, _ := json.Marshal(body)
	return h.send("POST", path, string(b), h.tab("alice", op))
}

func (h *host) status(user string) reply {
	GinkgoHelper()
	return h.send("GET", "/api/addons/tables/recovery", "", h.session(user))
}

func (h *host) stopTables() {
	GinkgoHelper()
	st, e := h.app.AddonManager().Status(context.Background(), "tables")
	Expect(e).NotTo(HaveOccurred())
	_, e = h.app.AddonManager().Submit(context.Background(), h.actor("alice"), addonsvc.LifecycleCommand{OperationID: fmt.Sprintf("disable-%d", time.Now().UnixNano()), AddonID: "tables", Action: "disable", Generation: st.Generation})
	Expect(e).NotTo(HaveOccurred())
	Eventually(func() string {
		list, _ := h.app.AddonManager().Apps(context.Background(), h.actor("alice"))
		for _, a := range list {
			if a.ID == "tables" {
				return a.State
			}
		}
		return "absent"
	}, 20*time.Second, 100*time.Millisecond).Should(Equal("stopped"))
}

func (h *host) startTables() {
	GinkgoHelper()
	st, e := h.app.AddonManager().Status(context.Background(), "tables")
	Expect(e).NotTo(HaveOccurred())
	_, e = h.app.AddonManager().Submit(context.Background(), h.actor("alice"), addonsvc.LifecycleCommand{OperationID: fmt.Sprintf("enable-%d", time.Now().UnixNano()), AddonID: "tables", Action: "enable", Generation: st.Generation})
	Expect(e).NotTo(HaveOccurred())
	h.running()
}

func (h *host) generation() uint64 {
	GinkgoHelper()
	st, e := h.app.AddonManager().Status(context.Background(), "tables")
	Expect(e).NotTo(HaveOccurred())
	return st.Generation
}

func backups(r reply) []map[string]any {
	GinkgoHelper()
	var out []map[string]any
	list, _ := r.body["backups"].([]any)
	for _, x := range list {
		out = append(out, x.(map[string]any))
	}
	return out
}

var _ = Describe("Add-on data recovery over the real owner", func() {
	It("makes a backup only for an administrator's own browser session and lists it", func() {
		h := ready(false)
		id := h.database("bob", "db-1", "Sales")
		Expect(id).NotTo(BeEmpty())
		// A person with the feature is not an administrator.
		Expect(h.send("POST", "/api/addons/tables/recovery/backup", `{"operationId":"b-1"}`, h.tab("bob", "b-1")).code).To(Equal(403))
		Expect(h.status("bob").code).To(Equal(403))
		// An administrator's API key is not a confirmation surface.
		Expect(h.send("POST", "/api/addons/tables/recovery/backup", `{"operationId":"b-1"}`, h.bearer("alice"), func(r *http.Request) { r.Header.Set("X-LocalAI-Confirm", "b-1") }).code).To(Equal(403))
		// The header must name the operation, and an operation is required.
		Expect(h.send("POST", "/api/addons/tables/recovery/backup", `{"operationId":"b-1"}`, h.tab("alice", "other")).code).To(Equal(403))
		Expect(h.send("POST", "/api/addons/tables/recovery/backup", `{}`, h.tab("alice", "")).code).To(Equal(400))
		Expect(h.send("POST", "/api/addons/Tables/recovery/backup", `{"operationId":"b-1"}`, h.tab("alice", "b-1")).code).To(Equal(403))
		got := h.admin("/api/addons/tables/recovery/backup", "b-1", map[string]any{})
		Expect(got.code).To(Equal(200), got.raw)
		backupID, _ := got.body["id"].(string)
		Expect(backupID).To(HavePrefix("b"))
		Expect(got.body["verified"]).To(BeTrue())
		// The same operation returns the same backup and makes no second one.
		again := h.admin("/api/addons/tables/recovery/backup", "b-1", map[string]any{})
		Expect(again.code).To(Equal(200))
		Expect(again.body["id"]).To(Equal(backupID))
		list := backups(h.status("alice"))
		Expect(list).To(HaveLen(1))
		Expect(list[0]["verification"]).To(Equal("verified"))
		// The add-on runs again and the database still answers.
		h.running()
		name, e := h.name("bob", id)
		Expect(e).NotTo(HaveOccurred())
		Expect(name).To(Equal("Sales"))
		// The backup holds the database file and is private.
		dir := filepath.Join(h.owner.Root, "backups", "tables", backupID)
		Expect(filepath.Join(dir, "data", "tables", "databases", id+".db")).To(BeAnExistingFile())
		info, e := os.Stat(dir)
		Expect(e).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o700)))
	})

	It("restores with warnings and does not give back a share that was revoked after the backup", func() {
		h := ready(false)
		id := h.database("bob", "db-1", "Sales")
		Expect(h.grant("bob", id, "carol", "editor").code).To(Equal(200))
		Expect(h.grant("bob", id, "dave", "viewer").code).To(Equal(200))
		got := h.admin("/api/addons/tables/recovery/backup", "b-1", map[string]any{})
		Expect(got.code).To(Equal(200), got.raw)
		backupID := got.body["id"].(string)
		// After the backup: carol loses access and the database is renamed.
		Expect(h.revoke("bob", id, "carol").code).To(Equal(200))
		rc := h.rename("bob", id, "rename-1", "Renamed", h.revision("bob", id))
		Expect(rc.code).To(Equal(200), rc.raw)
		// A restore needs the add-on stopped, the typed ID and a verified backup.
		running := h.admin("/api/addons/tables/recovery/restore", "r-1", map[string]any{"backupId": backupID, "confirm": "tables"})
		Expect(running.code).To(Equal(409))
		Expect(running.errType()).To(Equal("addon_running"))
		h.stopTables()
		wrong := h.admin("/api/addons/tables/recovery/restore", "r-2", map[string]any{"backupId": backupID, "confirm": "Tables"})
		Expect(wrong.code).To(Equal(400))
		Expect(wrong.errType()).To(Equal("confirmation_mismatch"))
		missing := h.admin("/api/addons/tables/recovery/restore", "r-3", map[string]any{"backupId": "b-nope", "confirm": "tables"})
		Expect(missing.code).To(Equal(404))
		Expect(missing.errType()).To(Equal("backup_not_found"))
		done := h.admin("/api/addons/tables/recovery/restore", "r-4", map[string]any{"backupId": backupID, "confirm": "tables"})
		Expect(done.code).To(Equal(200), done.raw)
		warnings, _ := done.body["warnings"].([]any)
		Expect(warnings).NotTo(BeEmpty())
		Expect(fmt.Sprint(warnings)).To(ContainSubstring("Changes made after that time are not in it"))
		prev, _ := done.body["previousCopy"].(string)
		Expect(prev).To(HavePrefix("previous-"))
		Expect(filepath.Join(h.owner.Root, "recovery", "tables", prev)).To(BeADirectory())
		// The same operation does not restore again.
		repeat := h.admin("/api/addons/tables/recovery/restore", "r-4", map[string]any{"backupId": backupID, "confirm": "tables"})
		Expect(repeat.code).To(Equal(200))
		Expect(repeat.body["at"]).To(Equal(done.body["at"]))
		h.startTables()
		// The data is as it was at the backup: the old name is back. Access
		// is as it is now: carol stays out, dave is a viewer.
		name, e := h.name("bob", id)
		Expect(e).NotTo(HaveOccurred())
		Expect(name).To(Equal("Sales"))
		_, e = h.name("carol", id)
		Expect(e).To(HaveOccurred())
		name, e = h.name("dave", id)
		Expect(e).NotTo(HaveOccurred())
		Expect(name).To(Equal("Sales"))
		Expect(h.grants("bob", id).code).To(Equal(200))
		g := h.grants("bob", id)
		Expect(g.raw).NotTo(ContainSubstring("carol"))
		Expect(g.raw).To(ContainSubstring("dave"))
		pol := done.body["policy"].(map[string]any)
		Expect(pol["grantsDropped"]).To(BeNumerically("==", 1))
		Expect(pol["revoked"]).To(BeNumerically("==", 1))
		st := h.status("alice")
		Expect(st.body["state"]).To(Equal("ready"))
		Expect(st.body["lastRestore"]).NotTo(BeNil())
	})

	It("restores only a backup of the add-on in the route and touches nothing otherwise", func() {
		h := ready(false)
		id := h.database("bob", "db-1", "Sales")
		got := h.admin("/api/addons/tables/recovery/backup", "b-1", map[string]any{})
		Expect(got.code).To(Equal(200), got.raw)
		backupID := got.body["id"].(string)
		rc := h.rename("bob", id, "rename-1", "Renamed", h.revision("bob", id))
		Expect(rc.code).To(Equal(200), rc.raw)
		h.stopTables()
		// The typed ID and the route name another add-on than the backup's.
		for _, path := range []string{"verify", "delete-backup"} {
			r := h.admin("/api/addons/notes/recovery/"+path, "x-"+path, map[string]any{"backupId": backupID})
			Expect(r.code).To(Equal(404), path+": "+r.raw)
		}
		bad := h.admin("/api/addons/notes/recovery/restore", "r-x", map[string]any{"backupId": backupID, "confirm": "notes"})
		Expect(bad.code).To(Equal(400), bad.raw)
		Expect(bad.errType()).To(Equal("invalid_request"))
		bad = h.admin("/api/addons/notes/recovery/restore", "r-y", map[string]any{"backupId": backupID, "confirm": "tables"})
		Expect(bad.code).To(Equal(400), bad.raw)
		Expect(filepath.Join(h.owner.Root, "backups", "tables", backupID)).To(BeADirectory())
		entries, _ := os.ReadDir(filepath.Join(h.owner.Root, "recovery", "tables"))
		for _, en := range entries {
			Expect(en.Name()).NotTo(HavePrefix("previous-"))
		}
		h.startTables()
		// Nothing was swapped in: the rename made after the backup stands.
		name, e := h.name("bob", id)
		Expect(e).NotTo(HaveOccurred())
		Expect(name).To(Equal("Renamed"))
	})

	It("gives a forwarding frontend the same typed answers as the owner", func() {
		h := ready(false)
		h.database("bob", "db-1", "Sales")
		front := h.frontend()
		backup := func(web *echo.Echo, op string) reply {
			GinkgoHelper()
			b, _ := json.Marshal(map[string]any{"operationId": op})
			return serve(web, "POST", "/api/addons/tables/recovery/backup", string(b), h.tab("alice", op))
		}
		ownerGot := backup(h.web, "p-1")
		frontGot := backup(front, "p-2")
		Expect(ownerGot.code).To(Equal(200), ownerGot.raw)
		Expect(frontGot.code).To(Equal(200), frontGot.raw)
		// A restore of a running add-on is refused the same way from both.
		ask := func(web *echo.Echo, op string) reply {
			b, _ := json.Marshal(map[string]any{"operationId": op, "backupId": ownerGot.body["id"], "confirm": "tables"})
			return serve(web, "POST", "/api/addons/tables/recovery/restore", string(b), h.tab("alice", op))
		}
		a, b := ask(h.web, "q-1"), ask(front, "q-2")
		Expect(a.code).To(Equal(409))
		Expect(b.code).To(Equal(a.code))
		Expect(b.errType()).To(Equal(a.errType()))
		// A person who is not an administrator is denied the same way.
		bob := serve(front, "GET", "/api/addons/tables/recovery", "", h.session("bob"))
		Expect(bob.code).To(Equal(403))
		list := serve(front, "GET", "/api/addons/tables/recovery", "", h.session("alice"))
		Expect(list.code).To(Equal(200), list.raw)
		Expect(backups(list)).To(HaveLen(2))
	})

	It("purges only with the typed ID, a stopped add-on and an explicit scope, and touches nothing else", func() {
		h := ready(false)
		id := h.database("bob", "db-1", "Sales")
		backupID := h.admin("/api/addons/tables/recovery/backup", "b-1", map[string]any{}).body["id"].(string)
		// Running: refused.
		got := h.admin("/api/addons/tables/purge", "x-1", map[string]any{"scope": map[string]any{"data": true}, "confirm": "tables"})
		Expect(got.code).To(Equal(409))
		Expect(got.errType()).To(Equal("addon_running"))
		h.stopTables()
		Expect(h.admin("/api/addons/tables/purge", "x-2", map[string]any{"scope": map[string]any{"data": true}, "confirm": "nope"}).errType()).To(Equal("confirmation_mismatch"))
		Expect(h.admin("/api/addons/tables/purge", "x-3", map[string]any{"scope": map[string]any{}, "confirm": "tables"}).errType()).To(Equal("invalid_request"))
		// A person with the feature cannot, and neither can a database
		// deletion or an uninstall do it: the data is still there.
		Expect(h.send("POST", "/api/addons/tables/purge", `{"operationId":"x-4","scope":{"data":true},"confirm":"tables"}`, h.tab("bob", "x-4")).code).To(Equal(403))
		Expect(h.dbFile(id)).To(BeAnExistingFile())
		done := h.admin("/api/addons/tables/purge", "x-5", map[string]any{"scope": map[string]any{"data": true}, "confirm": "tables"})
		Expect(done.code).To(Equal(200), done.raw)
		Expect(h.dbFile(id)).NotTo(BeAnExistingFile())
		// The backups were not in the scope.
		Expect(backups(h.status("alice"))).To(HaveLen(1))
		Expect(filepath.Join(h.owner.Root, "backups", "tables", backupID)).To(BeADirectory())
		// The owner no longer lists the database for its person.
		Expect(resourcesOf(h.list("bob"))).To(BeEmpty())
		bk := h.admin("/api/addons/tables/purge", "x-6", map[string]any{"scope": map[string]any{"backups": true}, "confirm": "tables"})
		Expect(bk.code).To(Equal(200), bk.raw)
		Expect(backups(h.status("alice"))).To(BeEmpty())
	})

	It("is never reachable by a model: no tool, no declared name and no route for an API key", func() {
		h := ready(false)
		h.database("bob", "db-1", "Sales")
		adapter := tools.NewToolAdapter(h.app.AddonGateway())
		defs, e := adapter.Discover(context.Background(), h.actor("alice"))
		Expect(e).NotTo(HaveOccurred())
		for _, d := range defs {
			Expect(d.Name).NotTo(ContainSubstring("recovery"))
			Expect(d.Name).NotTo(ContainSubstring("purge"))
			Expect(d.Name).NotTo(ContainSubstring("backup"))
			Expect(d.Name).NotTo(ContainSubstring("restore"))
		}
		for _, name := range []string{"addon__tables__recovery-backup", "addon__tables__purge", "addon__tables__recovery-restore", "addon__tables__recovery-status"} {
			_, e := adapter.Execute(context.Background(), h.actor("alice"), name, json.RawMessage(`{}`))
			Expect(e).To(HaveOccurred(), name)
		}
		// The typed dispatch of a key names the action as an administrator
		// action, but a key never reaches the human-surface mutations.
		for _, path := range []string{"/api/addons/tables/recovery/backup", "/api/addons/tables/recovery/restore", "/api/addons/tables/purge", "/api/addons/tables/recovery/prune"} {
			got := h.send("POST", path, `{"operationId":"k-1"}`, h.bearer("alice"))
			Expect(got.code).To(Equal(403), path)
		}
		// Nothing was made by those calls.
		Expect(backups(h.status("alice"))).To(BeEmpty())
		var manifest struct {
			Capabilities struct {
				Tools      []string `json:"tools"`
				AdminTools []string `json:"adminTools"`
			} `json:"capabilities"`
		}
		raw, e := os.ReadFile("../../../addons/tables/manifest.json")
		Expect(e).NotTo(HaveOccurred())
		Expect(json.Unmarshal(raw, &manifest)).To(Succeed())
		for _, t := range append(manifest.Capabilities.Tools, manifest.Capabilities.AdminTools...) {
			Expect(t).NotTo(MatchRegexp(`recovery|purge|backup|restore`))
		}
	})

	It("upgrades an add-on with data through a backup and a migration and keeps its databases", func() {
		h := ready(false)
		id := h.database("bob", "db-1", "Sales")
		Expect(h.grant("bob", id, "carol", "viewer").code).To(Equal(200))
		before := h.generation()
		_, e := h.app.AddonManager().Submit(context.Background(), h.actor("alice"), addonsvc.LifecycleCommand{OperationID: "upgrade-1", AddonID: "tables", Action: "upgrade", SourceID: "local", Version: "0.1.1"})
		Expect(e).NotTo(HaveOccurred())
		Expect(h.generation()).To(Equal(before + 1))
		h.running()
		// The data is intact and the shares are as they were.
		name, e := h.name("carol", id)
		Expect(e).NotTo(HaveOccurred())
		Expect(name).To(Equal("Sales"))
		// The add-on migrated its own data at start and said so.
		raw, e := os.ReadFile(filepath.Join(h.owner.Root, "data", "tables", "migration.json"))
		Expect(e).NotTo(HaveOccurred())
		Expect(string(raw)).To(ContainSubstring(`"complete"`))
		// The backup taken before is the recovery copy and is kept.
		list := backups(h.status("alice"))
		Expect(list).To(HaveLen(1))
		Expect(list[0]["reason"]).To(Equal("upgrade"))
		Expect(list[0]["pinned"]).To(BeTrue())
		Expect(list[0]["version"]).To(Equal("0.1.0"))
		st := h.status("alice")
		Expect(st.body["state"]).To(Equal("ready"))
		// The sole recovery copy cannot be deleted.
		del := h.admin("/api/addons/tables/recovery/delete-backup", "d-1", map[string]any{"backupId": list[0]["id"]})
		Expect(del.code).To(Equal(409))
		Expect(del.errType()).To(Equal("sole_recovery_copy"))
	})

	It("undoes an upgrade whose package does not start and runs the old generation on its own data", func() {
		h := ready(false)
		id := h.database("bob", "db-1", "Sales")
		before := h.generation()
		_, e := h.app.AddonManager().Submit(context.Background(), h.actor("alice"), addonsvc.LifecycleCommand{OperationID: "upgrade-bad", AddonID: "tables", Action: "upgrade", SourceID: "local", Version: "0.2.0"})
		Expect(e).To(HaveOccurred())
		Expect(h.generation()).To(Equal(before))
		h.running()
		name, e := h.name("bob", id)
		Expect(e).NotTo(HaveOccurred())
		Expect(name).To(Equal("Sales"))
		st := h.status("alice")
		Expect(st.body["state"]).To(Equal("ready"))
		// The backup that made it safe is still there.
		Expect(backups(st)).To(HaveLen(1))
		// A new database can be made: nothing is blocked.
		Expect(h.create("bob", "db-2", "More").code).To(Equal(200))
	})

	It("hands retained data only to the same trusted source on a reinstall", func() {
		h := ready(false)
		id := h.database("bob", "db-1", "Sales")
		ctx := context.Background()
		st, e := h.app.AddonManager().Status(ctx, "tables")
		Expect(e).NotTo(HaveOccurred())
		_, e = h.app.AddonManager().Submit(ctx, h.actor("alice"), addonsvc.LifecycleCommand{OperationID: "uninstall-1", AddonID: "tables", Action: "uninstall"})
		Expect(e).NotTo(HaveOccurred())
		_ = st
		// The same library under another source ID is another trusted
		// identity: the data stays where it is.
		lib := h.libraryURL
		Expect(h.app.AddonSources().Save(ctx, h.actor("alice"), catalog.Source{ID: "mirror", Name: "Mirror", URL: lib, Enabled: true, AllowLoopbackHTTP: true})).To(Succeed())
		_, e = h.app.AddonSources().Refresh(ctx, "mirror")
		Expect(e).NotTo(HaveOccurred())
		_, e = h.app.AddonManager().Submit(ctx, h.actor("alice"), addonsvc.LifecycleCommand{OperationID: "install-other", AddonID: "tables", Action: "install", SourceID: "mirror", Version: "0.1.0"})
		Expect(e).To(HaveOccurred())
		Expect(h.dbFile(id)).To(BeAnExistingFile())
		// The original source is accepted and the data is served again.
		_, e = h.app.AddonManager().Submit(ctx, h.actor("alice"), addonsvc.LifecycleCommand{OperationID: "install-same", AddonID: "tables", Action: "install", SourceID: "local", Version: "0.1.0"})
		Expect(e).NotTo(HaveOccurred())
		sts, e := h.app.AddonManager().Status(ctx, "tables")
		Expect(e).NotTo(HaveOccurred())
		_, e = h.app.AddonManager().Submit(ctx, h.actor("alice"), addonsvc.LifecycleCommand{OperationID: "enable-again", AddonID: "tables", Action: "enable", Generation: sts.Generation})
		Expect(e).NotTo(HaveOccurred())
		h.running()
		name, e := h.name("bob", id)
		Expect(e).NotTo(HaveOccurred())
		Expect(name).To(Equal("Sales"))
	})

	It("documents every route and every emitted field in the Swagger spec, the instructions and the feature registry", func() {
		spec, err := os.ReadFile("../../../swagger/swagger.json")
		Expect(err).NotTo(HaveOccurred())
		var doc struct {
			Paths       map[string]map[string]json.RawMessage `json:"paths"`
			Definitions map[string]struct {
				Properties map[string]any `json:"properties"`
			} `json:"definitions"`
		}
		Expect(json.Unmarshal(spec, &doc)).To(Succeed())
		for path, method := range map[string]string{
			"/api/addons/{id}/recovery":               "get",
			"/api/addons/{id}/recovery/backup":        "post",
			"/api/addons/{id}/recovery/verify":        "post",
			"/api/addons/{id}/recovery/restore":       "post",
			"/api/addons/{id}/recovery/delete-backup": "post",
			"/api/addons/{id}/recovery/prune":         "post",
			"/api/addons/{id}/purge":                  "post",
		} {
			Expect(doc.Paths).To(HaveKey(path))
			text := string(doc.Paths[path][method])
			Expect(text).To(ContainSubstring("MCP"), path)
			Expect(text).To(ContainSubstring(`"Addons"`), path)
		}
		h := ready(false)
		h.database("bob", "db-1", "Sales")
		backup := h.admin("/api/addons/tables/recovery/backup", "b-1", map[string]any{})
		Expect(backup.code).To(Equal(200))
		// Emitted bodies and documented definitions list the same fields.
		check := func(name string, got map[string]any) {
			GinkgoHelper()
			def, ok := doc.Definitions[name]
			Expect(ok).To(BeTrue(), name)
			for field := range got {
				Expect(def.Properties).To(HaveKey(field), name+"."+field)
			}
		}
		check("recovery.BackupReceipt", backup.body)
		st := h.status("alice")
		check("recovery.Status", st.body)
		for _, b := range backups(st) {
			check("recovery.BackupInfo", b)
		}
		h.stopTables()
		rec := h.admin("/api/addons/tables/recovery/restore", "r-1", map[string]any{"backupId": backup.body["id"], "confirm": "tables"})
		Expect(rec.code).To(Equal(200), rec.raw)
		check("recovery.RestoreReceipt", rec.body)
		check("recovery.RestoreReport", rec.body["policy"].(map[string]any))
		purge := h.admin("/api/addons/tables/purge", "p-1", map[string]any{"scope": map[string]any{"recoveryCopies": true}, "confirm": "tables"})
		Expect(purge.code).To(Equal(200), purge.raw)
		check("recovery.PurgeReceipt", purge.body)
		check("recovery.PurgeScope", purge.body["scope"].(map[string]any))
		registered := map[string]bool{}
		for _, entry := range auth.RouteFeatureRegistry {
			if entry.Feature == auth.FeatureAddons {
				registered[entry.Method+" "+entry.Pattern] = true
			}
		}
		for _, key := range []string{"GET /api/addons/:id/recovery", "POST /api/addons/:id/recovery/backup", "POST /api/addons/:id/recovery/verify", "POST /api/addons/:id/recovery/restore", "POST /api/addons/:id/recovery/delete-backup", "POST /api/addons/:id/recovery/prune", "POST /api/addons/:id/purge"} {
			Expect(registered).To(HaveKey(key))
		}
		ins := h.send("GET", "/api/instructions/addons", "", h.bearer("alice"))
		Expect(ins.code).To(Equal(200))
		for _, want := range []string{"/api/addons/{id}/recovery", "needs-recovery", "purge", "VACUUM"} {
			if want == "VACUUM" {
				continue
			}
			Expect(ins.raw).To(ContainSubstring(want), want)
		}
		docs, err := os.ReadFile("../../../docs/content/features/addons.md")
		Expect(err).NotTo(HaveOccurred())
		for _, want := range []string{"needs-recovery", "sole recovery copy", "retained data", "ownership mapping", "migration.json"} {
			Expect(strings.ToLower(string(docs))).To(ContainSubstring(strings.ToLower(want)), want)
		}
		_ = policy.ModeNamedUser
		_ = contract.Actor{}
	})
})
