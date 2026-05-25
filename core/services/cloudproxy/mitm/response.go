package mitm

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// connResponseWriter is a minimal HTTP/1.1 http.ResponseWriter
// that writes directly to a hijacked TLS connection.
type connResponseWriter struct {
	conn *tls.Conn
	bw   *bufio.Writer
	req  *http.Request

	header        http.Header
	wroteHeader   bool
	chunked       bool
	contentLength int64
	written       int64
	closeAfter    bool
}

func newConnResponseWriter(conn *tls.Conn, req *http.Request) *connResponseWriter {
	return &connResponseWriter{
		conn:          conn,
		bw:            bufio.NewWriter(conn),
		req:           req,
		header:        make(http.Header),
		contentLength: -1,
	}
}

func (w *connResponseWriter) Header() http.Header { return w.header }

func (w *connResponseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true

	if cl := w.header.Get("Content-Length"); cl != "" {
		if n, err := strconv.ParseInt(cl, 10, 64); err == nil {
			w.contentLength = n
		}
	}
	if w.contentLength < 0 {
		w.chunked = true
		w.header.Set("Transfer-Encoding", "chunked")
		w.header.Del("Content-Length")
	}

	// "Connection: close" is case-insensitive per RFC 9110 §7.6.1; some
	// upstreams send "Close" or "CLOSE". Use EqualFold so any casing
	// triggers the post-response disconnect.
	for _, v := range w.header.Values("Connection") {
		if strings.EqualFold(v, "close") {
			w.closeAfter = true
		}
	}

	_, _ = fmt.Fprintf(w.bw, "HTTP/1.1 %d %s\r\n", status, http.StatusText(status))
	_ = w.header.Write(w.bw)
	_, _ = w.bw.WriteString("\r\n")
}

func (w *connResponseWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.chunked {
		if _, err := fmt.Fprintf(w.bw, "%x\r\n", len(p)); err != nil {
			return 0, err
		}
		n, err := w.bw.Write(p)
		if err != nil {
			return n, err
		}
		if _, err := w.bw.WriteString("\r\n"); err != nil {
			return n, err
		}
		w.written += int64(n)
		return n, nil
	}
	n, err := w.bw.Write(p)
	w.written += int64(n)
	return n, err
}

func (w *connResponseWriter) Flush() {
	_ = w.bw.Flush()
}

func (w *connResponseWriter) finish() {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.chunked {
		_, _ = w.bw.WriteString("0\r\n\r\n")
	}
	_ = w.bw.Flush()
}
