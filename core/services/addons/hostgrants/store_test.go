// SPDX-License-Identifier: MIT
package hostgrants

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var bg = context.Background()

func grant(opID string, ops ...string) *AddonGrants {
	g := &AddonGrants{Identity: "id-1", Generation: 1, OperationID: opID}
	for _, o := range ops {
		g.Ops = append(g.Ops, OpGrant{Name: o, Source: SourceInstall, At: "2026-11-03T10:00:00Z", By: "admin", Version: "1.0.0"})
	}
	return g
}

func openStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "policy", "host-grants.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, path
}

func mustUpdate(t *testing.T, s *Store, p Patch, rev uint64, id string) Snapshot {
	t.Helper()
	r, err := s.Update(bg, p, rev, id)
	if err != nil {
		t.Fatalf("update %s: %v", id, err)
	}
	return r.Snapshot
}

func TestEmptyStoreDoesNotCreateTheDocument(t *testing.T) {
	s, path := openStore(t)
	snap, err := s.Snapshot(bg)
	if err != nil || snap.Revision != 0 || snap.Blanket.Mode != BlanketOff || len(snap.Grants) != 0 {
		t.Fatalf("unexpected empty snapshot %+v, %v", snap, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("document must not exist before the first write: %v", err)
	}
}

func TestUpdatePersistsAndReopens(t *testing.T) {
	s, path := openStore(t)
	snap := mustUpdate(t, s, Patch{Addons: map[string]*AddonGrants{"sandbox": grant("op-1", "workspace.commit", "workspace.materialize")}}, 0, "w1")
	if snap.Revision != 1 || snap.Grants["sandbox"].Ops[0].Name != "workspace.commit" {
		t.Fatalf("ops must be sorted and revision 1: %+v", snap)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("document mode: %v %v", info, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s2.Close() }()
	got, _ := s2.Snapshot(bg)
	if got.Revision != 1 || len(got.Grants["sandbox"].Ops) != 2 {
		t.Fatalf("reopened %+v", got)
	}
	// The replay survives a reopen.
	r, err := s2.Update(bg, Patch{Addons: map[string]*AddonGrants{"sandbox": grant("op-1", "workspace.commit", "workspace.materialize")}}, 0, "w1")
	if err != nil || !r.Replayed || r.Snapshot.Revision != 1 {
		t.Fatalf("replay after reopen: %+v %v", r, err)
	}
}

func TestRevisionConflictReplayAndBodyMismatch(t *testing.T) {
	s, _ := openStore(t)
	p := Patch{Addons: map[string]*AddonGrants{"a": grant("op", "egress.check")}}
	mustUpdate(t, s, p, 0, "w1")
	if _, err := s.Update(bg, p, 0, "w2"); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale revision must conflict: %v", err)
	}
	r, err := s.Update(bg, p, 0, "w1") // replay is checked before the revision
	if err != nil || !r.Replayed {
		t.Fatalf("replay: %+v %v", r, err)
	}
	other := Patch{Addons: map[string]*AddonGrants{"a": grant("op", "egress.audit")}}
	if _, err := s.Update(bg, other, 1, "w1"); !errors.Is(err, ErrOperationMismatch) {
		t.Fatalf("changed body must mismatch: %v", err)
	}
	snap, _ := s.Snapshot(bg)
	if snap.Revision != 1 {
		t.Fatalf("rejected writes must not bump the revision: %d", snap.Revision)
	}
	for _, id := range []string{"", " x", strings.Repeat("a", 129), "bad\nid"} {
		if _, err := s.Update(bg, Patch{}, 1, id); !errors.Is(err, ErrInvalid) {
			t.Fatalf("operation ID %q: %v", id, err)
		}
	}
}

func TestLimitsAndStrictValues(t *testing.T) {
	s, _ := openStore(t)
	many := map[string]*AddonGrants{}
	for i := 0; i <= MaxAddons; i++ {
		many[fmt.Sprintf("a-%d", i)] = grant("op", "egress.check")
	}
	ops := []string{}
	for i := 0; i <= MaxOpsPerAddon; i++ {
		ops = append(ops, fmt.Sprintf("x.%c%c", 'a'+i/26, 'a'+i%26))
	}
	deny := func(m map[string][]string) Patch { return Patch{Deny: &m} }
	dupOp := grant("op", "egress.check")
	dupOp.Ops = append(dupOp.Ops, dupOp.Ops[0])
	noIdentity := grant("op", "egress.check")
	noIdentity.Identity = ""
	noOps := grant("op")
	prefixOp := grant("op", "egress.")
	badSource := grant("op", "egress.check")
	badSource.Ops[0].Source = "api"
	blanketNoRev := grant("op", "egress.check")
	blanketNoRev.Ops[0].Source = SourceBlanket
	installWithRev := grant("op", "egress.check")
	installWithRev.Ops[0].BlanketRevision = 1
	badTime := grant("op", "egress.check")
	badTime.Ops[0].At = "yesterday"
	manyDeny := map[string][]string{}
	for i := 0; i <= MaxDenyKeys; i++ {
		manyDeny[fmt.Sprintf("a-%d", i)] = []string{"x."}
	}
	cases := map[string]Patch{
		"129 add-ons":          {Addons: many},
		"33 operations":        {Addons: map[string]*AddonGrants{"a": grant("op", ops...)}},
		"bad add-on ID":        {Addons: map[string]*AddonGrants{"A": grant("op", "egress.check")}},
		"star as add-on ID":    {Addons: map[string]*AddonGrants{"*": grant("op", "egress.check")}},
		"duplicate operation":  {Addons: map[string]*AddonGrants{"a": dupOp}},
		"empty identity":       {Addons: map[string]*AddonGrants{"a": noIdentity}},
		"entry without ops":    {Addons: map[string]*AddonGrants{"a": noOps}},
		"prefix as a grant":    {Addons: map[string]*AddonGrants{"a": prefixOp}},
		"unknown source":       {Addons: map[string]*AddonGrants{"a": badSource}},
		"blanket without rev":  {Addons: map[string]*AddonGrants{"a": blanketNoRev}},
		"revision without src": {Addons: map[string]*AddonGrants{"a": installWithRev}},
		"bad time":             {Addons: map[string]*AddonGrants{"a": badTime}},
		"blanket mode":         {Blanket: &Blanket{Mode: "sometimes"}},
		"129 deny keys":        deny(manyDeny),
		"duplicate deny entry": deny(map[string][]string{"a": {"x.", "x."}}),
		"star beside entry":    deny(map[string][]string{"a": {"*", "x."}}),
		"bad deny entry":       deny(map[string][]string{"a": {"X."}}),
		"bad deny key":         deny(map[string][]string{"A": {"x."}}),
		"bad hint":             {Hints: map[string][]string{"a": {"x."}}},
	}
	for name, p := range cases {
		if _, err := s.Update(bg, p, 0, "w-"+strings.ReplaceAll(name, " ", "-")); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", name, err)
		}
	}
	snap, _ := s.Snapshot(bg)
	if snap.Revision != 0 {
		t.Fatalf("invalid patches must not change the document: %+v", snap)
	}
	// Boundaries that are valid.
	max := map[string]*AddonGrants{}
	for i := 0; i < MaxAddons; i++ {
		max[fmt.Sprintf("a-%d", i)] = grant("op", "egress.check")
	}
	mustUpdate(t, s, Patch{Addons: max}, 0, "max")
	mustUpdate(t, s, Patch{Addons: map[string]*AddonGrants{"a-0": grant("op", ops[:MaxOpsPerAddon]...)}}, 1, "max-ops")
}

