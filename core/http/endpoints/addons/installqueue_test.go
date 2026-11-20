// SPDX-License-Identifier: MIT
package addons

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	managerpkg "github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/hostgrants"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// queueManager is a manager with the install queue. Its runs wait for
// release, so a test can look at a running and a queued install.
type queueManager struct {
	hgManager
	mu       sync.Mutex
	accepted []gateway.HostApproval
	cmds     []managerpkg.LifecycleCommand
	ran      []string
	dropped  []string
	release  chan struct{}
}

func (m *queueManager) PreviewHostAccess(_ context.Context, _, addonID, _ string) (gateway.ReleaseAccess, error) {
	return gateway.ReleaseAccess{AddonID: addonID, Title: addonID, Version: "1.0.0", Digest: "sha256:" + addonID, Required: []string{"workspace.materialize"}, FromIndex: true}, nil
}

func (m *queueManager) AcceptWithApproval(_ context.Context, _ contract.Actor, cmd managerpkg.LifecycleCommand, ap gateway.HostApproval) (managerpkg.OperationStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.accepted = append(m.accepted, ap)
	m.cmds = append(m.cmds, cmd)
	return managerpkg.OperationStatus{OperationID: cmd.OperationID, AddonID: cmd.AddonID, Phase: "accepted"}, nil
}

func (m *queueManager) RunAccepted(ctx context.Context, _ contract.Actor, id string) (managerpkg.OperationStatus, error) {
	m.mu.Lock()
	m.ran = append(m.ran, id)
	m.mu.Unlock()
	select {
	case <-m.release:
		return managerpkg.OperationStatus{OperationID: id, Phase: "completed"}, nil
	case <-ctx.Done():
		return managerpkg.OperationStatus{}, ctx.Err()
	}
}

func (m *queueManager) DropAccepted(_ context.Context, _ contract.Actor, id string) (managerpkg.OperationStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dropped = append(m.dropped, id)
	return managerpkg.OperationStatus{OperationID: id, Phase: "failed", FailureCode: managerpkg.FailureCancelled}, nil
}

func (m *queueManager) snapshot() (ran, dropped []string, accepted []gateway.HostApproval) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.ran...), append([]string(nil), m.dropped...), append([]gateway.HostApproval(nil), m.accepted...)
}

