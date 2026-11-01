//go:build linux || darwin

package hostexec_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing/fstest"
	"time"

	"gorm.io/gorm"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/hostexec"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/gormstore"
	"github.com/mudler/LocalAI/pkg/sqlitedb"
)

func newFiles() *files.Service {
	dsn := "file:" + filepath.Join(GinkgoT().TempDir(), "ws.db") + "?_busy_timeout=5000&_txlock=immediate"
	db, err := sqlitedb.Open(dsn, &gorm.Config{})
	Expect(err).NotTo(HaveOccurred())
	blobs, err := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
	Expect(err).NotTo(HaveOccurred())
	ws, err := gormstore.New(db, blobs, gormstore.Options{Admin: files.AdminPrincipal})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() {
		if raw, err := db.DB(); err == nil {
			_ = raw.Close()
		}
	})
	return files.New(ws)
}

type env struct {
	f     *files.Service
	r     *hostexec.Runner
	audit hostexec.AuditStore
	a     workspace.Actor
}

// userActor is the actor of an unscoped chat, as the chat's access gives it.
func userActor(_ context.Context, user, chat string) (workspace.Actor, error) {
	a := files.UserActor(user)
	a.ChatID = chat
	return a, nil
}

// homeView is the view the chat's access gives an unscoped chat: the home,
// the shared folders that shared lists, and the skill layers.
func homeView(f *files.Service, shared []session.SharedRoot) func(context.Context, workspace.Actor) (workspace.View, error) {
	return func(ctx context.Context, a workspace.Actor) (workspace.View, error) {
		v, err := session.BuildView(ctx, f.Store(), a, session.HomePath(a.Principal), "", shared, slog.New(slog.DiscardHandler))
		if err != nil {
			return workspace.View{}, err
		}
		x, err := session.NewAccess(a, v)
		if err != nil {
			return workspace.View{}, err
		}
		return x.SandboxView(ctx, f.Store())
	}
}

func newEnv(tweak ...func(*env, *hostexec.Options)) *env {
	if os.Geteuid() == 0 {
		Skip("the host backend refuses to run as root")
	}
	f := newFiles()
	Expect(f.EnsureHome(context.Background(), "alice")).To(Succeed())
	audit, _ := hostexec.NewJSONAudit("")
	e := &env{f: f, audit: audit}
	o := hostexec.Options{Store: f.Store(), StateDir: filepath.Join(GinkgoT().TempDir(), "scratch"), Audit: audit,
		Actor: userActor, View: homeView(f, nil)}
	for _, t := range tweak {
		t(e, &o)
	}
	r := hostexec.New(o)
	Expect(r.Unavailable()).To(BeEmpty())
	DeferCleanup(func() { _ = r.Close(context.Background()) })
	a := files.UserActor("alice")
	a.ChatID = "c1"
	e.r, e.a = r, a
	return e
}

func (e *env) bash(cmd string, timeout int) string {
	args := map[string]any{"command": cmd}
	if timeout > 0 {
		args["timeoutSeconds"] = timeout
	}
	raw, _ := json.Marshal(args)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out, err := e.r.Run(ctx, e.a, raw)
	Expect(err).NotTo(HaveOccurred(), cmd)
	return out
}

func (e *env) read(rel string) string {
	abs, _ := e.f.Abs("alice", rel)
	r, _, err := e.f.Store().Open(context.Background(), e.f.Actor("alice"), abs)
	Expect(err).NotTo(HaveOccurred(), rel)
	defer func() { _ = r.Close() }()
	b := new(strings.Builder)
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return b.String()
}

func (e *env) put(rel, content string) {
	abs, _ := e.f.Abs("alice", rel)
	_, err := e.f.Store().Put(context.Background(), e.f.Actor("alice"), abs, strings.NewReader(content), workspace.PutOptions{Overwrite: true})
	Expect(err).NotTo(HaveOccurred())
}

