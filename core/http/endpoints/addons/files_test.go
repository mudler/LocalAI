// SPDX-License-Identifier: MIT
package addons

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/auth"
	managerpkg "github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/hostgrants"
	"github.com/mudler/LocalAI/core/services/addons/packages"
	"github.com/mudler/LocalAI/core/services/addons/uploads"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fileOwner is the owner's file service over a real staging store.
type fileOwner struct {
	store  *uploads.Store
	max    int64
	reject error
}

func (f *fileOwner) FileBegin(_ context.Context, a contract.Actor, name string) (string, int64, error) {
	id, err := f.store.Begin(a.UserID, name, f.max)
	return id, f.max, err
}
func (f *fileOwner) FileChunk(_ context.Context, a contract.Actor, id string, off int64, p []byte) error {
	return f.store.Append(id, a.UserID, off, p)
}
func (f *fileOwner) FileFinish(_ context.Context, a contract.Actor, id string) (managerpkg.FileView, error) {
	e, err := f.store.Finish(id, a.UserID)
	if err == nil && f.reject != nil {
		_ = f.store.Remove(id, a.UserID)
		err = f.reject
	}
	if err != nil {
		return managerpkg.FileView{}, err
	}
	return managerpkg.FileView{FileID: e.ID, Name: e.Name, Size: e.Size, SHA256: e.SHA256, AddonID: "sample", Version: "1.0.0", Relation: managerpkg.FileNew,
		Required: []string{}, Optional: []string{}, Source: "local-file", Origin: "local file, unsigned"}, nil
}
func (f *fileOwner) FileAbort(_ context.Context, a contract.Actor, id string) {
	f.store.Abort(id, a.UserID)
}
func (f *fileOwner) FileDrop(_ context.Context, a contract.Actor, id string) error {
	return f.store.Remove(id, a.UserID)
}
func (f *fileOwner) FileDescribe(_ context.Context, a contract.Actor, id string) (managerpkg.FileView, error) {
	_, e, err := f.store.Open(id, a.UserID)
	return managerpkg.FileView{FileID: e.ID, SHA256: e.SHA256}, err
}

