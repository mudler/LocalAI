package agents

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("name collision detection", func() {
	It("only a collision on the name is a name taken", func() {
		for msg, want := range map[string]bool{
			"UNIQUE constraint failed: studio_agents.principal, studio_agents.name_key":                       true,
			`ERROR: duplicate key value violates unique constraint "idx_studio_agents_name" (SQLSTATE 23505)`: true,
			"UNIQUE constraint failed: studio_agents.id":                                                      false,
			`ERROR: duplicate key value violates unique constraint "studio_agents_pkey" (SQLSTATE 23505)`:     false,
			"database is locked": false,
		} {
			Expect(isNameTaken(errors.New(msg))).To(Equal(want), msg)
		}
	})
})
