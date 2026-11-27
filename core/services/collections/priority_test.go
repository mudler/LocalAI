package collections

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/priority"
)

type classSeen struct {
	class priority.Class
	set   bool
}

func (s *classSeen) RoundTrip(req *http.Request) (*http.Response, error) {
	s.class, s.set = priority.FromContext(req.Context())
	body, _ := io.ReadAll(req.Body)
	rec := httptest.NewRecorder()
	_, _ = rec.Write(body)
	return rec.Result(), nil
}

var _ = Describe("the priority class of the collections' calls", func() {
	post := func(t privateTransport, body string) (priority.Class, bool, string) {
		seen := t.next.(*classSeen)
		req, _ := http.NewRequest(http.MethodPost, "http://in.process/v1/embeddings", strings.NewReader(body))
		resp, err := t.RoundTrip(req)
		Expect(err).NotTo(HaveOccurred())
		echoed, _ := io.ReadAll(resp.Body)
		return seen.class, seen.set, string(echoed)
	}

	It("sends indexing calls as batch work", func() {
		s := &Service{inProcess: true}
		t := privateTransport{next: &classSeen{}, queries: &s.private, searches: &s.searches}
		class, set, _ := post(t, `{"model":"e","input":"a chunk of a document"}`)
		Expect(set).To(BeTrue())
		Expect(class).To(Equal(priority.Batch))
	})

	It("leaves the calls of a running search at the default class, and only them", func() {
		s := &Service{inProcess: true}
		t := privateTransport{next: &classSeen{}, queries: &s.private, searches: &s.searches}
		release := s.searches.hold("where is the key?", 0)

		class, set, echoed := post(t, `{"model":"e","input":"where is the key?"}`)
		Expect(set).To(BeFalse())
		Expect(class).To(Equal(priority.Default))
		Expect(echoed).To(Equal(`{"model":"e","input":"where is the key?"}`), "the body reaches the route unchanged")

		class, _, _ = post(t, `{"model":"e","input":"a chunk indexed meanwhile"}`)
		Expect(class).To(Equal(priority.Batch))

		release()
		class, _, _ = post(t, `{"model":"e","input":"where is the key?"}`)
		Expect(class).To(Equal(priority.Batch), "the search is over")
	})
})