var _ = Describe("the add-on install queue route", func() {
	const host = "192.0.2.10:8080"
	var (
		e   *echo.Echo
		mgr *queueManager
		gs  *galleryop.GalleryService
		oc  *galleryop.OpCache
	)
	BeforeEach(func() {
		store, err := hostgrants.Open(filepath.Join(GinkgoT().TempDir(), "hostgrants.json"))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(store.Close)
		mgr = &queueManager{release: make(chan struct{})}
		svc, err := gateway.NewHostGrantsOwner(gateway.HostGrantsConfig{Store: store, Manager: mgr,
			Admin: func(context.Context, contract.Actor) error { return nil }})
		Expect(err).NotTo(HaveOccurred())
		gs = galleryop.NewGalleryService(&config.ApplicationConfig{}, nil)
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		Expect(gs.Start(ctx, nil, nil)).To(Succeed())
		oc = galleryop.NewOpCache(gs)
		gw := &gateway.Gateway{HostGrantsOwner: svc, Revalidate: func(context.Context, contract.Actor) error { return nil }}
		mode := WithOwnership(auth.AddonModeLocalOwner)
		e = echo.New()
		g := e.Group("/api/addons", BrowserProtection())
		g.POST("/host-grants/preview", PreviewHostGrants(gw, nil, mode))
		g.POST("/host-grants/queue", QueueWithHostGrants(gw, nil, mode, WithOperationQueue(gs, oc)))
	})

	post := func(path, body, confirm string, human bool) (int, map[string]any) {
		r := httptest.NewRequest("POST", path, strings.NewReader(body))
		r.Host = host
		r.Header.Set("Content-Type", "application/json")
		if human {
			r.Header.Set("Sec-Fetch-Site", "same-origin")
			r.Header.Set("Sec-Fetch-Dest", "empty")
			r.Header.Set("Sec-Fetch-Mode", "cors")
		}
		if confirm != "" {
			r.Header.Set(ConfirmHeader, confirm)
		}
		w := httptest.NewRecorder()
		e.ServeHTTP(w, r)
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	preview := func(id string) map[string]any {
		code, out := post("/api/addons/host-grants/preview", `{"sourceId":"official","addonId":"`+id+`","version":"1.0.0"}`, "", true)
		Expect(code).To(Equal(http.StatusOK))
		Expect(out["fromIndex"]).To(BeTrue())
		return out
	}
	item := func(p map[string]any) string {
		b, _ := json.Marshal(map[string]any{"sourceId": "official", "addonId": p["addonId"], "version": "1.0.0",
			"approve": map[string]any{"digest": p["digest"], "ops": []string{"workspace.materialize"}, "revision": p["revision"], "nonce": p["nonce"]}})
		return string(b)
	}

	It("refuses a queue request that is not the administrator's own browser session", func() {
		p := preview("calendar")
		code, out := post("/api/addons/host-grants/queue", `{"items":[`+item(p)+`]}`, "queue", false)
		Expect(code).To(Equal(http.StatusForbidden))
		Expect(out["error"].(map[string]any)["type"]).To(Equal(gateway.HostGrantsNotHumanSurface))
		_, _, accepted := mgr.snapshot()
		Expect(accepted).To(BeEmpty())
	})

	It("queues several approved installs, runs them one at a time and lets a queued one be cancelled", func() {
		a, b := preview("calendar"), preview("notes")
		code, out := post("/api/addons/host-grants/queue", `{"items":[`+item(a)+`,`+item(b)+`]}`, "queue", true)
		Expect(code).To(Equal(http.StatusOK))
		items := out["items"].([]any)
		Expect(items).To(HaveLen(2))
		first, second := items[0].(map[string]any), items[1].(map[string]any)
		Expect(first["jobId"]).NotTo(BeEmpty())
		Expect(second["jobId"]).NotTo(BeEmpty())

		// The owner kept what each preview showed with its approval.
		_, _, accepted := mgr.snapshot()
		Expect(accepted).To(HaveLen(2))
		Expect(accepted[0].FromIndex).To(BeTrue())
		Expect(accepted[0].Reviewed).To(Equal([]string{"workspace.materialize"}))
		Expect(accepted[0].Revision).To(BeZero())

		// Both are rows of /api/operations, named by their add-on.
		Expect(oc.Get(galleryop.AddonOpKey("calendar"))).To(Equal(first["jobId"]))
		Expect(oc.Get(galleryop.AddonOpKey("notes"))).To(Equal(second["jobId"]))

		Eventually(func() []string { ran, _, _ := mgr.snapshot(); return ran }).Should(Equal([]string{first["operationId"].(string)}))
		Expect(gs.GetStatus(second["jobId"].(string)).IsQueued()).To(BeTrue())
		Expect(gs.CancelOperation(second["jobId"].(string))).To(Succeed())
		Eventually(func() []string { _, dropped, _ := mgr.snapshot(); return dropped }).Should(Equal([]string{second["operationId"].(string)}))

		close(mgr.release)
		Eventually(func() bool {
			st := gs.GetStatus(first["jobId"].(string))
			return st != nil && st.Processed && st.Error == nil
		}).Should(BeTrue())
		ran, _, _ := mgr.snapshot()
		Expect(ran).To(HaveLen(1), "the cancelled install never ran")
	})

	It("answers a refused item without stopping the others", func() {
		a := preview("calendar")
		bad := `{"sourceId":"official","addonId":"notes","version":"1.0.0","approve":{"digest":"sha256:notes","ops":[],"revision":0,"nonce":"not-a-nonce"}}`
		code, out := post("/api/addons/host-grants/queue", `{"items":[`+bad+`,`+item(a)+`]}`, "queue", true)
		Expect(code).To(Equal(http.StatusOK))
		items := out["items"].([]any)
		Expect(items[0].(map[string]any)["error"].(map[string]any)["type"]).To(Equal(gateway.HostGrantsApprovalStale))
		Expect(items[1].(map[string]any)["jobId"]).NotTo(BeEmpty())
	})

	It("turns an approval the package outgrew into an install that needs a new review", func() {
		err := runError(&gateway.HostGrantsError{Type: gateway.HostGrantsApprovalStale, Status: 409, Message: "stale"})
		var attention *galleryop.AddonAttentionError
		Expect(err).To(BeAssignableToTypeOf(attention))
		Expect(err.Error()).To(HavePrefix("the add-on needs your approval again"))
	})

	It("reports the download in bytes and percent", func() {
		st := installStatus(managerpkg.InstallProgress{Phase: managerpkg.InstallPhaseDownloading, Bytes: 2_500_000, Total: 10_000_000, Cancellable: true})
		Expect(st.Progress).To(BeNumerically("==", 25))
		Expect(st.DownloadedFileSize).To(Equal("2.5 MB"))
		Expect(st.TotalFileSize).To(Equal("10.0 MB"))
		Expect(st.Cancellable).To(BeTrue())
	})
})
