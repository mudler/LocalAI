// SPDX-License-Identifier: MIT
package tools_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/mudler/LocalAI/addons/gdrive/gdrivetest"
	"github.com/mudler/LocalAI/addons/gdrive/internal/tools"
	"github.com/mudler/LocalAI/addons/internal/protocol"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the write tools", func() {
	var (
		w   *world
		ctx = context.Background()
	)
	BeforeEach(func() {
		w = newWorld()
		// mine was created by the add-on; notes was written by the person.
		w.srv.AddFile(gdrivetest.File{ID: "mine", Name: "mine.txt", Parents: []string{"proj"}, Content: []byte("old"), CreatedByApp: true})
	})

	wenv := func(op, tool string, args map[string]any, scopes ...string) contract.Envelope {
		e := env(tool, args, scopes...)
		e.Request.OperationID = op
		e.Request.Action = "write"
		return e
	}
	// propose previews and applies, as the host does after a person approved.
	apply := func(op, tool string, args map[string]any) (contract.Receipt, error) {
		e := wenv(op, tool, args, bothScopes...)
		p, err := w.rt.Preview(ctx, e)
		if err != nil {
			return contract.Receipt{}, err
		}
		return w.rt.Apply(ctx, e, p.Revision)
	}
	writeCalls := func() int {
		n := 0
		for _, c := range w.srv.Calls() {
			if c.Method != "GET" {
				n++
			}
		}
		return n
	}

	allTools := map[string]map[string]any{
		"create-file": {"folder": "proj", "name": "new.txt", "content": "text"},
		"update-file": {"folder": "proj", "fileId": "mine", "content": "new"},
		"move-file":   {"folder": "proj", "fileId": "mine", "newParent": "sub"},
		"trash-file":  {"folder": "proj", "fileId": "mine"},
	}

	It("degrades without the optional scope: a clear refusal that names the scope, and nothing is sent", func() {
		for tool, args := range allTools {
			e := wenv("op-"+tool, tool, args, tools.ScopeRead)
			_, err := w.rt.Preview(ctx, e)
			Expect(err).To(MatchError(protocol.ErrBadRequest), tool)
			Expect(err.Error()).To(ContainSubstring(tools.ScopeWriteName), tool)
			Expect(err.Error()).To(ContainSubstring("Reconnect"), tool)
			_, err = w.rt.Apply(ctx, e, contract.Revision{Schema: 1, Data: 1})
			Expect(err).To(MatchError(ContainSubstring(tools.ScopeWriteName)), tool)
		}
		_, err := w.rt.Preview(ctx, wenv("op-x", "create-file", allTools["create-file"]))
		Expect(err).To(MatchError(ContainSubstring(tools.ScopeWriteName)), "no granted scopes at all")
		Expect(w.srv.Calls()).To(BeEmpty(), "the service was never asked, so it never answered 403")
	})

	It("keeps the read tools working with the read scope alone", func() {
		_, _, err := w.read("read-file", map[string]any{"folder": "proj", "fileId": "notes"}, tools.ScopeRead)
		Expect(err).NotTo(HaveOccurred())
	})

	It("previews without changing anything", func() {
		p, err := w.rt.Preview(ctx, wenv("op1", "create-file", allTools["create-file"], bothScopes...))
		Expect(err).NotTo(HaveOccurred())
		Expect(writeCalls()).To(Equal(0))
		var s struct {
			Count  int            `json:"count"`
			Sample map[string]any `json:"sample"`
		}
		Expect(json.Unmarshal(p.Summary, &s)).To(Succeed())
		Expect(s.Sample).To(HaveKeyWithValue("action", "Create a file"))
		Expect(s.Sample).To(HaveKeyWithValue("name", "new.txt"))
		Expect(s.Sample).To(HaveKeyWithValue("parentName", "Projects"))
		Expect(s.Sample).To(HaveKeyWithValue("content", "text"))
		Expect(p.OperationID).To(Equal("op1"))
	})

	It("creates a file once, with an operation property, even when applied twice", func() {
		rc, err := apply("op-create", "create-file", map[string]any{"folder": "proj", "parent": "sub", "name": "it's new.md", "content": "# hi\n", "mimeType": "text/markdown"})
		Expect(err).NotTo(HaveOccurred())
		Expect(rc.State).To(Equal(contract.ReceiptCommitted))
		got := w.srv.Find("it's new.md")
		Expect(got).To(HaveLen(1))
		Expect(string(got[0].Content)).To(Equal("# hi\n"))
		Expect(got[0].Parents).To(Equal([]string{"sub"}))
		Expect(got[0].MimeType).To(Equal("text/markdown"))
		Expect(got[0].AppProperties).To(HaveKeyWithValue("localaiOp", "op-create"))
		Expect(string(rc.Result.Payload)).To(ContainSubstring(got[0].ID))
		Expect(string(rc.Result.Payload)).To(ContainSubstring("https://drive.google.com/"))

		again, err := w.rt.Apply(ctx, wenv("op-create", "create-file", map[string]any{"folder": "proj", "parent": "sub", "name": "it's new.md"}, bothScopes...), rc.Revision)
		Expect(err).NotTo(HaveOccurred())
		Expect(again.State).To(Equal(contract.ReceiptCommitted))
		Expect(w.srv.Find("it's new.md")).To(HaveLen(1))
		rcv, err := w.rt.Receipt(ctx, "op-create")
		Expect(err).NotTo(HaveOccurred())
		Expect(rcv.State).To(Equal(contract.ReceiptCommitted))
	})

	It("creates in the allowed folder by default, with default type text/plain", func() {
		_, err := apply("op-def", "create-file", map[string]any{"folder": "proj", "name": "plain"})
		Expect(err).NotTo(HaveOccurred())
		f := w.srv.Find("plain")[0]
		Expect(f.Parents).To(Equal([]string{"proj"}))
		Expect(f.MimeType).To(Equal("text/plain"))
	})

	It("does not create twice when the answer was lost", func() {
		w.srv.DropWrites(true)
		e := wenv("op-lost", "create-file", map[string]any{"folder": "proj", "name": "lost.txt", "content": "x"}, bothScopes...)
		p, err := w.rt.Preview(ctx, e)
		Expect(err).NotTo(HaveOccurred())
		_, err = w.rt.Apply(ctx, e, p.Revision)
		Expect(err).To(HaveOccurred(), "the host sees an outcome that is not known")
		Expect(w.srv.Find("lost.txt")).To(HaveLen(1), "the service made it")
		rc, err := w.rt.Receipt(ctx, "op-lost")
		Expect(err).NotTo(HaveOccurred())
		Expect(rc.State).To(Equal(contract.ReceiptUnknown))

		w.srv.DropWrites(false)
		rc, err = w.rt.Apply(ctx, e, p.Revision)
		Expect(err).NotTo(HaveOccurred())
		Expect(rc.State).To(Equal(contract.ReceiptCommitted))
		Expect(w.srv.Find("lost.txt")).To(HaveLen(1), "found by its property, not made again")
	})

	It("renames and replaces the content of a file the add-on created", func() {
		rc, err := apply("op-up", "update-file", map[string]any{"folder": "proj", "fileId": "mine", "name": "renamed.txt", "content": "new text"})
		Expect(err).NotTo(HaveOccurred())
		Expect(rc.State).To(Equal(contract.ReceiptCommitted))
		f := w.srv.File("mine")
		Expect(f.Name).To(Equal("renamed.txt"))
		Expect(string(f.Content)).To(Equal("new text"))
	})

	Describe("update-file with a new content and a new name", func() {
		args := map[string]any{"folder": "proj", "fileId": "mine", "name": "renamed.txt", "content": "new text"}

		// withRenameRefused previews, then makes the rename (a PATCH of the
		// metadata, not of the upload) answer with status.
		withRenameRefused := func(op string, status int, reason string) (contract.Receipt, error) {
			e := wenv(op, "update-file", args, bothScopes...)
			p, err := w.rt.Preview(ctx, e)
			Expect(err).NotTo(HaveOccurred())
			w.srv.InjectMethod(http.MethodPatch, "/drive/v3/files/mine", status, reason, 1)
			return w.rt.Apply(ctx, e, p.Revision)
		}

		It("records a partial receipt when the content is replaced and the rename is refused", func() {
			rc, err := withRenameRefused("op-part", http.StatusForbidden, "forbidden")
			Expect(err).NotTo(HaveOccurred())
			Expect(rc.State).To(Equal(contract.ReceiptCommitted))
			var out struct {
				Data map[string]any `json:"data"`
			}
			Expect(json.Unmarshal(rc.Result.Payload, &out)).To(Succeed())
			Expect(out.Data).To(HaveKeyWithValue("partial", true))
			Expect(out.Data).To(HaveKeyWithValue("contentUpdated", true))
			Expect(out.Data).To(HaveKeyWithValue("renameRefused", true))
			Expect(out.Data["note"]).To(And(ContainSubstring("content was updated"), ContainSubstring("rename was refused"), ContainSubstring("new name only")))
			f := w.srv.File("mine")
			Expect(string(f.Content)).To(Equal("new text"))
			Expect(f.Name).To(Equal("mine.txt"))

			again, err := w.rt.Receipt(ctx, "op-part")
			Expect(err).NotTo(HaveOccurred())
			Expect(again.State).To(Equal(contract.ReceiptCommitted), "the journal keeps the partial receipt")
		})

		It("retries the rename alone with the new name only", func() {
			_, err := withRenameRefused("op-part2", http.StatusForbidden, "forbidden")
			Expect(err).NotTo(HaveOccurred())
			before := len(w.srv.Calls())
			rc, err := apply("op-retry", "update-file", map[string]any{"folder": "proj", "fileId": "mine", "name": "renamed.txt"})
			Expect(err).NotTo(HaveOccurred())
			Expect(rc.State).To(Equal(contract.ReceiptCommitted))
			Expect(w.srv.File("mine").Name).To(Equal("renamed.txt"))
			for _, c := range w.srv.Calls()[before:] {
				Expect(c.Path).NotTo(HavePrefix("/upload/"), "the content was not sent again")
			}
		})

		It("leaves the outcome unknown, with no receipt, when the rename fails in a way that does not prove anything", func() {
			e := wenv("op-unknown", "update-file", args, bothScopes...)
			p, err := w.rt.Preview(ctx, e)
			Expect(err).NotTo(HaveOccurred())
			w.srv.DropWrites(true)
			_, err = w.rt.Apply(ctx, e, p.Revision)
			Expect(err).To(HaveOccurred())
			rc, _ := w.rt.Receipt(ctx, "op-unknown")
			Expect(rc.State).To(Equal(contract.ReceiptUnknown))
		})
	})

	It("says what the narrow scope cannot do when a file was not created by the add-on", func() {
		_, err := apply("op-user", "update-file", map[string]any{"folder": "proj", "fileId": "notes", "name": "x.txt"})
		Expect(err).To(MatchError(ContainSubstring(tools.ScopeWriteName)))
		Expect(err).To(MatchError(ContainSubstring("only files made here")))
		Expect(w.srv.File("notes").Name).To(Equal("notes.txt"))
		Expect(err.Error()).NotTo(ContainSubstring("403"))
	})

	It("moves a file inside the allowed folder", func() {
		_, err := apply("op-mv", "move-file", map[string]any{"folder": "proj", "fileId": "mine", "newParent": "sub"})
		Expect(err).NotTo(HaveOccurred())
		Expect(w.srv.File("mine").Parents).To(Equal([]string{"sub"}))
	})

	It("moves a file to the trash and does not delete it", func() {
		_, err := apply("op-tr", "trash-file", map[string]any{"folder": "proj", "fileId": "mine"})
		Expect(err).NotTo(HaveOccurred())
		f := w.srv.File("mine")
		Expect(f).NotTo(BeNil())
		Expect(f.Trashed).To(BeTrue())
		for _, c := range w.srv.Calls() {
			Expect(c.Method).NotTo(Equal("DELETE"))
			Expect(c.Path).NotTo(ContainSubstring("permissions"))
		}
	})

	It("keeps every change inside the allowed folder", func() {
		w.srv.AddFile(gdrivetest.File{ID: "theirs", Name: "theirs.txt", Parents: []string{"other"}, CreatedByApp: true})
		for _, c := range []struct {
			tool string
			args map[string]any
		}{
			{"create-file", map[string]any{"folder": "proj", "parent": "other", "name": "x"}},
			{"update-file", map[string]any{"folder": "proj", "fileId": "theirs", "name": "x"}},
			{"move-file", map[string]any{"folder": "proj", "fileId": "theirs", "newParent": "sub"}},
			{"move-file", map[string]any{"folder": "proj", "fileId": "mine", "newParent": "other"}},
			{"trash-file", map[string]any{"folder": "proj", "fileId": "theirs"}},
			{"trash-file", map[string]any{"folder": "other", "fileId": "theirs"}},
		} {
			_, err := w.rt.Preview(ctx, wenv("op", c.tool, c.args, bothScopes...))
			Expect(err).To(MatchError(protocol.ErrBadRequest), c.tool)
		}
		Expect(writeCalls()).To(Equal(0))
		Expect(w.srv.File("theirs").Trashed).To(BeFalse())
	})

	It("refuses what the tools do not do", func() {
		w.srv.AddFile(gdrivetest.File{ID: "gdoc", Name: "A doc", MimeType: gdrivetest.DocumentType, Parents: []string{"proj"}, CreatedByApp: true})
		w.srv.AddFile(gdrivetest.File{ID: "bin", Name: "pic", MimeType: "image/png", Parents: []string{"proj"}, CreatedByApp: true})
		for name, c := range map[string]struct {
			tool string
			args map[string]any
		}{
			"content of a Doc":        {"update-file", map[string]any{"folder": "proj", "fileId": "gdoc", "content": "x"}},
			"content of a binary":     {"update-file", map[string]any{"folder": "proj", "fileId": "bin", "content": "x"}},
			"trash a folder":          {"trash-file", map[string]any{"folder": "proj", "fileId": "sub"}},
			"change the allowed root": {"trash-file", map[string]any{"folder": "proj", "fileId": "proj"}},
			"move into itself":        {"move-file", map[string]any{"folder": "proj", "fileId": "sub", "newParent": "sub"}},
			"a move to where it is":   {"move-file", map[string]any{"folder": "proj", "fileId": "mine", "newParent": "proj"}},
			"empty update":            {"update-file", map[string]any{"folder": "proj", "fileId": "mine"}},
			"a parent in an update":   {"update-file", map[string]any{"folder": "proj", "fileId": "mine", "name": "x", "parent": "sub"}},
			"sharing":                 {"create-file", map[string]any{"folder": "proj", "name": "x", "permissions": "anyone"}},
			"a type it does not make": {"create-file", map[string]any{"folder": "proj", "name": "x", "mimeType": "application/vnd.google-apps.document"}},
			"a name with a newline":   {"create-file", map[string]any{"folder": "proj", "name": "a\nb"}},
			"a binary content":        {"create-file", map[string]any{"folder": "proj", "name": "a", "content": "a\x00b"}},
			"a huge content":          {"create-file", map[string]any{"folder": "proj", "name": "a", "content": strings.Repeat("x", tools.MaxWriteBody+1)}},
			"the token as content":    {"create-file", map[string]any{"folder": "proj", "name": "a", "content": "see " + token}},
			"no file ID":              {"trash-file", map[string]any{"folder": "proj"}},
			"an unknown tool":         {"delete-file", map[string]any{"folder": "proj", "fileId": "mine"}},
		} {
			_, err := w.rt.Preview(ctx, wenv("op", c.tool, c.args, bothScopes...))
			Expect(err).To(MatchError(protocol.ErrBadRequest), name)
		}
		Expect(writeCalls()).To(Equal(0))
	})

	It("does not apply a change when the file changed after the preview", func() {
		e := wenv("op-stale", "update-file", map[string]any{"folder": "proj", "fileId": "mine", "content": "mine now"}, bothScopes...)
		p, err := w.rt.Preview(ctx, e)
		Expect(err).NotTo(HaveOccurred())
		_, err = apply("op-other", "update-file", map[string]any{"folder": "proj", "fileId": "mine", "name": "other.txt"})
		Expect(err).NotTo(HaveOccurred())
		rc, err := w.rt.Apply(ctx, e, p.Revision)
		Expect(err).NotTo(HaveOccurred())
		Expect(rc.State).To(Equal(contract.ReceiptRejected))
		Expect(string(w.srv.File("mine").Content)).To(Equal("old"))
	})

	It("tells the host to renew when the token was rejected during the apply", func() {
		e := wenv("op-401", "trash-file", map[string]any{"folder": "proj", "fileId": "mine"}, bothScopes...)
		p, err := w.rt.Preview(ctx, e)
		Expect(err).NotTo(HaveOccurred())
		w.srv.RevokeToken(token)
		_, err = w.rt.Apply(ctx, e, p.Revision)
		Expect(errors.Is(err, contract.ErrCredentialRejected)).To(BeTrue())
		Expect(w.srv.File("mine").Trashed).To(BeFalse())
	})

	It("refuses with a wait when the service limits the calls, and sent nothing", func() {
		e := wenv("op-rl", "trash-file", map[string]any{"folder": "proj", "fileId": "mine"}, bothScopes...)
		p, err := w.rt.Preview(ctx, e)
		Expect(err).NotTo(HaveOccurred())
		w.srv.Inject("/drive/v3/files/mine", 429, "rateLimitExceeded", "11", 1)
		_, err = w.rt.Apply(ctx, e, p.Revision)
		Expect(err).To(MatchError(ContainSubstring("Retry in 11 s")))
		Expect(w.srv.File("mine").Trashed).To(BeFalse())
	})
})
