// SPDX-License-Identifier: MIT
package application

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("execution binding owner resolver", func() {
	var a *Application
	var ctx context.Context
	var actor contract.Actor
	var claim gateway.ExecutionBindingClaim
	BeforeEach(func() {
		requireAddonRuntime()
		ctx = context.Background()
		actor = contract.Actor{UserID: auth.AddonLocalOwnerID, CredentialID: auth.AddonLocalOwnerID, Mode: auth.AddonModeLocalOwner}
		root := realTempDir()
		DeferCleanup(func() {
			_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
				if err == nil && info.IsDir() {
					return os.Chmod(p, 0700)
				}
				return nil
			})
		})
		binary := filepath.Join(root, "fixture")
		output, err := exec.Command("go", "build", "-o", binary, "../../tests/addons/fixture/cmd").CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(output))
		executable, err := os.ReadFile(binary)
		Expect(err).NotTo(HaveOccurred())
		raw, err := os.ReadFile("../../tests/addons/fixtures/packages/manifest.json")
		Expect(err).NotTo(HaveOccurred())
		var manifest map[string]any
		Expect(json.Unmarshal(raw, &manifest)).To(Succeed())
		manifest["id"] = "renamed-runner"
		manifest["capabilities"] = contract.Capabilities{Service: true, Actions: true, Tools: []string{"execute", "inspect"}, ExecutionProvider: &contract.ExecutionProvider{Tools: []string{"execute"}, StatusTool: "inspect"}, ToolDefinitions: []contract.ToolDefinition{
			{Name: "execute", Kind: contract.ToolKindAction, Shadows: "bash", Description: "Run a command", InputSchema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"],"additionalProperties":false}`)},
			{Name: "inspect", Kind: contract.ToolKindRead, UIOnly: true, Description: "Inspect execution", InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)},
		}}
		raw, err = json.Marshal(manifest)
		Expect(err).NotTo(HaveOccurred())
		var archive bytes.Buffer
		tw := tar.NewWriter(&archive)
		for name, data := range map[string][]byte{"manifest.json": raw, "bin/sample": executable, "fixture.json": []byte(`{"mode":"execution-status"}`)} {
			Expect(tw.WriteHeader(&tar.Header{Name: name, Mode: 0755, Size: int64(len(data))})).To(Succeed())
			_, err = tw.Write(data)
			Expect(err).NotTo(HaveOccurred())
		}
		Expect(tw.Close()).To(Succeed())
		var url string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/package.tar" {
				_, _ = w.Write(archive.Bytes())
				return
			}
			_ = catalog.WriteIndex(w, []catalog.Release{{ID: "renamed-runner", Name: "Renamed", Version: "1.2.3", OS: "linux", Arch: "amd64", Artifact: url + "/package.tar", Digest: fmt.Sprintf("sha256:%x", sha256.Sum256(archive.Bytes()))}})
		}))
		DeferCleanup(server.Close)
		url = server.URL
		ownerRoot := filepath.Join(root, "addons")
		Expect(os.MkdirAll(ownerRoot, 0700)).To(Succeed())
		admin := func(context.Context, contract.Actor) error { return nil }
		sources, err := catalog.NewSources(filepath.Join(ownerRoot, "sources.json"), admin, catalog.CredentialResolver{})
		Expect(err).NotTo(HaveOccurred())
		Expect(sources.Save(ctx, actor, catalog.Source{ID: "test", Name: "Test", URL: url, Enabled: true, AllowLoopbackHTTP: true})).To(Succeed())
		runCtx, cancel := context.WithCancel(ctx)
		DeferCleanup(cancel)
		a = &Application{applicationConfig: &config.ApplicationConfig{Context: runCtx, Addons: config.AddonConfig{Enabled: true, InstanceID: "owner", OwnerID: "owner", OwnerAddress: "https://owner.example", StorageID: "local", Root: ownerRoot, AdminCheck: admin, OwnershipMode: auth.AddonModeLocalOwner, Host: contract.HostCompatibility{Host: 1, API: 1, Protocol: 1, SDK: 1, DataFormat: 1, OS: "linux", Arch: "amd64"}}}}

		keys := filepath.Join(root, "keys")
		Expect(gateway.GenerateCarrierKeys(keys, []string{"127.0.0.1"})).To(Succeed())
		listener, e := net.Listen("tcp", "127.0.0.1:0")
		Expect(e).NotTo(HaveOccurred())
		address := listener.Addr().String()
		Expect(listener.Close()).To(Succeed())
		transport, e := gateway.LoadCarrierConfig(keys, "owner", "https://"+address)
		Expect(e).NotTo(HaveOccurred())
		a.applicationConfig.Addons.InstanceID = transport.InstanceID
		a.applicationConfig.Addons.OwnerID = transport.OwnerID
		a.applicationConfig.Addons.OwnerAddress = transport.OwnerAddress
		a.applicationConfig.Addons.ListenAddress = address
		a.applicationConfig.Addons.Transport = transport
		Expect(a.startAddons()).To(Succeed())
		DeferCleanup(a.Shutdown)
		_, err = a.addonManager.Submit(ctx, actor, addons.LifecycleCommand{OperationID: "install", AddonID: "renamed-runner", Action: "install", SourceID: "test", Version: "1.2.3"})
		Expect(err).NotTo(HaveOccurred())
		_, err = a.addonManager.Submit(ctx, actor, addons.LifecycleCommand{OperationID: "enable", AddonID: "renamed-runner", Action: "enable", Generation: 1})
		Expect(err).NotTo(HaveOccurred())
		a.studioStore, err = store.NewJSON(filepath.Join(root, "chats"))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(a.studioStore.(interface{ Close() error }).Close()).To(Succeed()) })
		a.studioAgents, err = agents.NewJSON(filepath.Join(root, "agents"))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(a.studioAgents.(interface{ Close() error }).Close()).To(Succeed()) })
		claim = gateway.ExecutionBindingClaim{ConversationID: "conversation"}
		Expect(a.studioStore.CreateChat(ctx, session.ChatRecord{ID: claim.ConversationID, Principal: studio.AnonymousPrincipal, Created: time.Now(), Updated: time.Now()})).To(Succeed())
		Expect(a.addonGateway.ResolveExecutionBinding).NotTo(BeNil())
		Expect(a.addonGateway.AuthorizeExecutionStatus).NotTo(BeNil())
	})
	It("binds the actual renamed running provider and rejects forged bindings and foreign actors", func() {
		b, err := a.addonGateway.BindingForActor(ctx, actor, claim)
		Expect(err).NotTo(HaveOccurred())
		Expect(b.AddonID).To(Equal("renamed-runner"))
		Expect(b.ExecutionTool).To(Equal("execute"))
		Expect(b.StatusTool).To(Equal("inspect"))
		current, err := a.addonManager.Runtime(ctx, b.AddonID)
		Expect(err).NotTo(HaveOccurred())
		Expect(b.Generation).To(Equal(current.Generation))
		Expect(b.Incarnation).To(Equal(current.Incarnation))
		Expect(a.addonGateway.AuthorizeExecutionStatus(ctx, actor, b)).To(Succeed())

		forged := b
		forged.StatusTool = "execute"
		Expect(a.addonGateway.AuthorizeExecutionStatus(ctx, actor, forged)).NotTo(Succeed())
		foreign := actor
		foreign.UserID = "other"
		Expect(a.addonGateway.AuthorizeExecutionStatus(ctx, foreign, b)).NotTo(Succeed())
	})
	It("observes the actual renamed provider through the Studio typed adapter and withdraws stale facts", func() {
		broker := a.StudioAddonTools().(*studioAddons)
		request, addon, explanation := broker.NewExecutionObservation(ctx, studio.AnonymousPrincipal, claim.ConversationID)
		Expect(request).NotTo(BeNil())
		defer request.Close()
		Expect(addon).To(Equal("renamed-runner"))
		value := request.Observe(ctx)
		Expect(string(value.Readiness)).To(Equal("available"))
		Expect(string(value.Isolation)).To(Equal("isolated"))
		Expect(explanation()).To(BeEmpty())
		Expect(request.Observe(ctx)).To(Equal(value))
		Expect(a.studioStore.SetAddonsOff(ctx, studio.AnonymousPrincipal, claim.ConversationID, []string{"renamed-runner"})).To(Succeed())
		Expect(string(request.Observe(ctx).Isolation)).To(Equal("unknown"))
		Expect(broker.Isolation(ctx, studio.AnonymousPrincipal, "renamed-runner")).To(BeEmpty())
	})
	It("invalidates old bindings after pins and chat switch ABA", func() {
		b, err := a.addonGateway.BindingForActor(ctx, actor, claim)
		Expect(err).NotTo(HaveOccurred())
		a.applicationConfig.SetChatsToolProviders(map[string]string{"bash": "renamed-runner"})
		a.applicationConfig.SetChatsToolProviders(nil)
		Expect(a.addonGateway.AuthorizeExecutionStatus(ctx, actor, b)).NotTo(Succeed())
		b, err = a.addonGateway.BindingForActor(ctx, actor, claim)
		Expect(err).NotTo(HaveOccurred())
		Expect(a.studioStore.SetAddonsOff(ctx, studio.AnonymousPrincipal, claim.ConversationID, []string{"renamed-runner"})).To(Succeed())
		_, err = a.addonGateway.BindingForActor(ctx, actor, claim)
		Expect(err).To(HaveOccurred())
		Expect(a.studioStore.SetAddonsOff(ctx, studio.AnonymousPrincipal, claim.ConversationID, nil)).To(Succeed())
		Expect(a.addonGateway.AuthorizeExecutionStatus(ctx, actor, b)).NotTo(Succeed())
	})
	It("denies foreign chat access and named-user contexts", func() {
		Expect(a.studioStore.CreateChat(ctx, session.ChatRecord{ID: "foreign", Principal: "someone", Created: time.Now(), Updated: time.Now()})).To(Succeed())
		_, err := a.addonGateway.BindingForActor(ctx, actor, gateway.ExecutionBindingClaim{ConversationID: "foreign"})
		Expect(err).To(HaveOccurred())
		a.applicationConfig.Addons.OwnershipMode = auth.AddonModeNamedUser
		_, err = a.addonGateway.ResolveExecutionBinding(ctx, actor, claim)
		Expect(err).To(MatchError(gateway.ErrUnavailable))
	})
	It("uses owner agent snapshots, denies unattended actions, and fences agent ABA", func() {
		ag := agents.Agent{ID: "agent", Principal: studio.AnonymousPrincipal, Name: "Runner", Addons: []string{"renamed-runner"}}
		Expect(a.studioAgents.Put(ctx, ag)).To(Succeed())
		Expect(a.studioStore.CreateChat(ctx, session.ChatRecord{ID: "agent-chat", Principal: studio.AnonymousPrincipal, AgentID: ag.ID, Origin: "trigger", Created: time.Now(), Updated: time.Now()})).To(Succeed())
		c := gateway.ExecutionBindingClaim{ConversationID: "agent-chat"}
		b, err := a.addonGateway.BindingForActor(ctx, actor, c)
		Expect(err).NotTo(HaveOccurred())
		ag.Addons = nil
		Expect(a.studioAgents.Put(ctx, ag)).To(Succeed())
		_, err = a.addonGateway.BindingForActor(ctx, actor, c)
		Expect(err).To(HaveOccurred())
		ag.Addons = []string{"renamed-runner"}
		Expect(a.studioAgents.Put(ctx, ag)).To(Succeed())
		Expect(a.addonGateway.AuthorizeExecutionStatus(ctx, actor, b)).NotTo(Succeed())
		Expect(a.studioStore.CreateChat(ctx, session.ChatRecord{ID: "unattended", Principal: studio.AnonymousPrincipal, Origin: "trigger", Created: time.Now(), Updated: time.Now()})).To(Succeed())
		_, err = a.addonGateway.BindingForActor(ctx, actor, gateway.ExecutionBindingClaim{ConversationID: "unattended"})
		Expect(err).To(HaveOccurred())
		Expect(a.studioAgents.Put(ctx, agents.Agent{ID: "foreign-agent", Principal: "foreign", Name: "Foreign", Addons: []string{"renamed-runner"}})).To(Succeed())
		Expect(a.studioStore.CreateChat(ctx, session.ChatRecord{ID: "foreign-agent-chat", Principal: studio.AnonymousPrincipal, AgentID: "foreign-agent", Created: time.Now(), Updated: time.Now()})).To(Succeed())
		_, err = a.addonGateway.BindingForActor(ctx, actor, gateway.ExecutionBindingClaim{ConversationID: "foreign-agent-chat"})
		Expect(err).To(HaveOccurred())
	})
	It("rejects stale runtime incarnations and previous owner lifetimes", func() {
		b, err := a.addonGateway.BindingForActor(ctx, actor, claim)
		Expect(err).NotTo(HaveOccurred())
		Expect(a.wireExecutionBinding(a.addonGateway)).To(Succeed())
		Expect(a.addonGateway.AuthorizeExecutionStatus(ctx, actor, b)).NotTo(Succeed())
		b, err = a.addonGateway.BindingForActor(ctx, actor, claim)
		Expect(err).NotTo(HaveOccurred())
		_, err = a.addonManager.Submit(ctx, actor, addons.LifecycleCommand{OperationID: "disable", AddonID: b.AddonID, Action: "disable", Generation: b.Generation})
		Expect(err).NotTo(HaveOccurred())
		Expect(a.addonGateway.AuthorizeExecutionStatus(ctx, actor, b)).NotTo(Succeed())
		_, err = a.addonManager.Submit(ctx, actor, addons.LifecycleCommand{OperationID: "enable-again", AddonID: b.AddonID, Action: "enable", Generation: b.Generation})
		Expect(err).NotTo(HaveOccurred())
		now, err := a.addonGateway.BindingForActor(ctx, actor, claim)
		Expect(err).NotTo(HaveOccurred())
		Expect(now.Incarnation).NotTo(Equal(b.Incarnation))
		Expect(a.addonGateway.AuthorizeExecutionStatus(ctx, actor, b)).NotTo(Succeed())
	})
	It("fails unsupported snapshots closed without changing ordinary chat access", func() {
		original := a.studioStore
		a.studioStore = unsupportedBindingStore{original}
		_, err := a.addonGateway.BindingForActor(ctx, actor, claim)
		Expect(err).To(MatchError(gateway.ErrUnavailable))
		rec, err := a.studioStore.GetChat(ctx, studio.AnonymousPrincipal, claim.ConversationID)
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.ID).To(Equal(claim.ConversationID))
		a.studioStore = original
	})

})

// Models the explicit unsupported contract of the SQL backend without inventing
// an authority counter or requiring a database for this owner integration test.
type unsupportedBindingStore struct{ store.Store }

func (unsupportedBindingStore) SelectionSnapshot(context.Context, string, string) (session.ChatRecord, string, error) {
	return session.ChatRecord{}, "", store.ErrSelectionRevisionUnsupported
}
