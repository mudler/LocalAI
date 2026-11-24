// SPDX-License-Identifier: MIT
//go:build auth

package application

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/addons/hostops"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type recordingExporter struct {
	calls int
}

func (r *recordingExporter) Checkpoints(context.Context, string, string) ([]hostops.Checkpoint, error) {
	return []hostops.Checkpoint{{Step: 1, Path: "/out/ckpt-1"}}, nil
}

func (r *recordingExporter) ExportModel(context.Context, string, string, schema.ExportRequest) (string, error) {
	r.calls++
	return "/models/m", nil
}

// With an account database the role decides in every mode, so the local-owner
// mode does not make a non-administrator an administrator.
var _ = Describe("the admin check with an account database", func() {
	It("follows the role in every mode", func() {
		db, err := auth.InitDB(filepath.Join(GinkgoT().TempDir(), "auth.db"))
		Expect(err).ToNot(HaveOccurred())
		mk := func(id, role string) {
			Expect(db.Create(&auth.User{ID: id, Email: id + "@example.com", Role: role, Status: auth.StatusActive}).Error).To(Succeed())
		}
		mk("admin", auth.RoleAdmin)
		mk("granted", auth.RoleUser)
		Expect(auth.UpdateUserPermissions(db, "granted", auth.PermissionMap{"fine_tuning": true})).To(Succeed())

		check := addonAdminCheck(db)
		Expect(check(contract.Actor{UserID: "granted", Mode: "local-owner"})).To(BeFalse(),
			"a non-administrator is an administrator in local-owner mode with an account database")
		Expect(check(contract.Actor{UserID: "admin", Mode: "local-owner"})).To(BeTrue(),
			"an administrator is refused in local-owner mode")

		in := hostops.NewInFlight()
		b := hostops.New(in, addonFeatureCheck(db), contract.DefaultLimits())
		b.SetAdminCheck(check)
		ex := &recordingExporter{}
		Expect(hostops.RegisterEngineModels(b, ex, nil)).To(Succeed())

		serve := func(user string) (int, contract.HostResponse) {
			end := in.Begin(contract.Envelope{
				Request:   contract.Request{AddonID: "fine-tuning", OperationID: "op1", Generation: 1, Action: "start", Arguments: json.RawMessage(`{}`)},
				Authority: contract.Authority{Actor: contract.Actor{UserID: user, CredentialID: "c1", Mode: "local-owner"}, Scope: "apply", ExpiresAt: time.Now().Add(time.Minute)},
			})
			defer end()
			body, _ := json.Marshal(contract.HostRequest{OperationID: "op1", Op: "finetune.export", Arguments: json.RawMessage(`{"jobId":"j1"}`)})
			req := httptest.NewRequest(http.MethodPost, "/host", bytes.NewReader(body))
			req.Header.Set(contract.HostCredentialHeader, "secret")
			rec := httptest.NewRecorder()
			b.Handler("fine-tuning", "secret", []string{"finetune."}).ServeHTTP(rec, req)
			var out contract.HostResponse
			_ = json.Unmarshal(rec.Body.Bytes(), &out)
			return rec.Code, out
		}

		code, out := serve("granted")
		Expect(code).To(Equal(http.StatusForbidden))
		Expect(out.Error).ToNot(BeNil())
		Expect(out.Error.Code).To(Equal(contract.HostFeature))
		Expect(ex.calls).To(BeZero(), "service called for a non-administrator")
		code, out = serve("admin")
		Expect(code).To(Equal(http.StatusOK), "administrator: %+v", out.Error)
	})
})
