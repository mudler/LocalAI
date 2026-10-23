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
	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/pkg/addons/contract"
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
		actor        = contract.Actor{UserID: auth.AddonLocalOwnerID, CredentialID: auth.AddonLocalOwnerID, Mode: auth.AddonModeLocalOwner}
	)

	BeforeEach(func() {
		requireAddonRuntime()
		stops = map[*Application]func(){}
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
})
