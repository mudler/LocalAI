// SPDX-License-Identifier: MIT
//go:build auth

package addons

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/mudler/LocalAI/core/http/auth"
	managerpkg "github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/packages"
	"github.com/mudler/LocalAI/core/services/addons/updates"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// updatesStack is the owner stack with a real lifecycle manager that has the
// add-on "notes" installed from the moving channel "development" and enabled,
// and a library whose channel a spec moves.
type updatesStack struct {
	*ownerStack
	manager   *managerpkg.Manager
	sources   *catalog.Sources
	frontends map[string]http.Handler
	mu        sync.Mutex
	archives  map[string][]byte
	releases  []catalog.Release
	clock     *time.Time
	library   *httptest.Server
}

func (s *updatesStack) publish(content string, requires catalog.Requirements) catalog.Release {
	GinkgoHelper()
	manifest := []byte(`{"schemaVersion":1,"id":"notes","version":"1.0.0-dev","executable":"bin/sample","os":"linux","arch":"amd64","host":{"min":1,"max":2},"api":{"min":1,"max":1},"protocol":{"min":1,"max":1},"dataFormat":{"min":1,"max":2},"launchMode":"always","capabilities":{"service":true,"tools":["add"]}}`)
	var b bytes.Buffer
	tw := tar.NewWriter(&b)
	for _, f := range []struct {
		n string
		d []byte
	}{{"manifest.json", manifest}, {"bin/sample", []byte(content)}} {
		Expect(tw.WriteHeader(&tar.Header{Name: f.n, Mode: 0755, Size: int64(len(f.d))})).To(Succeed())
		_, err := tw.Write(f.d)
		Expect(err).NotTo(HaveOccurred())
	}
	Expect(tw.Close()).To(Succeed())
	s.mu.Lock()
	defer s.mu.Unlock()
	name := fmt.Sprintf("/notes-%x.tar", sha256.Sum256([]byte(content)))
	s.archives[name] = b.Bytes()
	r := catalog.Release{AddonID: "notes", Name: "notes", Version: "1.0.0-dev", Channel: "development", Artifact: s.library.URL + name,
		Digest: fmt.Sprintf("sha256:%x", sha256.Sum256(b.Bytes())), OS: "linux", Arch: "amd64", Requires: requires}
	s.releases = []catalog.Release{r}
	return r
}

