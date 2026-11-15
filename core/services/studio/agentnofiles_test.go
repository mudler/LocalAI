//go:build linux || darwin

package studio_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
)

// The editor's "No files" choice is a saved agent with the files group in
// builtins_off. It has to close every way a chat reaches the user's files,
// not only hide the file tools: the image helpers, the Sandbox and the Office
// add-on read the same files through the chat's access.
var _ = Describe("a chat of an agent saved with No files", func() {
	var r *fenceRig
	BeforeEach(func() { r = newFenceRig() })

	probes := func() []studiotest.ToolStep {
		steps := append(fileSteps(),
			call("bash", `{"command":"ls; ls ..; cat ../Vault/known.txt ../../Vault/known.txt ../AGENTS.md ../../AGENTS.md /workspace/home/AGENTS.md 2>&1; grep -r FENCE-LEAK .. 2>&1; ls -R .. 2>&1; true"}`),
			call("bash", `{"command":"echo x > ../escape.txt; mkdir -p ../Vault; echo x > ../Vault/made.txt; true"}`),
			call("addon__office__read-docx", `{"path":"/Vault/known.txt"}`),
			call("addon__office__read-docx", `{"path":"../Vault/known.txt"}`),
			call("addon__office__read-docx", `{"path":"/users/`+alice+`/Vault/known.txt"}`),
			call("addon__office__write-docx", `{"path":"/office.txt","content":"x"}`),
			call("addon__office__write-docx", `{"path":"/users/`+alice+`/Vault/office.txt","content":"x"}`),
		)
		return steps
	}

	save := func(off ...string) agents.Agent {
		GinkgoHelper()
		a, err := r.svc.SaveAgent(r.ctx, alice, agents.Agent{Name: "Plain", Model: "m", Addons: []string{"office", "sandbox"}, BuiltinsOff: off})
		Expect(err).NotTo(HaveOccurred())
		return a
	}

	It("control: the same agent with Files on reaches the files", func() {
		conv := r.chat(save().ID)
		res := r.run(conv, "k:control", probes()...)
		Expect(joinOut(res)).To(ContainSubstring(fenceMark))
	})

	It("cannot list, read, write or search a file, by any tool", func() {
		a := save(builtins.GroupFiles)
		conv := r.chat(a.ID)
		res := r.run(conv, "k:nofiles", append(probes(), call("bash", `{"command":"echo kept > /workspace/chat/kept.txt; cat /workspace/chat/kept.txt"}`))...)
		Expect(res[len(res)-1].Out).To(ContainSubstring("kept"), "the chat keeps its own folder: %s", res[len(res)-1].Out)
		Expect(r.h.ToolNames(conv)).NotTo(ContainElements("list_files", "read_file", "write_file", "search_files"))
		for _, x := range res {
			if x.Tool == "read_file" || x.Tool == "list_files" || x.Tool == "write_file" || x.Tool == "search_files" {
				Expect(x.Err).To(HaveOccurred(), "%s ran: %s", x.Tool, x.Out)
			}
		}
		r.expectNoLeak()
		r.expectOutsideUnchanged()
	})

	It("ignores a folder the agent still holds while Files is off", func() {
		a, err := r.svc.SaveAgentWithFolder(r.ctx, alice, agents.Agent{Name: "Both", Model: "m", Addons: []string{"office", "sandbox"}, BuiltinsOff: []string{builtins.GroupFiles}},
			&studio.AgentFolderWrite{Path: "/Projects/Acme", Mode: agents.FolderWrite})
		Expect(err).NotTo(HaveOccurred())
		conv := r.chat(a.ID)
		r.run(conv, "k:both", probes()...)
		r.expectNoLeak()
		r.expectOutsideUnchanged()
		Expect(r.exists("/Projects/Acme/office.txt")).To(BeFalse(), "the stored folder was written")
	})

	It("keeps a sub-agent of a No files chat away from the files too", func() {
		conv := r.chat(save(builtins.GroupFiles).ID)
		r.on("k:child", probes()...)
		r.run(conv, "k:spawn", call("spawn_agent", `{"task":"k:child","wait":true}`))
		r.expectNoLeak()
		r.expectOutsideUnchanged()
	})

})
