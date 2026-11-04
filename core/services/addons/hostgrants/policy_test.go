// SPDX-License-Identifier: MIT
package hostgrants

import (
	"fmt"
	"reflect"
	"testing"
)

const (
	op     = "workspace.commit"
	ident  = "id-1"
	addon  = "sandbox"
	other  = "web"
	opFam  = "workspace."
	opName = "workspace.commit"
)

func uiEntry(source Source, identity string, ops ...string) AddonGrants {
	g := AddonGrants{Identity: identity, Generation: 1, OperationID: "op-1"}
	for _, o := range ops {
		og := OpGrant{Name: o, Source: source}
		if source == SourceBlanket {
			og.BlanketRevision = 1
		}
		g.Ops = append(g.Ops, og)
	}
	return g
}

// The precedence table is generated from independent boolean dimensions, and
// the expectation is written as a plain ordered list of rules so that it does
// not share logic with the code under test.
func TestPrecedenceMatrix(t *testing.T) {
	type dim struct{ fileDeny, uiDeny, floor, ui, blanket, identityOK bool }
	for bits := 0; bits < 1<<6; bits++ {
		d := dim{bits&1 != 0, bits&2 != 0, bits&4 != 0, bits&8 != 0, bits&16 != 0, bits&32 != 0}
		name := fmt.Sprintf("%+v", d)
		floor := Floor{Grants: map[string][]string{}, Deny: map[string][]string{}}
		doc := emptySnapshot()
		if d.fileDeny {
			floor.Deny[addon] = []string{opName}
		}
		if d.uiDeny {
			doc.Deny["*"] = []string{opFam}
		}
		if d.floor {
			floor.Grants[addon] = []string{opFam}
		}
		entryIdentity := ident
		if !d.identityOK {
			entryIdentity = "other-package"
		}
		switch {
		case d.ui:
			doc.Grants[addon] = uiEntry(SourceInstall, entryIdentity, opName)
		case d.blanket:
			doc.Grants[addon] = uiEntry(SourceBlanket, entryIdentity, opName)
		}
		got := floor.Decide(doc, Context{Identity: ident}, addon, opName)

		var want Decision
		switch {
		case d.fileDeny:
			want = Decision{Reason: ReasonDeniedByFile}
		case d.uiDeny:
			want = Decision{Reason: ReasonDeniedByAdmin}
		case d.floor:
			want = Decision{Allowed: true, Source: SourceDeployment}
		case (d.ui || d.blanket) && !d.identityOK:
			want = Decision{Reason: ReasonIdentityMismatch}
		case d.ui:
			want = Decision{Allowed: true, Source: SourceInstall}
		case d.blanket:
			want = Decision{Allowed: true, Source: SourceBlanket}
		default:
			want = Decision{Reason: ReasonNotGranted}
		}
		if got != want {
			t.Errorf("%s: got %+v, want %+v", name, got, want)
		}
	}
}

func TestDenyMatching(t *testing.T) {
	cases := []struct {
		rules map[string][]string
		addon string
		op    string
		want  bool
	}{
		{map[string][]string{"*": {"egress."}}, "a", "egress.check", true},
		{map[string][]string{"*": {"egress."}}, "a", "egressx.check", false},
		{map[string][]string{"a": {"egress."}}, "b", "egress.check", false},
		{map[string][]string{"a": {"workspace.admin"}}, "a", "workspace.admin", true},
		{map[string][]string{"a": {"workspace.admin"}}, "a", "workspace.admin2", false},
		{map[string][]string{"a": {"workspace.admin"}}, "a", "workspace.adminx", false},
		{map[string][]string{"a": {"workspace.admin"}}, "a", "workspace.commit", false},
		{map[string][]string{"a": {"*"}}, "a", "anything.at.all", true},
		{map[string][]string{"a": {"*"}}, "b", "anything.at.all", false},
		{map[string][]string{"*": {"*"}}, "b", "anything.at.all", true},
		{map[string][]string{"a": {"connector.file."}}, "a", "connector.file.put", true},
		{map[string][]string{"a": {"connector.file."}}, "a", "connector.inbound", false},
		{nil, "a", "x.y", false},
	}
	for i, c := range cases {
		if got := matchDeny(c.rules, c.addon, c.op); got != c.want {
			t.Errorf("case %d (%v %s %s): got %v", i, c.rules, c.addon, c.op, got)
		}
	}
}

func TestGrantsAreEqualityUIAndPrefixFloor(t *testing.T) {
	floor := Floor{Grants: map[string][]string{addon: {"egress."}}}
	doc := emptySnapshot()
	doc.Grants[addon] = uiEntry(SourceInstall, ident, "workspace.commit")
	c := Context{Identity: ident}
	if !floor.Decide(doc, c, addon, "egress.audit").Allowed {
		t.Error("a floor prefix grants every operation of the family")
	}
	if floor.Decide(doc, c, addon, "workspace.materialize").Allowed {
		t.Error("a UI grant is one exact operation, not its family")
	}
	if floor.Decide(doc, c, addon, "workspace.commit.x").Allowed {
		t.Error("a UI grant never matches by prefix")
	}
	if floor.Decide(doc, c, other, "workspace.commit").Allowed || floor.Decide(doc, c, other, "egress.audit").Allowed {
		t.Error("add-ons must not see each other's grants")
	}
}

