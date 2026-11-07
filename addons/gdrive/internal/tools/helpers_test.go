// SPDX-License-Identifier: MIT
package tools_test

import (
	"context"
	"encoding/json"
	"net/url"

	"github.com/mudler/LocalAI/addons/gdrive/gdrivetest"
	"github.com/mudler/LocalAI/addons/gdrive/internal/driveapi"
	"github.com/mudler/LocalAI/addons/gdrive/internal/tools"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const token = "ya29.SECRET_ACCESS_TOKEN_VALUE"

// reply is the wrapper of a result.
type reply struct {
	Source    string         `json:"source"`
	Untrusted bool           `json:"untrusted"`
	Notice    string         `json:"notice"`
	Data      map[string]any `json:"data"`
}

// world is a fake Drive with a small tree and a runtime over it.
//
//	root-id (My Drive)
//	  proj          the folder the host allows
//	    notes.txt, doc1, sheet1, slides1, draw1, image.png, big.txt, sub/deep.txt
//	  other         outside the allowed folder
//	    secret.txt
type world struct {
	srv *gdrivetest.Server
	rt  *tools.Runtime
}

func newWorld() *world {
	srv := gdrivetest.New()
	DeferCleanup(srv.Close)
	srv.AddToken(token, "person@example.test", gdrivetest.ScopeReadonly, gdrivetest.ScopeFile)
	srv.AddFolder("proj", "Projects", "")
	srv.AddFolder("other", "Other", "")
	srv.AddFolder("sub", "Sub", "proj")
	srv.AddFile(gdrivetest.File{ID: "notes", Name: "notes.txt", Parents: []string{"proj"}, Content: []byte("hello world\nsecond line\n")})
	srv.AddFile(gdrivetest.File{ID: "doc1", Name: "Plan", MimeType: gdrivetest.DocumentType, Parents: []string{"proj"}, Content: []byte("# Plan\n\nBody text\n")})
	srv.AddFile(gdrivetest.File{ID: "sheet1", Name: "Budget", MimeType: gdrivetest.SpreadsheetType, Parents: []string{"proj"}, Content: []byte("a,b\n1,2\n")})
	srv.AddFile(gdrivetest.File{ID: "slides1", Name: "Deck", MimeType: gdrivetest.PresentationType, Parents: []string{"proj"}, Content: []byte("Slide one\nSlide two\n")})
	srv.AddFile(gdrivetest.File{ID: "draw1", Name: "Sketch", MimeType: gdrivetest.DrawingType, Parents: []string{"proj"}, Content: []byte("x")})
	srv.AddFile(gdrivetest.File{ID: "image", Name: "image.png", MimeType: "image/png", Parents: []string{"proj"}, Content: []byte("\x89PNG\r\n\x00\x01")})
	srv.AddFile(gdrivetest.File{ID: "deep", Name: "deep.txt", Parents: []string{"sub"}, Content: []byte("deep content")})
	srv.AddFile(gdrivetest.File{ID: "secret", Name: "secret.txt", Parents: []string{"other"}, Content: []byte("not for the model")})
	api := driveapi.New()
	api.Base = srv.URL
	return &world{srv: srv, rt: tools.New(api)}
}

// env is the envelope of a call of tool with args as the host builds it:
// the allowed folder is the resource key and the scopes are the login's.
func env(tool string, args map[string]any, scopes ...string) contract.Envelope {
	return envFor(token, "proj", tool, args, scopes...)
}

func envFor(tok, key, tool string, args map[string]any, scopes ...string) contract.Envelope {
	a := map[string]any{"tool": tool}
	for k, v := range args {
		a[k] = v
	}
	raw, _ := json.Marshal(a)
	return contract.Envelope{Request: contract.Request{AddonID: "gdrive", OperationID: "op1", Generation: 1, Action: "read", Arguments: raw,
		ResourceKey: key, Credential: tok, GrantedScopes: scopes}}
}

var bothScopes = []string{tools.ScopeRead, tools.ScopeWrite}

// read calls a read tool and decodes the wrapper.
func (w *world) read(tool string, args map[string]any, scopes ...string) (reply, contract.Result, error) {
	if len(scopes) == 0 {
		scopes = bothScopes
	}
	res, err := w.rt.Read(context.Background(), env(tool, args, scopes...))
	var r reply
	if err == nil {
		ExpectWithOffset(1, json.Unmarshal(res.Payload, &r)).To(Succeed())
	}
	return r, res, err
}

func (w *world) mustRead(tool string, args map[string]any) reply {
	r, _, err := w.read(tool, args)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	return r
}

// names returns the names of the items of a list result.
func names(r reply) []string {
	var out []string
	for _, it := range r.Data["items"].([]any) {
		out = append(out, it.(map[string]any)["name"].(string))
	}
	return out
}

// listQueries returns the q parameter of every file list the fake served.
func (w *world) listQueries() []string {
	var out []string
	for _, c := range w.srv.Calls() {
		if c.Path == "/drive/v3/files" {
			v, _ := url.ParseQuery(c.Query)
			out = append(out, v.Get("q"))
		}
	}
	return out
}
