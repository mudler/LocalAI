// SPDX-License-Identifier: MIT
package studio

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/core/services/addons/packages"
	"github.com/mudler/LocalAI/core/services/addons/skilltree"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/skills"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Packaging tests cover artifact construction and installation integrity. This
// fixture starts at the verified receipt boundary and copies the real shipped
// skill into a temporary workspace; it never starts Tables or queries live data.
var _ = Describe("packaged Tables skill in a chat", func() {
	It("syncs the verified instructions and rechecks runtime, chat switches and grants before loading", func() {
		ctx := context.Background()
		raw, err := os.ReadFile("../../../addons/tables/manifest.json")
		Expect(err).NotTo(HaveOccurred())
		manifest, err := contract.ParseManifest(raw)
		Expect(err).NotTo(HaveOccurred())
		source := os.DirFS("../../../addons/tables/skills")
		verified, err := skilltree.Verify(source, manifest)
		Expect(err).NotTo(HaveOccurred())
		Expect(verified).To(HaveLen(1))
		installed := GinkgoT().TempDir()
		Expect(os.CopyFS(filepath.Join(installed, "skills"), source)).To(Succeed())
		receipt := packages.PackageReceipt{Directory: installed, Manifest: manifest, Skills: verified}
		tree, err := packages.SkillFS(receipt)
		Expect(err).NotTo(HaveOccurred())
		data, err := fs.ReadFile(tree, "tables-readonly/SKILL.md")
		Expect(err).NotTo(HaveOccurred())
		name, description, err := skills.Parse(data)
		Expect(err).NotTo(HaveOccurred())
		Expect(name).To(Equal("tables-readonly"))
		Expect(skills.Requires(data)).To(BeEmpty())
		body := strings.TrimSpace(strings.SplitN(string(data), "---", 3)[2])
		Expect(body).To(ContainSubstring("addon__tables__databases-list"))

		b := &skillBroker{}
		svc, f := newSkillService(b)
		Expect(f.SyncAddonSkills(ctx, "tables", tree)).To(Succeed())
		running := AddonSkillSet{AddonID: "tables", Title: "Tables", Skills: contract.SkillNames(manifest.Capabilities.SkillDefinitions), Running: true}
		b.set(running)
		rec, err := svc.Create(ctx, "alice", CreateInput{Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		load := svc.loadSkillRun(userChatActor("alice", rec.ID), loadOpts{})
		args := json.RawMessage(`{"name":"tables-readonly"}`)
		index := func(off, addonsOff []string, sc *scope) string {
			prompt, _, _, _, err := svc.skillTurn(ctx, "alice", off, addonsOff, sc, Commands{}, nil)
			Expect(err).NotTo(HaveOccurred())
			return prompt
		}
		allow := func() {
			Expect(index(nil, nil, nil)).To(ContainSubstring("- " + name + ": " + description))
			got, err := load(ctx, args)
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal(body), "load actual instructions without frontmatter or command/file tools")
		}
		deny := func() {
			got, err := load(ctx, args)
			Expect(got).To(BeEmpty())
			Expect(err).To(MatchError(HavePrefix(`unknown skill "tables-readonly".`)))
		}
		allow()
		// Disabled and stopped states are projected to Running=false by the adapter.
		for _, state := range []string{"disabled", "stopped"} {
			unavailable := running
			unavailable.Running = false
			b.set(unavailable)
			Expect(index(nil, nil, nil)).NotTo(ContainSubstring("- tables-readonly:"), state)
			deny()
			b.set(running)
			allow()
		}
		Expect(svc.SetAddonsOff(ctx, "alice", rec.ID, []string{"tables"})).To(Succeed())
		Expect(index(nil, []string{"tables"}, nil)).NotTo(ContainSubstring("- tables-readonly:"))
		deny()
		Expect(svc.SetAddonsOff(ctx, "alice", rec.ID, nil)).To(Succeed())
		Expect(svc.SetSkillsOff(ctx, "alice", rec.ID, []string{"addon:tables-readonly"})).To(Succeed())
		Expect(index([]string{"addon:tables-readonly"}, nil, nil)).NotTo(ContainSubstring("- tables-readonly:"))
		deny()
		Expect(svc.SetSkillsOff(ctx, "alice", rec.ID, nil)).To(Succeed())
		allow()
		agentStore, err := agents.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		svc.d.Agents = agentStore
		agent := agents.Agent{ID: "tables-reader", Principal: "alice", Name: "Tables reader", Skills: []string{name}}
		Expect(agentStore.Put(ctx, agent)).To(Succeed())
		agentRec, err := svc.Create(ctx, "alice", CreateInput{Model: "m", AgentID: agent.ID})
		Expect(err).NotTo(HaveOccurred())
		agentLoad := svc.loadSkillRun(userChatActor("alice", agentRec.ID), loadOpts{})
		for _, grant := range []bool{false, true, false} {
			agent.Addons = nil
			if grant {
				agent.Addons = []string{"tables"}
			}
			Expect(agentStore.Put(ctx, agent)).To(Succeed())
			prompt := index(nil, nil, svc.scopeOf(ctx, agentRec))
			got, err := agentLoad(ctx, args)
			if grant {
				Expect(prompt).To(ContainSubstring("- tables-readonly:"))
				Expect(err).NotTo(HaveOccurred())
				Expect(got).To(Equal(body))
			} else {
				Expect(prompt).NotTo(ContainSubstring("- tables-readonly:"))
				Expect(got).To(BeEmpty())
				Expect(err).To(MatchError(HavePrefix(`unknown skill "tables-readonly".`)))
			}
		}
		// A revoked broker grant removes the root even though the synced bytes stay.
		b.set()
		Expect(index(nil, nil, nil)).NotTo(ContainSubstring("- tables-readonly:"))
		deny()
		b.set(running)
		allow()
	})
})
