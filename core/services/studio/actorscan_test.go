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
	"agentaccess.go:userChatActor": "the base actor that chatAccess and agentReader fence, and the actor of a person's delete",
	// Host reads of the user's settings and choices, not the model's reads.
	"agentfolder.go:folderResolver.NodeOf":                              "resolves the folder a person chooses for an agent",
	"agentfolder.go:folderResolver.pathsOf":                             "finds where an agent's folder is now, for the person and for chatAccess",
	"chatcommands.go:Service.chatSkills":                                "lists the skills a person may run as commands",
	"skillsvc.go:Service.Skills":                                        "lists the user's skills for the skills page",
	"skillsvc.go:Service.AgentSkills":                                   "lists the agent-layer skills of the user's own agents for the skills page; a person's view, never the model's",
	"skillsvc.go:Service.chatSkillSet":                                  "reads the index of the skills the chat may use",
	"loadskill.go:Service.loadSkill":                                    "reads only skills the agent grants, by name",
	"chatfiles.go:Service.ResolveAttachments":                           "the attachments a person sees in the transcript are a user surface",
	"contextlist.go:Service.countLines":                                 "the owner's own Memory page lists the notes of their home; a person's action, never the model's",
	"contextlist.go:Service.listContextDir":                             "the owner's own Memory page lists the notes of their home; a person's action, never the model's",
	"contextlist.go:listNames":                                          "the owner's own Memory page lists the notes of their home; a person's action, never the model's",
	"changes.go:Changes.Revert":                                         "undoes the change set that the owner confirmed; a person's own action, never the model's",
	"contextturn.go:homeReader.ReadFile":                                "the host reads the user's notes at paths the loader builds from the chat record, never from the model; the chat's fence is not widened",
	"service.go:New":                                                    "the administrator of the change feed and the trigger watcher",
	"../../application/studio_files.go:Application.initStudioTextIndex": "the administrator of the text index",
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
		cur = funcName(d)
		ast.Inspect(d, visit)
	}
	return out
}

// The view of a chat is built once per turn, by chatAccess through the
// session manager (UserAccess for the home, ScopedAccess for an agent's
// folder), and handed down as a session.Access. A consumer that builds a view
// of its own would see more than the turn's access: the home of a scoped
// chat. viewBuilds finds, in the same folders:
//   - a call of BuildView or SkillLayerMounts;
//   - a call of NewAccess, UserAccess, ScopedAccess or PersonAccess;
//   - a call X.View(ctx, actor, mounts) with three arguments: a store's View.
//     (session.Access.View takes none.)
var viewAllowlist = map[string]string{
	"agentaccess.go:Service.chatAccess": "the resolver: the one place a chat's access is built",
	// A user surface: an add-on call that no chat made (an add-on page, the
	// web interface) works on the whole home, as the Files page does. A
	// chat's add-on call takes chatAccess.
	"addonaccess.go:Service.AddonAccess": "the access of an add-on call that no chat made",
	// Not converted yet: each is the work of a later lane of the agent folder
	// plan, and must take the chat's access there.
}

func viewBuilds(fset *token.FileSet, f *ast.File) map[string][]string {
	return scanCalls(fset, f, func(x *ast.CallExpr) string {
		name := ""
		switch fn := x.Fun.(type) {
		case *ast.Ident:
			name = fn.Name
		case *ast.SelectorExpr:
			name = fn.Sel.Name
		}
		switch {
		case name == "BuildView" || name == "SkillLayerMounts":
			return name
		case name == "NewAccess" || name == "UserAccess" || name == "ScopedAccess" || name == "PersonAccess":
			return name
		case name == "View" && len(x.Args) == 3:
			return "a store's View"
		}
		return ""
	})
}

// scanCalls returns, by function, the calls that what names.
func scanCalls(fset *token.FileSet, f *ast.File, what func(*ast.CallExpr) string) map[string][]string {
	out := map[string][]string{}
	for _, d := range f.Decls {
		cur := funcName(d)
		ast.Inspect(d, func(n ast.Node) bool {
			if x, ok := n.(*ast.CallExpr); ok {
				if w := what(x); w != "" {
					out[cur] = append(out[cur], fmt.Sprintf("line %d: %s", fset.Position(x.Pos()).Line, w))
				}
			}
			return true
		})
	}
	return out
}

// funcName names a declaration as the allowlists do.
func funcName(d ast.Decl) string {
	fd, ok := d.(*ast.FuncDecl)
	if !ok {
		return "<package>"
	}
	name := fd.Name.Name
	if fd.Recv != nil && len(fd.Recv.List) == 1 {
		t := fd.Recv.List[0].Type
		if st, ok := t.(*ast.StarExpr); ok {
			t = st.X
		}
		if id, ok := t.(*ast.Ident); ok {
			name = id.Name + "." + name
		}
	}
	return name
}

// scanSources runs scan on the non-test sources of actorScanDirs and checks
// the result against allow: a finding not on it is stray, an entry with no
// finding is stale.
func scanSources(scan func(*token.FileSet, *ast.File) map[string][]string, allow map[string]string) (stray, stale []string) {
	GinkgoHelper()
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
			for fn, lines := range scan(fset, f) {
				found[p+":"+fn] = lines
			}
		}
	}
	for k, lines := range found {
		if _, ok := allow[k]; !ok {
			stray = append(stray, k+" ("+strings.Join(lines, "; ")+")")
		}
	}
	for k := range allow {
		if _, ok := found[k]; !ok {
			stale = append(stale, k)
		}
	}
	sort.Strings(stray)
	sort.Strings(stale)
	return stray, stale
}

var _ = Describe("actor scan", func() {
	It("builds no workspace actor outside the allowlist", func() {
		stray, stale := scanSources(actorBuilds, actorAllowlist)
		Expect(stray).To(BeEmpty(), "these build a workspace actor without the chat's access: use chatAccess (or ChatAccess), or add them to actorAllowlist with the reason")
		Expect(stale).To(BeEmpty(), "these allowlist entries build no actor any more: remove them")
	})

	It("builds no view or access outside the resolver", func() {
		stray, stale := scanSources(viewBuilds, viewAllowlist)
		Expect(stray).To(BeEmpty(), "these build a view of their own instead of taking the turn's session.Access: pass the access down, or add them to viewAllowlist with the reason")
		Expect(stale).To(BeEmpty(), "these allowlist entries build no view any more: remove them")
	})

	It("catches a view builder, an access constructor and a store's View", func() {
		src := `package x
func a() { _, _ = session.BuildView(ctx, st, ac, p, "", nil, l) }
func b() { _, _ = st.View(ctx, ac, reqs) }
func (s *S) c() { _, _ = s.mgr.UserAccess(ctx, ac) }
func d() { _, _ = session.NewAccess(ac, v) }
func e() { _ = x.View(); _ = x.Place() }
func (s *S) f() { _, _ = s.mgr.PersonAccess(ctx, ac) }
func g() { _, _ = x.SubView(ctx, st, reqs) }
`
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, "x.go", src, parser.SkipObjectResolution)
		Expect(err).NotTo(HaveOccurred())
		got := viewBuilds(fset, f)
		Expect(got).To(HaveKey("a"))
		Expect(got).To(HaveKey("b"))
		Expect(got).To(HaveKey("S.c"))
		Expect(got).To(HaveKey("d"))
		Expect(got).NotTo(HaveKey("e"), "Access.View() builds nothing")
		Expect(got).To(HaveKey("S.f"))
		Expect(got).NotTo(HaveKey("g"), "a sub-view of an access reaches no more than the access")
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
