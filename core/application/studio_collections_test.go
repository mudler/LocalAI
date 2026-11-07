// SPDX-License-Identifier: MIT
package application

import (
	"context"
	"io"
	"net/http"
	"path"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/collections"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// roundTripFunc answers the embedding calls of the collections in memory, so
// the real chromem engine runs without a model or a network.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func embeddingClient() *http.Client {
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := `{"data":[{"embedding":[1,0,0],"index":0}],"model":"embed","usage":{}}`
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
}

// The Add and Remove of the chats' collection adapter run against the real
// collections service and its chromem engine: only the embedding call is
// answered in memory. They need no model, no database and no network, so they
// run in CI as they are.
var _ = Describe("the studio collections adapter on the real collections service", func() {
	const (
		alice = "11111111-1111-4111-8111-111111111111"
		bob   = "22222222-2222-4222-8222-222222222222"
	)
	var (
		ctx = context.Background()
		app *Application
		svc *collections.Service
		col studioCollections
	)

	BeforeEach(func() {
		appCtx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		state, err := system.GetSystemState(system.WithModelPath(GinkgoT().TempDir()), system.WithBackendPath(GinkgoT().TempDir()))
		Expect(err).NotTo(HaveOccurred())
		app, err = New(config.WithContext(appCtx), config.WithSystemState(state), config.DisableMetricsEndpoint,
			config.WithDisableStats(true))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(app.Shutdown()).To(Succeed()) })

		cfg := config.NewApplicationConfig()
		cfg.DataPath = GinkgoT().TempDir()
		cfg.Collections.VectorEngine = "chromem"
		cfg.Collections.EmbeddingModel = "embed"
		svc, err = collections.New(cfg, nil, collections.WithHTTPClient(embeddingClient()))
		Expect(err).NotTo(HaveOccurred())
		app.collectionsService.Store(svc)
		col = studioCollections{a: app}
		Expect(svc.CreateForUser(alice, "manuals")).To(Succeed())
	})

	baseNames := func(user, collection string) []string {
		entries, err := svc.ListEntriesForUser(user, collection)
		Expect(err).NotTo(HaveOccurred())
		var out []string
		for _, e := range entries {
			out = append(out, path.Base(e))
		}
		return out
	}

	It("stores the text as one entry that the collection can read and search", func() {
		Expect(col.Add(ctx, alice, "manuals", "reset-steps-1a2b3c4d.md", "# Reset\n\nHold the button for ten seconds.\n")).To(Succeed())
		Expect(baseNames(alice, "manuals")).To(Equal([]string{"reset-steps-1a2b3c4d.md"}))

		entries, _ := svc.ListEntriesForUser(alice, "manuals")
		content, chunks, err := svc.EntryContentForUser(alice, "manuals", entries[0])
		Expect(err).NotTo(HaveOccurred())
		Expect(content).To(ContainSubstring("Hold the button for ten seconds."))
		Expect(chunks).To(BeNumerically(">=", 1))

		hits, err := col.Search(ctx, alice, "manuals", "how do I reset it", 3)
		Expect(err).NotTo(HaveOccurred())
		Expect(hits).NotTo(BeEmpty())
		Expect(hits[0].Content).To(ContainSubstring("Hold the button"))
	})

	It("removes the entry by its file name and keeps the others", func() {
		Expect(col.Add(ctx, alice, "manuals", "a-11111111.md", "first text")).To(Succeed())
		Expect(col.Add(ctx, alice, "manuals", "b-22222222.md", "second text")).To(Succeed())
		Expect(baseNames(alice, "manuals")).To(ConsistOf("a-11111111.md", "b-22222222.md"))

		Expect(col.Remove(ctx, alice, "manuals", "a-11111111.md")).To(Succeed())
		Expect(baseNames(alice, "manuals")).To(Equal([]string{"b-22222222.md"}))
		hits, err := col.Search(ctx, alice, "manuals", "text", 5)
		Expect(err).NotTo(HaveOccurred())
		for _, h := range hits {
			Expect(h.Content).NotTo(ContainSubstring("first text"), "the removed passages are gone from the search")
		}
	})

	It("treats an entry that is gone already as done, so an undo can be repeated", func() {
		Expect(col.Add(ctx, alice, "manuals", "a-11111111.md", "text")).To(Succeed())
		Expect(col.Remove(ctx, alice, "manuals", "a-11111111.md")).To(Succeed())
		Expect(col.Remove(ctx, alice, "manuals", "a-11111111.md")).To(Succeed())
		Expect(col.Remove(ctx, alice, "manuals", "never-added.md")).To(Succeed())
		Expect(baseNames(alice, "manuals")).To(BeEmpty())
	})

	It("answers ErrNoCollection for a collection that does not exist", func() {
		Expect(col.Add(ctx, alice, "missing", "a.md", "text")).To(MatchError(studio.ErrNoCollection))
		Expect(col.Remove(ctx, alice, "missing", "a.md")).To(MatchError(studio.ErrNoCollection))
	})

	It("keeps each user's collections apart", func() {
		Expect(col.Add(ctx, bob, "manuals", "a.md", "text")).To(MatchError(studio.ErrNoCollection), "bob has no collection of that name")
		Expect(svc.CreateForUser(bob, "manuals")).To(Succeed())
		Expect(col.Add(ctx, bob, "manuals", "bob-only.md", "text")).To(Succeed())
		Expect(baseNames(bob, "manuals")).To(Equal([]string{"bob-only.md"}))
		Expect(baseNames(alice, "manuals")).To(BeEmpty())
		Expect(col.Remove(ctx, alice, "manuals", "bob-only.md")).To(Succeed(), "alice's remove touches only alice's collection")
		Expect(baseNames(bob, "manuals")).To(Equal([]string{"bob-only.md"}))
	})

	It("keeps the collections of an instance without accounts under the root, for the anonymous chat user", func() {
		Expect(svc.CreateForUser("", "shared")).To(Succeed())
		Expect(col.Add(ctx, studio.AnonymousPrincipal, "shared", "n.md", "text")).To(Succeed())
		Expect(baseNames("", "shared")).To(Equal([]string{"n.md"}))
		Expect(col.Remove(ctx, studio.AnonymousPrincipal, "shared", "n.md")).To(Succeed())
		Expect(baseNames("", "shared")).To(BeEmpty())
	})

	It("answers ErrNoCollection while the collections service has not started", func() {
		app.collectionsService.Store(nil)
		Expect(col.Add(ctx, alice, "manuals", "a.md", "text")).To(MatchError(studio.ErrNoCollection))
		Expect(col.Remove(ctx, alice, "manuals", "a.md")).To(MatchError(studio.ErrNoCollection))
	})
})
