// SPDX-License-Identifier: MIT
package tools_test

import (
	"context"
	"encoding/json"
	"net/url"
	"os"

	"github.com/mudler/LocalAI/addons/gdrive/gdrivetest"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/addons/sdk/protocol"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The key * is the choice "all folders": the host lets it through for a
// connection whose list is *, and the add-on then does not hold files to one
// folder. Any other key keeps the folder check.
var _ = Describe("a connection that allows all folders", func() {
	var w *world
	ctx := context.Background()
	allEnv := func(tool string, args map[string]any) contract.Envelope {
		a := map[string]any{"folder": "*"}
		for k, v := range args {
			a[k] = v
		}
		return envFor(token, "*", tool, a, bothScopes...)
	}
	readAll := func(tool string, args map[string]any) (reply, error) {
		res, err := w.rt.Read(ctx, allEnv(tool, args))
		var r reply
		if err == nil {
			ExpectWithOffset(1, json.Unmarshal(res.Payload, &r)).To(Succeed())
		}
		return r, err
	}
	BeforeEach(func() {
		w = newWorld()
		w.srv.AddFile(gdrivetest.File{ID: "shared1", Name: "team-plan.txt", Parents: []string{"drive-a"}, DriveID: "drive-a", Content: []byte("shared drive text")})
	})

	It("declares wildcardResources so the host honors the entry *", func() {
		b, err := os.ReadFile("../../manifest.json")
		Expect(err).NotTo(HaveOccurred())
		m, err := contract.ParseManifest(b)
		Expect(err).NotTo(HaveOccurred())
		Expect(m.Capabilities.Connector.Kinds[0].WildcardResources).To(BeTrue())
	})

	It("searches every folder, My Drive and shared drives alike", func() {
		r, err := readAll("search", map[string]any{"name": "t"})
		Expect(err).NotTo(HaveOccurred())
		Expect(names(r)).To(ContainElements("notes.txt", "secret.txt", "team-plan.txt"))
		var corpora []string
		for _, c := range w.srv.Calls() {
			if c.Path == "/drive/v3/files" {
				v, _ := url.ParseQuery(c.Query)
				corpora = append(corpora, v.Get("corpora"))
			}
		}
		Expect(corpora).To(ConsistOf("allDrives"), "the search asks for every drive")
	})

	It("reads a file outside any one folder and a file of a shared drive", func() {
		for _, id := range []string{"secret", "shared1"} {
			_, err := readAll("get-metadata", map[string]any{"fileId": id})
			Expect(err).NotTo(HaveOccurred(), id)
			r, err := readAll("read-file", map[string]any{"fileId": id})
			Expect(err).NotTo(HaveOccurred(), id)
			Expect(r.Data).NotTo(BeEmpty())
		}
	})

	It("lists the top of My Drive when no parent is given, and any folder when one is", func() {
		r, err := readAll("list-folder", nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(names(r)).To(ContainElements("Projects", "Other"))
		r, err = readAll("list-folder", map[string]any{"parent": "other"})
		Expect(err).NotTo(HaveOccurred())
		Expect(names(r)).To(ConsistOf("secret.txt"))
	})

	It("still refuses a folder argument that is not the key the host allowed", func() {
		_, err := w.rt.Read(ctx, envFor(token, "*", "list-folder", map[string]any{"folder": "proj"}, bothScopes...))
		Expect(err).To(MatchError(protocol.ErrBadRequest))
		_, err = w.rt.Read(ctx, envFor(token, "proj", "list-folder", map[string]any{"folder": "*"}, bothScopes...))
		Expect(err).To(MatchError(protocol.ErrBadRequest))
	})

	It("keeps the folder check for a call that names one folder", func() {
		_, _, err := w.read("get-metadata", map[string]any{"folder": "proj", "fileId": "secret"})
		Expect(err).To(MatchError(ContainSubstring("not in the folder the host allowed")))
	})

	It("writes anywhere with the write mode of the connection, and still proposes", func() {
		e := allEnv("create-file", map[string]any{"parent": "other", "name": "new.txt", "content": "text"})
		e.Request.Action = "write"
		e.Request.OperationID = "op-all"
		p, err := w.rt.Preview(ctx, e)
		Expect(err).NotTo(HaveOccurred())
		_, err = w.rt.Apply(ctx, e, p.Revision)
		Expect(err).NotTo(HaveOccurred())
		// With no parent the file goes to the top of My Drive.
		e = allEnv("create-file", map[string]any{"name": "top.txt", "content": "text"})
		e.Request.Action = "write"
		e.Request.OperationID = "op-all-2"
		_, err = w.rt.Preview(ctx, e)
		Expect(err).NotTo(HaveOccurred())
	})
})
