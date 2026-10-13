// SPDX-License-Identifier: MIT
//go:build auth

package addons

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons/artifacts"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/policy"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fileRuntime is a stand-in addon that answers a read with file outputs.
type fileRuntime struct {
	mu      sync.Mutex
	outputs []contract.OutputDescription
	bodies  map[string]string
	opens   map[string]int
}

func (r *fileRuntime) set(o contract.OutputDescription, body string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.outputs = []contract.OutputDescription{o}
	if r.bodies == nil {
		r.bodies, r.opens = map[string]string{}, map[string]int{}
	}
	r.bodies[o.Handle] = body
}

func (r *fileRuntime) Read(context.Context, contract.Envelope) (contract.Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return contract.Result{Payload: json.RawMessage(`{"rows":3}`), Outputs: append([]contract.OutputDescription(nil), r.outputs...)}, nil
}
func (r *fileRuntime) Preview(context.Context, contract.Envelope) (contract.Preview, error) {
	return contract.Preview{}, errors.New("unused")
}
func (r *fileRuntime) Apply(context.Context, contract.Envelope, contract.Revision) (contract.Receipt, error) {
	return contract.Receipt{}, errors.New("unused")
}
func (r *fileRuntime) Receipt(context.Context, string) (contract.Receipt, error) {
	return contract.Receipt{}, errors.New("unused")
}
func (r *fileRuntime) OpenOutput(_ context.Context, _ contract.Envelope, handle string) (io.ReadCloser, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	body, ok := r.bodies[handle]
	if !ok {
		return nil, errors.New("no such output")
	}
	r.opens[handle]++
	return io.NopCloser(strings.NewReader(body)), nil
}

type fileInstances struct{ rt *fileRuntime }

func (i fileInstances) Runtime(context.Context, string) (addonruntime.Instance, error) {
	return addonruntime.Instance{Generation: 1, Incarnation: "current", Client: i.rt}, nil
}

// artifactStack is the owner stack with the private artifact store attached.
type artifactStack struct {
	*ownerStack
	arts    *artifacts.Artifacts
	files   *fileRuntime
	mu      sync.Mutex
	enabled bool
	now     time.Time
}

func (a *artifactStack) clock() time.Time {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.now
}

func (a *artifactStack) advance(d time.Duration) {
	a.mu.Lock()
	a.now = a.now.Add(d)
	a.mu.Unlock()
}

func (a *artifactStack) enable(on bool) {
	a.mu.Lock()
	a.enabled = on
	a.mu.Unlock()
}

func newArtifactStack(mode string, withAccounts bool) *artifactStack {
	s := newOwnerStack(mode, withAccounts)
	a := &artifactStack{ownerStack: s, files: &fileRuntime{}, enabled: true, now: time.Now()}
	state := func(context.Context, string) (artifacts.AddonState, error) {
		a.mu.Lock()
		defer a.mu.Unlock()
		return artifacts.AddonState{Installed: true, Enabled: a.enabled, Generation: 1}, nil
	}
	var err error
	a.arts, err = artifacts.Open(artifacts.Options{Dir: filepath.Join(GinkgoT().TempDir(), "artifacts"), Policy: s.policy, Runtime: fileInstances{a.files}, Addon: state, Now: a.clock})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = a.arts.Close() })
	s.gateway.Runtime = fileInstances{a.files}
	s.gateway.Artifacts = a.arts.Gateway()
	if withAccounts && mode == auth.AddonModeNamedUser {
		Expect(s.policy.SetGrant(context.Background(), s.owner, s.key, "bob", policy.RoleViewer)).To(Succeed())
	}
	return a
}

func (a *artifactStack) exportBody(op string) string {
	return `{"addonId":"sample","resourceId":"` + a.key.ResourceID + `","operationId":"` + op + `","generation":1,"action":"read","arguments":{}}`
}

// export runs a read that produces one file output and returns its artifact ID.
func (a *artifactStack) export(e http.Handler, token, op string, o contract.OutputDescription, body string) string {
	a.files.set(o, body)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/addons/dispatch", bytes.NewBufferString(a.exportBody(op)))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+token)
	e.ServeHTTP(w, r)
	ExpectWithOffset(1, w.Code).To(Equal(200), w.Body.String())
	var result contract.Result
	ExpectWithOffset(1, json.Unmarshal(w.Body.Bytes(), &result)).To(Succeed())
	ExpectWithOffset(1, result.Outputs).To(HaveLen(1))
	// The client never learns the addon's own handle.
	ExpectWithOffset(1, w.Body.String()).NotTo(ContainSubstring(o.Handle))
	return result.Outputs[0].Handle
}

