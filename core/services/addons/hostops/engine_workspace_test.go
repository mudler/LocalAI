// SPDX-License-Identifier: MIT
package hostops

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/gormstore"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type wsFixture struct {
	*fixture
	files *files.Service
	data  string
}

func newWSFixture() *wsFixture {
	tmp := GinkgoT().TempDir()
	db, err := gorm.Open(sqlite.Open("file:"+filepath.Join(tmp, "ws.db")+"?_busy_timeout=5000&_txlock=immediate"), &gorm.Config{})
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() {
		if raw, err := db.DB(); err == nil {
			_ = raw.Close()
		}
	})
	// The blobs live under the same file system as the data directory, so a
	// hard link between them works.
	blobs, err := blob.NewFS(filepath.Join(tmp, "blobs"))
	Expect(err).ToNot(HaveOccurred())
	ws, err := gormstore.New(db, blobs, gormstore.Options{Admin: files.AdminPrincipal})
	Expect(err).ToNot(HaveOccurred())
	fs := files.New(ws)
	data := filepath.Join(tmp, "data")
	Expect(os.MkdirAll(filepath.Join(data, "sandbox"), 0o700)).To(Succeed())
	f := &wsFixture{fixture: &fixture{inflight: NewInFlight(), seen: &Call{}}, files: fs, data: data}
	f.b = New(f.inflight, always, contract.DefaultLimits())
	Expect(RegisterWorkspace(f.b, &WorkspaceHost{
		Store: ws, Files: fs,
		Principal: func(a contract.Actor) string { return a.UserID },
		DataDir:   func(id string) string { return filepath.Join(data, id) },
	})).To(Succeed())
	f.h = f.b.Handler("sandbox", "secret", []string{"workspace."})
	return f
}

// put writes a file into the home of a user.
func (f *wsFixture) put(user, rel, content string) {
	ctx := context.Background()
	Expect(f.files.EnsureHome(ctx, user)).To(Succeed())
	abs, err := f.files.Abs(user, rel)
	Expect(err).ToNot(HaveOccurred())
	_, err = f.files.Store().Put(ctx, f.files.Actor(user), abs, strings.NewReader(content), workspace.PutOptions{})
	Expect(err).ToNot(HaveOccurred())
}

func (f *wsFixture) live(user, op, scope string) {
	e := env("sandbox", op, scope, time.Now().Add(time.Minute))
	e.Authority.Actor.UserID = user
	DeferCleanup(f.inflight.Begin(e))
}

func upperTar(name, content string) []byte {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	Expect(tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(content))})).To(Succeed())
	_, _ = tw.Write([]byte(content))
	Expect(tw.Close()).To(Succeed())
	return buf.Bytes()
}

