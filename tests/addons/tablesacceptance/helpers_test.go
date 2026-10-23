// SPDX-License-Identifier: MIT
package tablesacceptance_test

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/mudler/LocalAI/tests/addons/foundation/cluster"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var nodes = []string{cluster.Owner, cluster.FrontA, cluster.FrontB}

var opCounter atomic.Int64

func opID(prefix string) string {
	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().UnixNano()%1_000_000_000, opCounter.Add(1))
}

// body decodes a JSON answer into a generic map.
func body(r *cluster.Response) map[string]any {
	GinkgoHelper()
	out := map[string]any{}
	_ = json.Unmarshal(r.Body, &out)
	return out
}

// errType is the typed error of an answer, or the empty string.
func errType(r *cluster.Response) string {
	e, _ := body(r)["error"].(map[string]any)
	s, _ := e["type"].(string)
	return s
}

// deployment is a running cluster with Tables installed and enabled.
type deployment struct {
	c   *cluster.Cluster
	gen uint64
}

// deploy installs and enables the packaged Tables through a frontend, as an
// administrator does, and waits until it runs.
func deploy(c *cluster.Cluster, version string) *deployment {
	GinkgoHelper()
	d := &deployment{c: c}
	admin := c.Token(cluster.FrontA, "admin")
	for _, action := range []string{"install", "enable"} {
		args := map[string]any{}
		if action == "install" {
			args = map[string]any{"sourceId": "fixture", "version": version}
		}
		r := d.dispatch(admin, map[string]any{"addonId": "tables", "generation": 1, "action": action, "arguments": args})
		Expect(r.Status).To(Equal(200), action+": "+r.String())
	}
	d.waitState("running")
	return d
}

func (d *deployment) app(client cluster.Client) map[string]any {
	GinkgoHelper()
	r, err := client.Do("GET", "/api/addons/apps", nil, nil)
	Expect(err).NotTo(HaveOccurred())
	if r.Status != 200 {
		return nil
	}
	list, _ := body(r)["apps"].([]any)
	for _, a := range list {
		m := a.(map[string]any)
		if m["id"] == "tables" {
			return m
		}
	}
	return nil
}

func (d *deployment) waitState(state string) {
	GinkgoHelper()
	admin := d.c.Token(cluster.Owner, "admin")
	Eventually(func() string {
		a := d.app(admin)
		if a == nil {
			return "absent"
		}
		if g, ok := a["generation"].(float64); ok {
			d.gen = uint64(g)
		}
		s, _ := a["state"].(string)
		return s
	}, 60*time.Second, 150*time.Millisecond).Should(Equal(state))
}

// dispatch sends one operation to /api/addons/dispatch. A missing operation ID
// is filled in; a missing generation is the current one.
func (d *deployment) dispatch(c cluster.Client, req map[string]any) *cluster.Response {
	GinkgoHelper()
	if _, ok := req["operationId"]; !ok {
		req["operationId"] = opID("op")
	}
	if _, ok := req["arguments"]; !ok {
		req["arguments"] = map[string]any{}
	}
	if _, ok := req["addonId"]; !ok {
		req["addonId"] = "tables"
	}
	if _, ok := req["generation"]; !ok {
		req["generation"] = d.gen
	}
	r, err := c.Do("POST", "/api/addons/dispatch", req, nil)
	Expect(err).NotTo(HaveOccurred())
	return r
}

// read runs one typed read of a resource.
func (d *deployment) read(c cluster.Client, resource string, args map[string]any) *cluster.Response {
	GinkgoHelper()
	return d.dispatch(c, map[string]any{"resourceId": resource, "action": "read", "arguments": args})
}

func payload(r *cluster.Response) map[string]any {
	GinkgoHelper()
	p, _ := body(r)["payload"].(map[string]any)
	return p
}

// tab sends a request as the person's own browser tab: the named session of
// the user, the same-origin fetch metadata and the confirmation header.
func (d *deployment) tab(node, user, method, path string, v any, confirm string) *cluster.Response {
	GinkgoHelper()
	extra := map[string]string{}
	if confirm != "" {
		extra["X-LocalAI-Confirm"] = confirm
	}
	r, err := d.c.Session(node, user).Do(method, path, v, extra)
	Expect(err).NotTo(HaveOccurred())
	return r
}

