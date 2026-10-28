package gormstore

import (
	"reflect"
	"strings"
	"testing"
)

// The checkpoint queries must stay plain SQL: json_each takes an array only
// on SQLite, and PostgreSQL is not available to the unit tests.
func TestNotInRevsIsPortableSQL(t *testing.T) {
	cond, args := notInRevs("j.rev", nil)
	if cond != "1 = 1" || len(args) != 0 {
		t.Fatalf("empty list = %q %v, want a condition that always holds", cond, args)
	}
	cond, args = notInRevs("j.rev", []int64{3, 5, 8})
	if cond != "j.rev NOT IN (?, ?, ?)" {
		t.Fatalf("condition = %q", cond)
	}
	if !reflect.DeepEqual(args, []any{int64(3), int64(5), int64(8)}) {
		t.Fatalf("args = %v", args)
	}
	if strings.Contains(strings.ToLower(cond), "json") {
		t.Fatalf("condition is not portable: %q", cond)
	}
}