var _ = Describe("commands on this machine", func() {
	It("passes none of the server's environment to a command", func() {
		GinkgoT().Setenv("LOCALAI_TEST_SECRET_TOKEN", "s3cr3t")
		GinkgoT().Setenv("DATABASE_URL", "postgres://user:pw@db/x")
		GinkgoT().Setenv("LOCALAI_API_KEY", "sk-test")
		e := newEnv()
		out := e.bash("env", 0)
		Expect(out).NotTo(ContainSubstring("s3cr3t"))
		Expect(out).NotTo(ContainSubstring("postgres://"))
		// The shell reads no startup file, so no profile (macOS path_helper
		// included) changes the fixed environment; bash adds only PWD, SHLVL
		// and _. Nothing may come from the server's own environment.
		got := map[string]string{}
		for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
			k, v, _ := strings.Cut(line, "=")
			got[k] = v
		}
		Expect(got["PATH"]).To(Equal("/usr/local/bin:/usr/bin:/bin"))
		Expect(got).To(HaveKeyWithValue("LANG", "C"))
		Expect(got).To(HaveKeyWithValue("TZ", "UTC"))
		Expect(got["HOME"]).NotTo(Equal(os.Getenv("HOME")))
		for _, k := range []string{"LOCALAI_TEST_SECRET_TOKEN", "DATABASE_URL", "LOCALAI_API_KEY"} {
			Expect(got).NotTo(HaveKey(k))
		}
	})

	It("sees the user's files and saves what a command writes as a change of the chat", func() {
		e := newEnv()
		e.put("/script.sh", "echo from-the-script > made.txt\n")
		out := e.bash("bash script.sh && cat made.txt", 0) // it starts in the home
		Expect(out).To(Equal("from-the-script\n"))
		Expect(e.read("/made.txt")).To(Equal("from-the-script\n"))
		hist, err := e.f.Store().History(context.Background(), e.f.Actor("alice"), workspace.HistoryQuery{Path: session.HomePath("alice"), Limit: 10})
		Expect(err).NotTo(HaveOccurred())
		Expect(hist[0].ChatID).To(Equal("c1"))
		Expect(hist[0].Title).To(ContainSubstring("Command on this machine"))
		// A file written afterwards with the file tools is there at the next call.
		e.put("/later.txt", "later\n")
		Expect(e.bash("cat later.txt", 0)).To(Equal("later\n"))
	})

	It("runs a skill's script in place from the read-only skill layers, and never changes them", func() {
		e := newEnv()
		Expect(e.f.SyncBuiltin(context.Background(), fstest.MapFS{
			"pdf/SKILL.md":         {Data: []byte("---\nname: pdf\ndescription: PDFs.\n---\nUse the script.\n")},
			"pdf/scripts/check.sh": {Data: []byte("echo checked $1\n")},
		})).To(Succeed())
		out := e.bash("bash ../shared-builtin-skills/pdf/scripts/check.sh doc.pdf", 0)
		Expect(out).To(Equal("checked doc.pdf\n"))
		// A write into a layer is not saved: the layer is read-only.
		e.bash("echo changed > ../shared-builtin-skills/pdf/scripts/check.sh; true", 0)
		r, _, err := e.f.Store().Open(context.Background(), e.f.Actor("alice"), files.BuiltinSkillsPath+"/pdf/scripts/check.sh")
		Expect(err).NotTo(HaveOccurred())
		data, err := io.ReadAll(r)
		_ = r.Close()
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(Equal("echo checked $1\n"))
	})

	It("kills the whole process group at the timeout", func() {
		e := newEnv()
		start := time.Now()
		out := e.bash("sleep 60 & echo $! > child.pid; wait", 1)
		Expect(out).To(ContainSubstring("[stopped after 1 seconds]"))
		Expect(time.Since(start)).To(BeNumerically("<", 20*time.Second))
		pid, err := strconv.Atoi(strings.TrimSpace(e.read("/child.pid")))
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() error { return syscall.Kill(pid, 0) }, 5*time.Second).Should(HaveOccurred(), "the background child %d still runs", pid)
	})

	It("audits every call: who, which chat, the command, the exit code and the time", func() {
		e := newEnv()
		e.bash("echo hi; exit 3", 0)
		evs, err := e.audit.Events(context.Background(), 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(evs).To(HaveLen(1))
		Expect(evs[0].User).To(Equal("alice"))
		Expect(evs[0].Chat).To(Equal("c1"))
		Expect(evs[0].Command).To(Equal("echo hi; exit 3"))
		Expect(evs[0].Exit).To(Equal(3))
		Expect(evs[0].At).NotTo(BeZero())
	})

	It("mounts the user's shared folders next to the home", func() {
		e := newEnv(func(e *env, o *hostexec.Options) {
			o.View = homeView(e.f, []session.SharedRoot{{Path: "/shared/team", Mode: workspace.ModeRead}})
			admin := workspace.Actor{Principal: files.AdminPrincipal}
			_, err := e.f.Store().Put(context.Background(), admin, "/shared/team/plan.md", strings.NewReader("plan\n"), workspace.PutOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(e.f.Store().Grant(context.Background(), admin, "alice", "/shared/team", workspace.ModeRead)).To(Succeed())
		})
		Expect(e.bash("cat ../shared-team/plan.md", 0)).To(Equal("plan\n"))
	})

	It("refuses to run as root", func() {
		f := newFiles()
		r := hostexec.New(hostexec.Options{Store: f.Store(), StateDir: GinkgoT().TempDir(), Geteuid: func() int { return 0 }})
		Expect(r.Unavailable()).To(ContainSubstring("root"))
		a := files.UserActor("alice")
		a.ChatID = "c1"
		out, err := r.Run(context.Background(), a, json.RawMessage(`{"command":"id"}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(HavePrefix("This server cannot run commands"))
	})

	It("mounts only the view the chat's access gives", func() {
		e := newEnv(func(e *env, o *hostexec.Options) {
			e.put("/Proj/in.txt", "inside\n")
			e.put("/top.txt", "OUTSIDE-9c2e\n")
			o.View = func(ctx context.Context, a workspace.Actor) (workspace.View, error) {
				return e.f.Store().View(ctx, a, []workspace.MountRequest{{Path: session.HomePath(a.Principal) + "/Proj", At: "home", Mode: workspace.ModeWrite}})
			}
		})
		Expect(e.bash("ls; ls ..", 0)).To(Equal("in.txt\nhome\n"))
		out := e.bash("cat top.txt ../top.txt ../../top.txt 2>&1; grep -r OUTSIDE-9c2e .. 2>&1; echo x > ../escape.txt; true", 0)
		Expect(out).NotTo(ContainSubstring("OUTSIDE-9c2e"))
		_, err := e.f.Store().Stat(context.Background(), e.f.Actor("alice"), session.HomePath("alice")+"/escape.txt")
		Expect(err).To(MatchError(workspace.ErrNotFound))
	})

	It("refuses a scoped chat, also when its tool is called directly", func() {
		e := newEnv()
		e.put("/Proj/in.txt", "inside\n")
		n, err := e.f.Store().Stat(context.Background(), e.f.Actor("alice"), session.HomePath("alice")+"/Proj")
		Expect(err).NotTo(HaveOccurred())
		scoped := e.a.Fenced(workspace.FenceRoot{Node: n.ID, Mode: workspace.ModeWrite})
		out, err := e.r.Tool(scoped).Run(context.Background(), json.RawMessage(`{"command":"echo ran > ran.txt"}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(Equal(hostexec.ScopedText))
		evs, err := e.audit.Events(context.Background(), 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(evs).To(BeEmpty(), "nothing ran")
	})

	It("refuses a chat whose access became scoped after its tool was built", func() {
		e := newEnv(func(e *env, o *hostexec.Options) {
			e.put("/Proj/in.txt", "inside\n")
			o.Actor = func(ctx context.Context, user, chat string) (workspace.Actor, error) {
				a, _ := userActor(ctx, user, chat)
				n, err := e.f.Store().Stat(ctx, a, session.HomePath(user)+"/Proj")
				if err != nil {
					return workspace.Actor{}, err
				}
				return a.Fenced(workspace.FenceRoot{Node: n.ID, Mode: workspace.ModeWrite}), nil
			}
		})
		Expect(e.bash("echo ran > ran.txt", 0)).To(Equal(hostexec.ScopedText))
		_, err := e.f.Store().Stat(context.Background(), e.f.Actor("alice"), session.HomePath("alice")+"/ran.txt")
		Expect(err).To(MatchError(workspace.ErrNotFound))
	})

	It("fails the call when the resolver fails", func() {
		e := newEnv(func(_ *env, o *hostexec.Options) {
			o.Actor = func(context.Context, string, string) (workspace.Actor, error) {
				return workspace.Actor{}, errors.New("the folder of this agent is missing")
			}
		})
		out, err := e.r.Run(context.Background(), e.a, json.RawMessage(`{"command":"echo ran"}`))
		Expect(err).To(MatchError(ContainSubstring("folder of this agent is missing")))
		Expect(out).To(BeEmpty())
	})

	It("is unavailable without the chat's actor and view", func() {
		f := newFiles()
		r := hostexec.New(hostexec.Options{Store: f.Store(), StateDir: GinkgoT().TempDir()})
		Expect(r.Unavailable()).NotTo(BeEmpty())
	})

	It("tells the model it has no isolation and the network of the server", func() {
		e := newEnv()
		t := e.r.Tool(e.a)
		Expect(t.Name).To(Equal("bash"))
		Expect(t.Description).To(And(ContainSubstring("no isolation"), ContainSubstring("internet")))
		Expect(t.Description).To(And(ContainSubstring("It starts in the user's home"), ContainSubstring("use relative paths")))
		Expect(string(t.Schema)).To(ContainSubstring(fmt.Sprintf(`"maximum":%d`, int(hostexec.MaxTimeout.Seconds()))))
	})
})
