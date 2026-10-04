// SPDX-License-Identifier: MIT
package e2e_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/services/routing/router"
	"github.com/mudler/LocalAI/pkg/httpclient"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
)

func decisionImage(blue bool) string {
	im := image.NewRGBA(image.Rect(0, 0, 64, 64))
	c := color.RGBA{255, 0, 0, 255}
	if blue {
		c = color.RGBA{0, 0, 255, 255}
	}
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			im.SetRGBA(x, y, c)
		}
	}
	var b bytes.Buffer
	Expect(png.Encode(&b, im)).To(Succeed())
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(b.Bytes())
}
func decisionPost(endpoint string, body any) (int, []byte) {
	return decisionPostAt(apiURL, endpoint, body)
}
func decisionPostAt(apiURL, endpoint string, body any) (int, []byte) {
	b, err := json.Marshal(body)
	Expect(err).NotTo(HaveOccurred())
	req, err := http.NewRequestWithContext(context.Background(), "POST", apiURL+endpoint, bytes.NewReader(b))
	Expect(err).NotTo(HaveOccurred())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Multimodal-Audit", "yes")
	resp, err := httpclient.NewWithTimeout(20 * time.Minute).Do(req)
	Expect(err).NotTo(HaveOccurred())
	defer func() { Expect(resp.Body.Close()).To(Succeed()) }()
	data, err := io.ReadAll(resp.Body)
	Expect(err).NotTo(HaveOccurred())
	return resp.StatusCode, data
}
func writeDecisionConfig(cfg map[string]any) {
	writeDecisionConfigAt(modelsPath, cfg)
}
func writeDecisionConfigAt(modelsPath string, cfg map[string]any) {
	b, err := yaml.Marshal(cfg)
	Expect(err).NotTo(HaveOccurred())
	Expect(os.WriteFile(filepath.Join(modelsPath, cfg["name"].(string)+".yaml"), b, 0600)).To(Succeed())
}
func decisionRouterConfig(name, classifier, model string) map[string]any {
	return map[string]any{"name": name, "router": map[string]any{"classifier": classifier, "classifier_model": model, "activation_threshold": 0.8, "fallback": "mm-red", "policies": []map[string]any{{"label": "red", "description": "The image is predominantly red"}, {"label": "blue", "description": "The image is predominantly blue"}}, "candidates": []map[string]any{{"model": "mm-red", "labels": []string{"red"}}, {"model": "mm-blue", "labels": []string{"blue"}}}}}
}
func setupDecisionFixtures() {
	// A real Score-capable backend identity, bound to the existing mock binary.
	// No production capability table mutation is needed.
	for _, name := range []string{"mm-decision", "mm-unsupported", "mm-error", "mm-cancel", "mm-embed", "mm-red", "mm-blue"} {
		uses := []string{"decisions"}
		if name == "mm-red" || name == "mm-blue" {
			uses = []string{"chat", "vision"}
		}
		writeDecisionConfig(map[string]any{"name": name, "backend": "llama-cpp", "known_usecases": uses, "parameters": map[string]any{"model": name + ".bin"}, "options": []string{"decision_audit:" + filepath.Join(tmpDir, "decision-audit")}})
	}
	writeDecisionConfig(decisionRouterConfig("mm-router", "decisions", "mm-decision"))
	writeDecisionConfig(decisionRouterConfig("mm-cancel-router", "decisions", "mm-cancel"))
	writeDecisionConfig(decisionRouterConfig("mm-fallback", "decisions", "mm-error"))
	writeDecisionConfig(decisionRouterConfig("mm-text", "score", "mock-classifier"))
	cached := decisionRouterConfig("mm-cached-text", "score", "mock-classifier")
	cached["router"].(map[string]any)["embedding_cache"] = map[string]any{"embedding_model": "mm-embed"}
	writeDecisionConfig(cached)
	overlap := decisionRouterConfig("mm-overlap", "decisions", "mm-decision")
	overlap["router"].(map[string]any)["activation_threshold"] = 0.5
	overlap["router"].(map[string]any)["candidates"] = []map[string]any{{"model": "mm-red", "labels": []string{"red"}}, {"model": "mm-blue", "labels": []string{"red", "blue"}}}
	writeDecisionConfig(overlap)
}
func imageChat(model string, images []string, anthropic bool) map[string]any {
	parts := []any{}
	for _, im := range images {
		if anthropic {
			parts = append(parts, map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": strings.SplitN(im, ",", 2)[1]}})
		} else {
			parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": im}})
		}
	}
	return map[string]any{"model": model, "max_tokens": 128, "messages": []any{map[string]any{"role": "user", "content": parts}}}
}
func lastDecision(name string) router.DecisionRecord {
	rows, err := localAIApp.RouterDecisions().List(context.Background(), router.DecisionListQuery{RouterModel: name, Limit: 1})
	Expect(err).NotTo(HaveOccurred())
	Expect(rows).To(HaveLen(1))
	return rows[0]
}

// Observe the registered route's billing stamp without replacing its handler.
var decisionUsage struct {
	sync.Mutex
	tokens any
}

func observeDecisionUsage(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		err := next(c)
		if c.Request().Header.Get("X-Multimodal-Audit") == "yes" {
			decisionUsage.Lock()
			decisionUsage.tokens = c.Get(middleware.ContextKeyTotalTokens)
			decisionUsage.Unlock()
		}
		return err
	}
}