func (a *artifactStack) waitComplete(e http.Handler, token, id string) {
	EventuallyWithOffset(1, func() string {
		r := send(e, "GET", "/api/addons/artifacts/"+id, "", bearerOf(token))
		ExpectWithOffset(1, r.code).To(Equal(200))
		return r.state()
	}, 5*time.Second, 5*time.Millisecond).Should(Equal("complete"))
}

type fetched struct {
	code   int
	header http.Header
	body   string
	err    error
}

func fetch(url string, mutate func(*http.Request)) fetched {
	req, err := http.NewRequest("GET", url, nil)
	Expect(err).NotTo(HaveOccurred())
	mutate(req)
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	Expect(err).NotTo(HaveOccurred())
	defer resp.Body.Close()
	b, rerr := io.ReadAll(resp.Body)
	return fetched{code: resp.StatusCode, header: resp.Header, body: string(b), err: rerr}
}

var _ = Describe("private artifact routes", Label("artifacts"), func() {
	csv := contract.OutputDescription{Handle: "csv-export", Name: "report.csv", MediaType: "text/csv", Size: 8}

	It("serves a private file result as a safe attachment over real HTTP", func() {
		a := newArtifactStack(auth.AddonModeNamedUser, true)
		e := a.frontend(a.gateway)
		server := httptest.NewServer(e)
		DeferCleanup(server.Close)
		id := a.export(e, a.tokens["alice"], "export-1", csv, "a,b\n1,2\n")
		Expect(id).To(MatchRegexp(`^a[0-9a-f]{32}$`))
		a.waitComplete(e, a.tokens["alice"], id)
		status := send(e, "GET", "/api/addons/artifacts/"+id, "", bearerOf(a.tokens["alice"]))
		Expect(status.body["name"]).To(Equal("report.csv"))
		Expect(status.body["mediaType"]).To(Equal("text/csv"))
		Expect(status.body["size"]).To(BeEquivalentTo(8))
		Expect(status.body["principal"]).To(Equal("alice"))
		Expect(status.body["expiresAt"]).NotTo(BeEmpty())
		raw, _ := json.Marshal(status.body)
		Expect(string(raw)).NotTo(ContainSubstring("csv-export"))
		Expect(string(raw)).NotTo(ContainSubstring(os.TempDir()))

		got := fetch(server.URL+"/api/addons/artifacts/"+id+"/download", func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+a.tokens["alice"])
		})
		Expect(got.code).To(Equal(200))
		Expect(got.err).NotTo(HaveOccurred())
		Expect(got.body).To(Equal("a,b\n1,2\n"))
		Expect(got.header.Get("Content-Type")).To(Equal("text/csv"))
		Expect(got.header.Get("Content-Disposition")).To(HavePrefix(`attachment; filename="report.csv"`))
		Expect(got.header.Get("Content-Length")).To(Equal("8"))
		Expect(got.header.Get("X-Content-Type-Options")).To(Equal("nosniff"))
		Expect(got.header.Get("Cache-Control")).To(Equal("private, no-store"))
		Expect(got.header.Get("Content-Security-Policy")).To(ContainSubstring("sandbox"))
		// The browser session of the principal downloads it as a navigation.
		nav := fetch(server.URL+"/api/addons/artifacts/"+id+"/download", func(r *http.Request) {
			r.AddCookie(&http.Cookie{Name: "session", Value: a.cookies["alice"]})
			r.Header.Set("Sec-Fetch-Site", "same-origin")
			r.Header.Set("Sec-Fetch-Mode", "navigate")
		})
		Expect(nav.code).To(Equal(200))
		Expect(nav.body).To(Equal("a,b\n1,2\n"))
		// A cross-site page cannot make the browser fetch it.
		cross := fetch(server.URL+"/api/addons/artifacts/"+id+"/download", func(r *http.Request) {
			r.AddCookie(&http.Cookie{Name: "session", Value: a.cookies["alice"]})
			r.Header.Set("Sec-Fetch-Site", "cross-site")
		})
		Expect(cross.code).To(Equal(403))
		Expect(cross.header.Get("Content-Disposition")).To(BeEmpty())
		// A read repeated with the same operation returns the same artifact.
		again := a.export(e, a.tokens["alice"], "export-1", csv, "a,b\n1,2\n")
		Expect(again).To(Equal(id))
		a.files.mu.Lock()
		Expect(a.files.opens["csv-export"]).To(Equal(1))
		a.files.mu.Unlock()
	})

	It("never renders hostile names or active media types inline", func() {
		a := newArtifactStack(auth.AddonModeNamedUser, true)
		e := a.frontend(a.gateway)
		server := httptest.NewServer(e)
		DeferCleanup(server.Close)
		hostile := map[string]struct{ name, media, wantType, wantName string }{
			"html": {"page.html", "text/html", "application/octet-stream", `filename="page.html"`},
			"svg":  {"image.svg", "image/svg+xml", "application/octet-stream", `filename="image.svg"`},
			"js":   {"x.js", "text/javascript", "application/octet-stream", `filename="x.js"`},
			"dot":  {".htaccess", "text/csv; charset=utf-16", "text/csv", `filename="htaccess"`},
			"dev":  {"con.csv", "text/csv", "text/csv", `filename="_con.csv"`},
		}
		i := 0
		for name, h := range hostile {
			i++
			op := "hostile-" + string(rune('a'+i))
			id := a.export(e, a.tokens["alice"], op, contract.OutputDescription{Handle: "hostile-" + string(rune('a'+i)), Name: h.name, MediaType: h.media, Size: 6}, "<b>x</b>"[:6])
			a.waitComplete(e, a.tokens["alice"], id)
			got := fetch(server.URL+"/api/addons/artifacts/"+id+"/download", func(r *http.Request) {
				r.Header.Set("Authorization", "Bearer "+a.tokens["alice"])
			})
			Expect(got.code).To(Equal(200), name)
			Expect(got.header.Get("Content-Type")).To(Equal(h.wantType), name)
			Expect(got.header.Get("Content-Disposition")).To(HavePrefix("attachment; "+h.wantName), name)
			Expect(got.header.Get("X-Content-Type-Options")).To(Equal("nosniff"), name)
			Expect(got.header.Get("Content-Disposition")).NotTo(HavePrefix("inline"), name)
		}
	})

	It("answers one 403 for unknown, foreign, incomplete and expired artifacts and requires the feature and a principal", func() {
		a := newArtifactStack(auth.AddonModeNamedUser, true)
		e := a.frontend(a.gateway)
		id := a.export(e, a.tokens["alice"], "export-1", csv, "a,b\n1,2\n")
		a.waitComplete(e, a.tokens["alice"], id)
		unknown := "a" + strings.Repeat("0", 32)
		deny := func(r reply) {
			Expect(r.code).To(Equal(403))
			Expect(r.errType()).To(Equal("addon_error"))
			Expect(r.body["error"].(map[string]any)["message"]).To(Equal("Addon request denied"))
		}
		var bodies []string
		for _, suffix := range []string{"", "/download"} {
			// Bob can read the source resource and still cannot see alice's file.
			foreign := send(e, "GET", "/api/addons/artifacts/"+id+suffix, "", bearerOf(a.tokens["bob"]))
			missing := send(e, "GET", "/api/addons/artifacts/"+unknown+suffix, "", bearerOf(a.tokens["bob"]))
			shape := send(e, "GET", "/api/addons/artifacts/not-an-id"+suffix, "", bearerOf(a.tokens["bob"]))
			traversal := send(e, "GET", "/api/addons/artifacts/..%2F..%2Fmeta"+suffix, "", bearerOf(a.tokens["bob"]))
			for _, r := range []reply{foreign, missing, shape, traversal} {
				deny(r)
				raw, _ := json.Marshal(r.body)
				bodies = append(bodies, string(raw))
			}
			Expect(foreign.body).To(Equal(missing.body))
			Expect(shape.body).To(Equal(missing.body))
			// The other user's session is no different from their key.
			cookie := send(e, "GET", "/api/addons/artifacts/"+id+suffix, "", func(r *http.Request) {
				r.AddCookie(&http.Cookie{Name: "session", Value: a.cookies["bob"]})
				r.Header.Set("Sec-Fetch-Site", "same-origin")
			})
			deny(cookie)
			Expect(cookie.body).To(Equal(missing.body))
			// Unauthenticated callers do not reach the store at all.
			Expect(send(e, "GET", "/api/addons/artifacts/"+id+suffix, "", nil).code).To(Equal(401))
		}
		for _, b := range bodies {
			Expect(b).NotTo(ContainSubstring(id))
		}
		// A request that tries to speak as another principal is not trusted.
		forged := send(e, "GET", "/api/addons/artifacts/"+id+"/download", "", func(r *http.Request) {
			bearerOf(a.tokens["bob"])(r)
			for _, h := range []string{"X-Forwarded-Actor", "X-Addon-Actor", "X-Forwarded-User", "X-Remote-User"} {
				r.Header.Set(h, "alice")
			}
		})
		deny(forged)
		// An expired artifact is gone for its owner too.
		a.advance(time.Hour + time.Minute)
		deny(send(e, "GET", "/api/addons/artifacts/"+id, "", bearerOf(a.tokens["alice"])))
		deny(send(e, "GET", "/api/addons/artifacts/"+id+"/download", "", bearerOf(a.tokens["alice"])))
	})

	It("denies the download of a disabled addon and serves it again after enable", func() {
		a := newArtifactStack(auth.AddonModeNamedUser, true)
		e := a.frontend(a.gateway)
		id := a.export(e, a.tokens["bob"], "export-1", csv, "a,b\n1,2\n")
		a.waitComplete(e, a.tokens["bob"], id)
		a.enable(false)
		r := send(e, "GET", "/api/addons/artifacts/"+id+"/download", "", bearerOf(a.tokens["bob"]))
		Expect(r.code).To(Equal(403))
		a.enable(true)
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/addons/artifacts/"+id+"/download", nil)
		req.Header.Set("Authorization", "Bearer "+a.tokens["bob"])
		e.ServeHTTP(w, req)
		Expect(w.Code).To(Equal(200))
		Expect(w.Body.String()).To(Equal("a,b\n1,2\n"))
		// Losing the source permission denies it, with the same answer.
		Expect(a.policy.Revoke(context.Background(), a.owner, a.key, "bob")).To(Succeed())
		r = send(e, "GET", "/api/addons/artifacts/"+id+"/download", "", bearerOf(a.tokens["bob"]))
		Expect(r.code).To(Equal(403))
		// The same result cannot be produced by a principal without access.
		denied := send(e, "POST", "/api/addons/dispatch", a.exportBody("export-2"), bearerOf(a.tokens["bob"]))
		Expect(denied.code).To(Equal(403))
	})

	It("relays the same bytes, headers and denials through two forwarding frontends", func() {
		a := newArtifactStack(auth.AddonModeNamedUser, true)
		front, back := a.forwardingFrontends(a.gateway)
		owner := a.frontend(a.gateway)
		// The file result is produced through one frontend; the owner holds it.
		id := a.export(front, a.tokens["alice"], "export-1", csv, "a,b\n1,2\n")
		a.waitComplete(back, a.tokens["alice"], id)
		var seen []map[string]string
		for _, h := range []http.Handler{owner, front, back} {
			w := httptest.NewRecorder()
			r := httptest.NewRequest("GET", "/api/addons/artifacts/"+id+"/download", nil)
			bearerOf(a.tokens["alice"])(r)
			h.ServeHTTP(w, r)
			Expect(w.Code).To(Equal(200))
			Expect(w.Body.String()).To(Equal("a,b\n1,2\n"))
			headers := map[string]string{}
			for _, n := range []string{"Content-Type", "Content-Disposition", "Content-Length", "X-Content-Type-Options", "Cache-Control", "Content-Security-Policy"} {
				headers[n] = w.Header().Get(n)
				Expect(headers[n]).NotTo(BeEmpty(), n)
			}
			seen = append(seen, headers)
			// The status lookup is the owner's answer from every frontend.
			st := send(h, "GET", "/api/addons/artifacts/"+id, "", bearerOf(a.tokens["alice"]))
			Expect(st.code).To(Equal(200))
			Expect(st.state()).To(Equal("complete"))
			Expect(st.body["principal"]).To(Equal("alice"))
		}
		Expect(seen[1]).To(Equal(seen[0]))
		Expect(seen[2]).To(Equal(seen[0]))
		// Denials are the same from every frontend and for every cause.
		unknown := "a" + strings.Repeat("7", 32)
		for _, h := range []http.Handler{owner, front, back} {
			for _, suffix := range []string{"", "/download"} {
				foreign := send(h, "GET", "/api/addons/artifacts/"+id+suffix, "", bearerOf(a.tokens["bob"]))
				missing := send(h, "GET", "/api/addons/artifacts/"+unknown+suffix, "", bearerOf(a.tokens["alice"]))
				Expect(foreign.code).To(Equal(403))
				Expect(missing.code).To(Equal(403))
				Expect(foreign.body).To(Equal(missing.body))
			}
		}
		// A disabled addon and a lost permission are denied by the owner the
		// same way whichever frontend received the request.
		a.enable(false)
		for _, h := range []http.Handler{owner, front, back} {
			Expect(send(h, "GET", "/api/addons/artifacts/"+id+"/download", "", bearerOf(a.tokens["alice"])).code).To(Equal(403))
		}
		a.enable(true)
		// An owner that is not serving artifacts answers unavailable or
		// denies; it never serves from a frontend's own disk.
		bare := &gateway.Gateway{Admission: a.gateway.Admission, Resolver: a.gateway.Resolver, Revalidate: a.gateway.Revalidate}
		f2, _ := a.forwardingFrontends(bare)
		Expect(send(f2, "GET", "/api/addons/artifacts/"+id+"/download", "", bearerOf(a.tokens["alice"])).code).To(Equal(403))
		Expect(send(f2, "GET", "/api/addons/artifacts/"+id, "", bearerOf(a.tokens["alice"])).code).To(Equal(403))
	})

	It("keeps a file result of a read denied whole without an artifact store", func() {
		a := newArtifactStack(auth.AddonModeNamedUser, true)
		a.files.set(csv, "a,b\n1,2\n")
		bare := &gateway.Gateway{Admission: a.gateway.Admission, Resolver: a.gateway.Resolver, Runtime: a.gateway.Runtime, Revalidate: a.gateway.Revalidate}
		e := a.frontend(bare)
		r := send(e, "POST", "/api/addons/dispatch", a.exportBody("export-1"), bearerOf(a.tokens["alice"]))
		Expect(r.code).To(Equal(403))
		raw, _ := json.Marshal(r.body)
		Expect(string(raw)).NotTo(ContainSubstring("csv-export"))
		// A read without a file output still works through the same gateway.
		a.files.mu.Lock()
		a.files.outputs = nil
		a.files.mu.Unlock()
		ok := send(e, "POST", "/api/addons/dispatch", a.exportBody("export-2"), bearerOf(a.tokens["alice"]))
		Expect(ok.code).To(Equal(200))
	})

	It("serves the local owner's file only in local-owner mode", func() {
		a := newArtifactStack(auth.AddonModeLocalOwner, false)
		e := a.frontend(a.gateway)
		a.files.set(csv, "a,b\n1,2\n")
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/api/addons/dispatch", bytes.NewBufferString(a.exportBody("export-1")))
		r.Header.Set("Content-Type", "application/json")
		e.ServeHTTP(w, r)
		Expect(w.Code).To(Equal(200), w.Body.String())
		var result contract.Result
		Expect(json.Unmarshal(w.Body.Bytes(), &result)).To(Succeed())
		id := result.Outputs[0].Handle
		Eventually(func() string { return send(e, "GET", "/api/addons/artifacts/"+id, "", nil).state() }, 5*time.Second, 5*time.Millisecond).Should(Equal("complete"))
		dl := httptest.NewRecorder()
		e.ServeHTTP(dl, httptest.NewRequest("GET", "/api/addons/artifacts/"+id+"/download", nil))
		Expect(dl.Code).To(Equal(200))
		Expect(dl.Body.String()).To(Equal("a,b\n1,2\n"))
		Expect(dl.Header().Get("Content-Disposition")).To(HavePrefix("attachment;"))
	})

	It("documents the artifact routes and the artifact schema in the Swagger spec", func() {
		spec, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "swagger", "swagger.json"))
		Expect(err).NotTo(HaveOccurred())
		var doc struct {
			Paths       map[string]map[string]any `json:"paths"`
			Definitions map[string]struct {
				Properties map[string]any `json:"properties"`
			} `json:"definitions"`
		}
		Expect(json.Unmarshal(spec, &doc)).To(Succeed())
		for path, method := range map[string]string{
			"/api/addons/artifacts/{id}":          "get",
			"/api/addons/artifacts/{id}/download": "get",
		} {
			Expect(doc.Paths).To(HaveKey(path))
			Expect(doc.Paths[path]).To(HaveKey(method), path)
		}
		download, _ := json.Marshal(doc.Paths["/api/addons/artifacts/{id}/download"])
		for _, text := range []string{"attachment", "nosniff", "private, no-store", "403", "503"} {
			Expect(string(download)).To(ContainSubstring(text))
		}
		// Every field the artifact view emits is documented.
		b, err := json.Marshal(gateway.Artifact{Sources: []gateway.ArtifactResource{{AddonID: "a", ResourceID: "r"}}, Reason: "x"})
		Expect(err).NotTo(HaveOccurred())
		var emitted map[string]any
		Expect(json.Unmarshal(b, &emitted)).To(Succeed())
		artifact := doc.Definitions["gateway.Artifact"]
		for field := range emitted {
			Expect(artifact.Properties).To(HaveKey(field), field)
		}
		for field := range artifact.Properties {
			Expect(emitted).To(HaveKey(field), field)
		}
		Expect(artifact.Properties).NotTo(HaveKey("handle"))
		Expect(artifact.Properties).NotTo(HaveKey("path"))
		Expect(doc.Definitions).To(HaveKey("gateway.ArtifactResource"))
	})
})
