package backend

import (
	"bytes"
	"context"
	"runtime/pprof"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Collection lifecycle", func() {
	It("does not hold the shared state lock while it rebuilds a collection", func() {
		selected := CollectionModelSettings{}
		b, st, fake := newModelBackend(&selected)
		Expect(b.CreateCollection("slow")).To(Succeed())
		_, err := b.Upload(context.Background(), "slow", "a.txt", strings.NewReader("hello"))
		Expect(err).ToNot(HaveOccurred())

		// Opening a collection that holds a document embeds it again; hold that call.
		entered, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		fake.mu.Lock()
		fake.beforeEmbed = func(string) { once.Do(func() { close(entered) }); <-release }
		fake.mu.Unlock()
		DeferCleanup(func() {
			select {
			case <-release:
			default:
				close(release)
			}
		})

		st.Mu.Lock()
		st.Collections["slow"] = nil // the engine failed to open at start
		st.Mu.Unlock()
		done := make(chan struct{})
		go func() {
			defer GinkgoRecover()
			defer close(done)
			_, ok := b.lookup("slow")
			Expect(ok).To(BeTrue())
		}()
		Eventually(entered).Should(BeClosed())

		got := make(chan struct{})
		go func() { st.Mu.RLock(); st.Mu.RUnlock(); close(got) }() //nolint:staticcheck // only the acquisition matters
		Eventually(got, 2*time.Second).Should(BeClosed(), "a reader waited for the engine to open")

		close(release)
		Eventually(done, 5*time.Second).Should(BeClosed())
	})

	It("opens a collection once when several readers find it unloaded", func() {
		selected := CollectionModelSettings{}
		b, st, _ := newModelBackend(&selected)
		Expect(b.CreateCollection("shared")).To(Succeed())
		st.Mu.Lock()
		st.Collections["shared"] = nil
		st.Mu.Unlock()

		var wg sync.WaitGroup
		kbs := make([]any, 8)
		for i := range kbs {
			wg.Add(1)
			go func() {
				defer GinkgoRecover()
				defer wg.Done()
				kb, ok := b.lookup("shared")
				Expect(ok).To(BeTrue())
				kbs[i] = kb
			}()
		}
		wg.Wait()
		for _, kb := range kbs {
			Expect(kb).To(BeIdenticalTo(kbs[0]))
		}
	})

	It("stops the source loop when the backend is closed", func() {
		loops := func() int {
			var buf bytes.Buffer
			Expect(pprof.Lookup("goroutine").WriteTo(&buf, 2)).To(Succeed())
			return strings.Count(buf.String(), "(*SourceManager).Start.func1")
		}
		before := loops()
		var backends []*backendInProcess
		for range 10 {
			b, _ := NewInProcessBackend(&Config{
				LLMAPIURL: "http://127.0.0.1:1/v1", CollectionDBPath: GinkgoT().TempDir(),
				FileAssets: GinkgoT().TempDir(), VectorEngine: "chromem", EmbeddingModel: "default",
			})
			backends = append(backends, b.(*backendInProcess))
		}
		Eventually(loops, 5*time.Second).Should(Equal(before + 10))
		for _, b := range backends {
			b.Close()
			b.Close() // closing twice is harmless
		}
		Eventually(loops, 5*time.Second).Should(Equal(before))
	})

	It("gives up a search at the reranker when the caller's context ends", func() {
		selected := CollectionModelSettings{RerankerModel: "ranker"}
		b, _, fake := newModelBackend(&selected)
		Expect(b.CreateCollection("ranked")).To(Succeed())
		_, err := b.Upload(context.Background(), "ranked", "a.txt", strings.NewReader("hello"))
		Expect(err).ToNot(HaveOccurred())
		entered, release := make(chan struct{}), make(chan struct{})
		fake.mu.Lock()
		fake.beforeRerank = func(string) { close(entered); <-release }
		fake.mu.Unlock()
		DeferCleanup(func() { close(release) })

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			_, err := b.Search(ctx, "ranked", "hello", 1)
			done <- err
		}()
		Eventually(entered).Should(BeClosed())
		cancel()
		Eventually(done, 2*time.Second).Should(Receive(MatchError(context.Canceled)))
	})

	It("refuses an upload or a search whose context has ended before it starts", func() {
		selected := CollectionModelSettings{}
		b, _, _ := newModelBackend(&selected)
		Expect(b.CreateCollection("gone")).To(Succeed())
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := b.Upload(ctx, "gone", "a.txt", strings.NewReader("hello"))
		Expect(err).To(MatchError(context.Canceled))
		_, err = b.Search(ctx, "gone", "hello", 1)
		Expect(err).To(MatchError(context.Canceled))
	})
})