func TestBlanketRevisionAndRevoke(t *testing.T) {
	s, _ := openStore(t)
	snap := mustUpdate(t, s, Patch{Blanket: &Blanket{Mode: BlanketOfficial, ChangedAt: "2026-11-03T10:00:00Z", ChangedBy: "admin", Revision: 99}}, 0, "b1")
	if snap.Blanket.Revision != 1 {
		t.Fatalf("store assigns the blanket revision: %+v", snap.Blanket)
	}
	g := grant("op", "egress.check", "egress.audit")
	g.Ops[1].Source, g.Ops[1].BlanketRevision = SourceBlanket, 1
	only := grant("op2", "workspace.admin")
	only.Ops[0].Source, only.Ops[0].BlanketRevision = SourceBlanket, 1
	snap = mustUpdate(t, s, Patch{Addons: map[string]*AddonGrants{"a": g, "b": only}}, 1, "g1")
	// Same mode and option keep the revision; a change bumps it.
	snap = mustUpdate(t, s, Patch{Blanket: &Blanket{Mode: BlanketOfficial}}, snap.Revision, "b2")
	if snap.Blanket.Revision != 1 {
		t.Fatalf("unchanged switch keeps its revision: %d", snap.Blanket.Revision)
	}
	snap = mustUpdate(t, s, Patch{Blanket: &Blanket{Mode: BlanketOff}}, snap.Revision, "b3")
	if snap.Blanket.Revision != 2 || len(snap.Grants["a"].Ops) != 2 {
		t.Fatalf("turning off keeps the grants: %+v", snap)
	}
	snap = mustUpdate(t, s, Patch{RevokeBlanket: true}, snap.Revision, "r1")
	if _, ok := snap.Grants["b"]; ok || len(snap.Grants["a"].Ops) != 1 || snap.Grants["a"].Ops[0].Name != "egress.check" {
		t.Fatalf("revoke removes only blanket ops and empty entries: %+v", snap.Grants)
	}
}

