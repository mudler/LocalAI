// SPDX-License-Identifier: MIT
package musicrequest

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/services/music"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type storeFunc func(context.Context) (*OwnedUpload, error)

func (f storeFunc) Create(c context.Context) (*OwnedUpload, error) { return f(c) }

type sink struct {
	n      int
	closed bool
	fail   bool
}

func (s *sink) Write(b []byte) (int, error) {
	if s.closed {
		return 0, errors.New("closed")
	}
	if s.fail {
		return 0, io.ErrClosedPipe
	}
	s.n += len(b)
	return len(b), nil
}
func (s *sink) Close() error { s.closed = true; return nil }
func ack() music.CleanupTask {
	ch := make(chan error, 1)
	ch <- nil
	return music.CleanupTask{Released: ch}
}
func uploadRequest(parts ...[2]string) *http.Request {
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	for _, p := range parts {
		if p[0] == "audio" {
			f, e := w.CreateFormFile("audio", "../../ignored.wav")
			Expect(e).NotTo(HaveOccurred())
			_, e = io.WriteString(f, p[1])
			Expect(e).NotTo(HaveOccurred())
		} else {
			Expect(w.WriteField(p[0], p[1])).To(Succeed())
		}
	}
	Expect(w.Close()).To(Succeed())
	r := httptest.NewRequest("POST", "/", &b)
	r.Header.Set("Content-Type", w.FormDataContentType())
	return r
}
func checkCode(err error, code int) {
	Expect(err).To(HaveOccurred())
	var e *Error
	Expect(errors.As(err, &e)).To(BeTrue())
	Expect(e.Code).To(Equal(code))
	Expect(e.Stage).To(Equal("validation"))
}

type shortReader struct {
	r io.Reader
	n int
}

func (r *shortReader) Read(p []byte) (int, error) {
	if len(p) > r.n {
		p = p[:r.n]
	}
	return r.r.Read(p)
}

type repeats struct {
	left  int64
	reads int64
}

func (r *repeats) Read(p []byte) (int, error) {
	if r.left == 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > r.left {
		p = p[:r.left]
	}
	for i := range p {
		p[i] = ' '
	}
	r.left -= int64(len(p))
	r.reads += int64(len(p))
	return len(p), nil
}

