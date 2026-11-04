// SPDX-License-Identifier: MIT
package application

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/hostops"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// These specs run the owner the way local-ai run does, install a package
// through the real host access service and ask the Authority through a broker.
var _ = Describe("host grants on the owner", func() {
	var (
		ownerData string
		keys      string
		port      string
		sourceURL string
		digests   map[string]string
		actor     = contract.Actor{UserID: auth.AddonLocalOwnerID, CredentialID: auth.AddonLocalOwnerID, Mode: auth.AddonModeLocalOwner}
	)

	pack := func(version string, required, optional []string) (data []byte) {
		manifest, e := os.ReadFile("../../tests/addons/fixtures/packages/manifest.json")
		ExpectWithOffset(1, e).NotTo(HaveOccurred())
		var m map[string]any
		ExpectWithOffset(1, json.Unmarshal(manifest, &m)).To(Succeed())
		m["version"] = version
		m["capabilities"] = map[string]any{"service": true, "hostOperations": map[string]any{"required": required, "optional": optional}}
		manifest, _ = json.Marshal(m)
		var b bytes.Buffer
		tw := tar.NewWriter(&b)
		for name, content := range map[string][]byte{"manifest.json": manifest, "bin/sample": []byte("binary " + version)} {
			ExpectWithOffset(1, tw.WriteHeader(&tar.Header{Name: name, Mode: 0755, Size: int64(len(content))})).To(Succeed())
			_, e = tw.Write(content)
			ExpectWithOffset(1, e).NotTo(HaveOccurred())
		}
		ExpectWithOffset(1, tw.Close()).To(Succeed())
		return b.Bytes()
	}

	BeforeEach(func() {
		requireAddonRuntime()
		keys = filepath.Join(realTempDir(), "keys")
		Expect(gateway.GenerateCarrierKeys(keys, []string{"127.0.0.1"})).To(Succeed())
		ownerData = realTempDir()
		l, e := net.Listen("tcp", "127.0.0.1:0")
		Expect(e).NotTo(HaveOccurred())
		port = strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
		Expect(l.Close()).To(Succeed())

		archives := map[string][]byte{
			"/1.2.3.tar": pack("1.2.3", []string{"workspace.materialize"}, []string{"egress.check"}),
			"/1.3.0.tar": pack("1.3.0", []string{"workspace.materialize", "egress.audit"}, []string{"egress.check"}),
		}
		digests = map[string]string{}
		for path, data := range archives {
			digests[path] = fmt.Sprintf("sha256:%x", sha256.Sum256(data))
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if data, ok := archives[r.URL.Path]; ok {
				_, _ = w.Write(data)
				return
			}
			rel := func(v string) catalog.Release {
				return catalog.Release{ID: "sample", Name: "Sample", Version: v, OS: "linux", Arch: "amd64", Artifact: sourceURL + "/" + v + ".tar", Digest: digests["/"+v+".tar"]}
			}
			_ = catalog.WriteIndex(w, []catalog.Release{rel("1.2.3"), rel("1.3.0")})
		}))
		DeferCleanup(server.Close)
		sourceURL = server.URL
		root := filepath.Join(ownerData, "addons")
		DeferCleanup(func() {
			_ = filepath.Walk(root, func(p string, i os.FileInfo, e error) error {
				if e == nil && i.IsDir() {
					_ = os.Chmod(p, 0700)
				}
				return nil
			})
		})
		Expect(os.MkdirAll(root, 0700)).To(Succeed())
		sources, e := catalog.NewSources(filepath.Join(root, "sources.json"), func(context.Context, contract.Actor) error { return nil }, catalog.CredentialResolver{})
		Expect(e).NotTo(HaveOccurred())
		Expect(sources.Save(context.Background(), actor, catalog.Source{ID: "test", Name: "Test", URL: sourceURL, Enabled: true, AllowLoopbackHTTP: true})).To(Succeed())
	})

	// startOwner builds the owner on the same data directory. Each start
	// stands for one run of the process.
	startOwner := func(opts ...func(*config.ApplicationConfig)) (*Application, func()) {
		transport, e := gateway.LoadCarrierConfig(keys, "owner", "https://127.0.0.1:"+port)
		Expect(e).NotTo(HaveOccurred())
		ctx, cancel := context.WithCancel(context.Background())
		// The suite runs in local-owner mode, where UI grants are locked unless the
		// administrator turns them on knowingly. Most specs test the grants.
		cfg := &config.ApplicationConfig{Context: ctx, DataPath: ownerData, AddonHostUIGrantsWithoutAuth: true}
		for _, o := range opts {
			o(cfg)
		}
		cfg.Addons = config.AddonConfig{
			Enabled:    true,
			InstanceID: transport.InstanceID, OwnerID: gateway.OwnerIdentity, OwnerAddress: "https://127.0.0.1:" + port,
			StorageID: "local", Root: filepath.Join(ownerData, "addons"), Transport: transport, OwnershipMode: auth.AddonModeLocalOwner,
			Host:          contract.HostCompatibility{Host: 1, API: contract.HostAPIVersion, Protocol: 1, SDK: 1, DataFormat: 1, OS: "linux", Arch: "amd64"},
			ListenAddress: "127.0.0.1:" + port,
		}
		a := &Application{applicationConfig: cfg}
		stopped := false
		stop := func() {
			if !stopped {
				stopped = true
				cancel()
				_ = a.Shutdown()
			}
		}
		if e = a.startAddons(); e != nil {
			stop()
			Fail("owner did not start: " + e.Error())
		}
		DeferCleanup(stop)
		return a, stop
	}

	human := func() context.Context { return gateway.WithHumanSurface(context.Background()) }
	call := func(a *Application, c gateway.HostGrantsCall) (map[string]any, error) {
		raw, e := a.AddonGateway().HostGrants(human(), actor, c)
		if e != nil {
			return nil, e
		}
		out := map[string]any{}
		ExpectWithOffset(1, json.Unmarshal(raw, &out)).To(Succeed())
		return out, nil
	}
	body := func(v any) json.RawMessage {
		b, e := json.Marshal(v)
		ExpectWithOffset(1, e).NotTo(HaveOccurred())
		return b
	}
	revision := func(a *Application) uint64 {
		out, e := call(a, gateway.HostGrantsCall{Op: gateway.HostGrantsRead})
		ExpectWithOffset(1, e).NotTo(HaveOccurred())
		return uint64(out["revision"].(float64))
	}
	// install previews a release and installs it with the approval of ops.
	install := func(a *Application, opID, action, version string, ops []string, expected string) error {
		pv, e := call(a, gateway.HostGrantsCall{Op: gateway.HostGrantsPreview, Body: body(gateway.HostGrantsPreviewRequest{SourceID: "test", AddonID: "sample", Version: version})})
		ExpectWithOffset(1, e).NotTo(HaveOccurred())
		req := gateway.HostGrantsInstallRequest{OperationID: opID, Action: action, SourceID: "test", AddonID: "sample", Version: version,
			Approve: gateway.HostGrantsApprove{Digest: pv["digest"].(string), Ops: ops, Revision: uint64(pv["revision"].(float64)), Nonce: pv["nonce"].(string)}}
		if expected != "" {
			req.ExpectedDigest, req.CandidateDigest = expected, pv["digest"].(string)
		}
		_, e = call(a, gateway.HostGrantsCall{Op: gateway.HostGrantsInstall, OperationID: opID, Body: body(req)})
		return e
	}
	// ask makes a host operation call the way the broker serves it, with the
	// Authority of the application.
	ask := func(a *Application, op string) int {
		in := hostops.NewInFlight()
		b := hostops.New(in, func(string, string) bool { return true }, contract.DefaultLimits())
		ExpectWithOffset(1, b.Register(hostops.Spec{Op: op, Scope: "read", Feature: "x", Handler: func(context.Context, hostops.Call) (json.RawMessage, error) {
			return json.RawMessage(`{"ok":true}`), nil
		}})).To(Succeed())
		h := b.HandlerForRuntime("sample", "secret", a.addonAuthority(), 0, "")
		end := in.Begin(contract.Envelope{
			Request:   contract.Request{AddonID: "sample", OperationID: "op1", Generation: 1, Action: "start", Arguments: json.RawMessage(`{}`)},
			Authority: contract.Authority{Actor: contract.Actor{UserID: "u1", CredentialID: "c1", Mode: "named-user"}, Scope: "read", ExpiresAt: time.Now().Add(time.Minute)},
		})
		defer end()
		raw, _ := json.Marshal(contract.HostRequest{OperationID: "op1", Op: op, Arguments: json.RawMessage(`{}`)})
		req := httptest.NewRequest(http.MethodPost, "/host", bytes.NewReader(raw))
		req.Header.Set("X-Addon-Host-Credential", "secret")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	submit := func(a *Application, opID, action, version, expected, candidate string) error {
		_, e := a.addonManager.Submit(context.Background(), actor, addons.LifecycleCommand{OperationID: opID, AddonID: "sample", Action: action, SourceID: "test", Version: version, ExpectedDigest: expected, CandidateDigest: candidate})
		return e
	}

	It("grants at install, revokes and denies without a restart, and keeps the grants across restarts", func() {
		a, stop := startOwner()
		Expect(a.AddonGateway().HostGrantsOwner).NotTo(BeNil())

		// Nothing approved: the install is refused and says what to approve.
		pv, e := call(a, gateway.HostGrantsCall{Op: gateway.HostGrantsPreview, Body: body(gateway.HostGrantsPreviewRequest{SourceID: "test", AddonID: "sample", Version: "1.2.3"})})
		Expect(e).NotTo(HaveOccurred())
		Expect(pv["canInstall"]).To(BeTrue())
		Expect(install(a, "i-none", "install", "1.2.3", nil, "")).To(HaveOccurred())
		Expect(ask(a, "workspace.materialize")).To(Equal(http.StatusForbidden))

		// Approved: the required operation works, the unticked optional one does not.
		Expect(install(a, "i-1", "install", "1.2.3", []string{"workspace.materialize"}, "")).To(Succeed())
		Expect(ask(a, "workspace.materialize")).To(Equal(http.StatusOK))
		Expect(ask(a, "egress.check")).To(Equal(http.StatusForbidden))
		Expect(a.addonAuthority().Snapshot("sample").Operations).To(Equal([]string{"workspace.materialize"}))

		// Revoke: the very next call is refused, with no restart.
		_, e = call(a, gateway.HostGrantsCall{Op: gateway.HostGrantsRevoke, AddonID: "sample", OperationID: "r-1", Body: body(gateway.HostGrantsRevokeRequest{OperationID: "r-1", Revision: revision(a)})})
		Expect(e).NotTo(HaveOccurred())
		Expect(ask(a, "workspace.materialize")).To(Equal(http.StatusForbidden))

		// Edit gives it back; a dry run of a deny names who loses what and writes nothing.
		_, e = call(a, gateway.HostGrantsCall{Op: gateway.HostGrantsEdit, AddonID: "sample", OperationID: "e-1", Body: body(gateway.HostGrantsEditRequest{OperationID: "e-1", Revision: revision(a), Ops: []string{"workspace.materialize", "egress.check"}})})
		Expect(e).NotTo(HaveOccurred())
		Expect(ask(a, "egress.check")).To(Equal(http.StatusOK))
		before := revision(a)
		dry, e := call(a, gateway.HostGrantsCall{Op: gateway.HostGrantsDenyPreview, Body: body(gateway.HostGrantsDenyRequest{Revision: before, Deny: map[string][]string{"sample": {"egress."}}, DryRun: true})})
		Expect(e).NotTo(HaveOccurred())
		Expect(dry["affects"]).NotTo(BeEmpty())
		Expect(revision(a)).To(Equal(before))
		Expect(ask(a, "egress.check")).To(Equal(http.StatusOK))

		// A deny set in the UI beats the grant at once; removing it restores the grant.
		_, e = call(a, gateway.HostGrantsCall{Op: gateway.HostGrantsDeny, OperationID: "d-1", Body: body(gateway.HostGrantsDenyRequest{OperationID: "d-1", Revision: before, Deny: map[string][]string{"sample": {"egress."}}})})
		Expect(e).NotTo(HaveOccurred())
		Expect(ask(a, "egress.check")).To(Equal(http.StatusForbidden))
		Expect(ask(a, "workspace.materialize")).To(Equal(http.StatusOK))
		_, e = call(a, gateway.HostGrantsCall{Op: gateway.HostGrantsDeny, OperationID: "d-2", Body: body(gateway.HostGrantsDenyRequest{OperationID: "d-2", Revision: revision(a), Deny: map[string][]string{}})})
		Expect(e).NotTo(HaveOccurred())
		Expect(ask(a, "egress.check")).To(Equal(http.StatusOK))

		// The audit file records the changes and names no path or secret.
		audit, e := os.ReadFile(filepath.Join(ownerData, "addons", addonHostGrantsAudit))
		Expect(e).NotTo(HaveOccurred())
		Expect(string(audit)).To(ContainSubstring(`"event":"grant"`))
		Expect(string(audit)).To(ContainSubstring(`"event":"revoke"`))
		Expect(string(audit)).NotTo(ContainSubstring(ownerData))

		// A restart reopens the document: the grants are still there.
		stop()
		again, _ := startOwner()
		Expect(ask(again, "workspace.materialize")).To(Equal(http.StatusOK))
		Expect(ask(again, "egress.check")).To(Equal(http.StatusOK))
	})

	It("lets a deny of the deployment beat a grant of the UI", func() {
		a, stop := startOwner()
		Expect(install(a, "i-1", "install", "1.2.3", []string{"workspace.materialize"}, "")).To(Succeed())
		Expect(ask(a, "workspace.materialize")).To(Equal(http.StatusOK))
		stop()
		denied, stopDenied := startOwner(func(c *config.ApplicationConfig) {
			c.AddonHostDeny = map[string][]string{"*": {"workspace."}}
		})
		Expect(ask(denied, "workspace.materialize")).To(Equal(http.StatusForbidden))
		stopDenied()
		// The grant was not lost: without the deny it works again.
		last, _ := startOwner()
		Expect(ask(last, "workspace.materialize")).To(Equal(http.StatusOK))
	})

	It("refuses everything when the document cannot be read", func() {
		Expect(os.MkdirAll(filepath.Join(ownerData, "addons"), 0700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(ownerData, "addons", addonHostGrantsFile), []byte("{not json"), 0600)).To(Succeed())
		a, _ := startOwner(func(c *config.ApplicationConfig) {
			c.AddonHostOps = map[string][]string{"sample": {"workspace."}}
		})
		Expect(ask(a, "workspace.materialize")).To(Equal(http.StatusForbidden), "the deny rules in the document are unknown")
		Expect(a.AddonHostGrantsStore()).To(BeNil())
	})

	It("holds an upgrade that asks for more and keeps the old version running", func() {
		a, _ := startOwner()
		Expect(install(a, "i-1", "install", "1.2.3", []string{"workspace.materialize"}, "")).To(Succeed())
		old := digests["/1.2.3.tar"]
		newer := digests["/1.3.0.tar"]
		// Without an approval the upgrade is refused and nothing changes.
		e := submit(a, "u-0", "upgrade", "1.3.0", old, newer)
		var refusal *addons.HostRequirementsRefusal
		Expect(e).To(HaveOccurred())
		Expect(errors.As(e, &refusal)).To(BeTrue(), e.Error())
		st, e := a.addonManager.Status(context.Background(), "sample")
		Expect(e).NotTo(HaveOccurred())
		Expect(st.Generation).To(Equal(uint64(1)))
		Expect(ask(a, "workspace.materialize")).To(Equal(http.StatusOK), "the old version keeps its grant")
		Expect(ask(a, "egress.audit")).To(Equal(http.StatusForbidden))

		// Approved in the browser: the new operation counts once the upgrade completed.
		Expect(install(a, "u-1", "upgrade", "1.3.0", []string{"egress.audit"}, old)).To(Succeed())
		st, _ = a.addonManager.Status(context.Background(), "sample")
		Expect(st.Generation).To(Equal(uint64(2)))
		Expect(ask(a, "workspace.materialize")).To(Equal(http.StatusOK))
		Expect(ask(a, "egress.audit")).To(Equal(http.StatusOK))
	})

	It("applies the blanket mode a deployment fixes without a dialog, and records it", func() {
		a, _ := startOwner(func(c *config.ApplicationConfig) { c.AddonHostBlanket = "any" })
		Expect(submit(a, "b-1", "install", "1.2.3", "", "")).To(Succeed())
		// Required operations are granted; the optional one needs includeOptional.
		Expect(ask(a, "workspace.materialize")).To(Equal(http.StatusOK))
		Expect(ask(a, "egress.check")).To(Equal(http.StatusForbidden))
		audit, e := os.ReadFile(filepath.Join(ownerData, "addons", addonHostGrantsAudit))
		Expect(e).NotTo(HaveOccurred())
		Expect(string(audit)).To(ContainSubstring(`"event":"blanket-grant"`))
		Expect(string(audit)).To(ContainSubstring(`"blanket":"any@rev0"`))
	})

	It("never copies a blanket mode a deployment fixes into the document", func() {
		a, stop := startOwner(func(c *config.ApplicationConfig) { c.AddonHostBlanket = "any" })
		Expect(submit(a, "b-1", "install", "1.2.3", "", "")).To(Succeed())
		out, e := call(a, gateway.HostGrantsCall{Op: gateway.HostGrantsRead})
		Expect(e).NotTo(HaveOccurred())
		blanket := out["blanket"].(map[string]any)
		Expect(blanket["mode"]).To(Equal("any"))
		Expect(blanket["setBy"]).To(Equal("deployment"))
		doc, e := os.ReadFile(filepath.Join(ownerData, "addons", addonHostGrantsFile))
		Expect(e).NotTo(HaveOccurred())
		Expect(string(doc)).NotTo(ContainSubstring(`"any"`), "the deployment value must not reach the document")

		// Restart without the flag: the switch is what a person set, which is off.
		stop()
		again, stopAgain := startOwner()
		out, e = call(again, gateway.HostGrantsCall{Op: gateway.HostGrantsRead})
		Expect(e).NotTo(HaveOccurred())
		blanket = out["blanket"].(map[string]any)
		Expect(blanket["mode"]).To(Equal("off"))
		Expect(blanket["setBy"]).To(Equal("ui"))
		// An upgrade that needs more is held again, as nobody typed the confirmation.
		e = submit(again, "u-0", "upgrade", "1.3.0", digests["/1.2.3.tar"], digests["/1.3.0.tar"])
		var refusal *addons.HostRequirementsRefusal
		Expect(errors.As(e, &refusal)).To(BeTrue(), fmt.Sprint(e))
		stopAgain()

		// A flag set again takes effect with no stored copy.
		fixed, _ := startOwner(func(c *config.ApplicationConfig) { c.AddonHostBlanket = "any" })
		Expect(submit(fixed, "u-1", "upgrade", "1.3.0", digests["/1.2.3.tar"], digests["/1.3.0.tar"])).To(Succeed())
		Expect(ask(fixed, "egress.audit")).To(Equal(http.StatusOK))
	})

	It("refuses an install and leaves the document unchanged when the audit line cannot be written", func() {
		a, _ := startOwner()
		before := revision(a)
		Expect(a.addonGrants.audit.Close()).To(Succeed())
		Expect(install(a, "i-1", "install", "1.2.3", []string{"workspace.materialize"}, "")).NotTo(Succeed())
		Expect(revision(a)).To(Equal(before))
		Expect(ask(a, "workspace.materialize")).To(Equal(http.StatusForbidden))
		st, e := a.addonManager.Status(context.Background(), "sample")
		Expect(e).NotTo(HaveOccurred())
		Expect(st.Installed).To(BeFalse())
	})

	It("locks the UI grants while sign-in is off, and opens them only when the administrator says so", func() {
		a, stop := startOwner(func(c *config.ApplicationConfig) {
			c.AddonHostUIGrantsWithoutAuth = false
			c.AddonHostOps = map[string][]string{"sample": {"workspace."}}
		})
		out, e := call(a, gateway.HostGrantsCall{Op: gateway.HostGrantsRead})
		Expect(e).NotTo(HaveOccurred())
		Expect(out["locked"]).To(BeTrue())
		Expect(out["lockedReason"]).To(Equal(gateway.LockedNoAuth))
		// Every write is refused with the typed code, and nothing is stored.
		before := revision(a)
		for _, c := range []gateway.HostGrantsCall{
			{Op: gateway.HostGrantsEdit, AddonID: "sample", OperationID: "e-1", Body: body(gateway.HostGrantsEditRequest{OperationID: "e-1", Ops: []string{"egress.check"}})},
			{Op: gateway.HostGrantsRevoke, AddonID: "sample", OperationID: "r-1", Body: body(gateway.HostGrantsRevokeRequest{OperationID: "r-1"})},
			{Op: gateway.HostGrantsDeny, OperationID: "d-1", Body: body(gateway.HostGrantsDenyRequest{OperationID: "d-1", Deny: map[string][]string{"sample": {"egress."}}})},
			{Op: gateway.HostGrantsBlanket, OperationID: "b-1", Body: body(gateway.HostGrantsBlanketRequest{OperationID: "b-1", Mode: "any", Confirm: gateway.BlanketConfirmAny})},
		} {
			_, e = call(a, c)
			var he *gateway.HostGrantsError
			Expect(errors.As(e, &he)).To(BeTrue(), c.Op)
			Expect(he.Type).To(Equal(gateway.HostGrantsLocked), c.Op)
			Expect(he.Message).To(ContainSubstring("Sign-in is off"))
		}
		Expect(revision(a)).To(Equal(before))
		// The preview tells the install dialog, and the policy file still grants.
		pv, e := call(a, gateway.HostGrantsCall{Op: gateway.HostGrantsPreview, Body: body(gateway.HostGrantsPreviewRequest{SourceID: "test", AddonID: "sample", Version: "1.2.3"})})
		Expect(e).NotTo(HaveOccurred())
		Expect(pv["locked"]).To(BeTrue())
		Expect(submit(a, "i-1", "install", "1.2.3", "", "")).To(Succeed())
		Expect(ask(a, "workspace.materialize")).To(Equal(http.StatusOK))
		stop()

		// Turned on knowingly, the same server accepts the write.
		open, _ := startOwner()
		out, e = call(open, gateway.HostGrantsCall{Op: gateway.HostGrantsRead})
		Expect(e).NotTo(HaveOccurred())
		Expect(out["locked"]).To(BeFalse())
		_, e = call(open, gateway.HostGrantsCall{Op: gateway.HostGrantsEdit, AddonID: "sample", OperationID: "e-2", Body: body(gateway.HostGrantsEditRequest{OperationID: "e-2", Revision: revision(open), Ops: []string{"egress.check"}})})
		Expect(e).NotTo(HaveOccurred())
	})

	It("keeps the grants of a split deployment on the owner only", func() {
		a, _ := startOwner()
		Expect(install(a, "i-1", "install", "1.2.3", []string{"workspace.materialize"}, "")).To(Succeed())
		transport, e := gateway.LoadCarrierConfig(keys, "frontend", "https://127.0.0.1:"+port)
		Expect(e).NotTo(HaveOccurred())
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		frontendData := realTempDir()
		f := &Application{applicationConfig: &config.ApplicationConfig{Context: ctx, DataPath: frontendData,
			AddonHostOps: map[string][]string{"sample": {"egress."}}}}
		f.applicationConfig.Addons = config.AddonConfig{Enabled: true, InstanceID: transport.InstanceID, OwnerID: gateway.OwnerIdentity,
			OwnerAddress: "https://127.0.0.1:" + port, StorageID: "local", Transport: transport, OwnershipMode: auth.AddonModeLocalOwner,
			Host: contract.HostCompatibility{Host: 1, API: contract.HostAPIVersion, Protocol: 1, SDK: 1, DataFormat: 1, OS: "linux", Arch: "amd64"}}
		Expect(f.startAddons()).To(Succeed())
		DeferCleanup(func() { _ = f.Shutdown() })
		// The frontend stores nothing and enforces only its own floor, never the grants of the owner.
		Expect(f.AddonHostGrantsStore()).To(BeNil())
		Expect(f.AddonGateway().HostGrantsOwner).To(BeNil())
		Expect(ask(f, "workspace.materialize")).To(Equal(http.StatusForbidden))
		entries, e := os.ReadDir(frontendData)
		Expect(e).NotTo(HaveOccurred())
		Expect(entries).To(BeEmpty())
		// Reading the owner's grants through the frontend works, and writes forward.
		out, e := call(f, gateway.HostGrantsCall{Op: gateway.HostGrantsRead})
		Expect(e).NotTo(HaveOccurred())
		Expect(out["revision"]).NotTo(BeZero())
	})
})
