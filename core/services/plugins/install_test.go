package plugins_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/plugins"
	"github.com/mudler/LocalAI/core/services/plugins/content"
	"github.com/mudler/LocalAI/core/services/plugins/store"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/jsonstore"
)

// recordQueue runs each job at once and keeps its error. A cancelled queue
// runs the job with a cancelled context, as a cancel while queued does.
type recordQueue struct {
	mu        sync.Mutex
	errs      map[string]error
	cancelled bool
}

func (q *recordQueue) Enqueue(j plugins.Job) {
	ctx, cancel := context.WithCancel(context.Background())
	if q.cancelled {
		cancel()
	}
	defer cancel()
	err := j.Run(ctx)
	q.mu.Lock()
	defer q.mu.Unlock()
	q.errs[j.ID] = err
}

func (q *recordQueue) err(id string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.errs[id]
}

const textPlugin = "text"
const codePlugin = "code"

// pluginFiles returns the files of a version of the fixture plugin "fmt".
func pluginFiles(kind, skill string) map[string]string {
	files := map[string]string{
		".claude-plugin/plugin.json": `{"name": "fmt", "version": "1.0.0", "description": "Formats code"}`,
		"skills/fmt/SKILL.md":        "---\nname: fmt\ndescription: Format code\n---\n" + skill,
		"commands/run.md":            "---\ndescription: Run it\nargument-hint: <path>\n---\nRun $ARGUMENTS",
	}
	if kind == codePlugin {
		files["hooks/hooks.json"] = `{"hooks": {"PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "${CLAUDE_PLUGIN_ROOT}/scripts/check.sh"}]}]}}`
		files["scripts/check.sh"] = "#!/bin/sh\nexit 0\n"
	}
	return files
}

func writeTree(files map[string]string) string {
	dir := GinkgoT().TempDir()
	for p, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		Expect(os.MkdirAll(filepath.Dir(full), 0o755)).To(Succeed())
		Expect(os.WriteFile(full, []byte(body), 0o644)).To(Succeed())
	}
	return dir
}

// pluginRepo is the fixture repository of the plugin "fmt": a version per
// commit, and the commit its default branch points at.
type pluginRepo struct {
	mu       sync.Mutex
	versions map[string]map[string]string
}

func (r *pluginRepo) set(sha string, files map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.versions[sha] = files
}

func (r *pluginRepo) fetch(sha string) (string, string, error) {
	r.mu.Lock()
	files, ok := r.versions[sha]
	r.mu.Unlock()
	if !ok {
		return "", "", errors.New("no such commit")
	}
	return writeTree(files), sha, nil
}

