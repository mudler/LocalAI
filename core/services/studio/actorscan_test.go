package studio_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The access of a chat comes from one resolver (chatAccess): it fences the
// actor when the chat's agent has a folder. Code that builds a workspace
// actor some other way gets an actor with no fence, which reaches the whole
// home. This scan finds every place that builds one, so a new caller either
// uses the chat's access or is added here in review, with its reason.
//
// It finds, in the non-test sources of the scanned folders:
//   - a workspace.Actor{...} literal with fields (the empty literal names no
//     principal and reaches nothing);
//   - a call of files.UserActor;
//   - a call X.Actor(p) with one argument: files.Service.Actor, the user's
//     actor by principal. (session.Access.Actor takes none.)

// actorScanDirs are the folders scanned, relative to this package.
var actorScanDirs = []string{".", "hostexec", "sandbox/runner", "../addons/hostops", "../../application"}

// actorAllowlist names each function that may build an actor, as
// "folder/file.go:Func" (Func is "Type.Method" for a method), with why.
var actorAllowlist = map[string]string{
	// The resolver and a person's own action on a chat.
	"agentaccess.go:userChatActor": "the base actor that chatAccess fences, and the actor of a person's delete",
	// Host reads of the user's settings and choices, not the model's reads.
	"agentfolder.go:folderResolver.NodeOf":                              "resolves the folder a person chooses for an agent",
	"agentfolder.go:folderResolver.pathsOf":                             "finds where an agent's folder is now, for the person and for chatAccess",
	"chatcommands.go:Service.chatSkills":                                "lists the skills a person may run as commands",
	"skillsvc.go:Service.Skills":                                        "lists the user's skills for the skills page",
	"skillsvc.go:Service.chatSkillSet":                                  "reads the index of the skills the chat may use",
	"loadskill.go:Service.loadSkill":                                    "reads only skills the agent grants, by name",
	"chatfiles.go:Service.ResolveAttachments":                           "the attachments a person sees in the transcript are a user surface",
	"service.go:New":                                                    "the administrator of the change feed",
	"../../application/studio_files.go:Application.initStudioTextIndex": "the administrator of the text index",
	// Not converted yet: each is the work of a later lane of the agent folder
	// plan, and must move to the chat's access there.
	"triggersvc.go:Triggers.checkFolder":                                "lane 7: the folder of a file trigger at each run",
	"triggersvc.go:Triggers.runMessage":                                 "lane 7: the rules of a trigger run",
	"triggersvc.go:Triggers.ensureRunner":                               "lane 7: the administrator of the trigger watcher",
	"triggerscrud.go:Triggers.build":                                    "lane 7: the folder of a trigger at create and update",
	"triggerscrud.go:Triggers.ConfirmProposal":                          "lane 7: the folder of a proposed trigger",
	"connectorfiles.go:Service.readChatFile":                            "lane 7: the files a connector reply sends",
	"sandbox/runner/runner.go:New":                                      "lane 5: the default actor hook of the sandbox runner",
	"../addons/hostops/engine_workspace.go:WorkspaceHost.view":          "lane 6: add-on workspace operations",
	"../addons/hostops/engine_workspace.go:WorkspaceHost.changes":       "lane 6: add-on workspace operations",
	"../addons/hostops/engine_workspace.go:WorkspaceHost.export":        "lane 6: add-on workspace operations",
	"../addons/hostops/engine_workspace.go:WorkspaceHost.importFile":    "lane 6: add-on workspace operations",
	"../../application/studio_service.go:Application.initStudioService": "lane 5: the actor hook of the host bash",
}

// actorBuilds returns the places in src that build an actor, as
// "Func: what" lines, by function.
func actorBuilds(fset *token.FileSet, f *ast.File) map[string][]string {
	out := map[string][]string{}
	var cur string
	record := func(n ast.Node, what string) {
		out[cur] = append(out[cur], fmt.Sprintf("line %d: %s", fset.Position(n.Pos()).Line, what))
	}
	visit := func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CompositeLit:
			if sel, ok := x.Type.(*ast.SelectorExpr); ok && sel.Sel.Name == "Actor" && len(x.Elts) > 0 {
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == "workspace" {
					record(x, "workspace.Actor literal")
				}
			}
		case *ast.CallExpr:
			sel, ok := x.Fun.(*ast.SelectorExpr)
			if !ok {
				break
			}
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == "files" && sel.Sel.Name == "UserActor" {
				record(x, "files.UserActor")
			} else if sel.Sel.Name == "Actor" && len(x.Args) == 1 {
				record(x, "an Actor(principal) call")
			}
		}
		return true
	}
	for _, d := range f.Decls {
		cur = "<package>"
		if fd, ok := d.(*ast.FuncDecl); ok {
			cur = fd.Name.Name
			if fd.Recv != nil && len(fd.Recv.List) == 1 {
				t := fd.Recv.List[0].Type
				if st, ok := t.(*ast.StarExpr); ok {
					t = st.X
				}
				if id, ok := t.(*ast.Ident); ok {
					cur = id.Name + "." + cur
				}
			}
		}
		ast.Inspect(d, visit)
	}
	return out
}

var _ = Describe("actor scan", func() {
	It("builds no workspace actor outside the allowlist", func() {
		fset := token.NewFileSet()
		found := map[string][]string{}
		for _, dir := range actorScanDirs {
			entries, err := os.ReadDir(dir)
			Expect(err).NotTo(HaveOccurred(), "read %s", dir)
			for _, e := range entries {
				name := e.Name()
				if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
					continue
				}
				p := filepath.ToSlash(filepath.Join(dir, name))
				f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
				Expect(err).NotTo(HaveOccurred(), "parse %s", p)
				for fn, lines := range actorBuilds(fset, f) {
					found[p+":"+fn] = lines
				}
			}
		}
		var stray, stale []string
		for k, lines := range found {
			if _, ok := actorAllowlist[k]; !ok {
				stray = append(stray, k+" ("+strings.Join(lines, "; ")+")")
			}
		}
		for k := range actorAllowlist {
			if _, ok := found[k]; !ok {
				stale = append(stale, k)
			}
		}
		sort.Strings(stray)
		sort.Strings(stale)
		Expect(stray).To(BeEmpty(), "these build a workspace actor without the chat's access: use chatAccess (or ChatAccess), or add them to actorAllowlist with the reason")
		Expect(stale).To(BeEmpty(), "these allowlist entries build no actor any more: remove them")
	})

	It("catches a literal, a UserActor call and a Files.Actor call", func() {
		src := `package x
func a() { _ = workspace.Actor{Principal: "p"} }
func b() { _ = files.UserActor("p") }
func (s *S) c() { _ = s.d.Files.Actor("p") }
func d() { _ = workspace.Actor{}; _ = x.Actor() }
`
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, "x.go", src, parser.SkipObjectResolution)
		Expect(err).NotTo(HaveOccurred())
		got := actorBuilds(fset, f)
		Expect(got).To(HaveKey("a"))
		Expect(got).To(HaveKey("b"))
		Expect(got).To(HaveKey("S.c"))
		Expect(got).NotTo(HaveKey("d"), "the empty literal and Access.Actor()")
	})
})
