// SPDX-License-Identifier: MIT
package application

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
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
	"github.com/mudler/LocalAI/core/library"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/hostops"
	"github.com/mudler/LocalAI/core/services/addons/modelsprobe"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The owner and the frontends are separate Application values with separate
// data paths. They share only the carrier key set and the owner address, as two
// processes of a split deployment would.
var _ = Describe("split owner and frontends", func() {
	var (
		keys         string
		ownerData    string
		ownerAddress string
		ownerPort    string
		sourceURL    string
		stops        map[*Application]func()
		nextModels   string
		actor        = contract.Actor{UserID: auth.AddonLocalOwnerID, CredentialID: auth.AddonLocalOwnerID, Mode: auth.AddonModeLocalOwner}
	)

	BeforeEach(func() {
		requireAddonRuntime()
		stops = map[*Application]func(){}
		nextModels = ""
		keys = filepath.Join(realTempDir(), "keys")
		Expect(gateway.GenerateCarrierKeys(keys, []string{"127.0.0.1"})).To(Succeed())
		ownerData = realTempDir()
		// The port is chosen once and reused, like a stable service address.
		l, e := net.Listen("tcp", "127.0.0.1:0")
		Expect(e).NotTo(HaveOccurred())
		ownerPort = strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
		Expect(l.Close()).To(Succeed())
		ownerAddress = "https://127.0.0.1:" + ownerPort

		manifest, e := os.ReadFile("../../tests/addons/fixtures/packages/manifest.json")
		Expect(e).NotTo(HaveOccurred())
		var b bytes.Buffer
		tw := tar.NewWriter(&b)
		for name, data := range map[string][]byte{"manifest.json": manifest, "bin/sample": []byte("binary")} {
			Expect(tw.WriteHeader(&tar.Header{Name: name, Mode: 0755, Size: int64(len(data))})).To(Succeed())
			_, e = tw.Write(data)
			Expect(e).NotTo(HaveOccurred())
		}
		Expect(tw.Close()).To(Succeed())
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/package.tar" {
				_, _ = w.Write(b.Bytes())
				return
			}
			_ = catalog.WriteIndex(w, []catalog.Release{{ID: "sample", Name: "Sample", Version: "1.2.3", OS: "linux", Arch: "amd64", Artifact: sourceURL + "/package.tar", Digest: fmt.Sprintf("sha256:%x", sha256.Sum256(b.Bytes()))}})
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

	// start builds one application the way local-ai run does for a role and
	// starts its add-on side.
	start := func(role, dataPath string) (*Application, error) {
		transport, e := gateway.LoadCarrierConfig(keys, role, ownerAddress)
		Expect(e).NotTo(HaveOccurred())
		ctx, cancel := context.WithCancel(context.Background())
		a := &Application{applicationConfig: &config.ApplicationConfig{Context: ctx, DataPath: dataPath}}
		if nextModels != "" {
			a.applicationConfig.SystemState = &system.SystemState{Model: system.Model{ModelsPath: nextModels}}
		}
		c := config.AddonConfig{
			Enabled:    true,
			InstanceID: transport.InstanceID, OwnerID: gateway.OwnerIdentity, OwnerAddress: ownerAddress,
			StorageID: "local", Root: filepath.Join(dataPath, "addons"), Transport: transport, OwnershipMode: auth.AddonModeLocalOwner,
			Host: contract.HostCompatibility{Host: 1, API: 1, Protocol: 1, SDK: 1, DataFormat: 1, OS: "linux", Arch: "amd64"},
		}
		if role == "frontend" {
			c.Root = ""
		}
		if role == "owner" {
			c.ListenAddress = "127.0.0.1:" + ownerPort
		}
		a.applicationConfig.Addons = c
		e = a.startAddons()
		stop := func() {
			cancel()
			_ = a.Shutdown()
		}
		if e != nil {
			stop()
			return nil, e
		}
		stops[a] = stop
		DeferCleanup(stop)
		return a, nil
	}
	startOK := func(role, dataPath string) *Application {
		a, e := start(role, dataPath)
		ExpectWithOffset(1, e).NotTo(HaveOccurred())
		return a
	}
	installed := func(a *Application) string {
		out, e := a.AddonGateway().Dispatch(context.Background(), actor, contract.Request{OperationID: "list", Action: "addons-installed", Arguments: []byte(`{}`)})
		ExpectWithOffset(1, e).NotTo(HaveOccurred())
		return string(out.Payload)
	}
	install := func(a *Application) {
		_, e := a.AddonGateway().Dispatch(context.Background(), actor, contract.Request{OperationID: "install", AddonID: "sample", Action: "install", Arguments: []byte(`{"sourceId":"test","version":"1.2.3"}`)})
		ExpectWithOffset(1, e).NotTo(HaveOccurred())
	}

	It("lets a frontend list apps and read add-on status through the owner", func() {
		startOK("owner", ownerData)
		frontendData := realTempDir()
		frontend := startOK("frontend", frontendData)
		install(frontend)
		_, e := frontend.AddonGateway().Apps(context.Background(), actor)
		Expect(e).NotTo(HaveOccurred())
		Expect(installed(frontend)).To(ContainSubstring("sample"))
		// The frontend wrote nothing: the state is on the owner.
		entries, e := os.ReadDir(frontendData)
		Expect(e).NotTo(HaveOccurred())
		Expect(entries).To(BeEmpty())
		Expect(filepath.Join(ownerData, "addons", "journal.json")).To(BeARegularFile())
	})

	It("serves two frontends that share one key set", func() {
		startOK("owner", ownerData)
		first := startOK("frontend", realTempDir())
		second := startOK("frontend", realTempDir())
		install(first)
		Expect(installed(first)).To(ContainSubstring("sample"))
		Expect(installed(second)).To(ContainSubstring("sample"))
	})

	It("starts a frontend while the owner is down and answers unavailable", func() {
		frontend := startOK("frontend", realTempDir())
		_, e := frontend.AddonGateway().Apps(context.Background(), actor)
		Expect(e).To(MatchError(gateway.ErrUnavailable))
		Expect(gateway.StatusFor(e)).To(Equal(http.StatusServiceUnavailable))
		_, e = frontend.AddonGateway().Dispatch(context.Background(), actor, contract.Request{OperationID: "list", Action: "addons-installed", Arguments: []byte(`{}`)})
		Expect(e).To(MatchError(gateway.ErrUnavailable))
		Expect(gateway.StatusFor(e)).To(Equal(http.StatusServiceUnavailable))
		// The same frontend works once the owner is up.
		startOK("owner", ownerData)
		_, e = frontend.AddonGateway().Apps(context.Background(), actor)
		Expect(e).NotTo(HaveOccurred())
	})

	It("reports the addons feature of a frontend from the reachability of its owner, cached", func() {
		ctx := context.Background()
		now := time.Now()
		frontend := startOK("frontend", realTempDir())
		frontend.addonsProbe.now = func() time.Time { return now }
		Expect(frontend.AddonsFeature(ctx)).To(BeFalse())
		// The owner starts, but the frontend keeps its cached answer.
		owner := startOK("owner", ownerData)
		Expect(owner.AddonsFeature(ctx)).To(BeTrue())
		now = now.Add(5 * time.Second)
		Expect(frontend.AddonsFeature(ctx)).To(BeFalse())
		// Once the cache expires the frontend asks again.
		now = now.Add(6 * time.Second)
		Expect(frontend.AddonsFeature(ctx)).To(BeTrue())
	})

	It("refuses a second owner on the same root", func() {
		startOK("owner", ownerData)
		_, e := start("owner", ownerData)
		Expect(e).To(MatchError(addons.ErrOwnerBusy))
	})

	It("serves the installed add-on after the owner restarts on the same root and address", func() {
		owner := startOK("owner", ownerData)
		frontend := startOK("frontend", realTempDir())
		install(frontend)
		Expect(installed(frontend)).To(ContainSubstring("sample"))
		// A process that ends cancels its context; that releases the owner's
		// stores. The release runs on its own goroutine.
		stops[owner]()
		_, e := frontend.AddonGateway().Dispatch(context.Background(), actor, contract.Request{OperationID: "list", Action: "addons-installed", Arguments: []byte(`{}`)})
		Expect(e).To(MatchError(gateway.ErrUnavailable))
		Eventually(func() error {
			_, e := start("owner", ownerData)
			return e
		}).Should(Succeed())
		// The audience is new, and the frontend's next call fetches it.
		Expect(installed(frontend)).To(ContainSubstring("sample"))
	})
	Describe("shared models path", func() {
		startWith := func(role, data, models string) *Application {
			nextModels = models
			defer func() { nextModels = "" }()
			return startOK(role, data)
		}

		It("reports shared when the frontend sees the owner's probe", func() {
			models := realTempDir()
			owner := startWith("owner", ownerData, models)
			frontend := startWith("frontend", realTempDir(), models)
			frontend.checkModelsProbe(context.Background())
			Expect(frontend.AddonModelExports(context.Background())).To(Equal(ModelExportsShared))
			Expect(owner.AddonModelExports(context.Background())).To(Equal(ModelExportsShared))
			p, e := modelsprobe.Read(models)
			Expect(e).NotTo(HaveOccurred())
			Expect(p.Nonce).To(Equal(owner.modelsProbe.own.Load().Nonce))
		})

		It("reports a mismatch for a different directory and blocks exports there", func() {
			startWith("owner", ownerData, realTempDir())
			frontend := startWith("frontend", realTempDir(), realTempDir())
			frontend.checkModelsProbe(context.Background())
			Expect(frontend.AddonModelExports(context.Background())).To(Equal(ModelExportsMismatch))
			Expect(frontend.ModelExportsAllowed(context.Background())).To(BeFalse())
		})

		It("reports a mismatch when the same storage is reached through another path", func() {
			models := realTempDir()
			startWith("owner", ownerData, models)
			link := filepath.Join(realTempDir(), "models")
			Expect(os.Symlink(models, link)).To(Succeed())
			frontend := startWith("frontend", realTempDir(), link)
			frontend.checkModelsProbe(context.Background())
			Expect(frontend.AddonModelExports(context.Background())).To(Equal(ModelExportsMismatch))
		})

		It("follows a new nonce after the owner restarts", func() {
			models := realTempDir()
			owner := startWith("owner", ownerData, models)
			frontend := startWith("frontend", realTempDir(), models)
			frontend.checkModelsProbe(context.Background())
			Expect(frontend.AddonModelExports(context.Background())).To(Equal(ModelExportsShared))
			first := owner.modelsProbe.own.Load().Nonce
			stops[owner]()
			var again *Application
			Eventually(func() error {
				var e error
				nextModels = models
				again, e = start("owner", ownerData)
				return e
			}).Should(Succeed())
			Expect(again.modelsProbe.own.Load().Nonce).NotTo(Equal(first))
			frontend.checkModelsProbe(context.Background())
			Expect(frontend.AddonModelExports(context.Background())).To(Equal(ModelExportsShared))
		})

		It("reports unknown, and keeps exports on, while the owner is down", func() {
			frontend := startWith("frontend", realTempDir(), realTempDir())
			frontend.checkModelsProbe(context.Background())
			Expect(frontend.AddonModelExports(context.Background())).To(Equal(ModelExportsUnknown))
			Expect(frontend.ModelExportsAllowed(context.Background())).To(BeTrue())
		})

		It("reports unknown for an owner without the probe endpoint", func() {
			owner := startWith("owner", ownerData, realTempDir())
			owner.addonGateway.ModelsProbe = nil
			frontend := startWith("frontend", realTempDir(), realTempDir())
			frontend.checkModelsProbe(context.Background())
			Expect(frontend.AddonModelExports(context.Background())).To(Equal(ModelExportsUnknown))
		})

		It("refuses an export and an import on the owner when its probe is missing", func() {
			models := realTempDir()
			owner := startWith("owner", ownerData, models)
			ex := &countingModels{}
			gx := guardedExporter{Exporter: ex, ok: owner.ownerProbeCurrent}
			gi := guardedImporter{Importer: ex, ok: owner.ownerProbeCurrent}
			_, e := gx.ExportModel(context.Background(), "u", "j", schema.ExportRequest{})
			Expect(e).NotTo(HaveOccurred())
			Expect(os.Remove(filepath.Join(models, modelsprobe.FileName))).To(Succeed())
			_, e = gx.ExportModel(context.Background(), "u", "j", schema.ExportRequest{})
			Expect(e).To(MatchError(modelsprobe.ErrNotShared))
			_, e = gi.ImportModel(context.Background(), "u", "j", schema.QuantizationImportRequest{})
			Expect(e).To(MatchError(modelsprobe.ErrNotShared))
			Expect(ex.calls).To(Equal(1))
			Expect(owner.AddonModelExports(context.Background())).To(Equal(ModelExportsMismatch))
		})

		It("keeps the owner running and refuses exports when it cannot write the probe", func() {
			owner := startWith("owner", ownerData, filepath.Join(realTempDir(), "missing"))
			Expect(owner.modelsProbe.own.Load()).To(BeNil())
			Expect(owner.ownerProbeCurrent()).To(BeFalse())
			Expect(owner.AddonsFeature(context.Background())).To(BeTrue())
		})
	})

	Describe("library operations", func() {
		libCtx := func() context.Context { return library.WithActor(context.Background(), actor) }

		It("lists the sources through the owner and keeps the frontend's data path empty", func() {
			startOK("owner", ownerData)
			frontendData := realTempDir()
			frontend := startOK("frontend", frontendData)
			l, e := frontend.LibraryAdmin().List(libCtx(), "")
			Expect(e).NotTo(HaveOccurred())
			var ids []string
			for _, in := range l.Sources {
				ids = append(ids, in.ID)
			}
			Expect(ids).To(ContainElement("test"))
			// A version the caller holds gives the cheap answer.
			again, e := frontend.LibraryAdmin().List(libCtx(), l.Version)
			Expect(e).NotTo(HaveOccurred())
			Expect(again.Unchanged).To(BeTrue())
			entries, e := os.ReadDir(frontendData)
			Expect(e).NotTo(HaveOccurred())
			Expect(entries).To(BeEmpty())
		})

		It("refuses a frontend on another OS or architecture", func() {
			startOK("owner", ownerData)
			frontend := startOK("frontend", realTempDir())
			frontend.applicationConfig.Addons.Host.Arch = "arm64"
			_, e := frontend.LibraryAdmin().List(libCtx(), "")
			Expect(e).To(MatchError(gateway.ErrUnavailable))
			Expect(gateway.UnavailableReason(e)).To(Equal(gateway.ReasonPlatformMismatch))
			Expect(frontend.RefreshLibraries(context.Background())).To(MatchError(gateway.ErrUnavailable))
		})

		It("refuses library calls to an owner that does not serve them", func() {
			owner := startOK("owner", ownerData)
			owner.addonGateway.Libraries = nil
			frontend := startOK("frontend", realTempDir())
			_, e := frontend.LibraryAdmin().List(libCtx(), "")
			Expect(gateway.UnavailableReason(e)).To(Equal(gateway.ReasonLibraryUnsupported))
		})

		It("checks the administrator right on the owner whatever the frontend did", func() {
			owner := startOK("owner", ownerData)
			stranger := contract.Actor{UserID: "someone", CredentialID: "key:1", Mode: auth.AddonModeLocalOwner}
			_, e := owner.addonGateway.Libraries.Library(context.Background(), stranger, gateway.LibraryCall{Op: gateway.LibraryList})
			Expect(e).To(MatchError(gateway.ErrForbidden))
			_, e = owner.addonGateway.Libraries.Library(context.Background(), actor, gateway.LibraryCall{Op: gateway.LibraryList})
			Expect(e).NotTo(HaveOccurred())
		})

		It("keeps a frontend with the official libraries off from creating an add-ons folder", func() {
			startOK("owner", ownerData)
			frontendData := realTempDir()
			frontend := startOK("frontend", frontendData)
			frontend.applicationConfig.OfficialLibraries = false
			Expect(frontend.ApplyLibraries()).To(Succeed())
			applyOfficialLibrariesAtStartup(frontend.applicationConfig)
			entries, e := os.ReadDir(frontendData)
			Expect(e).NotTo(HaveOccurred())
			Expect(entries).To(BeEmpty())
		})
	})
})

// countingModels is an exporter and importer that counts the calls it gets.
type countingModels struct{ calls int }

func (c *countingModels) Checkpoints(context.Context, string, string) ([]hostops.Checkpoint, error) {
	return nil, nil
}
func (c *countingModels) ExportModel(context.Context, string, string, schema.ExportRequest) (string, error) {
	c.calls++
	return "m", nil
}
func (c *countingModels) ImportModel(context.Context, string, string, schema.QuantizationImportRequest) (string, error) {
	c.calls++
	return "m", nil
}