func TestIdentityAndPendingEntries(t *testing.T) {
	floor := Floor{}
	doc := emptySnapshot()
	doc.Grants[addon] = uiEntry(SourceInstall, ident, opName)
	if d := floor.Decide(doc, Context{}, addon, opName); d.Allowed || d.Reason != ReasonIdentityMismatch {
		t.Errorf("unknown identity fails closed: %+v", d)
	}
	done := map[string]bool{}
	c := Context{Identity: ident, Completed: func(id string) bool { return done[id] }}
	if d := floor.Decide(doc, c, addon, opName); d.Allowed || d.Reason != ReasonNotGranted {
		t.Errorf("a pending entry is not effective: %+v", d)
	}
	done["op-1"] = true
	if !floor.Decide(doc, c, addon, opName).Allowed {
		t.Error("a completed entry is effective")
	}
	// A deny added after the grant wins at once, with no write to the entry.
	floor.Deny = map[string][]string{"*": {"workspace."}}
	if d := floor.Decide(doc, c, addon, opName); d.Allowed || d.Reason != ReasonDeniedByFile {
		t.Errorf("a later deny wins: %+v", d)
	}
}

func TestRegistryBoundsEveryGrant(t *testing.T) {
	floor := Floor{Grants: map[string][]string{addon: {"workspace."}}}
	doc := emptySnapshot()
	c := Context{Identity: ident, Registered: func(op string) bool { return op == "workspace.commit" }}
	if floor.Decide(doc, c, addon, "workspace.future").Allowed {
		t.Error("an operation the host does not register is never allowed")
	}
	if !floor.Decide(doc, c, addon, "workspace.commit").Allowed {
		t.Error("a registered operation under the prefix is allowed")
	}
}

func TestEntitled(t *testing.T) {
	doc := emptySnapshot()
	c := Context{Identity: ident}
	if (Floor{}).Entitled(doc, c, addon) {
		t.Error("nothing granted means not entitled")
	}
	if !(Floor{Grants: map[string][]string{addon: {"egress."}}}).Entitled(doc, c, addon) {
		t.Error("a floor grant entitles")
	}
	if (Floor{Grants: map[string][]string{addon: {"egress."}}, Deny: map[string][]string{addon: {"*"}}}).Entitled(doc, c, addon) {
		t.Error("an add-on-wide deny removes the entitlement")
	}
	if !(Floor{Grants: map[string][]string{addon: {"egress."}}, Deny: map[string][]string{addon: {"egress.audit"}}}).Entitled(doc, c, addon) {
		t.Error("a narrow deny leaves the rest of the prefix")
	}
	doc.Grants[addon] = uiEntry(SourceInstall, ident, "egress.check")
	if !(Floor{}).Entitled(doc, c, addon) {
		t.Error("a UI entry entitles")
	}
	if (Floor{Deny: map[string][]string{"*": {"egress."}}}).Entitled(doc, c, addon) {
		t.Error("a UI entry that is denied does not entitle")
	}
	if (Floor{}).Entitled(doc, Context{Identity: "other"}, addon) {
		t.Error("an identity mismatch does not entitle")
	}
}

func TestEffectiveKeepsOrder(t *testing.T) {
	floor := Floor{Grants: map[string][]string{addon: {"egress."}}}
	doc := emptySnapshot()
	got := floor.Effective(doc, Context{Identity: ident}, addon, []string{"workspace.commit", "egress.check"})
	if len(got) != 2 || got[0].Op != "workspace.commit" || got[0].Allowed || !got[1].Allowed || got[1].Source != SourceDeployment {
		t.Fatalf("%+v", got)
	}
}

