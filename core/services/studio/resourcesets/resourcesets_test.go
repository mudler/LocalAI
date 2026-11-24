package resourcesets_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/core/services/studio/resourcesets"
)

func TestAllows(t *testing.T) {
	s := resourcesets.Sets{"tables": {{ID: "db-a", Mode: "read"}, {ID: "db-b", Mode: "write"}}}
	cases := []struct {
		addon, id string
		write     bool
		want      bool
	}{
		{"tables", "db-a", false, true},
		{"tables", "db-a", true, false},
		{"tables", "db-b", false, true},
		{"tables", "db-b", true, true},
		{"tables", "db-c", false, false},
		{"other", "db-a", false, false},
	}
	for _, c := range cases {
		if got := s.Allows(c.addon, c.id, c.write); got != c.want {
			t.Errorf("%v: got %v", c, got)
		}
	}
	if (resourcesets.Sets)(nil).Allows("tables", "db-a", false) {
		t.Error("a nil set must allow nothing")
	}
}

func TestValidate(t *testing.T) {
	ok := resourcesets.Sets{"tables": {{ID: "db-a", Mode: "read"}}}
	if err := resourcesets.Validate(ok); err != nil {
		t.Fatal(err)
	}
	bad := map[string]resourcesets.Sets{
		"bad addon":  {"Bad Name": {{ID: "db-a", Mode: "read"}}},
		"empty id":   {"tables": {{ID: "", Mode: "read"}}},
		"path id":    {"tables": {{ID: "../x", Mode: "read"}}},
		"no mode":    {"tables": {{ID: "db-a"}}},
		"bad mode":   {"tables": {{ID: "db-a", Mode: "admin"}}},
		"repeat":     {"tables": {{ID: "db-a", Mode: "read"}, {ID: "db-a", Mode: "write"}}},
		"long id":    {"tables": {{ID: "a" + strings.Repeat("b", 80), Mode: "read"}}},
		"too many":   {"tables": make([]resourcesets.Entry, resourcesets.MaxEntriesPerAddon+1)},
		"too many a": manyAddons(resourcesets.MaxAddons + 1),
	}
	for name, s := range bad {
		if err := resourcesets.Validate(s); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func manyAddons(n int) resourcesets.Sets {
	out := resourcesets.Sets{}
	for i := range n {
		out[fmt.Sprintf("a%d", i)] = []resourcesets.Entry{{ID: "db-a", Mode: "read"}}
	}
	return out
}

func TestNormalizeCloneEqual(t *testing.T) {
	in := resourcesets.Sets{"tables": {{ID: "db-a", Mode: "read"}, {ID: "db-a", Mode: "write"}, {ID: "db-b", Mode: "read"}}, "empty": nil}
	got := resourcesets.Normalize(in)
	want := resourcesets.Sets{"tables": {{ID: "db-a", Mode: "write"}, {ID: "db-b", Mode: "read"}}}
	if !resourcesets.Equal(got, want) {
		t.Fatalf("got %v", got)
	}
	c := resourcesets.Clone(got)
	c["tables"][0].Mode = "read"
	if got["tables"][0].Mode != "write" {
		t.Fatal("a clone shares its entries")
	}
	if resourcesets.Clone(nil) != nil || resourcesets.Normalize(resourcesets.Sets{"x": nil}) != nil {
		t.Fatal("an empty set is nil")
	}
	raw, _ := json.Marshal(want)
	if string(raw) != `{"tables":[{"id":"db-a","mode":"write"},{"id":"db-b","mode":"read"}]}` {
		t.Fatalf("wire form: %s", raw)
	}
}
