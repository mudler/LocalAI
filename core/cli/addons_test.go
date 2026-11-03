// SPDX-License-Identifier: MIT
package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/alecthomas/kong"
	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/pkg/addons/zipformat"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Add-on zip commands", func() {
	manifest := `{"schemaVersion":1,"id":"sample","version":"1.2.3","executable":"bin/sample","os":"linux","arch":"amd64","host":{"min":1,"max":2},"api":{"min":2,"max":2},"protocol":{"min":1,"max":1},"dataFormat":{"min":1,"max":2},"capabilities":{"service":true,"hostOperations":{"required":["workspace.materialize"],"optional":["egress.check"]}}}`
	writeZip := func() string {
		var b bytes.Buffer
		Expect(zipformat.Write(&b, "sample", "1.2.3", "linux", "amd64", "bin/sample", []zipformat.Entry{
			{Name: "manifest.json", Data: []byte(manifest)}, {Name: "bin/sample", Data: []byte("x"), Exec: true},
		})).To(Succeed())
		p := filepath.Join(GinkgoT().TempDir(), "sample-1.2.3-linux-amd64.zip")
		Expect(os.WriteFile(p, b.Bytes(), 0o600)).To(Succeed())
		return p
	}
	parse := func(args ...string) (*AddonsCMD, error) {
		var command struct {
			Addons AddonsCMD `cmd:"" name:"addons"`
		}
		p, err := kong.New(&command)
		if err != nil {
			return nil, err
		}
		_, err = p.Parse(append([]string{"addons"}, args...))
		return &command.Addons, err
	}

	It("shows what a zip holds and that nothing signs it", func() {
		c, err := parse("inspect", writeZip())
		Expect(err).NotTo(HaveOccurred())
		var out bytes.Buffer
		c.Inspect.output = &out
		Expect(c.Inspect.Run(nil)).To(Succeed())
		Expect(out.String()).To(ContainSubstring("sample 1.2.3"))
		Expect(out.String()).To(ContainSubstring("linux/amd64"))
		Expect(out.String()).To(ContainSubstring("required workspace.materialize; optional egress.check"))
		Expect(out.String()).To(MatchRegexp(`sha256:\s+[0-9a-f]{64}`))
		Expect(out.String()).To(ContainSubstring("local file, unsigned"))
	})

	It("refuses a file that is not an add-on zip", func() {
		p := filepath.Join(GinkgoT().TempDir(), "x.zip")
		Expect(os.WriteFile(p, []byte("nope"), 0o600)).To(Succeed())
		c, err := parse("inspect", p)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Inspect.Run(nil)).To(MatchError(ContainSubstring("not a zip")))
	})

	It("uploads the file with the key, and prints the page that finishes the install", func() {
		path := writeZip()
		raw, _ := os.ReadFile(path)
		var gotAuth, gotName string
		var gotBytes []byte
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			Expect(r.Method + " " + r.URL.Path).To(Equal("POST /api/addons/files"))
			f, h, err := r.FormFile("file")
			Expect(err).NotTo(HaveOccurred())
			gotName = h.Filename
			gotBytes, _ = io.ReadAll(f)
			_ = json.NewEncoder(w).Encode(addons.FileView{FileID: "0123456789abcdef0123456789abcdef", SHA256: zipSHA(raw), Relation: "new"})
		}))
		DeferCleanup(srv.Close)
		c, err := parse("install", path, "--target", srv.URL, "--api-key", "k1")
		Expect(err).NotTo(HaveOccurred())
		var out bytes.Buffer
		c.Install.output = &out
		Expect(c.Install.Run(nil)).To(Succeed())
		Expect(gotAuth).To(Equal("Bearer k1"))
		Expect(gotName).To(Equal("sample-1.2.3-linux-amd64.zip"))
		Expect(gotBytes).To(Equal(raw))
		Expect(out.String()).To(ContainSubstring(srv.URL + "/app/addons?file=0123456789abcdef0123456789abcdef"))
		Expect(out.String()).To(ContainSubstring("in the browser"))
	})

	It("reports the reason the server gives", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":{"type":"wrong_platform","code":400,"message":"This file is for linux/arm64; this host is linux/amd64"}}`))
		}))
		DeferCleanup(srv.Close)
		c, err := parse("install", writeZip(), "--target", srv.URL)
		Expect(err).NotTo(HaveOccurred())
		var out bytes.Buffer
		c.Install.output = &out
		Expect(c.Install.Run(nil)).To(MatchError(ContainSubstring("wrong_platform")))
	})

	It("rejects a target that is not a web address", func() {
		c, err := parse("install", writeZip(), "--target", "ftp://x")
		Expect(err).NotTo(HaveOccurred())
		c.Install.output = io.Discard
		Expect(c.Install.Run(nil)).To(MatchError(ContainSubstring("--target")))
	})
})

func zipSHA(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}