var _ = Describe("streaming multipart", func() {
	It("transfers ownership and ignores the filename; cover full remains allowed", func() {
		s := &sink{}
		clean := 0
		p, _ := New(func(string) error { return nil }, storeFunc(func(context.Context) (*OwnedUpload, error) {
			return &OwnedUpload{Ref: "trusted", Writer: s, Cleanup: func() music.CleanupTask { clean++; return ack() }}, nil
		}))
		r := uploadRequest([2]string{"audio", "recording"}, [2]string{"model", "g"}, [2]string{"transcription_model", "t"}, [2]string{"style", "s"}, [2]string{"lyrics", ""}, [2]string{"params", `{"cot":"full"}`})
		r.Body = io.NopCloser(&shortReader{r: r.Body, n: 3})
		r.ContentLength = -1
		got, err := p.ParseCover(r)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Generation.Params.COT).To(Equal("full"))
		Expect(got.Generation.Params.Guidance).To(Equal(3.0))
		Expect(got.Generation.Params.Seed).To(Equal(int64(0)))
		Expect(s.n).To(Equal(9))
		Expect(s.closed).To(BeTrue())
		Expect(clean).To(Equal(0))
		Expect(got.Audio.Ref).To(Equal("trusted"))
		release(got.Audio)
		Expect(clean).To(Equal(1))
	})
	It("cleans partial Create failures and waits for definitive ACK", func() {
		s := &sink{}
		released := make(chan error, 2)
		started := make(chan struct{})
		done := make(chan error, 1)
		p, _ := New(func(string) error { return nil }, storeFunc(func(context.Context) (*OwnedUpload, error) {
			return &OwnedUpload{Ref: "partial", Writer: s, Cleanup: func() music.CleanupTask { close(started); return music.CleanupTask{Released: released} }}, errors.New("secret path")
		}))
		r := uploadRequest([2]string{"audio", "x"})
		go func() { _, err := p.ParseTranscription(r); done <- err }()
		Eventually(started).Should(BeClosed())
		Consistently(done, 20*time.Millisecond).ShouldNot(Receive())
		released <- errors.New("retry")
		Consistently(done, 20*time.Millisecond).ShouldNot(Receive())
		released <- nil
		var err error
		Eventually(done).Should(Receive(&err))
		checkCode(err, 503)
		Expect(s.closed).To(BeTrue())
		Expect(err.Error()).NotTo(ContainSubstring("secret"))
	})
	It("cleans rejected duplicate, unknown, invalid fields and failed writes", func() {
		for _, extra := range [][2]string{{"model", "again"}, {"url", "file:///bad"}, {"style", "no"}, {"audio", "second"}} {
			s := &sink{}
			clean := false
			p, _ := New(func(string) error { return nil }, storeFunc(func(context.Context) (*OwnedUpload, error) {
				return &OwnedUpload{Ref: "x", Writer: s, Cleanup: func() music.CleanupTask { clean = true; return ack() }}, nil
			}))
			_, err := p.ParseTranscription(uploadRequest([2]string{"audio", "abc"}, [2]string{"model", "m"}, extra))
			checkCode(err, 400)
			Expect(clean).To(BeTrue())
			Expect(s.closed).To(BeTrue())
		}
	})
	It("enforces individual text and file ceilings without writing the extra byte", func() {
		s := &sink{}
		var clean atomic.Bool
		p, _ := New(func(string) error { return nil }, storeFunc(func(context.Context) (*OwnedUpload, error) {
			return &OwnedUpload{Ref: "x", Writer: s, Cleanup: func() music.CleanupTask { clean.Store(true); return ack() }}, nil
		}))
		r := uploadRequest([2]string{"model", strings.Repeat("m", 129)})
		_, err := p.ParseTranscription(r)
		checkCode(err, 413)
		// Stream synthetic audio, retaining only the multipart framing.
		prefix := "--b\r\nContent-Disposition: form-data; name=\"audio\"; filename=\"ignored\"\r\n\r\n"
		payload := &repeats{left: AudioLimit + 1}
		r = httptest.NewRequest("POST", "/", io.MultiReader(strings.NewReader(prefix), payload, strings.NewReader("\r\n--b--\r\n")))
		r.Header.Set("Content-Type", "multipart/form-data; boundary=b")
		r.ContentLength = -1
		_, err = p.ParseTranscription(r)
		checkCode(err, 413)
		Expect(s.n).To(Equal(AudioLimit))
		Expect(clean.Load()).To(BeTrue())
	})
	It("rejects total chunked ceiling including epilogue before unbounded buffering", func() {
		s := &sink{}
		clean := false
		p, _ := New(func(string) error { return nil }, storeFunc(func(context.Context) (*OwnedUpload, error) {
			return &OwnedUpload{Ref: "x", Writer: s, Cleanup: func() music.CleanupTask { clean = true; return ack() }}, nil
		}))
		r := uploadRequest([2]string{"audio", "x"}, [2]string{"model", "m"})
		tail := &repeats{left: MultipartLimit + 10000}
		r.Body = io.NopCloser(io.MultiReader(r.Body, tail))
		r.ContentLength = -1
		_, err := p.ParseTranscription(r)
		checkCode(err, 413)
		Expect(tail.reads).To(BeNumerically("<=", MultipartLimit+1))
		Expect(clean).To(BeTrue())
	})
	It("streams to storage before permitting later source bytes", func() {
		s := &sink{}
		p, _ := New(func(string) error { return nil }, storeFunc(func(context.Context) (*OwnedUpload, error) {
			return &OwnedUpload{Ref: "x", Writer: s, Cleanup: ack}, nil
		}))
		r := uploadRequest([2]string{"audio", strings.Repeat("a", 12000)}, [2]string{"model", "m"})
		data, _ := io.ReadAll(r.Body)
		gate := &gatedReader{data: data, sink: s}
		r.Body = io.NopCloser(gate)
		r.ContentLength = -1
		got, err := p.ParseTranscription(r)
		Expect(err).NotTo(HaveOccurred())
		Expect(gate.checked).To(BeTrue())
		release(got.Audio)
	})
})

type gatedReader struct {
	data    []byte
	offset  int
	sink    *sink
	checked bool
}