// create makes a database as the person and returns its resource ID.
func (d *deployment) create(node, user, name string) string {
	GinkgoHelper()
	op := opID("create")
	r := d.tab(node, user, "POST", "/api/addons/resources", map[string]any{
		"addonId": "tables", "operationId": op, "generation": d.gen, "arguments": map[string]any{"name": name},
	}, op)
	Expect(r.Status).To(Equal(200), r.String())
	id, _ := body(r)["id"].(string)
	Expect(id).NotTo(BeEmpty(), r.String())
	return id
}

func (d *deployment) grant(node, owner, res, target, role string) *cluster.Response {
	GinkgoHelper()
	return d.tab(node, owner, "PUT", "/api/addons/resources/"+res+"/grants/"+d.c.Creds.Users[target].ID, map[string]any{"role": role}, res)
}

func (d *deployment) revoke(node, owner, res, target string) *cluster.Response {
	GinkgoHelper()
	return d.tab(node, owner, "DELETE", "/api/addons/resources/"+res+"/grants/"+d.c.Creds.Users[target].ID, nil, res)
}

// revision reads the current revision of a database.
func (d *deployment) revision(node, user, res string) map[string]any {
	GinkgoHelper()
	r := d.read(d.c.Token(node, user), res, map[string]any{"tool": "databases-inspect"})
	Expect(r.Status).To(Equal(200), r.String())
	rev, _ := payload(r)["revision"].(map[string]any)
	Expect(rev).NotTo(BeNil(), r.String())
	return rev
}

// apply sends one UI change as the person's tab.
func (d *deployment) apply(node, user, res, op, tool string, rev map[string]any, args any) *cluster.Response {
	GinkgoHelper()
	return d.tab(node, user, "POST", "/api/addons/resources/"+res+"/apply", map[string]any{
		"operationId": op, "generation": d.gen, "revision": rev, "tool": tool, "arguments": args,
	}, res)
}

// applyOK applies with the current revision and requires success.
func (d *deployment) applyOK(node, user, res, tool string, args any) map[string]any {
	GinkgoHelper()
	r := d.apply(node, user, res, opID("ap"), tool, d.revision(node, user, res), args)
	Expect(r.Status).To(Equal(200), tool+": "+r.String())
	return body(r)
}

// seedLedger creates a database with one table Entries(Item text, Qty
// integer) and the rows given.
func (d *deployment) seedLedger(node, user, name string, rows ...map[string]any) string {
	GinkgoHelper()
	res := d.create(node, user, name)
	d.applyOK(node, user, res, "tables-create", map[string]any{"table": "Entries"})
	d.applyOK(node, user, res, "columns-add", map[string]any{"table": "Entries", "column": "Item", "type": "text"})
	d.applyOK(node, user, res, "columns-add", map[string]any{"table": "Entries", "column": "Qty", "type": "integer"})
	if len(rows) > 0 {
		d.applyOK(node, user, res, "rows-insert", map[string]any{"table": "Entries", "rows": rows})
	}
	return res
}

// rowCount reads the rows of Entries as one account.
func (d *deployment) rowCount(node, user, res string) int {
	GinkgoHelper()
	r := d.read(d.c.Token(node, user), res, map[string]any{"tool": "rows-query", "table": "Entries"})
	Expect(r.Status).To(Equal(200), r.String())
	rows, _ := payload(r)["rows"].([]any)
	return len(rows)
}

// chatTool sends one scripted tool call through the chat completions route as
// the API key of a user. The scripted model answers its first turn with the
// call, so the broker, the executor and the owner see a real call.
func (d *deployment) chatTool(node, user, tool string, args map[string]any) *cluster.Response {
	GinkgoHelper()
	raw, err := json.Marshal(args)
	Expect(err).NotTo(HaveOccurred())
	message := "Please run this ADDON_TOOL_CALL:addon__tables__" + tool + ":" + base64.RawURLEncoding.EncodeToString(raw)
	r, err := d.c.Token(node, user).Do("POST", "/v1/chat/completions", map[string]any{
		"model": "mock-model", "metadata": map[string]string{"addon_tools": "true"},
		"messages": []map[string]any{{"role": "user", "content": message}},
	}, nil)
	Expect(err).NotTo(HaveOccurred())
	return r
}

