package httpapi

import (
	"context"
	"encoding/json"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"net/http"
	"net/http/httptest"
)

var _ = Describe("Load cancellation HTTP mapping", func() {
	It("preserves generation and pending outcome", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			Expect(r.Method).To(Equal("POST"))
			Expect(r.URL.Path).To(Equal("/api/models/model/load-cancel"))
			var body map[string]string
			Expect(json.NewDecoder(r.Body).Decode(&body)).To(Succeed())
			Expect(body).To(Equal(map[string]string{"job_id": "generation"}))
			w.WriteHeader(202)
			_, _ = w.Write([]byte(`{"model":"model","job_id":"generation","state":"uncertain"}`))
		}))
		defer server.Close()
		result, err := New(server.URL, "").CancelModelLoad(context.Background(), "model", "generation")
		Expect(err).NotTo(HaveOccurred())
		Expect(result.State).To(Equal("uncertain"))
		Expect(result.JobID).To(Equal("generation"))
	})
})