func (g *gatedReader) Read(p []byte) (int, error) {
	if g.offset >= 8192 && !g.checked {
		g.checked = true
		if g.sink.n == 0 {
			return 0, errors.New("buffered audio before writing")
		}
	}
	if g.offset == len(g.data) {
		return 0, io.EOF
	}
	if len(p) > 1024 {
		p = p[:1024]
	}
	n := copy(p, g.data[g.offset:])
	g.offset += n
	return n, nil
}

var _ = Describe("multipart rejection boundaries", func() {
	It("rejects missing, empty, corrupt multipart and forbidden cover params with cleanup", func() {
		for _, tc := range []struct {
			parts [][2]string
			cover bool
			code  int
		}{
			{[][2]string{{"model", "m"}}, false, 400},
			{[][2]string{{"audio", ""}, {"model", "m"}}, false, 422},
			{[][2]string{{"audio", "x"}}, false, 400},
			{[][2]string{{"audio", "x"}, {"model", "g"}, {"transcription_model", "t"}, {"style", "s"}}, true, 400},
			{[][2]string{{"audio", "x"}, {"model", "g"}, {"transcription_model", "t"}, {"style", "s"}, {"lyrics", ""}, {"params", `{"cot":"off"}`}}, true, 422},
			{[][2]string{{"audio", "x"}, {"model", "g"}, {"transcription_model", "t"}, {"style", "s"}, {"lyrics", ""}, {"params", `{"src":"/secret"}`}}, true, 422},
			{[][2]string{{"audio", "x"}, {"model", "g"}, {"transcription_model", "t"}, {"style", "s"}, {"lyrics", ""}, {"params", strings.Repeat(" ", 2049)}}, true, 413},
			{[][2]string{{"audio", "x"}, {"model", string([]byte{255})}}, false, 400},
		} {
			s := &sink{}
			clean := false
			p, _ := New(func(string) error { return nil }, storeFunc(func(context.Context) (*OwnedUpload, error) {
				return &OwnedUpload{Ref: "x", Writer: s, Cleanup: func() music.CleanupTask { clean = true; return ack() }}, nil
			}))
			r := uploadRequest(tc.parts...)
			var err error
			if tc.cover {
				_, err = p.ParseCover(r)
			} else {
				_, err = p.ParseTranscription(r)
			}
			checkCode(err, tc.code)
			if len(tc.parts) > 0 && tc.parts[0][0] == "audio" {
				Expect(clean).To(BeTrue())
				Expect(s.closed).To(BeTrue())
			}
		}
		p, _ := New(func(string) error { return nil }, nil)
		r := httptest.NewRequest("POST", "/", strings.NewReader("not multipart"))
		r.Header.Set("Content-Type", "multipart/form-data; boundary=x")
		_, err := p.ParseTranscription(r)
		checkCode(err, 400)
	})
	It("reports sink failures as local errors and releases ownership", func() {
		s := &sink{fail: true}
		clean := false
		p, _ := New(func(string) error { return nil }, storeFunc(func(context.Context) (*OwnedUpload, error) {
			return &OwnedUpload{Ref: "x", Writer: s, Cleanup: func() music.CleanupTask { clean = true; return ack() }}, nil
		}))
		_, err := p.ParseTranscription(uploadRequest([2]string{"audio", "x"}, [2]string{"model", "m"}))
		checkCode(err, 500)
		Expect(clean).To(BeTrue())
		Expect(s.closed).To(BeTrue())
	})
	It("honors earlier cancellation without inventing 499 or a new deadline", func() {
		p, _ := New(func(string) error { return nil }, nil)
		r := uploadRequest([2]string{"model", "m"})
		ctx, cancel := context.WithCancel(r.Context())
		cancel()
		_, err := p.ParseTranscription(r.WithContext(ctx))
		Expect(errors.Is(err, context.Canceled)).To(BeTrue())
		ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()
		_, err = p.ParseTranscription(r.WithContext(ctx))
		checkCode(err, 504)
	})
	It("bounds announced multipart size before reading or allocating storage", func() {
		source := &repeats{left: MultipartLimit + 1}
		p, _ := New(func(string) error { return nil }, nil)
		r := httptest.NewRequest("POST", "/", source)
		r.ContentLength = MultipartLimit + 1
		r.Header.Set("Content-Type", "multipart/form-data; boundary=x")
		_, err := p.ParseTranscription(r)
		checkCode(err, 413)
		Expect(source.reads).To(BeZero())
	})
})