var _ = Describe("Workspace host operations", func() {
	mounts := `"mounts":[{"path":"/proj","at":"proj","write":true}]`

	It("materializes the folders of the user of the live request", func() {
		f := newWSFixture()
		f.put("alice", "/proj/a.txt", "hello")
		f.live("alice", "op1", "act")
		code, out := f.call("secret", "op1", "workspace.materialize", `{`+mounts+`,"dir":"run1"}`)
		Expect(code).To(Equal(http.StatusOK), "%+v", out.Error)
		got, err := os.ReadFile(filepath.Join(f.data, "sandbox", "run1", "proj", "a.txt"))
		Expect(err).ToNot(HaveOccurred())
		Expect(string(got)).To(Equal("hello"))
	})

	It("never reaches the files of another user", func() {
		f := newWSFixture()
		f.put("bob", "/proj/secret.txt", "bob's")
		f.live("alice", "op1", "act")
		code, _ := f.call("secret", "op1", "workspace.materialize", `{`+mounts+`,"dir":"run1"}`)
		Expect(code).To(BeNumerically(">=", 400))
		_, err := os.Stat(filepath.Join(f.data, "sandbox", "run1", "proj", "secret.txt"))
		Expect(err).To(HaveOccurred())
		code, _ = f.call("secret", "op1", "workspace.materialize", `{"mounts":[{"path":"../bob/proj","at":"x"}],"dir":"run2"}`)
		Expect(code).To(Equal(http.StatusBadRequest))
	})

	It("refuses a directory outside the data directory of the add-on", func() {
		f := newWSFixture()
		f.put("alice", "/proj/a.txt", "x")
		Expect(os.Symlink(f.data, filepath.Join(f.data, "sandbox", "link"))).To(Succeed())
		f.live("alice", "op1", "act")
		for _, dir := range []string{"../other", "/etc", "a/../../b", "link", "link/x", "", "a\\b", "./x"} {
			body, _ := json.Marshal(map[string]any{"mounts": []map[string]any{{"path": "/proj", "at": "proj"}}, "dir": dir})
			code, _ := f.call("secret", "op1", "workspace.materialize", string(body))
			Expect(code).To(Equal(http.StatusBadRequest), "dir %q", dir)
		}
	})

	It("commits the upper layer as the user of the request", func() {
		f := newWSFixture()
		f.put("alice", "/proj/a.txt", "hello")
		f.live("alice", "op1", "act")
		code, _ := f.call("secret", "op1", "workspace.materialize", `{`+mounts+`,"dir":"run1"}`)
		Expect(code).To(Equal(http.StatusOK))
		Expect(os.WriteFile(filepath.Join(f.data, "sandbox", "run1", "upper.tar"), upperTar("proj/new.txt", "made by the sandbox"), 0o600)).To(Succeed())
		code, out := f.call("secret", "op1", "workspace.commit", `{`+mounts+`,"dir":"run1","base":0,"title":"sandbox run"}`)
		Expect(code).To(Equal(http.StatusOK), "%+v", out.Error)
		abs, _ := f.files.Abs("alice", "/proj/new.txt")
		rc, _, err := f.files.Store().Open(context.Background(), f.files.Actor("alice"), abs)
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = rc.Close() }()
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(rc)
		Expect(buf.String()).To(Equal("made by the sandbox"))
	})

	It("lets a read envelope materialize but not commit", func() {
		f := newWSFixture()
		f.put("alice", "/proj/a.txt", "x")
		f.live("alice", "op1", "read")
		code, _ := f.call("secret", "op1", "workspace.materialize", `{`+mounts+`,"dir":"run1"}`)
		Expect(code).To(Equal(http.StatusOK))
		code, out := f.call("secret", "op1", "workspace.commit", `{`+mounts+`,"dir":"run1","base":0}`)
		Expect(code).To(Equal(http.StatusForbidden))
		Expect(out.Error.Code).To(Equal("scope"))
	})

	It("takes the user from the request, never from the arguments", func() {
		f := newWSFixture()
		f.live("alice", "op1", "act")
		code, out := f.call("secret", "op1", "workspace.materialize", `{`+mounts+`,"dir":"run1","userId":"bob"}`)
		Expect(code).To(Equal(http.StatusBadRequest))
		Expect(out.Error.Code).To(Equal("bad_request"))
	})

	It("tells an administrator apart from anybody else", func() {
		f := newWSFixture()
		f.b.SetAdminCheck(func(a contract.Actor) bool { return a.UserID == "root" })
		f.live("root", "op1", "read")
		code, out := f.call("secret", "op1", "workspace.admin", `{}`)
		Expect(code).To(Equal(http.StatusOK), "%+v", out.Error)
		f.live("alice", "op2", "read")
		code, out = f.call("secret", "op2", "workspace.admin", `{}`)
		Expect(code).To(Equal(http.StatusForbidden))
		Expect(out.Error.Code).To(Equal("feature"))
	})

	It("lists the changes after a revision", func() {
		f := newWSFixture()
		f.put("alice", "/proj/a.txt", "x")
		f.live("alice", "op1", "act")
		code, out := f.call("secret", "op1", "workspace.changes", `{"since":0}`)
		Expect(code).To(Equal(http.StatusOK))
		Expect(string(out.Result)).To(ContainSubstring(`"changes"`))
	})

	It("exports a file of the user and never one of another user", func() {
		f := newWSFixture()
		f.put("alice", "/doc.txt", "mine")
		f.put("bob", "/doc.txt", "bob's")
		f.live("alice", "op1", "read")
		code, out := f.call("secret", "op1", "workspace.export", `{"path":"/doc.txt","to":"out/doc.txt"}`)
		Expect(code).To(Equal(http.StatusOK), "%+v", out.Error)
		got, err := os.ReadFile(filepath.Join(f.data, "sandbox", "out", "doc.txt"))
		Expect(err).ToNot(HaveOccurred())
		Expect(string(got)).To(Equal("mine"))
		code, _ = f.call("secret", "op1", "workspace.export", `{"path":"../bob/doc.txt","to":"out/x.txt"}`)
		Expect(code).To(Equal(http.StatusBadRequest))
		for _, to := range []string{"../x.txt", "/tmp/x.txt", "out/../../x.txt", "out/", ""} {
			body, _ := json.Marshal(map[string]string{"path": "/doc.txt", "to": to})
			code, _ = f.call("secret", "op1", "workspace.export", string(body))
			Expect(code).To(Equal(http.StatusBadRequest), "to %q", to)
		}
	})

	It("imports a file as the user of the request and refuses a link", func() {
		f := newWSFixture()
		f.live("alice", "op1", "act")
		Expect(os.WriteFile(filepath.Join(f.data, "sandbox", "made.txt"), []byte("from the add-on"), 0o600)).To(Succeed())
		code, out := f.call("secret", "op1", "workspace.import", `{"from":"made.txt","path":"/made.txt"}`)
		Expect(code).To(Equal(http.StatusOK), "%+v", out.Error)
		abs, _ := f.files.Abs("alice", "/made.txt")
		_, node, err := f.files.Store().Open(context.Background(), f.files.Actor("alice"), abs)
		Expect(err).ToNot(HaveOccurred())
		Expect(node.Size).To(BeNumerically("==", len("from the add-on")))
		secret := filepath.Join(GinkgoT().TempDir(), "secret.txt")
		Expect(os.WriteFile(secret, []byte("host secret"), 0o600)).To(Succeed())
		Expect(os.Symlink(secret, filepath.Join(f.data, "sandbox", "evil.txt"))).To(Succeed())
		code, _ = f.call("secret", "op1", "workspace.import", `{"from":"evil.txt","path":"/evil.txt"}`)
		Expect(code).To(BeNumerically(">=", 400))
		code, _ = f.call("secret", "op1", "workspace.import", `{"from":"../outside.txt","path":"/o.txt"}`)
		Expect(code).To(Equal(http.StatusBadRequest))
	})

	It("keeps both files when the file changed after the add-on read it", func() {
		f := newWSFixture()
		f.put("alice", "/doc.txt", "first")
		f.live("alice", "op1", "act")
		Expect(os.WriteFile(filepath.Join(f.data, "sandbox", "mine.txt"), []byte("mine"), 0o600)).To(Succeed())
		// The add-on read version 1 of the file; the user then saved another.
		abs, _ := f.files.Abs("alice", "/doc.txt")
		_, err := f.files.Store().Put(context.Background(), f.files.Actor("alice"), abs, strings.NewReader("second"), workspace.PutOptions{Overwrite: true})
		Expect(err).ToNot(HaveOccurred())
		code, out := f.call("secret", "op1", "workspace.import", `{"from":"mine.txt","path":"/doc.txt","baseVersion":1}`)
		Expect(code).To(Equal(http.StatusOK), "%+v", out.Error)
		var got struct {
			Path       string `json:"path"`
			ConflictOf string `json:"conflictOf"`
		}
		Expect(json.Unmarshal(out.Result, &got)).To(Succeed())
		Expect(got.ConflictOf).To(Equal("/doc.txt"))
		Expect(got.Path).ToNot(Equal("/doc.txt"))
	})

	It("commits once for a repeated operation", func() {
		f := newWSFixture()
		f.put("alice", "/proj/a.txt", "hello")
		f.live("alice", "op1", "act")
		code, _ := f.call("secret", "op1", "workspace.materialize", `{`+mounts+`,"dir":"run1"}`)
		Expect(code).To(Equal(http.StatusOK))
		Expect(os.WriteFile(filepath.Join(f.data, "sandbox", "run1", "upper.tar"), upperTar("proj/new.txt", "x"), 0o600)).To(Succeed())
		body := `{` + mounts + `,"dir":"run1","base":0,"title":"run"}`
		_, first := f.call("secret", "op1", "workspace.commit", body)
		_, second := f.call("secret", "op1", "workspace.commit", body)
		Expect(string(second.Result)).To(Equal(string(first.Result)))
	})

	It("gives an add-on outside the allowlist no workspace operation", func() {
		f := newWSFixture()
		h := f.b.Handler("sandbox", "secret", []string{"finetune."})
		f.h = h
		f.live("alice", "op1", "act")
		code, out := f.call("secret", "op1", "workspace.changes", `{}`)
		Expect(code).To(Equal(http.StatusForbidden))
		Expect(out.Error.Code).To(Equal("unknown_op"))
	})
})