var _ = Describe("Installs", func() {
	var (
		ctx    context.Context
		st     store.Store
		ff     *fakeFetcher
		repo   *pluginRepo
		queue  *recordQueue
		clk    *testClock
		ws     content.Workspace
		svc    *plugins.Service
		userOn bool
		bob    = plugins.Actor{ID: "bob"}
		apiKey = plugins.Actor{ID: "admin", Admin: true}
	)
	const fmtURL = "https://github.com/o/fmt.git#"

	point := func(sha string) {
		ff.mu.Lock()
		defer ff.mu.Unlock()
		ff.refs[fmtURL] = sha
	}

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		st, err = store.NewFileStore("")
		Expect(err).NotTo(HaveOccurred())
		ff = newFakeFetcher()
		repo = &pluginRepo{versions: map[string]map[string]string{}}
		ff.plugins["fmt"] = repo.fetch
		queue = &recordQueue{errs: map[string]error{}}
		clk = &testClock{now: time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)}
		blobs, err := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
		Expect(err).NotTo(HaveOccurred())
		files, err := jsonstore.New("", blobs, jsonstore.Options{Admin: "admin", Now: clk.Now})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = files.Close() })
		ws = content.Workspace{Files: files, Actor: workspace.Actor{Principal: "admin"}}
		userOn = true
		svc, err = plugins.New(plugins.Options{
			Store: st, Fetcher: ff, Queue: queue, Now: clk.Now, Workspace: ws, Root: "/plugins",
			UserInstalls: func() bool { return userOn },
		})
		Expect(err).NotTo(HaveOccurred())

		ff.setMarketplace("owner/team", marketplaceJSON("team", githubEntry("fmt", "o/fmt", "")), shaA, nil)
		_, err = svc.AddMarketplace(ctx, admin, plugins.AddMarketplaceRequest{Source: "owner/team"})
		Expect(err).NotTo(HaveOccurred())
		repo.set(shaA, pluginFiles(textPlugin, "v1"))
		point(shaA)
	})

	// install inspects and installs, and returns the job error.
	install := func(a plugins.Actor, scope string) (string, error) {
		p, err := svc.Inspect(ctx, a, "fmt@team")
		if err != nil {
			return "", err
		}
		job, err := svc.Install(ctx, a, p.Token, scope)
		if err != nil {
			return "", err
		}
		return job, queue.err(job)
	}
	installs := func() []store.Install {
		list, err := st.Installs(ctx, store.InstallFilter{})
		Expect(err).NotTo(HaveOccurred())
		return list
	}

	Describe("inspect", func() {
		It("returns the commit, the digest, the components and a token", func() {
			p, err := svc.Inspect(ctx, user, "fmt@team")
			Expect(err).NotTo(HaveOccurred())
			Expect(p.SHA).To(Equal(shaA))
			Expect(content.ValidDigest(p.Digest)).To(BeTrue())
			Expect(p.RunsCode).To(BeFalse())
			Expect(p.CodeReasons).To(BeEmpty())
			Expect(p.Name).To(Equal("fmt"))
			Expect(p.Model.Skills).To(HaveLen(1))
			Expect(p.Model.Commands).To(ContainElement(HaveField("ArgumentHint", "<path>")))
			Expect(p.Token).To(HaveLen(64))
			Expect(p.ExpiresAt).To(BeTemporally("==", clk.Now().Add(plugins.TokenTTL)))
			ok, err := content.Exists(ctx, ws, "/plugins", p.Digest)
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeTrue(), "the files are shared for the review")
		})

		It("classifies a plugin with a command hook as code, with its code files", func() {
			repo.set(shaB, pluginFiles(codePlugin, "v1"))
			point(shaB)
			p, err := svc.Inspect(ctx, admin, "fmt@team")
			Expect(err).NotTo(HaveOccurred())
			Expect(p.RunsCode).To(BeTrue())
			Expect(p.CodeReasons).To(Equal([]string{"hook-command"}))
			Expect(p.CodeFiles).To(ContainElements("hooks/hooks.json", "scripts/check.sh"))
		})

		It("refuses a plugin with a reserved name", func() {
			files := pluginFiles(textPlugin, "v1")
			files[".claude-plugin/plugin.json"] = `{"name": "builtin"}`
			repo.set(shaB, files)
			point(shaB)
			_, err := svc.Inspect(ctx, user, "fmt@team")
			Expect(errors.Is(err, plugins.ErrInvalid)).To(BeTrue(), "%v", err)
		})

		It("refuses an unknown plugin and an unsupported source", func() {
			_, err := svc.Inspect(ctx, user, "nope@team")
			Expect(errors.Is(err, plugins.ErrNotFound)).To(BeTrue())
			ff.setMarketplace("owner/team", marketplaceJSON("team", `{"name":"n","source":{"source":"npm","package":"x"}}`), shaB, nil)
			_, err = svc.RefreshMarketplace(ctx, admin, "team")
			Expect(err).NotTo(HaveOccurred())
			_, err = svc.Inspect(ctx, user, "n@team")
			Expect(errors.Is(err, plugins.ErrUnsupported)).To(BeTrue())
		})
	})

	Describe("the rights of spec 9.2", func() {
		codeVersion := func() {
			repo.set(shaB, pluginFiles(codePlugin, "v1"))
			point(shaB)
		}
		DescribeTable("install",
			func(prepare func(), a *plugins.Actor, scope string, want error) {
				if prepare != nil {
					prepare()
				}
				_, err := install(*a, scope)
				if want == nil {
					Expect(err).NotTo(HaveOccurred())
					Expect(installs()).To(HaveLen(1))
					return
				}
				Expect(errors.Is(err, want)).To(BeTrue(), "got %v", err)
				Expect(installs()).To(BeEmpty())
			},
			Entry("a user installs a text-only plugin for themselves", nil, &user, store.ScopeUser, nil),
			Entry("a user cannot while personal installs are off", func() { userOn = false }, &user, store.ScopeUser, plugins.ErrNeedsAdmin),
			Entry("an administrator installs for themselves while personal installs are off", func() { userOn = false }, &admin, store.ScopeUser, nil),
			Entry("a user cannot install for everyone", nil, &user, store.ScopeInstance, plugins.ErrForbidden),
			Entry("an administrator installs for everyone", nil, &admin, store.ScopeInstance, nil),
			Entry("a user cannot install a code plugin", codeVersion, &user, store.ScopeUser, plugins.ErrNeedsAdmin),
			Entry("an administrator installs code for everyone only", codeVersion, &admin, store.ScopeUser, plugins.ErrCodeForEveryone),
			Entry("code through an API key needs the browser session", codeVersion, &apiKey, store.ScopeInstance, plugins.ErrNotHumanSurface),
			Entry("an administrator installs code for everyone in the browser", codeVersion, &admin, store.ScopeInstance, nil),
		)

		DescribeTable("remove",
			func(scope string, owner, by plugins.Actor, want error) {
				_, err := install(owner, scope)
				Expect(err).NotTo(HaveOccurred())
				err = svc.Remove(ctx, by, installs()[0].ID)
				if want == nil {
					Expect(err).NotTo(HaveOccurred())
					Expect(installs()).To(BeEmpty())
					return
				}
				Expect(errors.Is(err, want)).To(BeTrue(), "got %v", err)
				Expect(installs()).To(HaveLen(1))
			},
			Entry("the owner removes a user install", store.ScopeUser, user, user, nil),
			Entry("another user cannot see it", store.ScopeUser, user, bob, plugins.ErrNotFound),
			Entry("an administrator removes a user install", store.ScopeUser, user, admin, nil),
			Entry("a user cannot remove an instance install", store.ScopeInstance, admin, user, plugins.ErrForbidden),
			Entry("an administrator removes an instance install", store.ScopeInstance, admin, admin, nil),
		)
	})

	Describe("tokens", func() {
		It("are single use and bound to the principal", func() {
			p, err := svc.Inspect(ctx, user, "fmt@team")
			Expect(err).NotTo(HaveOccurred())
			_, err = svc.Install(ctx, bob, p.Token, store.ScopeUser)
			Expect(err).To(MatchError(plugins.ErrTokenInvalid))
			_, err = svc.Install(ctx, user, p.Token, store.ScopeUser)
			Expect(err).NotTo(HaveOccurred())
			_, err = svc.Install(ctx, user, p.Token, store.ScopeUser)
			Expect(err).To(MatchError(plugins.ErrTokenInvalid))
		})

		It("expire after 15 minutes", func() {
			p, err := svc.Inspect(ctx, user, "fmt@team")
			Expect(err).NotTo(HaveOccurred())
			clk.Advance(plugins.TokenTTL)
			_, err = svc.Install(ctx, user, p.Token, store.ScopeUser)
			Expect(err).To(MatchError(plugins.ErrTokenInvalid))
		})
	})

	It("fails the job and writes nothing when the digest differs from the review", func() {
		p, err := svc.Inspect(ctx, user, "fmt@team")
		Expect(err).NotTo(HaveOccurred())
		// The same commit gives other files: a server that rewrote history.
		repo.set(shaA, pluginFiles(textPlugin, "changed"))
		job, err := svc.Install(ctx, user, p.Token, store.ScopeUser)
		Expect(err).NotTo(HaveOccurred())
		Expect(queue.err(job)).To(MatchError(plugins.ErrDigestMismatch))
		Expect(installs()).To(BeEmpty())
		_, err = st.Package(ctx, p.Digest)
		Expect(errors.Is(err, store.ErrNotFound)).To(BeTrue())
	})

	It("writes nothing for a job cancelled while queued", func() {
		queue.cancelled = true
		_, err := install(user, store.ScopeUser)
		Expect(errors.Is(err, context.Canceled)).To(BeTrue(), "%v", err)
		Expect(installs()).To(BeEmpty())
	})

	It("writes the package row, the install and the owner's state in one go", func() {
		_, err := install(user, store.ScopeUser)
		Expect(err).NotTo(HaveOccurred())
		in := installs()[0]
		Expect(in.PluginID).To(Equal("fmt@team"))
		Expect(in.Owner).To(Equal(user.ID))
		Expect(in.SourceSHA).To(Equal(shaA))
		Expect(in.ApprovedDigest).To(Equal(in.Digest))
		pkg, err := st.Package(ctx, in.Digest)
		Expect(err).NotTo(HaveOccurred())
		Expect(pkg.Name).To(Equal("fmt"))
		Expect(pkg.FileCount).To(Equal(3))
		us, err := st.UserState(ctx, user.ID, in.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(us.Enabled).To(BeTrue())
	})

	It("refuses a second install of the same key and a user install of an instance plugin", func() {
		_, err := install(admin, store.ScopeInstance)
		Expect(err).NotTo(HaveOccurred())
		_, err = install(admin, store.ScopeInstance)
		Expect(errors.Is(err, store.ErrConflict)).To(BeTrue())
		_, err = install(user, store.ScopeUser)
		var inst *store.InstanceInstalledError
		Expect(errors.As(err, &inst)).To(BeTrue())
		Expect(inst.InstallID).To(Equal(installs()[0].ID))
	})

	It("replaces the user installs with an instance install and keeps it on for their owners", func() {
		_, err := install(user, store.ScopeUser)
		Expect(err).NotTo(HaveOccurred())
		_, err = install(bob, store.ScopeUser)
		Expect(err).NotTo(HaveOccurred())
		_, err = install(admin, store.ScopeInstance)
		Expect(err).NotTo(HaveOccurred())
		list := installs()
		Expect(list).To(HaveLen(1))
		Expect(list[0].Scope).To(Equal(store.ScopeInstance))
		for _, p := range []string{user.ID, bob.ID} {
			us, err := st.UserState(ctx, p, list[0].ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(us.Enabled).To(BeTrue())
			Expect(us.NeedsReenable).To(BeFalse())
		}
		_, err = st.UserState(ctx, admin.ID, list[0].ID)
		Expect(errors.Is(err, store.ErrNotFound)).To(BeTrue(), "an instance install starts off for everyone else")
	})

	Describe("updates", func() {
		It("blocks a user install whose new version runs code and keeps the old version", func() {
			_, err := install(user, store.ScopeUser)
			Expect(err).NotTo(HaveOccurred())
			before := installs()[0]
			repo.set(shaB, pluginFiles(codePlugin, "v2"))
			point(shaB)
			_, err = svc.PreviewUpdate(ctx, user, before.ID)
			Expect(err).To(MatchError(plugins.ErrBlocked))
			after := installs()[0]
			Expect(after.State).To(Equal(store.StateBlocked))
			Expect(after.Digest).To(Equal(before.Digest))
			Expect(after.UpdateSHA).To(Equal(shaB))
		})

		It("updates a text-only user install for its owner", func() {
			_, err := install(user, store.ScopeUser)
			Expect(err).NotTo(HaveOccurred())
			before := installs()[0]
			repo.set(shaB, pluginFiles(textPlugin, "v2\nmore\n"))
			point(shaB)
			_, err = svc.PreviewUpdate(ctx, bob, before.ID)
			Expect(errors.Is(err, plugins.ErrNotFound)).To(BeTrue())
			p, err := svc.PreviewUpdate(ctx, user, before.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(p.Changed).To(ConsistOf(HaveField("Path", "skills/fmt/SKILL.md")))
			Expect(p.NeedsCodeReview).To(BeFalse())
			job, err := svc.Update(ctx, user, before.ID, p.Token, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(queue.err(job)).NotTo(HaveOccurred())
			after := installs()[0]
			Expect(after.SourceSHA).To(Equal(shaB))
			Expect(after.PreviousDigest).To(Equal(before.Digest))
			Expect(after.State).To(Equal(store.StateInstalled))
		})

		It("a user cannot approve an update of an instance install", func() {
			_, err := install(admin, store.ScopeInstance)
			Expect(err).NotTo(HaveOccurred())
			repo.set(shaB, pluginFiles(textPlugin, "v2"))
			point(shaB)
			_, err = svc.PreviewUpdate(ctx, user, installs()[0].ID)
			Expect(err).To(MatchError(plugins.ErrForbidden))
		})

		It("needs a tick per changed code file and the browser session when code changes, and asks users to re-enable", func() {
			_, err := install(admin, store.ScopeInstance)
			Expect(err).NotTo(HaveOccurred())
			in := installs()[0]
			for _, p := range []string{user.ID, bob.ID} {
				Expect(st.SaveUserState(ctx, store.UserState{Principal: p, InstallID: in.ID, Enabled: true})).To(Succeed())
			}
			repo.set(shaB, pluginFiles(codePlugin, "v2"))
			point(shaB)
			p, err := svc.PreviewUpdate(ctx, admin, in.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(p.NeedsCodeReview).To(BeTrue())
			Expect(p.NewCodeReasons).To(Equal([]string{"hook-command"}))
			Expect(p.CodeFiles).To(ContainElements("hooks/hooks.json", "scripts/check.sh"))
			Expect(p.Diffs).To(ContainElement(HaveField("Path", "scripts/check.sh")))
			Expect(p.Added).To(ContainElement(HaveField("Code", true)))

			_, err = svc.Update(ctx, apiKey, in.ID, p.Token, p.CodeFiles)
			Expect(err).To(MatchError(plugins.ErrNotHumanSurface))
			_, err = svc.Update(ctx, admin, in.ID, p.Token, p.CodeFiles[:1])
			Expect(errors.Is(err, plugins.ErrInvalid)).To(BeTrue())
			job, err := svc.Update(ctx, admin, in.ID, p.Token, p.CodeFiles)
			Expect(err).NotTo(HaveOccurred())
			Expect(queue.err(job)).NotTo(HaveOccurred())

			after := installs()[0]
			Expect(after.RunsCode).To(BeTrue())
			for _, pr := range []string{user.ID, bob.ID} {
				us, err := st.UserState(ctx, pr, in.ID)
				Expect(err).NotTo(HaveOccurred())
				Expect(us.NeedsReenable).To(BeTrue(), pr)
			}
			view, err := svc.TurnView(ctx, user.ID, plugins.ChatRef{})
			Expect(err).NotTo(HaveOccurred())
			Expect(view).To(BeEmpty(), "a plugin waiting for a re-enable does not contribute")
		})

		It("answers that there is no update at the installed commit", func() {
			_, err := install(user, store.ScopeUser)
			Expect(err).NotTo(HaveOccurred())
			_, err = svc.PreviewUpdate(ctx, user, installs()[0].ID)
			Expect(err).To(MatchError(plugins.ErrNoUpdate))
		})
	})

	Describe("rollback", func() {
		var in store.Install
		BeforeEach(func() {
			_, err := install(user, store.ScopeUser)
			Expect(err).NotTo(HaveOccurred())
			repo.set(shaB, pluginFiles(textPlugin, "v2"))
			point(shaB)
			p, err := svc.PreviewUpdate(ctx, user, installs()[0].ID)
			Expect(err).NotTo(HaveOccurred())
			_, err = svc.Update(ctx, user, p.InstallID, p.Token, nil)
			Expect(err).NotTo(HaveOccurred())
			in = installs()[0]
		})

		It("sets the previous version again and offers the newer one as an update", func() {
			Expect(svc.Rollback(ctx, user, in.ID)).To(Succeed())
			after := installs()[0]
			Expect(after.Digest).To(Equal(in.PreviousDigest))
			Expect(after.SourceSHA).To(Equal(shaA))
			Expect(after.State).To(Equal(store.StateUpdateAvailable))
			Expect(after.UpdateSHA).To(Equal(shaB))
			Expect(svc.Rollback(ctx, user, in.ID)).To(MatchError(plugins.ErrInvalid))
		})

		It("needs the files of the previous package", func() {
			Expect(content.Remove(ctx, ws, "/plugins", in.PreviousDigest)).To(Succeed())
			Expect(errors.Is(svc.Rollback(ctx, user, in.ID), plugins.ErrNotFound)).To(BeTrue())
		})
	})

	Describe("the turn view", func() {
		It("shows a user install to its owner, and an instance install once a user turns it on", func() {
			_, err := install(user, store.ScopeUser)
			Expect(err).NotTo(HaveOccurred())
			view, err := svc.TurnView(ctx, user.ID, plugins.ChatRef{})
			Expect(err).NotTo(HaveOccurred())
			Expect(view).To(HaveLen(1))
			Expect(view[0].Name).To(Equal("fmt"))
			Expect(view[0].Model.Skills).To(HaveLen(1))
			view, err = svc.TurnView(ctx, bob.ID, plugins.ChatRef{})
			Expect(err).NotTo(HaveOccurred())
			Expect(view).To(BeEmpty())
		})

		It("leaves out what the chat switched off and what the agent does not grant", func() {
			_, err := install(admin, store.ScopeInstance)
			Expect(err).NotTo(HaveOccurred())
			id := installs()[0].ID
			view, err := svc.TurnView(ctx, user.ID, plugins.ChatRef{})
			Expect(err).NotTo(HaveOccurred())
			Expect(view).To(BeEmpty(), "an instance install starts off")
			Expect(st.SaveUserState(ctx, store.UserState{Principal: user.ID, InstallID: id, Enabled: true})).To(Succeed())
			view, err = svc.TurnView(ctx, user.ID, plugins.ChatRef{})
			Expect(err).NotTo(HaveOccurred())
			Expect(view).To(HaveLen(1))
			view, err = svc.TurnView(ctx, user.ID, plugins.ChatRef{PluginsOff: []string{id}})
			Expect(err).NotTo(HaveOccurred())
			Expect(view).To(BeEmpty())
			view, err = svc.TurnView(ctx, user.ID, plugins.ChatRef{HasAgent: true})
			Expect(err).NotTo(HaveOccurred())
			Expect(view).To(BeEmpty())
			view, err = svc.TurnView(ctx, user.ID, plugins.ChatRef{HasAgent: true, AgentPlugins: []string{id}})
			Expect(err).NotTo(HaveOccurred())
			Expect(view).To(HaveLen(1))
		})
	})
})