func TestPlan(t *testing.T) {
	registered := map[string]bool{"workspace.materialize": true, "workspace.commit": true, "egress.check": true, "workspace.admin": true}
	c := Context{Identity: ident, Registered: func(op string) bool { return registered[op] }}
	floor := Floor{
		Grants: map[string][]string{addon: {"egress."}},
		Deny:   map[string][]string{addon: {"workspace.admin"}},
	}
	doc := emptySnapshot()
	doc.Deny["*"] = []string{"connector."}
	p := floor.Plan(doc, c, PlanInput{
		AddonID:  addon,
		Required: []string{"workspace.materialize", "egress.check"},
		Optional: []string{"workspace.commit", "workspace.admin", "connector.inbound", "nope.nothing"},
		// A lying approval: an undeclared name and a denied one.
		Wanted: []string{"workspace.materialize", "workspace.admin", "undeclared.op", "connector.inbound", "nope.nothing"},
	})
	want := Plan{
		Grant:           []string{"workspace.materialize"},
		Held:            []string{"egress.check"},
		Denied:          []string{"workspace.admin", "connector.inbound"},
		Unsupported:     []string{"nope.nothing"},
		BlockedRequired: nil,
		MissingRequired: nil,
	}
	if !reflect.DeepEqual(p, want) {
		t.Fatalf("got  %+v\nwant %+v", p, want)
	}

	p = floor.Plan(doc, c, PlanInput{AddonID: addon, Required: []string{"workspace.materialize", "workspace.admin", "nope.nothing"}})
	if !reflect.DeepEqual(p.BlockedRequired, []string{"workspace.admin"}) ||
		!reflect.DeepEqual(p.MissingRequired, []string{"workspace.materialize", "nope.nothing"}) || len(p.Grant) != 0 {
		t.Fatalf("required handling: %+v", p)
	}
	// Duplicate declarations count once.
	p = floor.Plan(doc, c, PlanInput{AddonID: addon, Required: []string{"workspace.commit", "workspace.commit"}, Wanted: []string{"workspace.commit"}})
	if !reflect.DeepEqual(p.Grant, []string{"workspace.commit"}) {
		t.Fatalf("%+v", p)
	}
}

func TestBlanket(t *testing.T) {
	registered := map[string]bool{"workspace.materialize": true, "workspace.commit": true, "workspace.admin": true}
	c := Context{Identity: ident, Registered: func(op string) bool { return registered[op] }}
	in := BlanketInput{AddonID: addon, Official: false,
		Required: []string{"workspace.materialize"}, Optional: []string{"workspace.commit"}}
	doc := emptySnapshot()
	floor := Floor{}

	if r := floor.EvaluateBlanket(doc, c, "", in); r.Applies || len(r.Plan.Grant) != 0 {
		t.Fatalf("off by default: %+v", r)
	}
	doc.Blanket = Blanket{Mode: BlanketOfficial, Revision: 3}
	if r := floor.EvaluateBlanket(doc, c, "", in); r.Applies {
		t.Fatalf("official mode needs an official package: %+v", r)
	}
	in.Official = true
	r := floor.EvaluateBlanket(doc, c, "", in)
	if !r.Applies || r.Revision != 3 || !reflect.DeepEqual(r.Plan.Grant, []string{"workspace.materialize"}) {
		t.Fatalf("required only: %+v", r)
	}
	doc.Blanket.IncludeOptional = true
	r = floor.EvaluateBlanket(doc, c, "", in)
	if !reflect.DeepEqual(r.Plan.Grant, []string{"workspace.materialize", "workspace.commit"}) {
		t.Fatalf("includeOptional: %+v", r)
	}
	// any covers a package that is not official.
	doc.Blanket = Blanket{Mode: BlanketAny, Revision: 4}
	in.Official = false
	if r = floor.EvaluateBlanket(doc, c, "", in); !r.Applies || len(r.Plan.Grant) != 1 {
		t.Fatalf("any: %+v", r)
	}
	// A deployment override replaces the stored mode.
	if r = floor.EvaluateBlanket(doc, c, BlanketOff, in); r.Applies {
		t.Fatalf("override off: %+v", r)
	}
	// A denied required operation blocks and is never granted.
	in.Required = []string{"workspace.materialize", "workspace.admin"}
	doc.Deny["*"] = []string{"workspace.admin"}
	r = floor.EvaluateBlanket(doc, c, "", in)
	if !reflect.DeepEqual(r.Plan.BlockedRequired, []string{"workspace.admin"}) || reflect.DeepEqual(r.Plan.Grant, []string{"workspace.admin"}) {
		t.Fatalf("deny beats blanket: %+v", r)
	}
	for _, g := range r.Plan.Grant {
		if g == "workspace.admin" {
			t.Fatal("blanket wrote a denied operation")
		}
	}
	// Add-ons with no declared request get nothing.
	if r = floor.EvaluateBlanket(doc, c, "", BlanketInput{AddonID: "slack"}); len(r.Plan.Grant) != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestMergeGrants(t *testing.T) {
	explicit := uiEntry(SourceInstall, ident, "workspace.commit")
	merged := MergeGrants(explicit, ident, 2, "op-2", []OpGrant{
		{Name: "workspace.commit", Source: SourceBlanket, BlanketRevision: 1},
		{Name: "egress.check", Source: SourceBlanket, BlanketRevision: 1},
	})
	if merged.OperationID != "op-2" || merged.Generation != 2 || len(merged.Ops) != 2 || merged.Ops[0].Source != SourceInstall {
		t.Fatalf("an explicit grant is never relabelled blanket: %+v", merged)
	}
	fresh := MergeGrants(explicit, "new-package", 1, "op-3", []OpGrant{{Name: "egress.check", Source: SourceInstall}})
	if len(fresh.Ops) != 1 || fresh.Identity != "new-package" {
		t.Fatalf("an entry of another identity is not carried over: %+v", fresh)
	}
}
