// SPDX-License-Identifier: MIT
package gateway

import (
	"context"
	"encoding/json"
	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/core/services/addons/packages"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"net/http"
	"net/http/httptest"
	"path/filepath"
)

var _ = Describe("lifecycle relays", func() {
	It("alternates real owner journal status and terminates coarse progress without submitting again", func() {
		actor := contract.Actor{UserID: "u", CredentialID: "k", Mode: "named-user"}
		root := filepath.Join(GinkgoT().TempDir(), "owner")
		manager, e := addons.NewManager(context.Background(), addons.OwnerConfig{InstanceID: "owner", OwnerID: "owner", OwnerAddress: "https://owner.example", StorageID: "disk", Root: root}, catalog.Catalog{}, &packages.Installer{Root: filepath.Join(root, "packages")}, func(_ context.Context, a contract.Actor) error {
			if a != actor {
				return ErrForbidden
			}
			return nil
		})
		Expect(e).NotTo(HaveOccurred())
		DeferCleanup(manager.Close)
		// Resolving an absent source durably records a failed install, without any
		// package or process fixture. Reading that failure must never restart it.
		_, e = manager.Submit(context.Background(), actor, addons.LifecycleCommand{AddonID: "sample", OperationID: "original", Action: "install", SourceID: "absent", Version: "1.0.0"})
		Expect(e).To(HaveOccurred())
		_, cs := carrierFixture(func(c *Carrier) http.Handler {
			h, e := c.OwnerHandler(&Gateway{Manager: manager, Revalidate: func(context.Context, contract.Actor) error { return nil }})
			Expect(e).NotTo(HaveOccurred())
			return h
		})
		request := contract.Request{AddonID: "sample", OperationID: "original", Action: "operation-status", Arguments: []byte(`{}`)}
		for _, id := range []string{"front-a", "front-b", "front-a"} {
			out, e := cs[id].Dispatch(context.Background(), actor, request)
			Expect(e).NotTo(HaveOccurred())
			var state addons.OperationStatus
			Expect(json.Unmarshal(out.Payload, &state)).To(Succeed())
			Expect(state.Phase).To(Equal("failed"))
			Expect(state.RuntimeState).To(Equal("not-observed"))
		}
		w := httptest.NewRecorder()
		Expect(cs["front-b"].Relay(context.Background(), actor, RelayRequest{Request: request, Kind: "progress"}, w)).To(Succeed())
		Expect(w.Body.String()).To(ContainSubstring("event: lifecycle"))
		Expect(w.Body.String()).To(ContainSubstring("failed"))
		request.OperationID = "unknown"
		_, e = cs["front-a"].Dispatch(context.Background(), actor, request)
		Expect(e).To(HaveOccurred())
	})
})
