// SPDX-License-Identifier: MIT
package hostops

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type fakeGrants struct {
	mu    sync.Mutex
	read  []string
	write string
	err   error
	seen  []ClientCall
	addon string
}

func (g *fakeGrants) Grant(_ context.Context, c ClientCall, addon string) ([]string, string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.seen = append(g.seen, c)
	g.addon = addon
	return g.read, g.write, g.err
}

var _ = Describe("client folder operations", func() {
	var (
		f      *wsFixture
		grants *fakeGrants
		audit  []ClientAudit
		host   *ClientFolderHost
	)
	client := contract.Actor{UserID: "alice", CredentialID: "client:key-9", Mode: "named-user"}

	// live records a client call. mutating gives it the apply scope, as the
	// runtime does for a route that is not a GET.
	live := func(op, scope string) {
		e := env("sandbox", op, scope, time.Now().Add(time.Minute))
		e.Authority.Actor = client
		DeferCleanup(f.inflight.Begin(e))
	}
	call := func(op, args string) (int, contract.HostResponse) {
		id := fmt.Sprintf("op-%d", len(audit)+time.Now().Nanosecond())
		live(id, "apply")
		return f.call("secret", id, op, args)
	}
	// js quotes a string as JSON, so a NUL or a newline reaches the host.
	js := func(v string) string { b, _ := json.Marshal(v); return string(b) }
	lastReason := func() string { return audit[len(audit)-1].Reason }

	BeforeEach(func() {
		audit = nil
		f = newWSFixture()
		grants = &fakeGrants{read: []string{"Notes", "Reports/2026"}, write: "Memory/clients/box"}
		host = &ClientFolderHost{Files: f.files, Grants: grants, Audit: func(a ClientAudit) { audit = append(audit, a) }}
		Expect(RegisterClientFolders(f.b, host)).To(Succeed())
		f.h = f.b.Handler("sandbox", "secret", []string{"client."})
		f.put("alice", "/Notes/todo.md", "buy milk")
		f.put("alice", "/Notes/deep/more.txt", "deep")
		f.put("alice", "/Notes/pic.png", "binary")
		f.put("alice", "/Notes/data.json", `{"a":1}`)
		f.put("alice", "/Reports/2026/q1.md", "q1")
		f.put("alice", "/Reports/2025/q4.md", "old")
		f.put("alice", "/Private/secret.md", "SECRET")
		f.put("alice", "/Memory/index.md", "index")
		f.put("alice", "/Memory/clients/box/old.md", "old note")
		f.put("alice", "/Memory/clients/other/x.md", "other")
		f.put("bob", "/Notes/todo.md", "bob's")
	})

	Describe("the operations of a client call", func() {
		It("opens exactly the three folder names beside the job names", func() {
			Expect(ClientCallOps).To(Equal(map[string]bool{
				"client.jobs.start": true, "client.jobs.get": true, "client.jobs.cancel": true,
				"client.workspace.list": true, "client.workspace.read": true, "client.workspace.write": true,
			}))
			Expect(SupportedOperations()).To(ContainElements("client.workspace.list", "client.workspace.read", "client.workspace.write"))
			Expect(f.b.Register(Spec{Op: "client.workspace.delete", Scope: "apply", Feature: "chats", Handler: func(context.Context, Call) (json.RawMessage, error) { return json.RawMessage(`{}`), nil }})).To(Succeed())
			code, _ := call("client.workspace.delete", `{"path":"Memory/clients/box/old.md"}`)
			Expect(code).To(Equal(http.StatusForbidden), "a name that is not on the list stays closed to a client call")
		})
		It("refuses the folder operations to a call that no client made", func() {
			e := env("sandbox", "plain", "apply", time.Now().Add(time.Minute))
			e.Authority.Actor = contract.Actor{UserID: "alice", CredentialID: "c1", Mode: "named-user"}
			DeferCleanup(f.inflight.Begin(e))
			for _, op := range []string{"client.workspace.list", "client.workspace.read", "client.workspace.write"} {
				code, _ := f.call("secret", "plain", op, `{"path":"Notes/todo.md","content":"x"}`)
				Expect(code).To(Equal(http.StatusForbidden), op)
			}
			Expect(grants.seen).To(BeEmpty())
		})
		It("refuses the folder operations without the owner's grant of the prefix", func() {
			f.h = f.b.Handler("sandbox", "secret", []string{"workspace."})
			code, _ := call("client.workspace.list", `{"path":"Notes"}`)
			Expect(code).To(Equal(http.StatusForbidden))
		})
	})

	Describe("list", func() {
		It("names the files and sizes of a granted folder, and only the text formats", func() {
			code, out := call("client.workspace.list", `{"path":"Notes"}`)
			Expect(code).To(Equal(http.StatusOK), "%+v", out.Error)
			Expect(string(out.Result)).To(ContainSubstring(`"name":"todo.md","size":8`))
			Expect(string(out.Result)).To(ContainSubstring(`"name":"data.json"`))
			Expect(string(out.Result)).To(ContainSubstring(`"name":"deep","dir":true`))
			Expect(string(out.Result)).NotTo(ContainSubstring("pic.png"))
		})
		It("lists a folder below a granted folder", func() {
			code, out := call("client.workspace.list", `{"path":"Notes/deep"}`)
			Expect(code).To(Equal(http.StatusOK))
			Expect(string(out.Result)).To(ContainSubstring("more.txt"))
		})
		It("lists the granted folders for an empty path", func() {
			code, out := call("client.workspace.list", `{"path":""}`)
			Expect(code).To(Equal(http.StatusOK))
			Expect(string(out.Result)).To(Equal(`{"entries":[{"name":"Memory/clients/box","dir":true},{"name":"Notes","dir":true},{"name":"Reports/2026","dir":true}],"path":""}`))
		})
		It("refuses a folder that is not granted, a parent of a granted folder and a sibling", func() {
			for _, p := range []string{"Private", "Reports", "Reports/2025", "Memory", "Memory/clients", "Memory/clients/other", "Notes2"} {
				code, _ := call("client.workspace.list", fmt.Sprintf(`{"path":%s}`, js(p)))
				Expect(code).To(Equal(http.StatusForbidden), p)
			}
		})
	})

	Describe("read", func() {
		It("returns one file of a read folder and of the write folder", func() {
			code, out := call("client.workspace.read", `{"path":"Notes/todo.md"}`)
			Expect(code).To(Equal(http.StatusOK), "%+v", out.Error)
			Expect(string(out.Result)).To(ContainSubstring(`"content":"buy milk"`))
			code, out = call("client.workspace.read", `{"path":"Memory/clients/box/old.md"}`)
			Expect(code).To(Equal(http.StatusOK))
			Expect(string(out.Result)).To(ContainSubstring("old note"))
			code, _ = call("client.workspace.read", `{"path":"Notes/deep/more.txt"}`)
			Expect(code).To(Equal(http.StatusOK))
		})
		It("never returns a file outside the grant", func() {
			for _, p := range []string{"Private/secret.md", "Memory/index.md", "Reports/2025/q4.md", "Memory/clients/other/x.md"} {
				code, out := call("client.workspace.read", fmt.Sprintf(`{"path":%s}`, js(p)))
				Expect(code).To(Equal(http.StatusForbidden), p)
				Expect(string(out.Result)).NotTo(ContainSubstring("SECRET"))
			}
		})
		DescribeTable("refuses a path that tries to leave the folder",
			func(p string) {
				code, out := call("client.workspace.read", fmt.Sprintf(`{"path":%s}`, js(p)))
				Expect(code).To(BeNumerically(">=", 400), p)
				Expect(string(out.Result)).NotTo(ContainSubstring("SECRET"))
				Expect(lastReason()).NotTo(BeEmpty())
			},
			Entry("parent", "Notes/../Private/secret.md"),
			Entry("parent first", "../alice/Notes/todo.md"),
			Entry("dot", "Notes/./todo.md"),
			Entry("absolute", "/Notes/todo.md"),
			Entry("backslash", `Notes\todo.md`),
			Entry("backslash parent", `Notes\..\Private\secret.md`),
			Entry("NUL", "Notes/todo.md\x00.png"),
			Entry("empty segment", "Notes//todo.md"),
			Entry("trailing slash", "Notes/todo.md/"),
			Entry("control character", "Notes/to\ndo.md"),
			Entry("another user's folder", "users/bob/Notes/todo.md"),
			Entry("a file name that is only the folder", "Notes"),
			Entry("empty", ""),
		)
		It("refuses a wrong extension and a file over the read limit", func() {
			code, _ := call("client.workspace.read", `{"path":"Notes/pic.png"}`)
			Expect(code).To(Equal(http.StatusBadRequest))
			Expect(lastReason()).To(Equal("not a text format"))
			host.ReadBytes = 4
			code, _ = call("client.workspace.read", `{"path":"Notes/todo.md"}`)
			Expect(code).To(Equal(http.StatusBadRequest))
			host.ReadBytes = 0
			f.put("alice", "/Notes/big.txt", strings.Repeat("a", DefaultClientReadBytes+1))
			code, _ = call("client.workspace.read", `{"path":"Notes/big.txt"}`)
			Expect(code).To(Equal(http.StatusBadRequest))
			f.put("alice", "/Notes/ok.txt", strings.Repeat("a", DefaultClientReadBytes))
			code, _ = call("client.workspace.read", `{"path":"Notes/ok.txt"}`)
			Expect(code).To(Equal(http.StatusOK), "the default cap is 512 KiB, inclusive")
		})
		It("refuses a binary file with a text name and a missing file", func() {
			f.put("alice", "/Notes/bin.txt", "a\x00b")
			code, _ := call("client.workspace.read", `{"path":"Notes/bin.txt"}`)
			Expect(code).To(Equal(http.StatusBadRequest))
			code, _ = call("client.workspace.read", `{"path":"Notes/none.md"}`)
			Expect(code).To(Equal(http.StatusNotFound))
		})
	})

	Describe("write", func() {
		read := func(p string) string { return f.read("alice", p) }

		It("writes a file inside the write folder as the owner, and audits client, path, size and digest", func() {
			code, out := call("client.workspace.write", `{"path":"Memory/clients/box/new.md","content":"hello"}`)
			Expect(code).To(Equal(http.StatusOK), "%+v", out.Error)
			Expect(read("/Memory/clients/box/new.md")).To(Equal("hello"))
			Expect(string(out.Result)).To(ContainSubstring(`"sha256":"2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"`))
			Expect(audit).To(HaveLen(1))
			Expect(audit[0]).To(Equal(ClientAudit{Op: "client.workspace.write", Addon: "sandbox", Client: "key-9", Path: "Memory/clients/box/new.md", Bytes: 5, SHA256: "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"}))
			Expect(grants.seen).To(ContainElement(ClientCall{OwnerID: "alice", ClientID: "key-9"}))
			Expect(grants.addon).To(Equal("sandbox"))
		})
		It("overwrites a file, creates sub folders and creates the write folder when it is missing", func() {
			code, _ := call("client.workspace.write", `{"path":"Memory/clients/box/old.md","content":"v2"}`)
			Expect(code).To(Equal(http.StatusOK))
			Expect(read("/Memory/clients/box/old.md")).To(Equal("v2"))
			code, _ = call("client.workspace.write", `{"path":"Memory/clients/box/a/b/c.json","content":"{}"}`)
			Expect(code).To(Equal(http.StatusOK))
			grants.write = "Fresh/folder"
			code, _ = call("client.workspace.write", `{"path":"Fresh/folder/x.txt","content":"x"}`)
			Expect(code).To(Equal(http.StatusOK))
			Expect(read("/Fresh/folder/x.txt")).To(Equal("x"))
		})
		It("keeps only the newest versions of a file a client writes in a loop", func() {
			store := f.files.Store()
			owner := f.files.Actor("alice")
			versions := func(p string) int {
				abs, err := f.files.Abs("alice", p)
				Expect(err).ToNot(HaveOccurred())
				hist, err := store.History(context.Background(), owner, workspace.HistoryQuery{Path: abs, Limit: 1000})
				Expect(err).ToNot(HaveOccurred())
				n := 0
				for _, cs := range hist {
					for _, op := range cs.Ops {
						if op.Kind == workspace.OpWrite {
							n++
						}
					}
				}
				return n
			}
			for i := 0; i < 20; i++ {
				code, out := call("client.workspace.write", fmt.Sprintf(`{"path":"Memory/clients/box/loop.md","content":"v%d"}`, i))
				Expect(code).To(Equal(http.StatusOK), "%+v", out.Error)
			}
			Expect(read("/Memory/clients/box/loop.md")).To(Equal("v19"))
			Expect(versions("/Memory/clients/box/loop.md")).To(Equal(clientKeepVersions))
			// Another file of the folder keeps its own history.
			code, _ := call("client.workspace.write", `{"path":"Memory/clients/box/old.md","content":"v2"}`)
			Expect(code).To(Equal(http.StatusOK))
			Expect(versions("/Memory/clients/box/old.md")).To(Equal(1))
			Expect(versions("/Memory/clients/box/loop.md")).To(Equal(clientKeepVersions))
		})
		It("does not prune the history of the owner's own writes", func() {
			abs, err := f.files.Abs("alice", "/Memory/clients/box/mine.md")
			Expect(err).ToNot(HaveOccurred())
			for i := 0; i < 12; i++ {
				_, err := f.files.Store().Put(context.Background(), f.files.Actor("alice"), abs, strings.NewReader(fmt.Sprintf("m%d", i)), workspace.PutOptions{Overwrite: true})
				Expect(err).ToNot(HaveOccurred())
			}
			hist, err := f.files.Store().History(context.Background(), f.files.Actor("alice"), workspace.HistoryQuery{Path: abs, Limit: 1000})
			Expect(err).ToNot(HaveOccurred())
			Expect(len(hist)).To(BeNumerically(">", clientKeepVersions))
			code, _ := call("client.workspace.write", `{"path":"Memory/clients/box/other.md","content":"x"}`)
			Expect(code).To(Equal(http.StatusOK))
			hist, err = f.files.Store().History(context.Background(), f.files.Actor("alice"), workspace.HistoryQuery{Path: abs, Limit: 1000})
			Expect(err).ToNot(HaveOccurred())
			Expect(len(hist)).To(BeNumerically(">", clientKeepVersions), "a client write pruned a file it did not write")
		})
		It("refuses a write to a read-only folder, to the parent and to a sibling, and changes nothing", func() {
			for _, p := range []string{"Notes/new.md", "Notes/todo.md", "Reports/2026/new.md", "Memory/index.md", "Memory/clients/other/y.md", "Memory/clients/new.md", "Private/p.md"} {
				code, _ := call("client.workspace.write", fmt.Sprintf(`{"path":%s,"content":"x"}`, js(p)))
				Expect(code).To(Equal(http.StatusForbidden), p)
				Expect(lastReason()).To(Equal("outside the write folder"), p)
			}
			Expect(read("/Notes/todo.md")).To(Equal("buy milk"))
			Expect(read("/Memory/index.md")).To(Equal("index"))
			Expect(read("/Notes/new.md")).To(BeEmpty())
		})
		It("refuses a key that has no write folder", func() {
			grants.write = ""
			code, _ := call("client.workspace.write", `{"path":"Notes/new.md","content":"x"}`)
			Expect(code).To(Equal(http.StatusForbidden))
		})
		DescribeTable("refuses a path that tries to leave the write folder",
			func(p string) {
				code, _ := call("client.workspace.write", fmt.Sprintf(`{"path":%s,"content":"x"}`, js(p)))
				Expect(code).To(BeNumerically(">=", 400), p)
				Expect(read("/Private/secret.md")).To(Equal("SECRET"))
				Expect(read("/Memory/clients/x.md")).To(BeEmpty())
				Expect(read("/pwn.md")).To(BeEmpty())
			},
			Entry("parent", "Memory/clients/box/../x.md"),
			Entry("deep parent", "Memory/clients/box/../../../pwn.md"),
			Entry("dot", "Memory/clients/box/./x.md"),
			Entry("absolute", "/Memory/clients/box/x.md"),
			Entry("backslash", `Memory/clients/box/..\x.md`),
			Entry("NUL", "Memory/clients/box/x.md\x00"),
			Entry("NUL before the extension", "Memory/clients/box/x\x00.md"),
			Entry("hidden file", "Memory/clients/box/.x.md"),
			Entry("space in the name", "Memory/clients/box/a b.md"),
			Entry("colon", "Memory/clients/box/a:b.md"),
			Entry("too deep", "Memory/clients/box/a/b/c/d/e/f/g/h/i/j/k/l/m/n/x.md"),
		)
		It("takes only .md, .txt and .json, and valid text", func() {
			for _, p := range []string{"x.png", "x.exe", "x.md.sh", "x", "x.MD.png", "x."} {
				code, _ := call("client.workspace.write", fmt.Sprintf(`{"path":"Memory/clients/box/%s","content":"x"}`, p))
				Expect(code).To(Equal(http.StatusBadRequest), p)
			}
			for _, p := range []string{"a.md", "a.txt", "a.json", "UP.MD"} {
				code, out := call("client.workspace.write", fmt.Sprintf(`{"path":"Memory/clients/box/%s","content":"{}"}`, p))
				Expect(code).To(Equal(http.StatusOK), "%s %+v", p, out.Error)
			}
			code, _ := call("client.workspace.write", `{"path":"Memory/clients/box/bad.json","content":"{not json"}`)
			Expect(code).To(Equal(http.StatusBadRequest))
			code, _ = call("client.workspace.write", `{"path":"Memory/clients/box/nul.txt","content":"a\u0000b"}`)
			Expect(code).To(Equal(http.StatusBadRequest))
		})
		It("limits a file to 256 KiB, inclusive", func() {
			code, _ := call("client.workspace.write", fmt.Sprintf(`{"path":"Memory/clients/box/big.txt","content":%q}`, strings.Repeat("a", DefaultClientWriteFileBytes+1)))
			Expect(code).To(Equal(http.StatusBadRequest))
			Expect(lastReason()).To(Equal("over the file limit"))
			code, _ = call("client.workspace.write", fmt.Sprintf(`{"path":"Memory/clients/box/edge.txt","content":%q}`, strings.Repeat("a", DefaultClientWriteFileBytes)))
			Expect(code).To(Equal(http.StatusOK))
		})
		It("refuses a write that would fill the folder, and allows a replacement within it", func() {
			host.WriteFolderBytes = 100 // old.md holds 8 bytes
			code, _ := call("client.workspace.write", fmt.Sprintf(`{"path":"Memory/clients/box/a.txt","content":%q}`, strings.Repeat("a", 93)))
			Expect(code).To(Equal(http.StatusBadRequest))
			Expect(lastReason()).To(Equal("over the folder limit"))
			code, _ = call("client.workspace.write", fmt.Sprintf(`{"path":"Memory/clients/box/a.txt","content":%q}`, strings.Repeat("a", 92)))
			Expect(code).To(Equal(http.StatusOK))
			code, _ = call("client.workspace.write", fmt.Sprintf(`{"path":"Memory/clients/box/a.txt","content":%q}`, strings.Repeat("b", 92)))
			Expect(code).To(Equal(http.StatusOK), "the old a.txt is not counted twice")
			code, _ = call("client.workspace.write", `{"path":"Memory/clients/box/b.txt","content":"x"}`)
			Expect(code).To(Equal(http.StatusBadRequest))
		})
		It("has a default folder limit of 50 MiB", func() {
			Expect((&ClientFolderHost{}).writeFolderBytes()).To(Equal(int64(50 << 20)))
			Expect((&ClientFolderHost{}).writeFileBytes()).To(Equal(int64(256 << 10)))
			Expect((&ClientFolderHost{}).readBytes()).To(Equal(int64(512 << 10)))
		})
		It("refuses a path that is a folder", func() {
			code, _ := call("client.workspace.write", `{"path":"Memory/clients/box/dir.md/x.md","content":"x"}`)
			Expect(code).To(Equal(http.StatusOK))
			code, _ = call("client.workspace.write", `{"path":"Memory/clients/box/dir.md","content":"x"}`)
			Expect(code).To(Equal(http.StatusBadRequest))
		})
		It("needs the apply scope, which a read-only call does not have", func() {
			id := "read-only"
			live(id, "read")
			code, out := f.call("secret", id, "client.workspace.write", `{"path":"Memory/clients/box/x.md","content":"x"}`)
			Expect(code).To(Equal(http.StatusForbidden))
			Expect(out.Error.Code).To(Equal(contract.HostScope))
			Expect(read("/Memory/clients/box/x.md")).To(BeEmpty())
			// The read operations still run under it.
			live("read-only-2", "read")
			code, _ = f.call("secret", "read-only-2", "client.workspace.read", `{"path":"Notes/todo.md"}`)
			Expect(code).To(Equal(http.StatusOK))
		})
		It("never takes an owner, a client or an add-on from the arguments", func() {
			for _, body := range []string{`{"path":"Memory/clients/box/x.md","content":"x","owner":"bob"}`, `{"path":"Memory/clients/box/x.md","content":"x","client":"other"}`, `{"path":"Memory/clients/box/x.md","content":"x","addon":"o"}`} {
				code, _ := call("client.workspace.write", body)
				Expect(code).To(Equal(http.StatusBadRequest), body)
			}
			Expect(f.read("bob", "/Memory/clients/box/x.md")).To(BeEmpty())
		})
	})

	Describe("names that look like a granted folder", func() {
		BeforeEach(func() {
			grants.read = []string{"Notes", "Küche"}
			f.put("alice", "/Küche/a.md", "nfc folder")
		})
		It("refuses a case variant and a decomposed (NFD) variant of a granted folder", func() {
			Expect(func() int { code, _ := call("client.workspace.read", `{"path":"Küche/a.md"}`); return code }()).To(Equal(http.StatusOK), "the granted spelling works")
			for _, p := range []string{"notes/todo.md", "NOTES/todo.md", "Notes2/todo.md", "kÜche/a.md", "Ku\u0308che/a.md"} {
				for _, op := range []string{"client.workspace.read", "client.workspace.list"} {
					code, _ := call(op, fmt.Sprintf(`{"path":%s}`, js(p)))
					Expect(code).To(Equal(http.StatusForbidden), op+" "+p)
				}
			}
			for _, p := range []string{"memory/clients/box/x.md", "Memory/Clients/box/x.md", "MEMORY/clients/box/x.md", "Memory/clients/bo\u0301x/x.md"} {
				code, _ := call("client.workspace.write", fmt.Sprintf(`{"path":%s,"content":"x"}`, js(p)))
				Expect(code).To(Equal(http.StatusForbidden), p)
			}
			Expect(f.read("alice", "/memory/clients/box/x.md")).To(BeEmpty())
		})
	})

	Describe("the answer of read", func() {
		It("stays under the payload limit when the encoder escapes the content", func() {
			// "<" is one byte in the file and six in the answer.
			f.put("alice", "/Notes/lt.txt", strings.Repeat("<", 100000))
			code, out := call("client.workspace.read", `{"path":"Notes/lt.txt"}`)
			Expect(code).To(Equal(http.StatusOK), "%+v", out.Error)
			Expect(int64(len(out.Result))).To(BeNumerically("<", contract.DefaultLimits().PayloadBytes))
			f.put("alice", "/Notes/lt2.txt", strings.Repeat("<", 200000))
			code, _ = call("client.workspace.read", `{"path":"Notes/lt2.txt"}`)
			Expect(code).To(Equal(http.StatusBadRequest), "200 KB of escapes is over the payload, though under the file cap")
			Expect(lastReason()).To(Equal("the encoded answer is over the payload limit"))
		})
		It("keeps a file at the default cap whose answer needs no escape", func() {
			Expect(DefaultClientReadBytes * 2).To(BeNumerically("<=", contract.DefaultLimits().PayloadBytes))
		})
	})

	Describe("list pages", func() {
		It("counts only the entries it shows, and pages with a cursor", func() {
			grants.read = []string{"Many"}
			for i := 0; i < maxClientListEntries; i++ {
				f.put("alice", fmt.Sprintf("/Many/f%03d.md", i), "x")
			}
			for i := 0; i < 5; i++ {
				f.put("alice", fmt.Sprintf("/Many/skip%d.png", i), "x")
			}
			code, out := call("client.workspace.list", `{"path":"Many"}`)
			Expect(code).To(Equal(http.StatusOK))
			var page struct {
				Entries   []struct{ Name string }
				Truncated bool
				Next      string
			}
			Expect(json.Unmarshal(out.Result, &page)).To(Succeed())
			Expect(page.Entries).To(HaveLen(maxClientListEntries))
			Expect(page.Truncated).To(BeFalse(), "the five png files are not shown and do not count")
			f.put("alice", "/Many/g.md", "x")
			code, out = call("client.workspace.list", `{"path":"Many"}`)
			Expect(code).To(Equal(http.StatusOK))
			page.Entries, page.Next = nil, ""
			Expect(json.Unmarshal(out.Result, &page)).To(Succeed())
			Expect(page.Truncated).To(BeTrue())
			Expect(page.Next).To(Equal("f199.md"))
			code, out = call("client.workspace.list", fmt.Sprintf(`{"path":"Many","after":%s}`, js(page.Next)))
			Expect(code).To(Equal(http.StatusOK))
			page.Entries, page.Next, page.Truncated = nil, "", true
			Expect(json.Unmarshal(out.Result, &page)).To(Succeed())
			Expect(page.Entries).To(HaveLen(1))
			Expect(page.Entries[0].Name).To(Equal("g.md"))
			Expect(page.Truncated).To(BeFalse())
		})
	})

	Describe("the size of the write folder", func() {
		It("refuses a new file past the node cap, and still allows an overwrite", func() {
			host.MaxFolderNodes = 3 // old.md is one
			for _, n := range []string{"a", "b"} {
				code, _ := call("client.workspace.write", fmt.Sprintf(`{"path":"Memory/clients/box/%s.md","content":"x"}`, n))
				Expect(code).To(Equal(http.StatusOK), n)
			}
			code, _ := call("client.workspace.write", `{"path":"Memory/clients/box/c.md","content":"x"}`)
			Expect(code).To(Equal(http.StatusBadRequest))
			Expect(lastReason()).To(Equal("the write folder has too many files"))
			code, _ = call("client.workspace.write", `{"path":"Memory/clients/box/a.md","content":"yy"}`)
			Expect(code).To(Equal(http.StatusOK))
		})
		It("does not call a folder with more nodes than the cap 'full'", func() {
			host.MaxFolderNodes = 1
			f.put("alice", "/Memory/clients/box/second.md", "x")
			code, _ := call("client.workspace.write", `{"path":"Memory/clients/box/a.md","content":"x"}`)
			Expect(code).To(Equal(http.StatusBadRequest))
			Expect(lastReason()).To(Equal("the write folder has too many files"))
			Expect(lastReason()).NotTo(Equal("over the folder limit"))
		})
		It("has a default cap of 4096 nodes", func() {
			Expect((&ClientFolderHost{}).maxNodes()).To(Equal(4096))
		})
		It("is an outage, not a full folder, when the store cannot list", func() {
			actor, _, err := host.fence(context.Background(), "alice", []string{"Notes"}, workspace.ModeRead)
			Expect(err).NotTo(HaveOccurred())
			abs, _ := f.files.Abs("alice", "/Private")
			_, err = host.walk(context.Background(), actor, abs)
			Expect(err).To(HaveOccurred())
			Expect(err).NotTo(MatchError(errFolderTooBig))
		})
		It("lets a write that shrinks a file pass when the folder is at its limit", func() {
			host.WriteFolderBytes = 100
			code, _ := call("client.workspace.write", fmt.Sprintf(`{"path":"Memory/clients/box/a.txt","content":%q}`, strings.Repeat("a", 92)))
			Expect(code).To(Equal(http.StatusOK))
			code, _ = call("client.workspace.write", `{"path":"Memory/clients/box/new.txt","content":"x"}`)
			Expect(code).To(Equal(http.StatusBadRequest))
			code, _ = call("client.workspace.write", `{"path":"Memory/clients/box/a.txt","content":"small"}`)
			Expect(code).To(Equal(http.StatusOK))
			code, _ = call("client.workspace.write", `{"path":"Memory/clients/box/new.txt","content":"x"}`)
			Expect(code).To(Equal(http.StatusOK))
		})
		It("names the client in the history title", func() {
			code, out := call("client.workspace.write", `{"path":"Memory/clients/box/t.md","content":"x"}`)
			Expect(code).To(Equal(http.StatusOK))
			var res struct{ Revision int64 }
			Expect(json.Unmarshal(out.Result, &res)).To(Succeed())
			abs, _ := f.files.Abs("alice", "/Memory/clients/box")
			hist, err := f.files.Store().History(context.Background(), f.files.Actor("alice"), workspace.HistoryQuery{Path: abs, Limit: 5})
			Expect(err).NotTo(HaveOccurred())
			Expect(hist).NotTo(BeEmpty())
			Expect(hist[0].Title).To(Equal("Paired client key-9 wrote t.md"))
		})
	})

	Describe("a write that waits for the folder", func() {
		key := folderKey("alice", []string{"Memory", "clients", "box"})
		It("does not complete for a key that was revoked while it waited", func() {
			unlock, err := host.lock(context.Background(), key)
			Expect(err).NotTo(HaveOccurred())
			live("queued", "apply")
			done := make(chan int, 1)
			go func() {
				defer GinkgoRecover()
				code, _ := f.call("secret", "queued", "client.workspace.write", `{"path":"Memory/clients/box/late.md","content":"late"}`)
				done <- code
			}()
			Eventually(func() int { grants.mu.Lock(); defer grants.mu.Unlock(); return len(grants.seen) }).Should(Equal(1))
			Consistently(done, "100ms").ShouldNot(Receive())
			grants.mu.Lock()
			grants.err = fmt.Errorf("%w: revoked", ErrForbidden)
			grants.mu.Unlock()
			unlock()
			Eventually(done).Should(Receive(Equal(http.StatusForbidden)))
			Expect(f.read("alice", "/Memory/clients/box/late.md")).To(BeEmpty())
			Expect(lastReason()).To(ContainSubstring("revoked"))
		})
		It("does not complete when the write folder was changed while it waited", func() {
			unlock, err := host.lock(context.Background(), key)
			Expect(err).NotTo(HaveOccurred())
			live("queued", "apply")
			done := make(chan int, 1)
			go func() {
				defer GinkgoRecover()
				code, _ := f.call("secret", "queued", "client.workspace.write", `{"path":"Memory/clients/box/late.md","content":"late"}`)
				done <- code
			}()
			Eventually(func() int { grants.mu.Lock(); defer grants.mu.Unlock(); return len(grants.seen) }).Should(Equal(1))
			grants.mu.Lock()
			grants.write = "Elsewhere"
			grants.mu.Unlock()
			unlock()
			Eventually(done).Should(Receive(Equal(http.StatusForbidden)))
			Expect(f.read("alice", "/Memory/clients/box/late.md")).To(BeEmpty())
			Expect(f.read("alice", "/Elsewhere/late.md")).To(BeEmpty())
		})
		It("gives up when its context ends", func() {
			unlock, err := host.lock(context.Background(), key)
			Expect(err).NotTo(HaveOccurred())
			defer unlock()
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			_, err = host.lock(ctx, key)
			Expect(err).To(MatchError(context.DeadlineExceeded))
			other, err := host.lock(context.Background(), folderKey("alice", []string{"Other"}))
			Expect(err).NotTo(HaveOccurred(), "another folder has its own lock")
			other()
		})
	})

	Describe("failing closed", func() {
		It("gives one generic refusal when the grant source refuses, whatever the reason, and audits each reason", func() {
			for _, why := range []string{"paused", "expired", "revoked", "unknown add-on", "no folder grant"} {
				audit = nil
				grants.err = fmt.Errorf("%w: %s", ErrForbidden, why)
				for _, op := range []string{"client.workspace.list", "client.workspace.read", "client.workspace.write"} {
					code, out := call(op, `{"path":"Notes/todo.md","content":"x"}`)
					Expect(code).To(Equal(http.StatusForbidden), why+" "+op)
					Expect(out.Error.Message).To(Equal("runtime endpoint closed"))
					Expect(out.Error.Message).NotTo(ContainSubstring(why))
				}
				Expect(audit).To(HaveLen(3))
				Expect(audit[0].Reason).To(ContainSubstring(why))
			}
			Expect(f.read("alice", "/Notes/new.md")).To(BeEmpty())
		})
		It("refuses an empty grant, and a grant that names a bad folder", func() {
			grants.read, grants.write = nil, ""
			code, _ := call("client.workspace.read", `{"path":"Notes/todo.md"}`)
			Expect(code).To(Equal(http.StatusForbidden))
			grants.read = []string{"../Private"}
			code, _ = call("client.workspace.read", `{"path":"Private/secret.md"}`)
			Expect(code).To(Equal(http.StatusForbidden))
		})
		It("is an outage, not a refusal, when the grant store fails", func() {
			grants.err = fmt.Errorf("database down")
			code, _ := call("client.workspace.read", `{"path":"Notes/todo.md"}`)
			Expect(code).To(Equal(http.StatusServiceUnavailable))
		})
		It("refuses without the owner's files feature", func() {
			f.b.has = func(string, string) bool { return false }
			code, out := call("client.workspace.read", `{"path":"Notes/todo.md"}`)
			Expect(code).To(Equal(http.StatusForbidden))
			Expect(out.Error.Code).To(Equal(contract.HostFeature))
		})
		It("reads another owner's files never: the owner is the one of the call", func() {
			code, out := call("client.workspace.read", `{"path":"Notes/todo.md"}`)
			Expect(code).To(Equal(http.StatusOK))
			Expect(string(out.Result)).To(ContainSubstring("buy milk"))
			Expect(string(out.Result)).NotTo(ContainSubstring("bob"))
		})
		It("registers nothing without a files service or a grant source", func() {
			b := New(NewInFlight(), nil, contract.DefaultLimits())
			Expect(RegisterClientFolders(b, &ClientFolderHost{})).To(Succeed())
			Expect(RegisterClientFolders(b, nil)).To(Succeed())
			Expect(b.Ops()).To(BeEmpty())
		})
	})
})
