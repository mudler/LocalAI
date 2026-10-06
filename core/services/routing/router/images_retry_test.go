// SPDX-License-Identifier: MIT
package router

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/systemone"
	. "github.com/onsi/ginkgo/v2"
	"image"
	"image/png"
	"reflect"
	"sync"
)

var _ = It("TestRetryCollectionAdmission", func() {
	for i := 0; i < systemone.MaxAdmissions; i++ {
		release, err := systemone.AcquireAdmission(context.Background())
		if err != nil {
			Fail(fmt.Sprint(err))
		}
		defer release()
	}
	_, err := (Probe{Prompt: "text"}).HasImages(context.Background())
	if !errors.Is(err, systemone.ErrAdmissionCapacity) {
		Fail(fmt.Sprintf("unguarded collection: %v", err))
	}
})

var _ = It("TestRetryCollectionCancellationAndRelease", func() {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (Probe{Prompt: "text"}).HasImages(ctx); !errors.Is(err, context.Canceled) {
		Fail(fmt.Sprintf("cancel: %v", err))
	}
	for i := 0; i < systemone.MaxAdmissions*2; i++ {
		if _, err := (Probe{State: []byte(`{`)}).HasImages(context.Background()); err == nil {
			Fail(fmt.Sprint("invalid JSON accepted"))
		}
	}
	for i := 0; i < systemone.MaxAdmissions; i++ {
		release, err := systemone.AcquireAdmission(context.Background())
		if err != nil {
			Fail(fmt.Sprintf("leaked lease: %v", err))
		}
		defer release()
	}
})

var _ = It("TestRetryConcurrentCollectionBound", func() {
	// Leave one shared slot. Concurrent direct callers must never exceed it,
	// and must recover after collection errors and cancellations.
	for i := 0; i < systemone.MaxAdmissions-1; i++ {
		release, err := systemone.AcquireAdmission(context.Background())
		if err != nil {
			Fail(fmt.Sprint(err))
		}
		defer release()
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer GinkgoRecover()
			defer wg.Done()
			<-start
			for j := 0; j < 10; j++ {
				_, err := (Probe{State: []byte(`{"messages":[]}`)}).HasImages(context.Background())
				if err != nil && !errors.Is(err, systemone.ErrAdmissionCapacity) {
					Fail(fmt.Sprintf("collection: %v", err))
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	release, err := systemone.AcquireAdmission(context.Background())
	if err != nil {
		Fail(fmt.Sprint(err))
	}
	defer release()
	if _, err = (Probe{Prompt: "text"}).HasImages(context.Background()); !errors.Is(err, systemone.ErrAdmissionCapacity) {
		Fail(fmt.Sprintf("bound: %v", err))
	}
})

type retryInner struct {
	want Probe
}

func (*retryInner) Name() string { return "spy" }
func (s *retryInner) Classify(_ context.Context, p Probe) (Decision, error) {
	if !reflect.DeepEqual(s.want, p) {
		Fail(fmt.Sprintf("inner probe changed: %#v", p))
	}
	return Decision{Score: 1}, nil
}

type retryNoEmbed struct{}

func (retryNoEmbed) Embed(context.Context, string) ([]float32, error) { panic("image cache embed") }

type retryNoStore struct{ backend.VectorStore }

func (retryNoStore) Search(context.Context, []float32) (float64, []byte, bool, error) {
	panic("image cache search")
}
func (retryNoStore) Insert(context.Context, []float32, []byte) error { panic("image cache insert") }

var _ = It("TestRetryCacheCompleteProbe", func() {
	for _, image := range []string{"AA==", "AQ=="} {
		p := Probe{Prompt: "identical", Messages: []string{"first", "second"}, State: []byte(`{}`), Images: []byte(`["data:image/png;base64,` + image + `"]`)}
		c := NewEmbeddingCacheClassifier(&retryInner{p}, retryNoEmbed{}, retryNoStore{}, .9, .5).WithTokenTrim(func(string) (int, error) { panic("image cache trim") }, 10)
		if _, err := c.Classify(context.Background(), p); err != nil {
			Fail(fmt.Sprint(err))
		}
	}
})

type retryRunner func(context.Context, *schema.SystemOneRequest) (*schema.SystemOneResponse, error)

func (f retryRunner) Decide(ctx context.Context, r *schema.SystemOneRequest) (*schema.SystemOneResponse, error) {
	return f(ctx, r)
}

var _ = It("TestRetryImageCancellationNoFallback", func() {
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewGray(image.Rect(0, 0, 1, 1))); err != nil {
		Fail(fmt.Sprint(err))
	}
	images, _ := json.Marshal([]string{"data:image/png;base64," + base64.StdEncoding.EncodeToString(b.Bytes())})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	called := false
	c, err := NewDecisionsClassifier([]ScorePolicy{{Label: "visual", Description: "image"}}, retryRunner(func(context.Context, *schema.SystemOneRequest) (*schema.SystemOneResponse, error) {
		called = true
		cancel()
		return nil, context.Canceled
	}), 0)
	if err != nil {
		Fail(fmt.Sprint(err))
	}
	cfg := &config.ModelConfig{Name: "route", Router: config.RouterConfig{Classifier: "decisions", Fallback: "fallback", Candidates: []config.RouterCandidate{{Model: "candidate", Labels: []string{"visual"}}}}}
	_, err = Resolve(ctx, cfg, c, func(string) (*config.ModelConfig, error) { Fail(fmt.Sprint("cancel loaded fallback")); return nil, nil }, Probe{State: []byte(`{}`), Images: images})
	if !called || !errors.Is(err, context.Canceled) {
		Fail(fmt.Sprintf("cancel: called=%v err=%v", called, err))
	}
})

var _ = It("TestRetryOversizedDirectProbe", func() {
	p := Probe{State: make([]byte, systemone.MaxImageBodyBytes+1)}
	if _, err := p.HasImages(context.Background()); err == nil {
		Fail(fmt.Sprint("oversized collection accepted"))
	}
	c, err := NewDecisionsClassifier([]ScorePolicy{{Label: "x", Description: "x"}}, retryRunner(func(context.Context, *schema.SystemOneRequest) (*schema.SystemOneResponse, error) {
		Fail(fmt.Sprint("oversized runner called"))
		return nil, nil
	}), 0)
	if err != nil {
		Fail(fmt.Sprint(err))
	}
	if _, err = c.Classify(context.Background(), p); err == nil {
		Fail(fmt.Sprint("oversized native probe accepted"))
	}
})
