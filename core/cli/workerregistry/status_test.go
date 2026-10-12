// SPDX-License-Identifier: MIT
package workerregistry

import (
	"context"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"net/http"
	"net/http/httptest"
	"sync"
)

var _ = Describe("Worker status heartbeat", func() {
	It("rejects non-2xx acknowledgements without echoing the response", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403); _, _ = w.Write([]byte("secret")) }))
		defer server.Close()
		c := RegistrationClient{FrontendURL: server.URL}
		err := c.Heartbeat(context.Background(), "node", nil)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).NotTo(ContainSubstring("secret"))
	})
})

var _ = Describe("Worker status registration", func() {
	It("observes static retry approval and errors rather than discarding status", func() {
		var mu sync.Mutex
		response := `{"id":"node","status":"pending"}`
		code := http.StatusCreated
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			w.WriteHeader(code)
			_, _ = w.Write([]byte(response))
		}))
		defer server.Close()
		outcome := ""
		failed := false
		client := RegistrationClient{FrontendURL: server.URL, ObserveRegistration: func(res *RegisterResponse, err error) {
			failed = err != nil
			if res != nil {
				outcome = res.Status
			}
		}}
		_, _, _, _, err := client.RegisterWithRetry(context.Background(), nil, 1)
		Expect(err).NotTo(HaveOccurred())
		Expect(outcome).To(Equal("pending"))
		mu.Lock()
		response = `{"id":"node","status":"healthy"}`
		mu.Unlock()
		_, _, _, _, err = client.RegisterWithRetry(context.Background(), nil, 1)
		Expect(err).NotTo(HaveOccurred())
		Expect(outcome).To(Equal("healthy"))
		mu.Lock()
		code = 403
		mu.Unlock()
		_, err = client.RegisterFull(context.Background(), nil)
		Expect(err).To(HaveOccurred())
		Expect(failed).To(BeTrue())
	})
})