var _ = Describe("add-on file routes", func() {
	const host = "192.0.2.10:8080"
	var (
		e     *echo.Echo
		owner *fileOwner
	)
	BeforeEach(func() {
		store, err := hostgrants.Open(filepath.Join(GinkgoT().TempDir(), "hostgrants.json"))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(store.Close)
		owner = &fileOwner{store: &uploads.Store{Root: filepath.Join(GinkgoT().TempDir(), "uploads")}, max: 1 << 20}
		svc, err := gateway.NewHostGrantsOwner(gateway.HostGrantsConfig{Store: store, Manager: &hgManager{},
			Admin: func(context.Context, contract.Actor) error { return nil }})
		Expect(err).NotTo(HaveOccurred())
		svc.WithFiles(owner)
		gw := &gateway.Gateway{HostGrantsOwner: svc, Revalidate: func(context.Context, contract.Actor) error { return nil }}
		e = echo.New()
		mode := WithOwnership(auth.AddonModeLocalOwner)
		g := e.Group("/api/addons", BrowserProtection())
		g.POST("/files", UploadAddonFile(gw, nil, mode))
		g.GET("/files/:fileId", GetAddonFile(gw, nil, mode))
		g.DELETE("/files/:fileId", DeleteAddonFile(gw, nil, mode))
		registerHostGrants(e, gw)
	})
	form := func(name string, content []byte) (*bytes.Buffer, string) {
		var b bytes.Buffer
		mw := multipart.NewWriter(&b)
		if name != "" {
			part, err := mw.CreateFormFile(name, "sample.zip")
			Expect(err).NotTo(HaveOccurred())
			_, _ = part.Write(content)
		}
		Expect(mw.Close()).To(Succeed())
		return &b, mw.FormDataContentType()
	}
	do := func(method, path string, body *bytes.Buffer, ctype string, headers map[string]string) (int, map[string]any) {
		var r *http.Request
		if body != nil {
			r = httptest.NewRequest(method, path, body)
		} else {
			r = httptest.NewRequest(method, path, nil)
		}
		r.Host = host
		if ctype != "" {
			r.Header.Set("Content-Type", ctype)
		}
		r.Header.Set("Sec-Fetch-Site", "same-origin")
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		e.ServeHTTP(w, r)
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	upload := func(content []byte) (int, map[string]any) {
		b, ct := form("file", content)
		return do("POST", "/api/addons/files", b, ct, nil)
	}

	It("stages a file and answers with its digest", func() {
		payload := bytes.Repeat([]byte("zip"), 1000)
		code, out := upload(payload)
		Expect(code).To(Equal(200), "%v", out)
		sum := sha256.Sum256(payload)
		Expect(out["sha256"]).To(Equal(hex.EncodeToString(sum[:])))
		Expect(out["origin"]).To(Equal("local file, unsigned"))
		Expect(out["source"]).To(Equal("local-file"))
		for _, k := range []string{"fileId", "name", "size", "addonId", "version", "relation", "required", "optional"} {
			Expect(out).To(HaveKey(k), k)
		}
		id := out["fileId"].(string)
		code, got := do("GET", "/api/addons/files/"+id, nil, "", nil)
		Expect(code).To(Equal(200))
		Expect(got["fileId"]).To(Equal(id))
		code, _ = do("DELETE", "/api/addons/files/"+id, nil, "", nil)
		Expect(code).To(Equal(204))
		code, got = do("GET", "/api/addons/files/"+id, nil, "", nil)
		Expect(code).To(Equal(404))
		Expect(got["error"].(map[string]any)["type"]).To(Equal("not_found"))
	})

	It("accepts an administrator's API key to stage a file, as the command line does", func() {
		b, ct := form("file", []byte("zip"))
		code, _ := do("POST", "/api/addons/files", b, ct, map[string]string{"Sec-Fetch-Site": "", "Authorization": "Bearer key"})
		Expect(code).To(Equal(200))
	})

	It("refuses a cross-site or unproven request that rides a session cookie", func() {
		cookie := map[string]string{"Cookie": "session=abc"}
		with := func(h map[string]string) map[string]string {
			out := map[string]string{}
			for k, v := range cookie {
				out[k] = v
			}
			for k, v := range h {
				out[k] = v
			}
			return out
		}
		b, ct := form("file", []byte("zip"))
		code, _ := do("POST", "/api/addons/files", b, ct, with(map[string]string{"Sec-Fetch-Site": "cross-site"}))
		Expect(code).To(Equal(403))
		b, ct = form("file", []byte("zip"))
		code, _ = do("POST", "/api/addons/files", b, ct, with(map[string]string{"Sec-Fetch-Site": ""}))
		Expect(code).To(Equal(403))
		ents, _ := filepath.Glob(filepath.Join(owner.store.Root, "*"))
		Expect(ents).To(BeEmpty())
	})

	It("refuses a request without a file part", func() {
		b, ct := form("", nil)
		code, _ := do("POST", "/api/addons/files", b, ct, nil)
		Expect(code).To(Equal(400))
		b, ct = form("other", []byte("zip"))
		code, _ = do("POST", "/api/addons/files", b, ct, nil)
		Expect(code).To(Equal(400))
		code, _ = do("POST", "/api/addons/files", bytes.NewBufferString(`{"file":"x"}`), "application/json", nil)
		Expect(code).To(Equal(400))
	})

	It("types a file over the limit and a file that broke a rule", func() {
		code, out := upload(bytes.Repeat([]byte("a"), (1<<20)+1))
		Expect(code).To(Equal(413))
		Expect(out["error"].(map[string]any)["type"]).To(Equal("too_large"))
		owner.reject = &packages.FileError{Code: packages.FileUnsafePath, Msg: "The zip holds an unsafe file path"}
		code, out = upload([]byte("zip"))
		Expect(code).To(Equal(400))
		Expect(out["error"].(map[string]any)["type"]).To(Equal("unsafe_path"))
		Expect(out["error"].(map[string]any)["message"]).To(Equal("The zip holds an unsafe file path"))
	})

	It("does not let a script install a staged file: the install keeps its human surface rule", func() {
		_, out := upload([]byte("zip"))
		id := out["fileId"].(string)
		body := `{"operationId":"f1","action":"install","sourceId":"local-file","addonId":"sample","version":"` + id + `","downgrade":true,"approve":{"digest":"sha256:aa","ops":[],"revision":0,"nonce":"n"}}`
		code, res := do("POST", "/api/addons/host-grants/install", bytes.NewBufferString(body), "application/json", map[string]string{"Authorization": "Bearer key", ConfirmHeader: "sample@sha256:aa"})
		Expect(code).To(Equal(403))
		Expect(res["error"].(map[string]any)["type"]).To(Equal("not_human_surface"))
		Expect(strings.Contains(body, "downgrade")).To(BeTrue())
	})
})