// A trusted sink may have asynchronous writers, but Close must join them.
type joiningSink struct {
	entered chan struct{}
	allow   chan struct{}
	closed  chan struct{}
}

func (s *joiningSink) Write(p []byte) (int, error) { return len(p), nil }
func (s *joiningSink) Close() error {
	select {
	case <-s.closed:
		return nil
	default:
	}
	close(s.entered)
	<-s.allow
	close(s.closed)
	return nil
}

var _ = Describe("upload close boundary", func() {
	It("cannot return ownership before Close has joined writers", func() {
		s := &joiningSink{entered: make(chan struct{}), allow: make(chan struct{}), closed: make(chan struct{})}
		done := make(chan error, 1)
		p, _ := New(func(string) error { return nil }, storeFunc(func(context.Context) (*OwnedUpload, error) {
			return &OwnedUpload{Ref: "x", Writer: s, Cleanup: ack}, nil
		}))
		r := uploadRequest([2]string{"audio", "x"}, [2]string{"model", "m"})
		go func() { _, err := p.ParseTranscription(r); done <- err }()
		Eventually(s.entered).Should(BeClosed())
		Consistently(done, 20*time.Millisecond).ShouldNot(Receive())
		close(s.allow)
		Eventually(done).Should(Receive(BeNil()))
		Expect(s.closed).To(BeClosed())
	})
})

type failingCloseSink struct{ joiningSink }

func (s *failingCloseSink) Close() error {
	_ = s.joiningSink.Close()
	return errors.New("private storage path")
}

var _ = Describe("failed Close ownership", func() {
	It("joins writers before cleanup and cannot return before explicit release ACK", func() {
		s := &failingCloseSink{joiningSink{entered: make(chan struct{}), allow: make(chan struct{}), closed: make(chan struct{})}}
		cleanupStarted := make(chan struct{})
		released := make(chan error, 2)
		done := make(chan error, 1)
		p, _ := New(func(string) error { return nil }, storeFunc(func(context.Context) (*OwnedUpload, error) {
			return &OwnedUpload{Ref: "partial", Writer: s, Cleanup: func() music.CleanupTask {
				select {
				case <-s.closed:
				default:
					panic("cleanup before writer join")
				}
				close(cleanupStarted)
				return music.CleanupTask{Released: released}
			}}, nil
		}))
		r := uploadRequest([2]string{"audio", "x"}, [2]string{"model", "m"})
		go func() { _, err := p.ParseTranscription(r); done <- err }()
		Eventually(s.entered).Should(BeClosed())
		Consistently(cleanupStarted, 20*time.Millisecond).ShouldNot(BeClosed())
		Consistently(done, 20*time.Millisecond).ShouldNot(Receive())
		close(s.allow)
		Eventually(cleanupStarted).Should(BeClosed())
		Consistently(done, 20*time.Millisecond).ShouldNot(Receive())
		released <- errors.New("retry cleanup")
		Consistently(done, 20*time.Millisecond).ShouldNot(Receive())
		released <- nil
		var err error
		Eventually(done).Should(Receive(&err))
		checkCode(err, 500)
		Expect(err.(*Error).Reason).To(Equal("upload_failed"))
		Expect(err.Error()).NotTo(ContainSubstring("private"))
		Expect(s.closed).To(BeClosed())
	})
	It("rejects unpaired surrogate escapes in multipart params and cleans the upload", func() {
		for _, params := range []string{`{"cot":"\ud800"}`, `{"\udc00":1}`, `{"other":{"nested":"\ud800"}}`} {
			clean := false
			s := &sink{}
			p, _ := New(func(string) error { return nil }, storeFunc(func(context.Context) (*OwnedUpload, error) {
				return &OwnedUpload{Ref: "x", Writer: s, Cleanup: func() music.CleanupTask { clean = true; return ack() }}, nil
			}))
			_, err := p.ParseCover(uploadRequest([2]string{"audio", "x"}, [2]string{"model", "g"}, [2]string{"transcription_model", "t"}, [2]string{"style", "s"}, [2]string{"lyrics", ""}, [2]string{"params", params}))
			checkCode(err, 400)
			Expect(err.(*Error).Reason).To(Equal("invalid_unicode"))
			Expect(clean).To(BeTrue())
			Expect(s.closed).To(BeTrue())
		}
	})
})