// pending lists the pending proposals of the caller.
func (d *deployment) pending(node, user string) []map[string]any {
	GinkgoHelper()
	r, err := d.c.Token(node, user).Do("GET", "/api/addons/proposals?state=pending", nil, nil)
	Expect(err).NotTo(HaveOccurred())
	Expect(r.Status).To(Equal(200), r.String())
	var out []map[string]any
	list, _ := body(r)["proposals"].([]any)
	for _, x := range list {
		out = append(out, x.(map[string]any))
	}
	return out
}

// propose stores one typed proposal as the API key of a user.
func (d *deployment) propose(node, user, res, action, op string, args map[string]any) *cluster.Response {
	GinkgoHelper()
	r, err := d.c.Token(node, user).Do("POST", "/api/addons/proposals", map[string]any{
		"addonId": "tables", "operationId": op, "generation": d.gen, "resourceId": res, "action": action, "arguments": args,
	}, nil)
	Expect(err).NotTo(HaveOccurred())
	return r
}

// confirm confirms a proposal as the person's own tab.
func (d *deployment) confirm(node, user, id, digest string) *cluster.Response {
	GinkgoHelper()
	return d.tab(node, user, "POST", "/api/addons/proposals/"+id+"/confirm", map[string]any{"digest": digest}, id)
}

// ---- the add-on's own SQLite database, read directly ----

// databaseFile is the SQLite file of a database on the owner. Tests read it
// to count what the add-on really committed; they never write it.
func (d *deployment) databaseFile(res string) string {
	return filepath.Join(d.c.Config.Nodes[cluster.Owner].Root, "data", "tables", "tables", "databases", res+".db")
}

// receipts lists the operation IDs the add-on committed for a database, from
// its own receipt table, oldest first.
func (d *deployment) receipts(res string) []string {
	GinkgoHelper()
	db, err := sql.Open("sqlite3", "file:"+d.databaseFile(res)+"?mode=ro&_busy_timeout=5000")
	Expect(err).NotTo(HaveOccurred())
	defer func() { _ = db.Close() }()
	rows, err := db.Query("SELECT operation_id FROM tables_schema_receipts ORDER BY applied_at, operation_id")
	Expect(err).NotTo(HaveOccurred())
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var id string
		Expect(rows.Scan(&id)).To(Succeed())
		out = append(out, id)
	}
	Expect(rows.Err()).NotTo(HaveOccurred())
	return out
}

func count(list []string, id string) int {
	n := 0
	for _, x := range list {
		if x == id {
			n++
		}
	}
	return n
}

// ---- the add-on process ----

// tablesPID is the process id of the Tables executable the owner supervises,
// or zero. It is found as a child of the owner by its command line.
func (d *deployment) tablesPID() int {
	GinkgoHelper()
	for pid, cmd := range cluster.ChildrenOf(d.c.Nodes[cluster.Owner].PID()) {
		if first := strings.Fields(cmd); len(first) > 0 && strings.HasSuffix(first[0], "/tables") {
			return pid
		}
	}
	return 0
}

// waitTables waits for a running Tables process that is not the one given.
func (d *deployment) waitTables(not int) int {
	GinkgoHelper()
	var pid int
	Eventually(func() int {
		pid = d.tablesPID()
		if pid == not || !cluster.Alive(pid) {
			return 0
		}
		return pid
	}, 60*time.Second, 100*time.Millisecond).ShouldNot(BeZero())
	d.waitState("running")
	return pid
}

// operation reads the status of an apply the person made, as the person.
func (d *deployment) operation(node, user, res, op string) map[string]any {
	GinkgoHelper()
	r := d.tab(node, user, "GET", "/api/addons/resources/"+res+"/operations/"+op+"?kind=apply", nil, res)
	Expect(r.Status).To(Equal(200), r.String())
	return body(r)
}

// lifecycle runs one administrator action through a node.
func (d *deployment) lifecycle(node, action string, generation uint64, version string) *cluster.Response {
	GinkgoHelper()
	args := map[string]any{}
	if version != "" {
		args = map[string]any{"sourceId": "fixture", "version": version}
	}
	return d.dispatch(d.c.Token(node, "admin"), map[string]any{"action": action, "generation": generation, "arguments": args})
}
