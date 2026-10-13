// SPDX-License-Identifier: MIT
package addons

import (
	"context"
	"errors"

	"github.com/mudler/LocalAI/core/services/addons/packages"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("declared tools of the installed generation", Label("tools"), func() {
	It("answers from the current installed manifest only and follows a generation change", func() {
		cfg, c, i := fixture()
		m := open(cfg, c, i)
		v := contract.VersionRange{Min: 1, Max: 1}
		receipt := func(id string, tools ...string) packages.PackageReceipt {
			return packages.PackageReceipt{Manifest: contract.Manifest{SchemaVersion: 1, ID: id, Version: "1.0.0", Executable: "bin/x", OS: "linux", Arch: "amd64", Host: v, API: v, Protocol: v, DataFormat: v, Capabilities: contract.Capabilities{Service: true, Tools: tools}}}
		}
		m.journal.Addons["notes"] = Status{AddonID: "notes", Installed: true, DesiredEnabled: true, Generation: 2, Generations: []Generation{
			{Number: 1, Receipt: receipt("notes", "old-tool")}, {Number: 2, Receipt: receipt("notes", "summarize")}}}
		m.journal.Addons["gone"] = Status{AddonID: "gone", Installed: false, Generation: 1, Generations: []Generation{{Number: 1, Receipt: receipt("gone", "x")}}}
		got, e := m.DeclaredTools(ctx, "notes", 2)
		Expect(e).NotTo(HaveOccurred())
		Expect(got).To(Equal([]string{"summarize"}))
		for name, call := range map[string]func() error{
			"an older generation": func() error { _, e := m.DeclaredTools(ctx, "notes", 1); return e },
			"a future generation": func() error { _, e := m.DeclaredTools(ctx, "notes", 3); return e },
			"uninstalled":         func() error { _, e := m.DeclaredTools(ctx, "gone", 1); return e },
			"unknown":             func() error { _, e := m.DeclaredTools(ctx, "missing", 1); return e },
		} {
			Expect(call()).To(HaveOccurred(), name)
		}
		// An upgrade moves the generation: the cache follows it.
		st := m.journal.Addons["notes"]
		st.Generation = 3
		st.Generations = append(st.Generations, Generation{Number: 3, Receipt: receipt("notes", "translate")})
		m.journal.Addons["notes"] = st
		got, e = m.DeclaredTools(ctx, "notes", 3)
		Expect(e).NotTo(HaveOccurred())
		Expect(got).To(Equal([]string{"translate"}))
		_, e = m.DeclaredTools(ctx, "notes", 2)
		Expect(e).To(HaveOccurred())
		// An uncertain journal is not a declaration.
		m.uncertain = true
		_, e = m.DeclaredTools(ctx, "notes", 3)
		Expect(e).To(MatchError(ErrRecoveryRequired))
		m.uncertain = false
		m.runtimeRecoveryRequired = true
		_, e = m.DeclaredTools(ctx, "notes", 3)
		Expect(e).To(MatchError(ErrRecoveryRequired))
		m.runtimeRecoveryRequired = false
		// A real denial stays a denial and is never an unavailable error.
		_, e = m.DeclaredTools(ctx, "notes", 2)
		Expect(e).To(MatchError(ErrForbidden))
		Expect(errors.Is(e, ErrRecoveryRequired) || errors.Is(e, ErrNotOwner)).To(BeFalse())
		// A done context is reported as such, not as a denial.
		done, cancel := context.WithCancel(ctx)
		cancel()
		_, e = m.DeclaredTools(done, "notes", 3)
		Expect(e).To(MatchError(context.Canceled))
		// A closed manager is not the owner.
		Expect(m.Close()).To(Succeed())
		_, e = m.DeclaredTools(ctx, "notes", 3)
		Expect(e).To(MatchError(ErrNotOwner))
	})
})
