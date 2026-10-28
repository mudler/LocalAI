package agents

import (
	"errors"
	"testing"
)

func TestOnlyACollisionOnTheNameIsANameTaken(t *testing.T) {
	for msg, want := range map[string]bool{
		"UNIQUE constraint failed: studio_agents.principal, studio_agents.name_key":                       true,
		`ERROR: duplicate key value violates unique constraint "idx_studio_agents_name" (SQLSTATE 23505)`: true,
		"UNIQUE constraint failed: studio_agents.id":                                                      false,
		`ERROR: duplicate key value violates unique constraint "studio_agents_pkey" (SQLSTATE 23505)`:     false,
		"database is locked": false,
	} {
		if got := isNameTaken(errors.New(msg)); got != want {
			t.Errorf("isNameTaken(%q) = %v, want %v", msg, got, want)
		}
	}
}
