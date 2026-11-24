// SPDX-License-Identifier: MIT
package hostgrants

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/mudler/LocalAI/core/services/addons/hostops"
)

func TestAuthorityFollowsTheStoreLive(t *testing.T) {
	s, _ := openStore(t)
	auth := NewAuthority(AuthorityConfig{
		Floor:   Floor{Grants: map[string][]string{addon: {"egress."}}, Deny: map[string][]string{"*": {"connector."}}},
		Store:   s,
		Context: func(string) Context { return Context{Identity: "id-1"} },
	})
	if ok, why := auth.Allows(addon, "workspace.commit"); ok || why != hostops.ReasonNotGranted {
		t.Fatalf("%v %v", ok, why)
	}
	snap := mustUpdate(t, s, Patch{Addons: map[string]*AddonGrants{addon: grant("op", "workspace.commit", "egress.check")}}, 0, "g1")
	if ok, _ := auth.Allows(addon, "workspace.commit"); !ok {
		t.Fatal("a UI grant applies to the next call with no restart")
	}
	deny := map[string][]string{addon: {"workspace.commit"}}
	snap = mustUpdate(t, s, Patch{Deny: &deny}, snap.Revision, "d1")
	if ok, why := auth.Allows(addon, "workspace.commit"); ok || why != hostops.ReasonDeniedByAdmin || !why.Denied() {
		t.Fatalf("a UI deny applies to the next call: %v %v", ok, why)
	}
	if ok, why := auth.Allows(addon, "connector.inbound"); ok || why != hostops.ReasonDeniedByPolicyFile {
		t.Fatalf("%v %v", ok, why)
	}
	mustUpdate(t, s, Patch{Addons: map[string]*AddonGrants{addon: nil}}, snap.Revision, "r1")
	if auth.Entitled(addon) != true {
		t.Fatal("the floor still entitles")
	}
	if auth.Entitled(other) {
		t.Fatal("another add-on holds nothing")
	}
	got := auth.Snapshot(addon)
	want := hostops.EffectiveSet{Revision: 3, Prefixes: []string{"egress."}, Denied: []string{"connector.", "workspace.commit"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestAuthoritySnapshotListsUIOperationsOnly(t *testing.T) {
	s, _ := openStore(t)
	mustUpdate(t, s, Patch{Addons: map[string]*AddonGrants{addon: grant("op", "workspace.commit", "egress.check")}}, 0, "g1")
	auth := NewAuthority(AuthorityConfig{
		Floor:   Floor{Grants: map[string][]string{addon: {"egress."}}},
		Store:   s,
		Context: func(string) Context { return Context{Identity: "id-1"} },
	})
	got := auth.Snapshot(addon)
	if !reflect.DeepEqual(got.Operations, []string{"workspace.commit"}) {
		t.Fatalf("an operation the floor already grants is not repeated: %+v", got)
	}
}

func TestStaticAuthorityWithoutAStoreAndWithoutContext(t *testing.T) {
	auth := NewAuthority(AuthorityConfig{Floor: Floor{Grants: map[string][]string{addon: {"egress."}}}})
	if ok, _ := auth.Allows(addon, "egress.check"); !ok {
		t.Fatal("the floor applies without a store")
	}
	s, _ := openStore(t)
	mustUpdate(t, s, Patch{Addons: map[string]*AddonGrants{addon: grant("op", "workspace.commit")}}, 0, "g1")
	noCtx := NewAuthority(AuthorityConfig{Store: s})
	if ok, why := noCtx.Allows(addon, "workspace.commit"); ok || why != hostops.ReasonIdentityMismatch {
		t.Fatalf("without an identity a UI entry fails closed: %v %v", ok, why)
	}
}

func TestClosedStoreStillAppliesTheFileDeny(t *testing.T) {
	s, _ := openStore(t)
	auth := NewAuthority(AuthorityConfig{Floor: Floor{Grants: map[string][]string{addon: {"egress."}}, Deny: map[string][]string{addon: {"egress.audit"}}}, Store: s})
	_ = s.Close()
	if ok, _ := auth.Allows(addon, "egress.audit"); ok {
		t.Fatal("file deny must not depend on the store")
	}
	if ok, _ := auth.Allows(addon, "egress.check"); !ok {
		t.Fatal("the floor must not depend on the store")
	}
}

// Calls read the published snapshot while writers replace it. Run with -race.
func TestAuthorityReadsRaceWithWrites(t *testing.T) {
	s, _ := openStore(t)
	auth := NewAuthority(AuthorityConfig{Store: s, Context: func(string) Context { return Context{Identity: "id-1"} }})
	done := make(chan struct{})
	go func() {
		defer close(done)
		var rev uint64
		for i := 0; i < 50; i++ {
			rev = mustUpdate(t, s, Patch{Addons: map[string]*AddonGrants{addon: grant("op", "workspace.commit")}}, rev, fmt.Sprintf("w%d", i)).Revision
		}
	}()
	for {
		select {
		case <-done:
			return
		default:
			auth.Allows(addon, "workspace.commit")
			auth.Snapshot(addon)
			auth.Entitled(addon)
		}
	}
}
