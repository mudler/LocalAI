package e2eplatform_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	. "github.com/onsi/gomega"
)

const testPassword = "E2e-Platform-Pass-1!x"

// signUp registers a user on an instance that has authentication on, and
// returns a client that sends the user's API key. The first user is the
// administrator.
func signUp(in *instance, email string) (*api, string) {
	a := newAPI(in.api.base)
	out := a.mustJSON(http.MethodPost, "/api/auth/register", obj{"email": email, "password": testPassword, "name": email,
		"acknowledge_weak_password": true}, http.StatusCreated)
	user, _ := out["user"].(obj)
	Expect(user).ToNot(BeNil(), fmt.Sprint(out))
	key := a.mustJSON(http.MethodPost, "/api/auth/api-keys", obj{"name": "e2e"}, http.StatusCreated)
	tok, _ := key["key"].(string)
	Expect(tok).ToNot(BeEmpty())
	return a.withToken(tok), user["id"].(string)
}

// callMarker makes the mock model call the tool: the mock reads
// ADDON_TOOL_CALL:<tool>:<base64url JSON arguments> from the user message.
func callMarker(tool string, args any) string {
	raw, err := json.Marshal(args)
	Expect(err).ToNot(HaveOccurred())
	return "ADDON_TOOL_CALL:" + tool + ":" + base64.URLEncoding.EncodeToString(raw)
}

type chatMsg struct {
	Seq  int64           `json:"seq"`
	Role string          `json:"role"`
	Text string          `json:"text"`
	App  json.RawMessage `json:"app"`
}

func (a *api) newChat(extra obj) string {
	body := obj{"model": "mock-model"}
	for k, v := range extra {
		body[k] = v
	}
	return a.mustJSON(http.MethodPost, "/api/chats", body, http.StatusCreated)["id"].(string)
}

// turn sends a message and waits for the turn to end.
func (a *api) turn(chat, text string) []chatMsg {
	a.mustJSON(http.MethodPost, "/api/chats/"+chat+"/messages", obj{"text": text}, http.StatusOK)
	Eventually(func() bool {
		c := a.mustJSON(http.MethodGet, "/api/chats/"+chat, nil, http.StatusOK)
		running, _ := c["running"].(bool)
		last, _ := c["last_turn"].(string)
		return !running && last != ""
	}, 60*time.Second, 100*time.Millisecond).Should(BeTrue())
	return a.transcript(chat)
}

func (a *api) transcript(chat string) []chatMsg {
	var all []chatMsg
	var after int64
	for {
		code, out := a.do(http.MethodGet, fmt.Sprintf("/api/chats/%s/messages?after=%d&limit=1000", chat, after), nil)
		Expect(code).To(Equal(http.StatusOK), string(out))
		var r struct {
			Messages []chatMsg `json:"messages"`
		}
		Expect(json.Unmarshal(out, &r)).To(Succeed())
		if len(r.Messages) == 0 {
			return all
		}
		all = append(all, r.Messages...)
		after = r.Messages[len(r.Messages)-1].Seq
	}
}

func toolResults(msgs []chatMsg) []string {
	var out []string
	for _, m := range msgs {
		if m.Role == "tool" {
			out = append(out, m.Text)
		}
	}
	return out
}

func anyContains(list []string, sub string) bool {
	for _, s := range list {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func createConnector(a *api, body obj) string {
	return a.mustJSON(http.MethodPost, "/api/chats/mcp-connectors", body, http.StatusCreated)["id"].(string)
}

func setSetting(a *api, key string, value any) {
	a.mustJSON(http.MethodPost, "/api/settings", obj{key: value}, http.StatusOK)
}
