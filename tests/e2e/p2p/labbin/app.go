package main

import (
	"crypto/rand"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/websocket"
)

// protoHalfClose is the lab's own protocol for the half-close check: the
// server reads to the end of the request, then answers.
const protoHalfClose = "/localai-lab/halfclose/1"

var upgrader = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

// appHandler is the small HTTP service of a lab server: a hello, a download of
// any size, server-sent events and a websocket echo.
func appHandler() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/hello", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintln(w, "hello")
	})
	mux.HandleFunc("/download", func(w http.ResponseWriter, r *http.Request) {
		n, err := strconv.ParseInt(r.URL.Query().Get("bytes"), 10, 64)
		if err != nil || n < 0 {
			http.Error(w, "bad bytes", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Length", strconv.FormatInt(n, 10))
		_, _ = io.Copy(w, io.LimitReader(rand.Reader, n))
	})
	mux.HandleFunc("/sse", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		for i := range 5 {
			_, _ = fmt.Fprintf(w, "data: event %d\n\n", i)
			f.Flush()
			time.Sleep(100 * time.Millisecond)
		}
	})
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		for {
			t, b, err := c.ReadMessage()
			if err != nil {
				return
			}
			if c.WriteMessage(t, b) != nil {
				return
			}
		}
	})
	return mux
}
