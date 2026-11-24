// SPDX-License-Identifier: MIT
package protocol_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/addons/sdk/idle"
	. "github.com/mudler/LocalAI/pkg/addons/sdk/protocol"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// clientRuntime answers one unary route and one stream.
type clientRuntime struct {
	idle.Runtime
	feed chan string
}

func (d clientRuntime) ClientCall(_ context.Context, r contract.ClientRequest) (contract.ClientResponse, error) {
	b, _ := json.Marshal(map[string]string{"route": r.RouteID, "client": r.Client.ID})
	return contract.ClientResponse{Status: 200, ContentType: "application/json", Body: b}, nil
}

func (d clientRuntime) ClientStream(ctx context.Context, _ contract.ClientRequest) (io.ReadCloser, error) {
	pr, pw := io.Pipe()
	go func() {
		defer func() { _ = pw.Close() }()
		for {
			select {
			case <-ctx.Done():
				return
			case s, ok := <-d.feed:
				if !ok {
					return
				}
				_, _ = pw.Write([]byte(s))
			}
		}
	}()
	return pr, nil
}

var _ = Describe("Handler and client routes", func() {
	start := func(rt contract.Runtime) string {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		st := Startup{Registration: Registration{AddonID: "demo", Generation: 1, Incarnation: "inc", Protocol: 1}, Credential: "secret", Limits: contract.DefaultLimits()}
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		go func() { _ = Serve(ctx, l, st, rt) }()
		return "http://" + l.Addr().String()
	}
	post := func(url, path string, auth bool) (*http.Response, error) {
		b, _ := json.Marshal(contract.ClientRequest{RouteID: "r", Method: "GET", Path: "/r", Client: contract.ClientIdentity{ID: "d1", Name: "n"}})
		req, _ := http.NewRequest("POST", url+path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		if auth {
			req.Header.Set("Authorization", "Bearer secret")
			req.Header.Set("X-LocalAI-Incarnation", "inc")
		}
		return http.DefaultClient.Do(req)
	}

	It("serves /client and refuses a call without the credential", func() {
		url := start(clientRuntime{})
		resp, err := post(url, "/client", true)
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()
		Expect(resp.StatusCode).To(Equal(200))
		var out contract.ClientResponse
		Expect(json.NewDecoder(resp.Body).Decode(&out)).To(Succeed())
		Expect(string(out.Body)).To(ContainSubstring(`"client":"d1"`))
		resp2, err := post(url, "/client", false)
		Expect(err).ToNot(HaveOccurred())
		_ = resp2.Body.Close()
		Expect(resp2.StatusCode).To(Equal(401))
	})

	It("has no client route for a runtime that declared none", func() {
		url := start(idle.Runtime{})
		for _, p := range []string{"/client", "/client/stream"} {
			resp, err := post(url, p, true)
			Expect(err).ToNot(HaveOccurred())
			_ = resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(404), p)
		}
	})

	It("keeps a stream open past the read timeout of the server and flushes each event", func() {
		feed := make(chan string)
		url := start(clientRuntime{feed: feed})
		resp, err := post(url, "/client/stream", true)
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()
		Expect(resp.Header.Get("Content-Type")).To(Equal("text/event-stream"))
		rd := bufio.NewReader(resp.Body)
		feed <- "id: 1\ndata: a\n\n"
		line, err := rd.ReadString('\n')
		Expect(err).ToNot(HaveOccurred())
		Expect(line).To(Equal("id: 1\n"))
		// The server reads a request for two seconds at most; the stream must
		// outlive that.
		time.Sleep(2500 * time.Millisecond)
		feed <- "id: 2\ndata: b\n\n"
		var got strings.Builder
		for !strings.Contains(got.String(), "id: 2\n") {
			l, err := rd.ReadString('\n')
			Expect(err).ToNot(HaveOccurred())
			got.WriteString(l)
		}
	})
})
