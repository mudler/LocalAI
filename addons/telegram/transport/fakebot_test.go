// SPDX-License-Identifier: MIT
package transport_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	"github.com/mudler/LocalAI/addons/telegram/transport"
	. "github.com/onsi/ginkgo/v2"
)

const secretToken = "123456:SECRET-token-value"

// fakeBot is a Bot API server. Each call is recorded; the answer of a method
// is set with reply.
type fakeBot struct {
	srv   *httptest.Server
	mu    sync.Mutex
	calls []call
	reply map[string]func(body map[string]any) (int, string)
	// files are the downloads of the bot, by their path after /file/bot<token>/.
	files map[string][]byte
}

type call struct {
	Path string
	Body map[string]any
}

func newFakeBot() *fakeBot {
	f := &fakeBot{reply: map[string]func(map[string]any) (int, string){}, files: map[string][]byte{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rest, ok := strings.CutPrefix(r.URL.EscapedPath(), "/file/bot"); ok {
			f.mu.Lock()
			f.calls = append(f.calls, call{Path: r.URL.EscapedPath()})
			_, file, _ := strings.Cut(rest, "/")
			data, found := f.files[file]
			f.mu.Unlock()
			if !found {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write(data)
			return
		}
		body := map[string]any{}
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			// A file upload: the fields as text, and the file as its field,
			// name and size.
			if r.ParseMultipartForm(4<<20) == nil {
				for k, v := range r.MultipartForm.Value {
					body[k] = v[0]
				}
				for k, fhs := range r.MultipartForm.File {
					body["_file_field"], body["_file_name"], body["_file_size"] = k, fhs[0].Filename, fhs[0].Size
				}
			}
		} else {
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &body)
		}
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
	DeferCleanup(f.srv.Close)
	return f
}

func (f *fakeBot) client() *transport.API {
	return &transport.API{BaseURL: f.srv.URL, Client: f.srv.Client()}
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
