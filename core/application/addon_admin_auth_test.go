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
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/addons/hostops"
	"github.com/mudler/LocalAI/pkg/addons/contract"
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
func TestTheAdminCheckWithAnAccountDatabaseFollowsTheRoleInEveryMode(t *testing.T) {
	db, err := auth.InitDB(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	mk := func(id, role string) {
		if err := db.Create(&auth.User{ID: id, Email: id + "@example.com", Role: role, Status: auth.StatusActive}).Error; err != nil {
			t.Fatal(err)
		}
	}
	mk("admin", auth.RoleAdmin)
	mk("granted", auth.RoleUser)
	if err := auth.UpdateUserPermissions(db, "granted", auth.PermissionMap{"fine_tuning": true}); err != nil {
		t.Fatal(err)
	}

	check := addonAdminCheck(db)
	if check(contract.Actor{UserID: "granted", Mode: "local-owner"}) {
		t.Fatal("a non-administrator is an administrator in local-owner mode with an account database")
	}
	if !check(contract.Actor{UserID: "admin", Mode: "local-owner"}) {
		t.Fatal("an administrator is refused in local-owner mode")
	}

	in := hostops.NewInFlight()
	b := hostops.New(in, addonFeatureCheck(db), contract.DefaultLimits())
	b.SetAdminCheck(check)
	ex := &recordingExporter{}
	if err := hostops.RegisterEngineModels(b, ex, nil); err != nil {
		t.Fatal(err)
	}

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
	if code != http.StatusForbidden || out.Error == nil || out.Error.Code != contract.HostFeature {
		t.Fatalf("feature-holding non-admin: %d %+v", code, out.Error)
	}
	if ex.calls != 0 {
		t.Fatalf("service called %d times for a non-administrator", ex.calls)
	}
	if code, out := serve("admin"); code != http.StatusOK {
		t.Fatalf("administrator: %d %+v", code, out.Error)
	}
}