func newUpdatesStack() *updatesStack {
	s := &updatesStack{ownerStack: newOwnerStack(auth.AddonModeNamedUser, true), archives: map[string][]byte{}}
	for _, u := range []struct {
		id      string
		granted bool
	}{{"carol", true}, {"dave", false}} {
		Expect(s.db.Create(&auth.User{ID: u.id, Role: auth.RoleUser, Status: auth.StatusActive}).Error).To(Succeed())
		token, _, err := auth.CreateAPIKey(s.db, u.id, "agent", auth.RoleUser, "secret", nil)
		Expect(err).NotTo(HaveOccurred())
		s.tokens[u.id] = token
		cookie, err := auth.CreateSession(s.db, u.id, "secret")
		Expect(err).NotTo(HaveOccurred())
		s.cookies[u.id] = cookie
		if u.granted {
			Expect(s.db.Create(&auth.UserPermission{ID: uuid.NewString(), UserID: u.id, Permissions: auth.PermissionMap{auth.FeatureAddons: true}}).Error).To(Succeed())
		}
	}
	s.library = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if r.URL.Path == "/library.yaml" {
			_ = catalog.WriteIndex(w, s.releases)
			return
		}
		if d, ok := s.archives[r.URL.Path]; ok {
			_, _ = w.Write(d)
			return
		}
		http.NotFound(w, r)
	}))
	DeferCleanup(s.library.Close)
	ctx := context.Background()
	root := GinkgoT().TempDir()
	admin := func(ctx context.Context, a contract.Actor) error { return auth.ValidateAddonActor(ctx, s.db, a, true) }
	var err error
	s.sources, err = catalog.NewSources(filepath.Join(root, "sources.json"), admin, appsResolver)
	Expect(err).NotTo(HaveOccurred())
	Expect(s.sources.Save(ctx, s.owner, catalog.Source{ID: "test", Name: "Test", URL: s.library.URL + "/library.yaml", Enabled: true, AllowLoopbackHTTP: true})).To(Succeed())
	s.publish("build-1", catalog.Requirements{})
	_, err = s.sources.Refresh(ctx, "test")
	Expect(err).NotTo(HaveOccurred())
	c := catalog.Catalog{Sources: s.sources, TargetOS: "linux", TargetArch: "amd64"}
	cfg := managerpkg.OwnerConfig{InstanceID: "owner", OwnerID: "owner", OwnerAddress: "https://owner.example", StorageID: "disk", Root: filepath.Join(root, "owner")}
	installer := &packages.Installer{Root: filepath.Join(cfg.Root, "packages"), Catalog: c, Host: contract.HostCompatibility{Host: 1, API: 1, Protocol: 1, SDK: 1, DataFormat: 1, OS: "linux", Arch: "amd64"}}
	DeferCleanup(func() {
		_ = filepath.Walk(root, func(p string, info os.FileInfo, e error) error {
			if e == nil && info.IsDir() {
				_ = os.Chmod(p, 0700)
			}
			return nil
		})
	})
	s.manager, err = managerpkg.NewManager(ctx, cfg, c, installer, admin)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { Expect(s.manager.Close()).To(Succeed()) })
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	s.clock = &now
	s.manager.EnableUpdates(updates.Config{Now: func() time.Time { return now }, MinRefresh: time.Minute})
	_, err = s.manager.Submit(ctx, s.owner, managerpkg.LifecycleCommand{OperationID: "install-notes", AddonID: "notes", Action: "install", SourceID: "test", Version: "development"})
	Expect(err).NotTo(HaveOccurred())
	_, err = s.manager.Submit(ctx, s.owner, managerpkg.LifecycleCommand{OperationID: "enable-notes", AddonID: "notes", Action: "enable", Generation: 1})
	Expect(err).NotTo(HaveOccurred())
	s.gateway.Manager = s.manager
	a, b := s.forwardingFrontends(s.gateway)
	s.frontends = map[string]http.Handler{"owner": s.frontend(s.gateway), "front-a": a, "front-b": b}
	return s
}

func (s *updatesStack) each(check func(name string, e http.Handler)) {
	for _, name := range []string{"owner", "front-a", "front-b"} {
		check(name, s.frontends[name])
	}
}

func (s *updatesStack) advance(d time.Duration) { *s.clock = s.clock.Add(d) }

// installedDigest is the digest of the package that runs.
func (s *updatesStack) installedDigest() string {
	st, err := s.manager.Status(context.Background(), "notes")
	Expect(err).NotTo(HaveOccurred())
	return st.Generations[len(st.Generations)-1].Receipt.Digest
}

func (s *updatesStack) generation() uint64 {
	st, err := s.manager.Status(context.Background(), "notes")
	Expect(err).NotTo(HaveOccurred())
	return st.Generation
}

