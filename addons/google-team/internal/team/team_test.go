// SPDX-License-Identifier: MIT
package team

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/addons/internal/googleapi"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestTeam(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Google team")
}

func envelope(op, token, body string) contract.Envelope {
	return contract.Envelope{Request: contract.Request{
		OperationID: op, Credential: token, ResourceKey: "team", Arguments: json.RawMessage(body),
	}}
}

var _ = Describe("Google team", func() {
	var r *Runtime
	var server *httptest.Server
	var etag string
	var sent int
	var body map[string]any
	var ifMatch string
	var dir string
	ctx := context.Background()

	BeforeEach(func() {
		etag, sent, body, ifMatch = `"v1"`, 0, nil, ""
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case req.URL.Path == "/calendar/v3/calendars/primary":
				id := "one@example.com"
				if req.Header.Get("Authorization") == "Bearer second-secret" {
					id = "two@example.com"
				}
				_ = json.NewEncoder(w).Encode(map[string]string{"id": id})
			case req.Method == http.MethodGet && req.URL.Path == "/calendar/v3/calendars/primary/events/event1":
				_ = json.NewEncoder(w).Encode(map[string]string{"etag": etag})
			case req.Method == http.MethodPatch && req.URL.Path == "/calendar/v3/calendars/primary/events/event1":
				sent++
				ifMatch = req.Header.Get("If-Match")
				_ = json.NewDecoder(req.Body).Decode(&body)
				_ = json.NewEncoder(w).Encode(map[string]string{"id": "event1"})
			case req.Method == http.MethodPost && req.URL.Path == "/tasks/v1/lists/list1/tasks":
				sent++
				_ = json.NewDecoder(req.Body).Decode(&body)
				_ = json.NewEncoder(w).Encode(map[string]string{"id": "task1"})
			case req.Method == http.MethodGet && req.URL.Path == "/tasks/v1/lists/list1/tasks":
				Expect(req.URL.Query().Get("pageToken")).To(Equal("next"))
				_, _ = w.Write([]byte(`{"items":[{"id":"task1"}]}`))
			default:
				http.Error(w, "unexpected", http.StatusNotFound)
			}
		}))
		api := googleapi.New(server.URL, "test")
		api.HTTP = server.Client()
		dir = GinkgoT().TempDir()
		r = New(api, dir)
		Expect(r.err).NotTo(HaveOccurred())
	})
	AfterEach(func() { _ = r.db.Close(); server.Close() })

	change := func(op, token, data string) (contract.Receipt, error) {
		env := envelope(op, token, data)
		p, err := r.Preview(ctx, env)
		if err != nil {
			return contract.Receipt{}, err
		}
		return r.Apply(ctx, env, p.Revision)
	}

	It("validates the manifest against the host contract", func() {
		raw, err := os.ReadFile(filepath.Join("..", "..", "manifest.json"))
		Expect(err).NotTo(HaveOccurred())
		_, err = contract.ParseManifest(raw)
		Expect(err).NotTo(HaveOccurred())
	})

	It("isolates accounts and enforces unique user associations", func() {
		rc, err := change("op1", "first-secret", `{"tool":"change","space":"team","changeType":"member-put","id":"alice","name":"Alice","email":"alice@example.com","localUserRef":"user-1"}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(rc.State).To(Equal(contract.ReceiptCommitted))
		_, err = change("op2", "first-secret", `{"tool":"change","space":"team","changeType":"member-put","id":"bob","name":"Bob","localUserRef":"user-1"}`)
		Expect(err).To(HaveOccurred())
		rc, err = change("op3", "first-secret", `{"tool":"change","space":"team","changeType":"member-put","id":"bob","name":"Bob"}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(rc.State).To(Equal(contract.ReceiptCommitted))
		result, err := r.Read(ctx, envelope("op4", "second-secret", `{"tool":"members","space":"team"}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(result.Payload)).NotTo(ContainSubstring("Alice"))
		result, err = r.Read(ctx, envelope("op5", "first-secret", `{"tool":"members","space":"team"}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(result.Payload)).To(ContainSubstring("Alice"))
		Expect(string(result.Payload)).To(ContainSubstring("Bob"))
		who := envelope("who", "first-secret", `{"tool":"whoami","space":"team"}`)
		who.Authority.Actor.UserID = "user-1"
		result, err = r.Read(ctx, who)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(result.Payload)).To(ContainSubstring(`"linked":true`))
		Expect(string(result.Payload)).To(ContainSubstring("Alice"))
		who.Authority.Actor.UserID = "user-2"
		result, err = r.Read(ctx, who)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(result.Payload)).To(ContainSubstring(`"linked":false`))
	})

	It("rejects stale approvals and sends current changes with the inspected etag", func() {
		_, err := change("member", "first-secret", `{"tool":"change","space":"team","changeType":"member-put","id":"alice","name":"Alice","email":"alice@example.com"}`)
		Expect(err).NotTo(HaveOccurred())
		env := envelope("event", "first-secret", `{"tool":"change","space":"team","changeType":"event-update","id":"event1","title":"Meeting","start":"2026-11-27T10:00:00Z","end":"2026-11-27T11:00:00Z","assignees":["alice"]}`)
		p, err := r.Preview(ctx, env)
		Expect(err).NotTo(HaveOccurred())
		etag = `"v2"`
		rc, err := r.Apply(ctx, env, p.Revision)
		Expect(err).NotTo(HaveOccurred())
		Expect(rc.State).To(Equal(contract.ReceiptRejected))
		Expect(sent).To(Equal(0))
		env.Request.OperationID = "event-new"
		p, err = r.Preview(ctx, env)
		Expect(err).NotTo(HaveOccurred())
		rc, err = r.Apply(ctx, env, p.Revision)
		Expect(err).NotTo(HaveOccurred())
		Expect(rc.State).To(Equal(contract.ReceiptCommitted))
		Expect(ifMatch).To(Equal(`"v2"`))
		Expect(body["description"]).To(Equal("@litedesk:assignee=alice"))
		Expect(body["attendees"]).To(HaveLen(1))
		_, err = r.Apply(ctx, env, p.Revision)
		Expect(err).NotTo(HaveOccurred())
		Expect(sent).To(Equal(1))
	})

	It("keeps the team and write receipt across a restart", func() {
		env := envelope("member", "first-secret", `{"tool":"change","space":"team","changeType":"member-put","id":"alice","name":"Alice"}`)
		p, err := r.Preview(ctx, env)
		Expect(err).NotTo(HaveOccurred())
		_, err = r.Apply(ctx, env, p.Revision)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.db.Close()).To(Succeed())
		r = New(googleapi.New(server.URL, "test"), dir)
		r.api.HTTP = server.Client()
		Expect(r.err).NotTo(HaveOccurred())
		result, err := r.Read(ctx, envelope("read", "first-secret", `{"tool":"members","space":"team"}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(result.Payload)).To(ContainSubstring("Alice"))
		rc, err := r.Apply(ctx, env, p.Revision)
		Expect(err).NotTo(HaveOccurred())
		Expect(rc.State).To(Equal(contract.ReceiptCommitted))
	})

	It("creates assigned tasks and pages results without widening the space", func() {
		_, err := change("member", "first-secret", `{"tool":"change","space":"team","changeType":"member-put","id":"bob","name":"Bob"}`)
		Expect(err).NotTo(HaveOccurred())
		rc, err := change("task", "first-secret", `{"tool":"change","space":"team","changeType":"task-create","listId":"list1","title":"Work","notes":"hello","assignees":["bob"]}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(rc.State).To(Equal(contract.ReceiptCommitted))
		Expect(body["notes"]).To(Equal("hello\n@litedesk:assignee=bob"))
		result, err := r.Read(ctx, envelope("read", "first-secret", `{"tool":"tasks","space":"team","listId":"list1","cursor":"next"}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(result.Payload)).To(ContainSubstring("task1"))
		bad := envelope("bad", "first-secret", `{"tool":"tasks","space":"team","listId":"list1"}`)
		bad.Request.ResourceKey = "other"
		_, err = r.Read(ctx, bad)
		Expect(err).To(HaveOccurred())
	})

	It("refuses invalid dates, unknown assignees and unsafe identifiers", func() {
		for i, data := range []string{
			`{"tool":"change","space":"team","changeType":"event-create","title":"X","start":"2026-11-27","end":"2026-11-27"}`,
			`{"tool":"change","space":"team","changeType":"task-create","listId":"list1","title":"X","assignees":["missing"]}`,
			`{"tool":"change","space":"team","changeType":"task-delete","listId":"..","id":"task1"}`,
		} {
			_, err := change(strings.Repeat("x", i+1), "first-secret", data)
			Expect(err).To(HaveOccurred())
		}
		Expect(sent).To(Equal(0))
	})
})
