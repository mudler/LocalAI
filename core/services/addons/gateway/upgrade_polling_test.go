// SPDX-License-Identifier: MIT
package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/recovery"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type blockedUpgradePolicy struct{ entered, release chan struct{} }

func (*blockedUpgradePolicy) Mode() string                                 { return "named-user" }
func (*blockedUpgradePolicy) Hold(context.Context, string) (func(), error) { return func() {}, nil }
func (p *blockedUpgradePolicy) Export(context.Context, string) (recovery.PolicyExport, error) {
	close(p.entered)
	<-p.release
	return recovery.PolicyExport{}, nil
}
func (*blockedUpgradePolicy) Restore(context.Context, string, recovery.PolicyExport, recovery.RestoreOptions) (recovery.RestoreReport, error) {
	return recovery.RestoreReport{}, nil
}
func (*blockedUpgradePolicy) LostWrites(context.Context, string, time.Time) (int, error) {
	return 0, nil
}
func (*blockedUpgradePolicy) Forget(context.Context, string) (int, error) { return 0, nil }

var _ = Describe("durable upgrade carrier polling", func() {
	It("accepts before a slow backup, detaches the request and polls through either frontend", func() {
		actor := contract.Actor{UserID: "u", CredentialID: "k", Mode: "named-user"}
		manager := ownerManager(actor, true)
		st, e := manager.Status(context.Background(), "sample")
		Expect(e).NotTo(HaveOccurred())
		Expect(os.MkdirAll(st.DataDirectory, 0700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(st.DataDirectory, "data"), []byte("retained"), 0600)).To(Succeed())
		policy := &blockedUpgradePolicy{make(chan struct{}), make(chan struct{})}
		defer func() {
			select {
			case <-policy.release:
			default:
				close(policy.release)
			}
		}()
		r, e := recovery.New(recovery.Config{Root: filepath.Dir(filepath.Dir(st.DataDirectory)), Host: manager.RecoveryHost(), Policy: policy})
		Expect(e).NotTo(HaveOccurred())
		manager.SetRecovery(r)
		_, cs := carrierFixture(func(c *Carrier) http.Handler {
			h, e := c.OwnerHandler(&Gateway{Manager: manager, Revalidate: func(context.Context, contract.Actor) error { return nil }})
			Expect(e).NotTo(HaveOccurred())
			return h
		})
		// Bound the old ordinary client deadline, rather than sleeping 65 seconds.
		cs["front-a"].client.Timeout = 100 * time.Millisecond
		request, cancel := context.WithCancel(context.Background())
		cmd := contract.Request{AddonID: "sample", OperationID: "upgrade", Action: "upgrade", Arguments: []byte(`{"sourceId":"test","version":"1.2.3"}`)}
		out, e := cs["front-a"].Dispatch(request, actor, cmd)
		Expect(e).NotTo(HaveOccurred())
		cancel()
		var accepted addons.OperationStatus
		Expect(json.Unmarshal(out.Payload, &accepted)).To(Succeed())
		Expect(accepted.Phase).To(Equal("accepted"))
		Eventually(policy.entered).Should(BeClosed())
		// Work remains blocked beyond the old deadline while status reads succeed.
		Consistently(func() string {
			out, e := cs["front-b"].Dispatch(context.Background(), actor, contract.Request{AddonID: "sample", OperationID: "upgrade", Action: "operation-status", Arguments: []byte(`{}`)})
			Expect(e).NotTo(HaveOccurred())
			var status addons.OperationStatus
			Expect(json.Unmarshal(out.Payload, &status)).To(Succeed())
			return status.Stage
		}, 150*time.Millisecond, 20*time.Millisecond).Should(Equal("backing-up"))
		_, e = cs["front-b"].Dispatch(context.Background(), actor, cmd)
		Expect(e).NotTo(HaveOccurred())
		changed := cmd
		changed.Arguments = []byte(`{"sourceId":"test","version":"9.0.0"}`)
		_, e = cs["front-b"].Dispatch(context.Background(), actor, changed)
		Expect(e).To(HaveOccurred())
		close(policy.release)
		Eventually(func() string {
			out, e := cs["front-a"].Dispatch(context.Background(), actor, contract.Request{AddonID: "sample", OperationID: "upgrade", Action: "operation-status", Arguments: []byte(`{}`)})
			Expect(e).NotTo(HaveOccurred())
			var status addons.OperationStatus
			Expect(json.Unmarshal(out.Payload, &status)).To(Succeed())
			return status.Phase
		}).Should(Equal("completed"))
		st, e = manager.Status(context.Background(), "sample")
		Expect(e).NotTo(HaveOccurred())
		Expect(st.Generation).To(Equal(uint64(2)))
	})
})