var _ = Describe("update routes", Label("updates"), func() {
	var s *updatesStack
	BeforeEach(func() { s = newUpdatesStack() })
	checkBody := func(op string) string { return fmt.Sprintf(`{"operationId":%q}`, op) }
	upgradeBody := func(op, expected, candidate string) string {
		return fmt.Sprintf(`{"operationId":%q,"expectedDigest":%q,"candidateDigest":%q}`, op, expected, candidate)
	}
	alice := func(op string) func(*http.Request) { return humanTab(s.cookies["alice"], op) }

	It("gives every caller with the feature the same status from every frontend, redacted for non-administrators", func() {
		s.each(func(name string, e http.Handler) {
			Expect(send(e, "GET", "/api/addons/notes/update", "", nil).code).To(Equal(401), name)
			Expect(send(e, "GET", "/api/addons/notes/update", "", bearerOf(s.tokens["dave"])).code).To(Equal(403), name+" without the feature")
			Expect(send(e, "GET", "/api/addons/nothing/update", "", bearerOf(s.tokens["carol"])).code).To(Equal(403), name+" unknown add-on")
			reader := send(e, "GET", "/api/addons/notes/update", "", bearerOf(s.tokens["carol"]))
			Expect(reader.code).To(Equal(200), name)
			Expect(reader.body["state"]).To(Equal("up-to-date"))
			Expect(reader.body["installedVersion"]).To(Equal("1.0.0-dev"))
			for _, hidden := range []string{"installedDigest", "candidateDigest", "sourceId", "channel", "selection"} {
				Expect(reader.body).NotTo(HaveKey(hidden), name+" non-administrator sees "+hidden)
			}
			admin := send(e, "GET", "/api/addons/notes/update", "", bearerOf(s.tokens["alice"]))
			Expect(admin.code).To(Equal(200), name)
			Expect(admin.body).To(HaveKeyWithValue("channel", "development"))
			Expect(admin.body).To(HaveKeyWithValue("sourceId", "test"))
			Expect(admin.body["installedDigest"]).To(Equal(s.installedDigest()))
		})
	})

	It("checks only for an administrator's own browser session, and applies nothing", func() {
		before := s.installedDigest()
		s.publish("build-2", catalog.Requirements{})
		s.each(func(name string, e http.Handler) {
			op := "check-" + name
			path := "/api/addons/notes/update/check"
			Expect(send(e, "POST", path, checkBody(op), nil).code).To(Equal(401), name)
			Expect(send(e, "POST", path, checkBody(op), humanTab(s.cookies["carol"], op)).code).To(Equal(403), name+" non-administrator session")
			// An administrator's key is not the administrator's own session.
			Expect(send(e, "POST", path, checkBody(op), bearerOf(s.tokens["alice"])).code).To(Equal(403), name+" key")
			Expect(send(e, "POST", path, checkBody(op), without(alice(op), ConfirmHeader)).code).To(Equal(403), name+" no confirmation")
			Expect(send(e, "POST", path, checkBody(op), with(alice(op), "Sec-Fetch-Site", "cross-site")).code).To(BeElementOf(400, 403), name)
			Expect(send(e, "POST", path, `{}`, alice(op)).code).To(Equal(400), name+" no operation ID")
			Expect(send(e, "POST", path, `{"operationId":"x","extra":1}`, alice("x")).code).To(Equal(400), name+" unknown field")
		})
		Expect(s.installedDigest()).To(Equal(before))
		got := send(s.frontends["front-a"], "POST", "/api/addons/notes/update/check", checkBody("c1"), alice("c1"))
		Expect(got.code).To(Equal(200))
		Expect(got.body["state"]).To(Equal("update-available"))
		Expect(got.body["available"]).To(BeTrue())
		Expect(got.body["candidateDigest"]).NotTo(BeEmpty())
		// A status read after the check answers the same, from the owner too.
		again := send(s.frontends["owner"], "GET", "/api/addons/notes/update", "", bearerOf(s.tokens["alice"]))
		Expect(again.body["state"]).To(Equal("update-available"))
		// Nothing was applied by checking.
		Expect(s.installedDigest()).To(Equal(before))
		Expect(s.generation()).To(Equal(uint64(1)))
		// An explicit refresh within the interval is throttled, not run again.
		second := send(s.frontends["front-b"], "POST", "/api/addons/notes/update/check", checkBody("c2"), alice("c2"))
		Expect(second.code).To(Equal(200))
		Expect(second.body["throttled"]).To(BeTrue())
	})

	It("upgrades only to the checked candidate, with the same typed refusals from every frontend, and never twice", func() {
		before := s.installedDigest()
		next := s.publish("build-2", catalog.Requirements{})
		Expect(send(s.frontends["owner"], "POST", "/api/addons/notes/update/check", checkBody("c"), alice("c")).code).To(Equal(200))
		path := "/api/addons/notes/upgrade"
		s.each(func(name string, e http.Handler) {
			op := "up-" + name
			Expect(send(e, "POST", path, upgradeBody(op, before, next.Digest), nil).code).To(Equal(401), name)
			Expect(send(e, "POST", path, upgradeBody(op, before, next.Digest), humanTab(s.cookies["carol"], op)).code).To(Equal(403), name+" non-administrator")
			Expect(send(e, "POST", path, upgradeBody(op, before, next.Digest), bearerOf(s.tokens["alice"])).code).To(Equal(403), name+" key")
			Expect(send(e, "POST", path, `{"operationId":"x"}`, alice("x")).code).To(Equal(409), name+" without the pins")
			stale := send(e, "POST", path, upgradeBody(op, "sha256:"+fmt.Sprintf("%064x", 1), next.Digest), alice(op))
			Expect(stale.code).To(Equal(409), name)
			Expect(stale.errType()).To(Equal("stale_installed"), name)
			changed := send(e, "POST", path, upgradeBody(op, before, "sha256:"+fmt.Sprintf("%064x", 2)), alice(op))
			Expect(changed.code).To(Equal(409), name)
			Expect(changed.errType()).To(Equal("candidate_changed"), name)
			_, err := s.manager.OperationStatus(context.Background(), s.owner, op)
			Expect(err).To(MatchError(managerpkg.ErrOperationNotFound), "a refused request records nothing")
		})
		Expect(s.generation()).To(Equal(uint64(1)))

		done := send(s.frontends["front-a"], "POST", path, upgradeBody("up-1", before, next.Digest), alice("up-1"))
		Expect(done.code).To(Equal(200), fmt.Sprint(done.body))
		Expect(done.body["payload"]).To(HaveKeyWithValue("Phase", "completed"))
		Expect(s.generation()).To(Equal(uint64(2)))
		Expect(s.installedDigest()).To(Equal(next.Digest))
		// The same operation, from another frontend, replays; it runs nowhere again.
		replay := send(s.frontends["front-b"], "POST", path, upgradeBody("up-1", before, next.Digest), alice("up-1"))
		Expect(replay.code).To(Equal(200))
		Expect(replay.body["payload"]).To(Equal(done.body["payload"]))
		// Another operation ID for the same transition is a conflict.
		other := send(s.frontends["owner"], "POST", path, upgradeBody("up-2", before, next.Digest), alice("up-2"))
		Expect(other.code).To(Equal(409))
		Expect(other.errType()).To(Equal("upgrade_conflict"))
		Expect(s.generation()).To(Equal(uint64(2)))
		// The status of what runs now is up to date.
		now := send(s.frontends["front-a"], "GET", "/api/addons/notes/update", "", bearerOf(s.tokens["carol"]))
		Expect(now.body["state"]).To(Equal("up-to-date"))
	})

	It("runs two concurrent upgrades from two frontends once: the same ID shares the answer, another ID is a conflict", func() {
		before := s.installedDigest()
		next := s.publish("build-2", catalog.Requirements{})
		Expect(send(s.frontends["owner"], "POST", "/api/addons/notes/update/check", checkBody("c"), alice("c")).code).To(Equal(200))
		path := "/api/addons/notes/upgrade"
		race := func(ids [2]string, expected, candidate string) [2]reply {
			var out [2]reply
			var wg sync.WaitGroup
			for i, name := range []string{"front-a", "front-b"} {
				wg.Add(1)
				go func() {
					defer wg.Done()
					defer GinkgoRecover()
					out[i] = send(s.frontends[name], "POST", path, upgradeBody(ids[i], expected, candidate), alice(ids[i]))
				}()
			}
			wg.Wait()
			return out
		}
		same := race([2]string{"same", "same"}, before, next.Digest)
		Expect(same[0].code).To(Equal(200))
		Expect(same[1].code).To(Equal(200))
		Expect(same[0].body["payload"]).To(Equal(same[1].body["payload"]))
		Expect(s.generation()).To(Equal(uint64(2)))

		// A new candidate, and two different operation IDs for it.
		current := s.installedDigest()
		third := s.publish("build-3", catalog.Requirements{})
		s.advance(2 * time.Minute)
		Expect(send(s.frontends["owner"], "POST", "/api/addons/notes/update/check", checkBody("c4"), alice("c4")).code).To(Equal(200))
		differ := race([2]string{"first", "second"}, current, third.Digest)
		codes := []int{differ[0].code, differ[1].code}
		Expect(codes).To(ConsistOf(200, 409))
		for _, r := range differ {
			if r.code == 409 {
				Expect(r.errType()).To(Equal("upgrade_conflict"))
			}
		}
		Expect(s.generation()).To(Equal(uint64(3)))
	})

	It("refuses an incompatible candidate and a library that cannot be read, with a typed answer", func() {
		before := s.installedDigest()
		next := s.publish("build-2", catalog.Requirements{SDK: contract.VersionRange{Min: 4, Max: 4}})
		got := send(s.frontends["owner"], "POST", "/api/addons/notes/update/check", checkBody("c"), alice("c"))
		Expect(got.body["state"]).To(Equal("incompatible"))
		Expect(got.body["reasons"]).To(ConsistOf("sdk-version"))
		Expect(got.body["available"]).To(Equal(false))
		s.each(func(name string, e http.Handler) {
			r := send(e, "POST", "/api/addons/notes/upgrade", upgradeBody("inc-"+name, before, next.Digest), alice("inc-"+name))
			Expect(r.code).To(Equal(409), name)
			Expect(r.errType()).To(Equal("incompatible"), name)
		})
		s.library.Close()
		s.advance(time.Hour)
		failed := send(s.frontends["front-b"], "POST", "/api/addons/notes/update/check", checkBody("c2"), alice("c2"))
		Expect(failed.code).To(Equal(200))
		Expect(failed.body["state"]).To(Equal("check-failed"))
		Expect(failed.body["error"]).To(Equal("refresh-failed"))
		Expect(failed.body).To(HaveKey("lastSuccess"))
		Expect(failed.body["available"]).NotTo(BeTrue())
		Expect(s.generation()).To(Equal(uint64(1)))
	})

	It("reports a lost upgrade as an unknown outcome with its original operation, and a lost status read or check as plainly unavailable", func() {
		certs, roots := certificates()
		pins := map[string][32]byte{}
		keys := map[string]ed25519.PublicKey{}
		private := map[string]ed25519.PrivateKey{}
		for _, id := range []string{"owner", "front-a"} {
			pins[id] = sha256.Sum256(certs[id].Certificate[0])
			pub, priv, e := ed25519.GenerateKey(rand.Reader)
			Expect(e).NotTo(HaveOccurred())
			keys[id], private[id] = pub, priv
		}
		owner := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/addons/audience" {
				_ = json.NewEncoder(w).Encode(map[string]string{"audience": strings.Repeat("a", 64)})
				return
			}
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		}))
		carrier := func(id string) *gateway.Carrier {
			c, e := gateway.NewCarrier(gateway.CarrierConfig{InstanceID: id, OwnerID: "owner", OwnerAddress: "https://" + owner.Listener.Addr().String(), Certificate: certs[id], Roots: roots, Pins: pins, SigningKey: private[id], VerificationKeys: keys})
			Expect(e).NotTo(HaveOccurred())
			return c
		}
		owner.TLS = carrier("owner").ServerTLS()
		owner.StartTLS()
		DeferCleanup(owner.Close)
		front := s.frontend(&gateway.Gateway{Forward: carrier("front-a"), Revalidate: auth.AddonRevalidate(s.db, auth.AddonModeNamedUser)})
		digest := "sha256:" + fmt.Sprintf("%064x", 3)
		r := send(front, "POST", "/api/addons/notes/upgrade", upgradeBody("lost-up", digest, digest), alice("lost-up"))
		Expect(r.code).To(Equal(503))
		Expect(r.errType()).To(Equal("outcome_unknown"))
		Expect(r.errOperation()).To(Equal("lost-up"))
		for _, call := range []reply{
			send(front, "GET", "/api/addons/notes/update", "", bearerOf(s.tokens["carol"])),
			send(front, "POST", "/api/addons/notes/update/check", checkBody("lost-check"), alice("lost-check")),
		} {
			Expect(call.code).To(Equal(503))
			Expect(call.errType()).To(Equal("addon_error"))
			Expect(call.errOperation()).To(BeEmpty())
		}
	})

	It("keeps the update actions out of the tools and names every field of the status in the Swagger spec", func() {
		for _, a := range []string{gateway.ActionUpdateStatus, gateway.ActionUpdateCheck, gateway.ActionUpdateUpgrade} {
			Expect(gateway.UpdateAction(a)).To(BeTrue())
		}
		spec, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "swagger", "swagger.json"))
		Expect(err).NotTo(HaveOccurred())
		var doc struct {
			Paths       map[string]map[string]any `json:"paths"`
			Definitions map[string]struct {
				Properties map[string]any `json:"properties"`
			} `json:"definitions"`
		}
		Expect(json.Unmarshal(spec, &doc)).To(Succeed())
		for path, method := range map[string]string{"/api/addons/{id}/update": "get", "/api/addons/{id}/update/check": "post", "/api/addons/{id}/upgrade": "post"} {
			Expect(doc.Paths).To(HaveKey(path))
			Expect(doc.Paths[path]).To(HaveKey(method), path)
			text, _ := json.Marshal(doc.Paths[path])
			Expect(string(text)).To(ContainSubstring("Addons"), path)
		}
		upgrade, _ := json.Marshal(doc.Paths["/api/addons/{id}/upgrade"])
		for _, text := range []string{"stale_installed", "candidate_changed", "upgrade_conflict", "integrity_error", "outcome_unknown", "409"} {
			Expect(string(upgrade)).To(ContainSubstring(text))
		}
		emitted := func(v any) map[string]any {
			b, err := json.Marshal(v)
			Expect(err).NotTo(HaveOccurred())
			var m map[string]any
			Expect(json.Unmarshal(b, &m)).To(Succeed())
			return m
		}
		full := updates.Status{AddonID: "a", State: updates.UpdateAvailable, Available: true, Generation: 1, InstalledVersion: "1", InstalledDigest: "d", CandidateVersion: "2", CandidateDigest: "e",
			SourceID: "s", Channel: "c", Selection: "x", CheckedAt: time.Now(), LastSuccess: time.Now(), Stale: true, Throttled: true, Reasons: []string{"r"}, Error: "e"}
		for name, v := range map[string]any{"updates.Status": full, "addons.UpgradeRequest": UpgradeRequest{OperationID: "o", ExpectedDigest: "a", CandidateDigest: "b", Generation: 1}, "addons.UpdateCheckRequest": UpdateCheckRequest{OperationID: "o"}} {
			def, ok := doc.Definitions[name]
			Expect(ok).To(BeTrue(), name)
			shape := emitted(v)
			for field := range shape {
				Expect(def.Properties).To(HaveKey(field), name+"."+field)
			}
			for field := range def.Properties {
				Expect(shape).To(HaveKey(field), name+" documents "+field+" that is never emitted")
			}
		}
	})
})
