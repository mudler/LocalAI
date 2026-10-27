package connectors_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
)

const secretToken = "123456:SECRET-token-value"

// fakeBot is a Bot API server. Each call is recorded; the answer of a method is
// set with reply.
type fakeBot struct {
	srv   *httptest.Server
	mu    sync.Mutex
	calls []call
	reply map[string]func(body map[string]any) (int, string)
}

type call struct {
	Path string
	Body map[string]any
}

func newFakeBot(t *testing.T) *fakeBot {
	t.Helper()
	f := &fakeBot{reply: map[string]func(map[string]any) (int, string){}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		path := r.URL.EscapedPath()
		method := path[strings.LastIndex(path, "/")+1:]
		f.mu.Lock()
		f.calls = append(f.calls, call{Path: path, Body: body})
		fn := f.reply[method]
		f.mu.Unlock()
		code, out := http.StatusOK, `{"ok":true,"result":true}`
		if fn != nil {
			code, out = fn(body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = w.Write([]byte(out))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeBot) client() *connectors.Telegram {
	return &connectors.Telegram{BaseURL: f.srv.URL, Client: f.srv.Client()}
}

func (f *fakeBot) called(method string) []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []call
	for _, c := range f.calls {
		if strings.HasSuffix(c.Path, "/"+method) {
			out = append(out, c)
		}
	}
	return out
}

func TestUpdatesReadsTextAndMarksTheRest(t *testing.T) {
	f := newFakeBot(t)
	f.reply["getUpdates"] = func(body map[string]any) (int, string) {
		return 200, `{"ok":true,"result":[
 {"update_id":10,"message":{"chat":{"id":55,"type":"private"},"from":{"id":55,"first_name":"Ada","last_name":"L","username":"ada"},"text":"hello"}},
 {"update_id":11,"message":{"chat":{"id":55,"type":"private"},"from":{"id":55,"first_name":"Ada"},"photo":[{"file_id":"x"}]}},
 {"update_id":12,"edited_message":{"chat":{"id":55,"type":"private"},"from":{"id":55},"text":"edit"}},
 {"update_id":13,"channel_post":{"chat":{"id":-100,"type":"channel"},"text":"post"}},
 {"update_id":14,"message":{"chat":{"id":-200,"type":"group"},"from":{"id":7,"first_name":"Bo"},"text":"in a group"}}]}`
	}
	ups, err := f.client().Updates(context.Background(), secretToken, 10, 25)
	if err != nil || len(ups) != 5 {
		t.Fatalf("updates = %+v, %v", ups, err)
	}
	if u := ups[0]; u.ID != 10 || u.ChatID != 55 || u.ChatType != "private" || u.FromID != 55 || u.FromName != "Ada L" || u.FromUsername != "ada" || u.Text != "hello" || u.Unsupported || u.Skip {
		t.Fatalf("text update = %+v", u)
	}
	if !ups[1].Unsupported || ups[1].Skip {
		t.Fatalf("photo update = %+v", ups[1])
	}
	if !ups[2].Skip || !ups[3].Skip {
		t.Fatalf("edited or channel update not skipped: %+v %+v", ups[2], ups[3])
	}
	if ups[4].ChatType != "group" {
		t.Fatalf("group update = %+v", ups[4])
	}
	c := f.called("getUpdates")[0]
	if c.Body["offset"] != float64(10) || c.Body["timeout"] != float64(25) {
		t.Fatalf("request = %+v", c.Body)
	}
	if !strings.Contains(c.Path, "/bot"+secretToken+"/") {
		t.Fatalf("the token is not in the path: %s", c.Path)
	}
}

func TestErrorsAreTypedAndNeverCarryTheToken(t *testing.T) {
	f := newFakeBot(t)
	ctx := context.Background()
	cases := []struct {
		name string
		code int
		body string
		is   func(error) bool
	}{
		{"unauthorized", 401, `{"ok":false,"error_code":401,"description":"Unauthorized"}`, func(e error) bool { return errors.Is(e, connectors.ErrUnauthorized) }},
		{"conflict", 409, `{"ok":false,"error_code":409,"description":"Conflict: terminated by other getUpdates request"}`, func(e error) bool { return errors.Is(e, connectors.ErrConflict) }},
		{"retry", 429, `{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":7}}`, func(e error) bool {
			var r *connectors.RetryAfter
			return errors.As(e, &r) && r.Seconds == 7
		}},
		{"other", 400, `{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`, func(e error) bool { return e != nil }},
	}
	for _, c := range cases {
		f.reply["sendMessage"] = func(map[string]any) (int, string) { return c.code, c.body }
		err := f.client().Send(ctx, secretToken, 1, "hi")
		if !c.is(err) {
			t.Errorf("%s: %v", c.name, err)
		}
		if err != nil && strings.Contains(err.Error(), "SECRET") {
			t.Errorf("%s: the error has the token: %v", c.name, err)
		}
	}
	// A dead connection: the URL has the token, the error must not.
	dead := &connectors.Telegram{BaseURL: "http://127.0.0.1:1", Client: &http.Client{}}
	_, err := dead.Me(ctx, secretToken)
	if err == nil || strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), secretToken) {
		t.Fatalf("network error = %v", err)
	}
}

func TestMeSendAndTypingSpeakTheBotAPI(t *testing.T) {
	f := newFakeBot(t)
	f.reply["getMe"] = func(map[string]any) (int, string) {
		return 200, `{"ok":true,"result":{"id":99,"is_bot":true,"username":"helper_bot"}}`
	}
	c := f.client()
	bot, err := c.Me(context.Background(), secretToken)
	if err != nil || bot.ID != 99 || bot.Username != "helper_bot" {
		t.Fatalf("me = %+v %v", bot, err)
	}
	text := strings.Repeat("x", 4096)
	if err := c.Send(context.Background(), secretToken, -42, text); err != nil {
		t.Fatal(err)
	}
	s := f.called("sendMessage")[0].Body
	if s["chat_id"] != float64(-42) || s["text"] != text {
		t.Fatalf("send = %+v", s["chat_id"])
	}
	if _, set := s["parse_mode"]; set {
		t.Fatal("a parse mode was set: the text of a model must go as plain text")
	}
	if err := c.Typing(context.Background(), secretToken, 5); err != nil {
		t.Fatal(err)
	}
	if a := f.called("sendChatAction")[0].Body; a["action"] != "typing" || a["chat_id"] != float64(5) {
		t.Fatalf("typing = %+v", a)
	}
}

func TestATokenWithOddCharactersStaysInOnePathSegment(t *testing.T) {
	f := newFakeBot(t)
	f.reply["getMe"] = func(map[string]any) (int, string) { return 200, `{"ok":true,"result":{"id":1,"username":"b"}}` }
	if _, err := f.client().Me(context.Background(), "1/../x?y"); err != nil {
		t.Fatal(err)
	}
	if p := f.called("getMe")[0].Path; strings.Contains(p, "/../") || strings.Count(p, "/") != 2 {
		t.Fatalf("path = %q", p)
	}
}
