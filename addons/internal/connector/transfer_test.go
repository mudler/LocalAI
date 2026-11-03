// SPDX-License-Identifier: MIT
package connector_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"

	"github.com/mudler/LocalAI/addons/internal/connector"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// memHost is the host side of a handle in memory: it keeps what was put, and
// serves what is to be got. fail makes the next calls fail.
type memHost struct {
	got      bytes.Buffer
	final    bool
	src      []byte
	fail     int
	max      int64
	tooLarge bool
	calls    int
}

func (h *memHost) Do(_ context.Context, _, op string, args, out any) error {
	h.calls++
	if h.fail > 0 {
		h.fail--
		return errors.New("network")
	}
	switch a := args.(type) {
	case contract.ConnectorFilePut:
		if h.tooLarge || (h.max > 0 && int64(h.got.Len()+len(a.Data)) > h.max) {
			return &contract.HostError{Code: contract.HostTooLarge, Message: "too large"}
		}
		if a.Offset == int64(h.got.Len()) {
			h.got.Write(a.Data)
			h.final = a.Final
		}
		*(out.(*contract.ConnectorFilePutResult)) = contract.ConnectorFilePutResult{Received: int64(h.got.Len())}
	case contract.ConnectorFileGet:
		end := min(a.Offset+a.Length, int64(len(h.src)))
		*(out.(*contract.ConnectorFileGetResult)) = contract.ConnectorFileGetResult{Data: h.src[a.Offset:end], EOF: end == int64(len(h.src))}
	}
	return nil
}

var _ = Describe("file transfer by handle", func() {
	tr := &contract.ConnectorTransfer{Handle: "h", MaxBytes: 1 << 30}

	It("puts a stream in chunks and ends it, also when its size is a multiple of the chunk", func() {
		for _, n := range []int{1, contract.TransferChunkBytes - 1, contract.TransferChunkBytes, 3*contract.TransferChunkBytes + 7} {
			h := &memHost{}
			data := bytes.Repeat([]byte{'x'}, n)
			sent, err := connector.Put(context.Background(), h, tr, bytes.NewReader(data))
			Expect(err).NotTo(HaveOccurred())
			Expect(sent).To(BeEquivalentTo(n))
			Expect(h.got.Bytes()).To(Equal(data))
			Expect(h.final).To(BeTrue())
		}
	})

	It("sends a chunk again after a failed call, once the host says where it is", func() {
		h := &memHost{fail: 2}
		_, err := connector.Put(context.Background(), h, tr, strings.NewReader("hello"))
		Expect(err).NotTo(HaveOccurred())
		Expect(h.got.String()).To(Equal("hello"))
		h = &memHost{fail: 3}
		_, err = connector.Put(context.Background(), h, tr, strings.NewReader("hello"))
		Expect(err).To(HaveOccurred(), "three tries, then the transfer fails")
	})

	It("reports a file over the bound of the handle as too large", func() {
		_, err := connector.Put(context.Background(), &memHost{tooLarge: true}, tr, strings.NewReader("hello"))
		Expect(err).To(MatchError(connector.ErrTooLarge))
	})

	It("needs a host", func() {
		_, err := connector.Put(context.Background(), nil, tr, strings.NewReader("x"))
		Expect(err).To(MatchError(connector.ErrNoHost))
		_, err = io.ReadAll(connector.Get(context.Background(), nil, tr))
		Expect(err).To(MatchError(connector.ErrNoHost))
	})

	It("gets a file chunk by chunk as a reader", func() {
		src := bytes.Repeat([]byte("abc"), contract.TransferChunkBytes)
		h := &memHost{src: src}
		got, err := io.ReadAll(connector.Get(context.Background(), h, tr))
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(src))
		Expect(h.calls).To(Equal(3), "one call per chunk")
	})
})

// streaming is a Platform that streams.
type streaming struct {
	script
	body    string
	size    int64
	upload  []byte
	gotName string
}

func (s *streaming) MediaStream(_ context.Context, _, _ string, _ int64) (connector.MediaStream, error) {
	return connector.MediaStream{Name: "../a.txt", MIME: "text/plain", Size: s.size, Body: io.NopCloser(strings.NewReader(s.body))}, nil
}

func (s *streaming) SendStream(_ context.Context, req contract.ConnectorRequest, body io.Reader) (string, error) {
	s.upload, _ = io.ReadAll(body)
	s.gotName = req.Transfer.Name
	return "f1", nil
}

var _ = Describe("the connector route with a handle", func() {
	serve := func(p connector.Platform, h connector.Host, req contract.ConnectorRequest) (contract.ConnectorResponse, error) {
		req.Kind, req.Token = "chat", "t"
		return connector.ServeWith(context.Background(), p, h, req)
	}

	It("streams the file of an op media to the host and answers with the name", func() {
		h := &memHost{}
		out, err := serve(&streaming{body: "hello", size: 5}, h, contract.ConnectorRequest{Op: contract.ConnectorOpMedia, Media: "m", Transfer: &contract.ConnectorTransfer{Handle: "h", MaxBytes: 100}})
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Error).To(BeNil())
		Expect(out.Received).To(Equal(&contract.ConnectorReceived{Name: "a.txt", MIME: "text/plain"}))
		Expect(h.got.String()).To(Equal("hello"))
	})

	It("refuses a file the platform says is over the bound, before any byte moves", func() {
		h := &memHost{}
		out, _ := serve(&streaming{body: "hello", size: 500}, h, contract.ConnectorRequest{Op: contract.ConnectorOpMedia, Media: "m", Transfer: &contract.ConnectorTransfer{Handle: "h", MaxBytes: 100}})
		Expect(out.Error.Code).To(Equal(contract.ConnectorTooLarge))
		Expect(h.calls).To(BeZero())
	})

	It("stops a platform that states a small size and sends more", func() {
		h := &memHost{max: 100}
		out, _ := serve(&streaming{body: strings.Repeat("x", 500), size: 5}, h, contract.ConnectorRequest{Op: contract.ConnectorOpMedia, Media: "m", Transfer: &contract.ConnectorTransfer{Handle: "h", MaxBytes: 100}})
		Expect(out.Error.Code).To(Equal(contract.ConnectorTooLarge))
	})

	It("sends the file of an op send_file from the host's chunks", func() {
		p := &streaming{}
		h := &memHost{src: []byte("report body")}
		out, err := serve(p, h, contract.ConnectorRequest{Op: contract.ConnectorOpSendFile, Chat: "c", Transfer: &contract.ConnectorTransfer{Handle: "h", Name: "r.txt", MIME: "text/plain", Size: 11}})
		Expect(err).NotTo(HaveOccurred())
		Expect(out.MessageID).To(Equal("f1"))
		Expect(string(p.upload)).To(Equal("report body"))
		Expect(p.gotName).To(Equal("r.txt"))
	})

	It("refuses a handle on a platform that cannot stream, and a name that is not safe", func() {
		_, err := serve(&script{}, &memHost{}, contract.ConnectorRequest{Op: contract.ConnectorOpMedia, Media: "m", Transfer: &contract.ConnectorTransfer{Handle: "h", MaxBytes: 1}})
		Expect(err).To(MatchError(contract.ErrRefused))
		_, err = serve(&streaming{}, &memHost{}, contract.ConnectorRequest{Op: contract.ConnectorOpSendFile, Transfer: &contract.ConnectorTransfer{Handle: "h", Name: "../x", Size: 1}})
		Expect(err).To(MatchError(contract.ErrRefused))
	})
})
