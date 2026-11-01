package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/schema"
	studiosvc "github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/jsonstore"
)

func TestCapacityHandlers(t *testing.T) {
	ctx := context.Background()
	b, err := blob.NewFS(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	ws, err := jsonstore.New(filepath.Join(t.TempDir(), "ws"), b, jsonstore.Options{Admin: files.AdminPrincipal})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	f := files.New(ws)
	if err := f.EnsureHome(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := ws.Mkdir(ctx, f.Actor("alice"), session.HomePath("alice")+"/PrivateWork"); err != nil {
		t.Fatal(err)
	}
	st, err := store.NewJSON("")
	if err != nil {
		t.Fatal(err)
	}
	ags, err := agents.NewJSON("")
	if err != nil {
		t.Fatal(err)
	}
	harness := studiotest.NewGateHarness()
	svc, err := studiosvc.New(studiosvc.Deps{Store: st, Agents: ags, Files: f, Provider: provider.New(), StateDir: t.TempDir(), Harness: harness})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close(ctx)
	ag, err := svc.SaveAgentWithFolder(ctx, "alice", agents.Agent{Name: "Scoped", Model: "test"}, &studiosvc.AgentFolderWrite{Path: "/PrivateWork", Mode: agents.FolderRead})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := svc.Create(ctx, "alice", studiosvc.CreateInput{AgentID: ag.ID})
	if err != nil {
		t.Fatal(err)
	}
	admin := workspace.Actor{Principal: files.AdminPrincipal}
	for i := 0; i < 31; i++ {
		n, err := ws.Mkdir(ctx, admin, fmt.Sprintf("/shared/SecretShare%02d", i))
		if err != nil {
			t.Fatal(err)
		}
		if err := ws.Grant(ctx, admin, files.EveryoneGroup, n.Path, workspace.ModeRead); err != nil {
			t.Fatal(err)
		}
	}
	h := New(svc, nil)
	for _, send := range []bool{false, true} {
		body := fmt.Sprintf(`{"agent_id":%q}`, ag.ID)
		path := "/api/chats"
		if send {
			body = `{"text":"hi"}`
			path += "/" + rec.ID + "/messages"
		}
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		out := httptest.NewRecorder()
		c := echo.New().NewContext(req, out)
		c.Set("auth_user", &auth.User{ID: "alice", Role: auth.RoleAdmin})
		var err error
		if send {
			c.SetParamNames("id")
			c.SetParamValues(rec.ID)
			err = h.Send(c)
		} else {
			err = h.Create(c)
		}
		if err != nil {
			t.Fatal(err)
		}
		var response schema.ErrorResponse
		if err := json.Unmarshal(out.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if out.Code != http.StatusConflict || response.Error == nil || response.Error.Type != "agent_folder_limit" {
			t.Errorf("send=%v: got %d %s", send, out.Code, out.Body.String())
			continue
		}
		want := "This agent cannot start because its file access exceeds the folder limit. Ask an administrator to reduce the shared folders available to you, then try again."
		if response.Error.Message != want {
			t.Errorf("unsafe or incorrect message: %q", response.Error.Message)
		}
	}
	if harness.Starts() != 0 {
		t.Error("refused request started a harness")
	}
}
