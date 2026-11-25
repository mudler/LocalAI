package collections

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/privacy"
)

type levelSeen struct{ level privacy.Level }

func (s *levelSeen) RoundTrip(req *http.Request) (*http.Response, error) {
	s.level = privacy.FromContext(req.Context())
	body, _ := io.ReadAll(req.Body)
	rec := httptest.NewRecorder()
	_, _ = rec.Write(body)
	return rec.Result(), nil
}

var _ = Describe("the search of a zero-retention request", func() {
	post := func(t http.RoundTripper, body string) (privacy.Level, string) {
		seen := t.(privateTransport).next.(*levelSeen)
		req, _ := http.NewRequest(http.MethodPost, "http://in.process/v1/embeddings", strings.NewReader(body))
		resp, err := t.RoundTrip(req)
		Expect(err).NotTo(HaveOccurred())
		echoed, _ := io.ReadAll(resp.Body)
		return seen.level, string(echoed)
	}

	It("puts the level on the calls that carry a held query, and only on them", func() {
		s := &Service{inProcess: true}
		t := privateTransport{next: &levelSeen{}, queries: &s.private}
		release, err := s.privateSearch(privacy.WithLevel(context.Background(), privacy.ZeroRetention), `where is "the" key?`)
		Expect(err).NotTo(HaveOccurred())

		level, echoed := post(t, `{"model":"e","input":"where is \"the\" key?"}`)
		Expect(level).To(Equal(privacy.ZeroRetention))
		Expect(echoed).To(Equal(`{"model":"e","input":"where is \"the\" key?"}`), "the body reaches the route unchanged")
		level, _ = post(t, `{"model":"e","input":"another person's search"}`)
		Expect(level).To(Equal(privacy.Normal))

		release()
		level, _ = post(t, `{"model":"e","input":"where is \"the\" key?"}`)
		Expect(level).To(Equal(privacy.Normal), "the query is no longer held")
	})

	It("forwards a body over the limit whole while a zero-retention search is held", func() {
		s := &Service{inProcess: true}
		t := privateTransport{next: &levelSeen{}, queries: &s.private}
		release, err := s.privateSearch(privacy.WithLevel(context.Background(), privacy.ZeroRetention), "held query")
		Expect(err).NotTo(HaveOccurred())
		defer release()

		big := strings.Repeat("0123456789abcdef", maxPrivateBody/16+4096) + "END-OF-BODY"
		Expect(len(big)).To(BeNumerically(">", maxPrivateBody))
		req, _ := http.NewRequest(http.MethodPost, "http://in.process/v1/embeddings", strings.NewReader(big))
		resp, err := t.RoundTrip(req)
		Expect(err).NotTo(HaveOccurred())
		echoed, _ := io.ReadAll(resp.Body)
		Expect(len(echoed)).To(Equal(len(big)), "nothing of the body is cut")
		Expect(string(echoed) == big).To(BeTrue())
		Expect(t.next.(*levelSeen).level).To(Equal(privacy.Normal), "a body without the held query keeps its level")
	})

	It("holds nothing for a request that keeps its traces", func() {
		s := &Service{inProcess: true}
		release, err := s.privateSearch(privacy.WithLevel(context.Background(), privacy.MemoryOff), "q")
		Expect(err).NotTo(HaveOccurred())
		defer release()
		Expect(s.private.empty()).To(BeTrue())
	})

	It("refuses the search when the collections call the API over the network", func() {
		s := &Service{}
		_, err := s.privateSearch(privacy.WithLevel(context.Background(), privacy.ZeroRetention), "q")
		Expect(errors.Is(err, ErrSearchNotPrivate)).To(BeTrue())
	})
})
