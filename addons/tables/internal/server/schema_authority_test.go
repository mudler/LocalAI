// SPDX-License-Identifier: MIT
package server_test

import (
	"context"
	"errors"

	"github.com/mudler/LocalAI/addons/internal/protocol"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The host derives the schema action from the tool kind and admits only the
// owner to it. The add-on checks the same rule itself, so a caller that reaches
// the runtime with a row action cannot change a schema.
var _ = Describe("the add-on's own schema authority", func() {
	ctx := context.Background()
	schemaTools := map[string]map[string]any{
		"databases-rename": {"name": "Renamed"},
		"tables-create":    {"table": "Orders"},
		"columns-add":      {"table": "People", "column": "City", "type": "text"},
	}
	for tool, args := range schemaTools {
		It("refuses "+tool+" under a write action, on preview and on apply", func() {
			r := newExportRig()
			a := map[string]any{"tool": tool}
			for k, v := range args {
				a[k] = v
			}
			_, e := r.t.Preview(ctx, r.env("editor", "op-"+tool, "preview", "write", a))
			Expect(errors.Is(e, protocol.ErrForbidden)).To(BeTrue(), "preview: %v", e)
			_, e = r.t.Apply(ctx, r.env("editor", "op-"+tool, "apply", "write", a), contract.Revision{Schema: 1})
			Expect(errors.Is(e, protocol.ErrForbidden)).To(BeTrue(), "apply: %v", e)
		})
		It("refuses "+tool+" under the schema action with the wrong scope or no actor", func() {
			r := newExportRig()
			a := map[string]any{"tool": tool}
			for k, v := range args {
				a[k] = v
			}
			_, e := r.t.Preview(ctx, r.env("owner", "op-"+tool, "apply", "schema", a))
			Expect(errors.Is(e, protocol.ErrForbidden)).To(BeTrue(), "preview with scope apply: %v", e)
			_, e = r.t.Apply(ctx, r.env("", "op-"+tool, "apply", "schema", a), contract.Revision{Schema: 1})
			Expect(errors.Is(e, protocol.ErrForbidden)).To(BeTrue(), "apply without an actor: %v", e)
		})
	}
	It("still previews and applies a schema change under the schema action", func() {
		r := newExportRig()
		a := map[string]any{"tool": "databases-rename", "name": "Renamed"}
		p, e := r.t.Preview(ctx, r.env("owner", "op-ok", "preview", "schema", a))
		Expect(e).NotTo(HaveOccurred())
		rc, e := r.t.Apply(ctx, r.env("owner", "op-ok", "apply", "schema", a), p.Revision)
		Expect(e).NotTo(HaveOccurred())
		Expect(rc.State).To(Equal(contract.ReceiptCommitted))
	})
})
