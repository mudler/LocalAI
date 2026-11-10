package sandboxpolicy_test

import (
	"strings"
	"testing"

	"github.com/mudler/LocalAI/pkg/sandboxpolicy"
)

func TestOpenByDefault(t *testing.T) {
	var p sandboxpolicy.Policy
	for _, id := range []string{"base", "media", "heavy"} {
		if !p.Allows(id, "alice", "user") {
			t.Errorf("%s: the empty policy must allow everyone", id)
		}
	}
	if p.DefaultProfile() != "base" || p.Pinned("media") {
		t.Error("the empty policy defaults to base and pins nothing")
	}
}

func TestAccessByRoleAndUser(t *testing.T) {
	p := sandboxpolicy.Policy{Access: map[string]sandboxpolicy.Access{
		"media": {Roles: []string{"admin"}, Users: []string{"carol"}},
		"heavy": {Roles: []string{}},
		"base":  {Roles: []string{"admin"}},
	}}
	cases := []struct {
		profile, user, role string
		want                bool
	}{
		{"media", "root", "admin", true},
		{"media", "carol", "user", true},
		{"media", "alice", "user", false},
		{"media", "alice", "", false},
		{"heavy", "alice", "user", true},
		{"base", "alice", "user", true}, // the fallback is never closed
	}
	for _, c := range cases {
		if got := p.Allows(c.profile, c.user, c.role); got != c.want {
			t.Errorf("%+v: got %v", c, got)
		}
	}
}

func TestParseRefusesBadPolicies(t *testing.T) {
	good := `{"default":"media","access":{"media":{"roles":["admin"],"users":["alice"]}},"memory_mib":4096,"disk_mib":20480,"pin_previous":["media"]}`
	p, err := sandboxpolicy.Parse([]byte(good))
	if err != nil || p.Default != "media" || !p.Pinned("media") || p.Access["media"].Users[0] != "alice" {
		t.Fatalf("good policy: %+v, %v", p, err)
	}
	if _, err := sandboxpolicy.Parse([]byte(`{}`)); err != nil {
		t.Fatalf("the empty policy is valid: %v", err)
	}
	for name, raw := range map[string]string{
		"unknown field": `{"x":1}`,
		"bad default":   `{"default":"Media Pro"}`,
		"bad role":      `{"access":{"media":{"roles":["root"]}}}`,
		"bad id":        `{"access":{"Me dia":{}}}`,
		"empty user":    `{"access":{"media":{"users":[""]}}}`,
		"low memory":    `{"memory_mib":10}`,
		"huge disk":     `{"disk_mib":99999999999}`,
		"repeat pin":    `{"pin_previous":["media","media"]}`,
		"trailing":      `{} {}`,
		"not an object": `[]`,
		"too large":     `{"default":"` + strings.Repeat("a", 70<<10) + `"}`,
	} {
		if _, err := sandboxpolicy.Parse([]byte(raw)); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestNormalizedDropsOpenAccess(t *testing.T) {
	p := sandboxpolicy.Policy{Access: map[string]sandboxpolicy.Access{"media": {}, "heavy": {Roles: []string{"admin"}}}}.Normalized()
	if _, ok := p.Access["media"]; ok || len(p.Access) != 1 || p.PinPrevious == nil {
		t.Fatalf("%+v", p)
	}
}