var _ = Describe("Shared multimodal public API", Label("Multimodal"), func() {
	It("forwards exact structured state and ordered images through SystemOne Score", func() {
		state := map[string]any{"nested": []any{true, 17.0, "unchanged"}}
		images := []string{decisionImage(false), decisionImage(true)}
		body := map[string]any{"model": "mm-decision", "state": state, "images": images, "questions": map[string]any{"color": map[string]any{"type": "noul"}}}
		code, data := decisionPost("/systemone", body)
		Expect(code).To(Equal(200), string(data))
		var r map[string]any
		Expect(json.Unmarshal(data, &r)).To(Succeed())
		expected, err := json.Marshal(body)
		Expect(err).NotTo(HaveOccurred())
		actual, err := json.Marshal(r["received"])
		Expect(err).NotTo(HaveOccurred())
		Expect(actual).To(MatchJSON(expected))
		Expect(r["answers"]).To(HaveKey("color"))
	})
	It("rejects malformed, oversized and unsupported images without usage", func() {
		for _, tc := range []struct {
			model  string
			images []string
			code   int
		}{{"mm-decision", []string{"data:image/png;base64,AB=="}, 400}, {"mm-decision", []string{strings.Repeat("x", 17<<20)}, 413}, {"mm-unsupported", []string{decisionImage(false)}, 501}} {
			code, data := decisionPost("/systemone", map[string]any{"model": tc.model, "state": map[string]any{}, "images": tc.images, "questions": map[string]any{"q": map[string]any{"type": "noul"}}})
			Expect(code).To(Equal(tc.code), string(data))
			Expect(string(data)).NotTo(ContainSubstring("input_tokens"))
			decisionUsage.Lock()
			Expect(decisionUsage.tokens).To(BeNil())
			decisionUsage.Unlock()
		}
	})
	for _, anthropic := range []bool{false, true} {
		anthropic := anthropic
		It("routes image-only requests and retains exact downstream image order", func() {
			endpoint := "/chat/completions"
			if anthropic {
				endpoint = "/messages"
			}
			for _, blue := range []bool{false, true, false} {
				images := []string{decisionImage(blue), decisionImage(!blue)}
				code, data := decisionPost(endpoint, imageChat("mm-router", images, anthropic))
				Expect(code).To(Equal(200), string(data))
				var result map[string]any
				Expect(json.Unmarshal(data, &result)).To(Succeed())
				var text string
				if anthropic {
					text = result["content"].([]any)[0].(map[string]any)["text"].(string)
				} else {
					text = result["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)["content"].(string)
				}
				var echoed struct {
					Images []string `json:"images"`
					Model  string   `json:"model"`
				}
				Expect(json.Unmarshal([]byte(text), &echoed)).To(Succeed())
				expectedImages := []string{strings.SplitN(images[0], ",", 2)[1], strings.SplitN(images[1], ",", 2)[1]}
				if anthropic {
					expectedImages = images
				}
				Expect(echoed.Images).To(Equal(expectedImages))
				winner := "mm-red"
				if blue {
					winner = "mm-blue"
				}
				Expect(echoed.Model).To(ContainSubstring(winner))
				d := lastDecision("mm-router")
				Expect(d.ServedModel).To(Equal(winner))
				Expect(d.Cached).To(BeFalse())
				Expect(d.LabelScores).To(HaveLen(2))
				Expect(d.LabelScores[0].Score + d.LabelScores[1].Score).To(BeNumerically(">", 1.0))
			}
		})
	}
	It("selects a covering candidate when independent policies overlap", func() {
		code, data := decisionPost("/chat/completions", imageChat("mm-overlap", []string{decisionImage(false)}, false))
		Expect(code).To(Equal(200), string(data))
		d := lastDecision("mm-overlap")
		Expect(d.ServedModel).To(Equal("mm-blue"))
		Expect(d.Label).To(Equal("red,blue"))
		Expect(d.LabelScores).To(Equal([]router.LabelScore{{Label: "red", Score: .95}, {Label: "blue", Score: .6}}))
	})
	It("bypasses embedding cache for differing images instead of conflating image-only probes", func() {
		audit := filepath.Join(tmpDir, "decision-audit.embedding")
		Expect(os.Remove(audit)).To(SatisfyAny(Succeed(), WithTransform(os.IsNotExist, BeTrue())))
		for _, blue := range []bool{false, true, false} {
			code, data := decisionPost("/chat/completions", imageChat("mm-cached-text", []string{decisionImage(blue)}, false))
			Expect(code).To(Equal(200), string(data))
			d := lastDecision("mm-cached-text")
			Expect(d.Cached).To(BeFalse())
			Expect(d.Label).To(Equal("fallback"))
			Expect(d.Classifier).To(Equal("score"))
			_, err := os.Stat(audit)
			Expect(os.IsNotExist(err)).To(BeTrue(), "image probes must not call Embedding")
			stats := localAIApp.RouterClassifierRegistry().EmbeddingCacheStatsByRouter()
			Expect(stats).To(HaveKeyWithValue("mm-cached-text", router.EmbeddingCacheStats{}))
		}
	})

	It("does not dispatch fallback after parent cancellation", func() {
		audit := filepath.Join(tmpDir, "decision-audit")
		before := decisionPredictAudit()
		body, err := json.Marshal(imageChat("mm-cancel-router", []string{decisionImage(true)}, false))
		Expect(err).NotTo(HaveOccurred())
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, "POST", apiURL+"/chat/completions", bytes.NewReader(body))
		Expect(err).NotTo(HaveOccurred())
		req.Header.Set("Content-Type", "application/json")
		done := make(chan error, 1)
		go func() {
			r, e := httpclient.NewWithTimeout(time.Minute).Do(req)
			if r != nil {
				_ = r.Body.Close()
			}
			done <- e
		}()
		Eventually(func() bool { _, err := os.Stat(audit + ".entered"); return err == nil }, 15*time.Second, 10*time.Millisecond).Should(BeTrue())
		cancel()
		Eventually(done).Should(Receive(HaveOccurred()))
		Eventually(func() bool { _, err := os.Stat(audit + ".cancelled"); return err == nil }, 10*time.Second, 10*time.Millisecond).Should(BeTrue())
		Consistently(func() int {
			rows, err := localAIApp.RouterDecisions().List(context.Background(), router.DecisionListQuery{RouterModel: "mm-cancel-router"})
			Expect(err).NotTo(HaveOccurred())
			return len(rows)
		}, 500*time.Millisecond, 20*time.Millisecond).Should(BeZero())
		Expect(decisionPredictAudit()).To(Equal(before), "cancelled request must not call fallback Predict")
	})
	It("does not trim image-bearing state to the classifier's text context budget", func() {
		images := []string{decisionImage(true), decisionImage(false)}
		body := imageChat("mm-router", images, false)
		messages := append([]any{map[string]any{"role": "user", "content": strings.Repeat("old context ", 3000)}, map[string]any{"role": "assistant", "content": "arbitrary prior answer"}}, body["messages"].([]any)...)
		body["messages"] = messages
		code, data := decisionPost("/chat/completions", body)
		Expect(code).To(Equal(200), string(data))
		captured, err := os.ReadFile(filepath.Join(tmpDir, "decision-audit.score"))
		Expect(err).NotTo(HaveOccurred())
		var received struct {
			State []struct {
				Role    string `json:"role"`
				Content any    `json:"content"`
			} `json:"state"`
			Images []string `json:"images"`
		}
		Expect(json.Unmarshal(captured, &received)).To(Succeed())
		actual, err := json.Marshal(received.State)
		Expect(err).NotTo(HaveOccurred())
		expected, err := json.Marshal(messages)
		Expect(err).NotTo(HaveOccurred())
		Expect(actual).To(MatchJSON(expected))
		Expect(received.Images).To(BeEmpty(), "embedded images must not be duplicated at top level")
		Expect(lastDecision("mm-router").ServedModel).To(Equal("mm-blue"))
	})

	It("falls back on normal classifier errors and explicit text-only rejection without losing images", func() {
		for _, name := range []string{"mm-fallback", "mm-text"} {
			images := []string{decisionImage(true), decisionImage(false)}
			body := imageChat(name, images, false)
			body["messages"] = append([]any{map[string]any{"role": "user", "content": "arbitrary old question 731"}, map[string]any{"role": "assistant", "content": "arbitrary old answer 942"}}, body["messages"].([]any)...)
			code, data := decisionPost("/chat/completions", body)
			Expect(code).To(Equal(200), string(data))
			Expect(lastDecision(name).ServedModel).To(Equal("mm-red"))
			var r struct {
				Choices []struct{ Message struct{ Content string } }
			}
			Expect(json.Unmarshal(data, &r)).To(Succeed())
			var echoed struct {
				Images []string
				Prompt string
			}
			Expect(json.Unmarshal([]byte(r.Choices[0].Message.Content), &echoed)).To(Succeed())
			Expect(echoed.Images).To(Equal([]string{strings.SplitN(images[0], ",", 2)[1], strings.SplitN(images[1], ",", 2)[1]}))
			Expect(echoed.Prompt).To(ContainSubstring("arbitrary old question 731"))
			Expect(echoed.Prompt).To(ContainSubstring("arbitrary old answer 942"))
			Expect(strings.Index(echoed.Prompt, "arbitrary old question 731")).To(BeNumerically("<", strings.Index(echoed.Prompt, "arbitrary old answer 942")))
		}
	})
})

func decisionPredictAudit() string {
	data, err := os.ReadFile(filepath.Join(tmpDir, "decision-audit.predict"))
	if os.IsNotExist(err) {
		return ""
	}
	Expect(err).NotTo(HaveOccurred())
	return string(data)
}
