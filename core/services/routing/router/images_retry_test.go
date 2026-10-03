// SPDX-License-Identifier: MIT
package router

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/systemone"
	"image"
	"image/png"
	"reflect"
	"sync"
	"testing"
)

func TestRetryCollectionAdmission(t *testing.T) {
	for i := 0; i < systemone.MaxAdmissions; i++ {
		release, err := systemone.AcquireAdmission(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer release()
	}
	_, err := (Probe{Prompt: "text"}).HasImages(context.Background())
	if !errors.Is(err, systemone.ErrAdmissionCapacity) {
		t.Fatalf("unguarded collection: %v", err)
	}
}

func TestRetryCollectionCancellationAndRelease(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (Probe{Prompt: "text"}).HasImages(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	for i := 0; i < systemone.MaxAdmissions*2; i++ {
		if _, err := (Probe{State: []byte(`{`)}).HasImages(context.Background()); err == nil {
			t.Fatal("invalid JSON accepted")
		}
	}
	for i := 0; i < systemone.MaxAdmissions; i++ {
		release, err := systemone.AcquireAdmission(context.Background())
		if err != nil {
			t.Fatalf("leaked lease: %v", err)
		}
		defer release()
	}
}

func TestRetryConcurrentCollectionBound(t *testing.T) {
	// Leave one shared slot. Concurrent direct callers must never exceed it,
	// and must recover after collection errors and cancellations.
	for i := 0; i < systemone.MaxAdmissions-1; i++ {
		release, err := systemone.AcquireAdmission(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer release()
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 10; j++ {
				_, err := (Probe{State: []byte(`{"messages":[]}`)}).HasImages(context.Background())
				if err != nil && !errors.Is(err, systemone.ErrAdmissionCapacity) {
					t.Errorf("collection: %v", err)
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	release, err := systemone.AcquireAdmission(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err = (Probe{Prompt: "text"}).HasImages(context.Background()); !errors.Is(err, systemone.ErrAdmissionCapacity) {
		t.Fatalf("bound: %v", err)
	}
}

type retryInner struct {
	t    *testing.T
	want Probe
}

func (*retryInner) Name() string { return "spy" }
func (s *retryInner) Classify(_ context.Context, p Probe) (Decision, error) {
	if !reflect.DeepEqual(s.want, p) {
		s.t.Fatalf("inner probe changed: %#v", p)
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
func TestRetryCacheCompleteProbe(t *testing.T) {
	for _, image := range []string{"AA==", "AQ=="} {
		p := Probe{Prompt: "identical", Messages: []string{"first", "second"}, State: []byte(`{}`), Images: []byte(`["data:image/png;base64,` + image + `"]`)}
		c := NewEmbeddingCacheClassifier(&retryInner{t, p}, retryNoEmbed{}, retryNoStore{}, .9, .5).WithTokenTrim(func(string) (int, error) { panic("image cache trim") }, 10)
		if _, err := c.Classify(context.Background(), p); err != nil {
			t.Fatal(err)
		}
	}
}

type retryRunner func(context.Context, *schema.SystemOneRequest) (*schema.SystemOneResponse, error)

func (f retryRunner) Decide(ctx context.Context, r *schema.SystemOneRequest) (*schema.SystemOneResponse, error) {
	return f(ctx, r)
}
func TestRetryImageCancellationNoFallback(t *testing.T) {
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewGray(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
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
		t.Fatal(err)
	}
	cfg := &config.ModelConfig{Name: "route", Router: config.RouterConfig{Classifier: "decisions", Fallback: "fallback", Candidates: []config.RouterCandidate{{Model: "candidate", Labels: []string{"visual"}}}}}
	_, err = Resolve(ctx, cfg, c, func(string) (*config.ModelConfig, error) { t.Fatal("cancel loaded fallback"); return nil, nil }, Probe{State: []byte(`{}`), Images: images})
	if !called || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: called=%v err=%v", called, err)
	}
}

func TestRetryOversizedDirectProbe(t *testing.T) {
	p := Probe{State: make([]byte, systemone.MaxImageBodyBytes+1)}
	if _, err := p.HasImages(context.Background()); err == nil {
		t.Fatal("oversized collection accepted")
	}
	c, err := NewDecisionsClassifier([]ScorePolicy{{Label: "x", Description: "x"}}, retryRunner(func(context.Context, *schema.SystemOneRequest) (*schema.SystemOneResponse, error) {
		t.Fatal("oversized runner called")
		return nil, nil
	}), 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Classify(context.Background(), p); err == nil {
		t.Fatal("oversized native probe accepted")
	}
}
