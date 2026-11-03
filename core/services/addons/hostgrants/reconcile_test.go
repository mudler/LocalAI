// SPDX-License-Identifier: MIT
package hostgrants

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPlanReconcile(t *testing.T) {
	snap := emptySnapshot()
	snap.Grants["kept"] = *grant("done", "egress.check")
	snap.Grants["gone"] = *grant("done", "egress.check", "egress.audit")
	snap.Grants["swapped"] = *grant("done", "egress.check")
	snap.Grants["pending"] = *grant("never-completed", "egress.check")
	snap.Deny["*"] = []string{"connector."}
	snap.Hints["gone"] = []string{"egress.check"}
	patch, removals := PlanReconcile(snap, ReconcileInput{
		Installed: func(id string) (string, bool) {
			switch id {
			case "kept", "pending":
				return "id-1", true
			case "swapped":
				return "another-package", true
			}
			return "", false
		},
		OperationCompleted: func(id string) bool { return id == "done" },
	})
	want := []Removal{
		{AddonID: "gone", Reason: RemovedNotInstalled, Ops: []string{"egress.check", "egress.audit"}},
		{AddonID: "pending", Reason: RemovedOperationIncomplete, Ops: []string{"egress.check"}},
		{AddonID: "swapped", Reason: RemovedIdentityMismatch, Ops: []string{"egress.check"}},
	}
	if !reflect.DeepEqual(removals, want) {
		t.Fatalf("got %+v", removals)
	}
	if len(patch.Addons) != 3 || patch.Addons["kept"] != nil || patch.Deny != nil || patch.Hints != nil || patch.Blanket != nil {
		t.Fatalf("the patch must touch only the stale entries: %+v", patch)
	}
}

// A crash after the grant write and before the journal commit leaves a pending
// entry. After a restart, reconciliation removes it, whichever way it died.
func TestReconcileAfterACrash(t *testing.T) {
	s, path := openStore(t)
	mustUpdate(t, s, Patch{Addons: map[string]*AddonGrants{
		"half-installed": grant("op-crashed", "workspace.materialize"),
		"installed":      grant("op-done", "egress.check"),
		"uninstalled":    grant("op-old", "egress.check"),
	}}, 0, "grants-1")
	// The process dies here: no Close, no cleanup. The lock dies with it.
	_ = s.Close()

	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s2.Close() }()
	in := ReconcileInput{
		Installed:          func(id string) (string, bool) { return "id-1", id != "uninstalled" },
		OperationCompleted: func(id string) bool { return id != "op-crashed" },
	}
	removed, err := s2.Reconcile(bg, in)
	if err != nil || len(removed) != 2 {
		t.Fatalf("removed %+v, %v", removed, err)
	}
	snap, _ := s2.Snapshot(bg)
	if len(snap.Grants) != 1 || snap.Grants["installed"].OperationID != "op-done" {
		t.Fatalf("%+v", snap.Grants)
	}
	// Nothing left: a second pass writes nothing.
	rev := snap.Revision
	removed, err = s2.Reconcile(bg, in)
	if err != nil || removed != nil {
		t.Fatalf("%+v, %v", removed, err)
	}
	if again, _ := s2.Snapshot(bg); again.Revision != rev {
		t.Fatal("an empty reconcile must not write")
	}
}

func TestReconcileReplaysAfterALostResponse(t *testing.T) {
	s, _ := openStore(t)
	mustUpdate(t, s, Patch{Addons: map[string]*AddonGrants{"a": grant("op", "egress.check")}}, 0, "g")
	in := ReconcileInput{Installed: func(string) (string, bool) { return "", false }}
	snap, _ := s.Snapshot(bg)
	patch, _ := PlanReconcile(snap, in)
	if _, err := s.Update(bg, patch, snap.Revision, "reconcile-r1"); err != nil {
		t.Fatal(err)
	}
	// The same operation arriving again is a replay, not a second write.
	r, err := s.Update(bg, patch, snap.Revision, "reconcile-r1")
	if err != nil || !r.Replayed {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestAuditAppendsFsyncedLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "host-grants-audit.jsonl")
	a, err := OpenAudit(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Append(AuditEntry{Event: AuditGrant, AddonID: "sandbox", Ops: []string{"egress.check"}, Source: "install", OperationID: "op-1", RevisionBefore: 1, RevisionAfter: 2, By: "admin"}); err != nil {
		t.Fatal(err)
	}
	if err := a.Append(AuditEntry{Event: AuditBlanketGrant, Blanket: "official@rev3", Time: "2026-11-03T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if err := a.Append(AuditEntry{}); err == nil {
		t.Error("an entry needs an event")
	}
	if err := a.Append(AuditEntry{Event: AuditRefused, Reason: string(make([]byte, maxAuditLine))}); err == nil {
		t.Error("an oversized entry is refused")
	}
	_ = a.Close()
	if err := a.Append(AuditEntry{Event: AuditGrant}); err == nil {
		t.Error("a closed audit refuses")
	}
	// Reopening appends; it never truncates.
	a2, err := OpenAudit(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = a2.Append(AuditEntry{Event: AuditRevoke, AddonID: "sandbox"})
	_ = a2.Close()

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var events []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e AuditEntry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil || e.Time == "" {
			t.Fatalf("line %q: %v", sc.Text(), err)
		}
		events = append(events, e.Event)
	}
	if !reflect.DeepEqual(events, []string{AuditGrant, AuditBlanketGrant, AuditRevoke}) {
		t.Fatalf("%v", events)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0600 {
		t.Fatalf("mode %v", info.Mode())
	}
	var none *Audit
	if none.Append(AuditEntry{Event: AuditGrant}) != nil || none.Close() != nil {
		t.Error("a nil audit is a no-op")
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenAudit(link); err == nil {
		t.Error("a symlink must be refused")
	}
}

func TestUpgradeAddedOperationIsPendingOnItsOwn(t *testing.T) {
	entry := *grant("install-op", "workspace.materialize")
	entry.Ops = append(entry.Ops, OpGrant{Name: "egress.check", Source: SourceUpgrade, OperationID: "upgrade-op"})
	snap := emptySnapshot()
	snap.Grants["a"] = entry
	completed := map[string]bool{"install-op": true}
	ctx := Context{Identity: "id-1", Completed: func(id string) bool { return completed[id] }}

	// Before the upgrade completes, the old operation works and the new one does not.
	var f Floor
	if !f.Decide(snap, ctx, "a", "workspace.materialize").Allowed {
		t.Fatal("the operation held before the upgrade stopped working")
	}
	if d := f.Decide(snap, ctx, "a", "egress.check"); d.Allowed || d.Reason != ReasonNotGranted {
		t.Fatalf("%+v", d)
	}
	completed["upgrade-op"] = true
	if !f.Decide(snap, ctx, "a", "egress.check").Allowed {
		t.Fatal("the operation of a completed upgrade is not effective")
	}

	// A crash before the upgrade completed removes only what it added.
	delete(completed, "upgrade-op")
	in := ReconcileInput{
		Installed:          func(string) (string, bool) { return "id-1", true },
		OperationCompleted: func(id string) bool { return completed[id] },
	}
	patch, removals := PlanReconcile(snap, in)
	if len(removals) != 1 || !reflect.DeepEqual(removals[0].Ops, []string{"egress.check"}) || removals[0].Reason != RemovedOperationIncomplete {
		t.Fatalf("%+v", removals)
	}
	kept := patch.Addons["a"]
	if kept == nil || len(kept.Ops) != 1 || kept.Ops[0].Name != "workspace.materialize" {
		t.Fatalf("%+v", kept)
	}
}