func TestKeepAsOwnSurvivesBlanketRevoke(t *testing.T) {
	g := *grant("op", "egress.check")
	g.Ops[0].Source, g.Ops[0].BlanketRevision = SourceBlanket, 3
	kept := KeepAsOwn(g, "2026-11-04T10:00:00Z", "admin")
	if kept.Ops[0].Source != SourceEdit || kept.Ops[0].BlanketRevision != 0 || g.Ops[0].Source != SourceBlanket {
		t.Fatalf("KeepAsOwn must relabel a copy: %+v / %+v", kept, g)
	}
}

func TestHintsAndDenyReplace(t *testing.T) {
	s, _ := openStore(t)
	deny := map[string][]string{"*": {"egress."}, "web": {"workspace.admin"}}
	snap := mustUpdate(t, s, Patch{Deny: &deny, Hints: map[string][]string{"a": {"workspace.commit", "workspace.admin"}}}, 0, "d1")
	if len(snap.Deny) != 2 || snap.Hints["a"][0] != "workspace.admin" {
		t.Fatalf("%+v", snap)
	}
	empty := map[string][]string{}
	snap = mustUpdate(t, s, Patch{Deny: &empty, Hints: map[string][]string{"a": nil}}, 1, "d2")
	if len(snap.Deny) != 0 || len(snap.Hints) != 0 {
		t.Fatalf("a pointer to an empty map clears; an empty hint deletes: %+v", snap)
	}
}

func TestPersistFailureLeavesTheOldDocument(t *testing.T) {
	s, path := openStore(t)
	mustUpdate(t, s, Patch{Addons: map[string]*AddonGrants{"a": grant("op", "egress.check")}}, 0, "w1")
	real := s.persist
	s.persist = func(document) (bool, error) { return false, errors.New("disk full") }
	if _, err := s.Update(bg, Patch{Addons: map[string]*AddonGrants{"a": nil}}, 1, "w2"); err == nil {
		t.Fatal("expected the write to fail")
	}
	snap, err := s.Snapshot(bg)
	if err != nil || snap.Revision != 1 || len(snap.Grants["a"].Ops) != 1 {
		t.Fatalf("a failed write must leave memory unchanged: %+v %v", snap, err)
	}
	s.persist = real
	// The failed operation ID was never recorded, so it can be used again.
	mustUpdate(t, s, Patch{Addons: map[string]*AddonGrants{"a": nil}}, 1, "w2")
	_ = s.Close()
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s2.Close() }()
	if got, _ := s2.Snapshot(bg); got.Revision != 2 || len(got.Grants) != 0 {
		t.Fatalf("%+v", got)
	}
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".host-grants-*"))
	if len(matches) != 0 {
		t.Fatalf("temporary files left behind: %v", matches)
	}
}

func TestAmbiguousCommitMakesTheStoreRefuseUntilReopened(t *testing.T) {
	s, path := openStore(t)
	real := s.persist
	s.persist = func(d document) (bool, error) {
		renamed, _ := real(d)
		return renamed, errors.New("directory sync failed")
	}
	p := Patch{Addons: map[string]*AddonGrants{"a": grant("op", "egress.check")}}
	if _, err := s.Update(bg, p, 0, "w1"); !errors.Is(err, ErrUncertain) {
		t.Fatalf("want ErrUncertain, got %v", err)
	}
	if _, err := s.Snapshot(bg); !errors.Is(err, ErrUncertain) {
		t.Fatalf("snapshot must refuse: %v", err)
	}
	_ = s.Close()
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s2.Close() }()
	r, err := s2.Update(bg, p, 0, "w1")
	if err != nil || !r.Replayed || r.Snapshot.Revision != 1 {
		t.Fatalf("the same operation reconciles as a replay: %+v %v", r, err)
	}
}

func TestOpenRejectsBadFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string, mode os.FileMode) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	good := `{"schemaVersion":1,"revision":1,"blanket":{"mode":"off","includeOptional":false,"revision":0},"grants":{},"deny":{},"hints":{},"receipts":{}}`
	if s, err := Open(write("good.json", good, 0600)); err != nil {
		t.Fatalf("control: %v", err)
	} else {
		_ = s.Close()
	}
	bad := map[string]struct {
		body string
		mode os.FileMode
	}{
		"corrupt":        {`{`, 0600},
		"group-readable": {good, 0640},
		"unknown field":  {strings.Replace(good, `"hints"`, `"extra":1,"hints"`, 1), 0600},
		"revision 0":     {strings.Replace(good, `"revision":1,"blanket"`, `"revision":0,"blanket"`, 1), 0600},
		"version 2":      {strings.Replace(good, `"schemaVersion":1`, `"schemaVersion":2`, 1), 0600},
		"trailing":       {good + `{}`, 0600},
		"bad receipt":    {strings.Replace(good, `"receipts":{}`, `"receipts":{"x":{"body":"zz","revision":1}}`, 1), 0600},
	}
	for name, c := range bad {
		if s, err := Open(write(strings.ReplaceAll(name, " ", "-")+".json", c.body, c.mode)); err == nil {
			_ = s.Close()
			t.Errorf("%s: expected an error", name)
		} else if !errors.Is(err, ErrUnavailable) {
			t.Errorf("%s: want ErrUnavailable, got %v", name, err)
		}
	}
	// A symlink to the document is refused.
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(filepath.Join(dir, "good.json"), link); err != nil {
		t.Fatal(err)
	}
	if s, err := Open(link); err == nil {
		_ = s.Close()
		t.Error("symlink must be refused")
	}
}

func TestSecondOpenIsRefusedWhileLocked(t *testing.T) {
	_, path := openStore(t)
	if s2, err := Open(path); err == nil {
		_ = s2.Close()
		t.Fatal("a second owner must not open the same document")
	}
}

func TestReceiptsAreBounded(t *testing.T) {
	s, _ := openStore(t)
	var rev uint64
	for i := 0; i < MaxReceipts+5; i++ {
		rev = mustUpdate(t, s, Patch{Hints: map[string][]string{"a": {"egress.check"}}}, rev, fmt.Sprintf("w%d", i)).Revision
	}
	if len(s.doc.Receipts) != MaxReceipts {
		t.Fatalf("receipts = %d", len(s.doc.Receipts))
	}
	if _, ok := s.doc.Receipts["w0"]; ok {
		t.Fatal("the oldest receipt must be evicted first")
	}
	if _, ok := s.doc.Receipts[fmt.Sprintf("w%d", MaxReceipts+4)]; !ok {
		t.Fatal("the newest receipt must stay")
	}
}

// Many writers race on one document. Each retries on a revision conflict with
// the same operation ID, as a real caller does. Run with -race.
func TestConcurrentWritersAreSerialised(t *testing.T) {
	s, path := openStore(t)
	const writers = 24
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := fmt.Sprintf("a-%d", i)
			opID := fmt.Sprintf("op-%d", i)
			for {
				snap, err := s.Snapshot(bg)
				if err != nil {
					t.Error(err)
					return
				}
				_, err = s.Update(bg, Patch{Addons: map[string]*AddonGrants{id: grant(opID, "egress.check")}}, snap.Revision, opID)
				if errors.Is(err, ErrConflict) {
					continue
				}
				if err != nil {
					t.Error(err)
				}
				return
			}
		}()
	}
	// Concurrent readers must always see a consistent, validated document.
	stop := make(chan struct{})
	var readers sync.WaitGroup
	for i := 0; i < 4; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				snap, err := s.Snapshot(bg)
				if err != nil || validate(snap) != nil {
					t.Errorf("inconsistent snapshot: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(stop)
	readers.Wait()
	snap, _ := s.Snapshot(bg)
	if snap.Revision != writers || len(snap.Grants) != writers {
		t.Fatalf("revision %d with %d entries, want %d", snap.Revision, len(snap.Grants), writers)
	}
	_ = s.Close()
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s2.Close() }()
	if got, _ := s2.Snapshot(bg); got.Revision != writers {
		t.Fatalf("persisted revision %d", got.Revision)
	}
}

// The same operation sent from many goroutines (a retrying client) writes once.
func TestConcurrentReplayWritesOnce(t *testing.T) {
	s, _ := openStore(t)
	p := Patch{Addons: map[string]*AddonGrants{"a": grant("op", "egress.check")}}
	var wg sync.WaitGroup
	var mu sync.Mutex
	replays := 0
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := s.Update(bg, p, 0, "same")
			if err != nil {
				t.Error(err)
				return
			}
			if r.Replayed {
				mu.Lock()
				replays++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	snap, _ := s.Snapshot(bg)
	if snap.Revision != 1 || replays != 15 {
		t.Fatalf("revision %d, replays %d", snap.Revision, replays)
	}
}

func TestCancelledContextDoesNotWrite(t *testing.T) {
	s, _ := openStore(t)
	ctx, cancel := context.WithCancel(bg)
	cancel()
	if _, err := s.Update(ctx, Patch{}, 0, "w"); !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
	if _, err := s.Snapshot(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
	_ = s.Close()
	if _, err := s.Snapshot(bg); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("closed store: %v", err)
	}
}
