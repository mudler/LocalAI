package session

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("the cause of a failed turn", func() {
	// The text the server log showed for a model that could not be loaded.
	const cooldownText = `failed to select tool: failed to pick tool: tool selection failed: streaming decision failed, not retrying a permanent error: localai stream: request: Post "http://studio.invalid/v1/chat/completions": the model could not be loaded ` + MarkLoadFailed + `: model "test" load is in cooldown after a recent failure; retry after 8s`

	ginkgo.DescribeTable("names the cause and a next step",
		func(err error, code, model string, retry int, reason ...string) {
			f := Classify(err)
			Expect(f.Code).To(Equal(code))
			Expect(f.Model).To(Equal(model))
			Expect(f.RetryAfter).To(Equal(retry))
			for _, want := range reason {
				Expect(f.Reason).To(ContainSubstring(want))
			}
			Expect(f.Reason).NotTo(ContainSubstring("studio.invalid"))
			Expect(f.Detail).NotTo(ContainSubstring("studio.invalid"))
		},
		ginkgo.Entry("backend missing", errors.New(strings.Replace(cooldownText, MarkLoadFailed, MarkBackendMissing, 1)),
			CodeBackendMissing, "test", 8, `The model "test"`, "backend is not installed"),
		ginkgo.Entry("backend missing, first failure", errors.New(`the model failed 4 times in a row (last: HTTP 500 failed to load model with internal loader: backend not found: default)`),
			CodeBackendMissing, "", 0, "backend is not installed"),
		ginkgo.Entry("cooling down", errors.New(cooldownText),
			CodeCooldown, "test", 8, `"test"`, "8 seconds"),
		ginkgo.Entry("cooling down for minutes", errors.New(`the model could not be loaded: model "big" load is in cooldown after a recent failure; retry after 1m30s`),
			CodeCooldown, "big", 90, "90 seconds"),
		ginkgo.Entry("load failed", errors.New(`the model could not be loaded [studio:load-failed]: its backend failed to start`),
			CodeLoadFailed, "", 0, "could not be started"),
		ginkgo.Entry("model not found", errors.New(`request: Post "http://10.0.0.5:8080/v1/chat/completions": model "gone" not found`),
			CodeModelNotFound, "gone", 0, `"gone"`, "was not found"),
		ginkgo.Entry("provider error", errors.New(`the model failed 4 times in a row (last: HTTP 502 bad gateway upstream.internal:8080)`),
			CodeUpstream, "", 0, "HTTP 502"),
		ginkgo.Entry("upstream key", errors.New("the upstream refused the key "+MarkUpstreamKey), CodeUpstreamKey, "", 0, "refused the key"),
		ginkgo.Entry("upstream path", errors.New("x "+MarkUpstreamPath), CodeUpstreamPath, "", 0, "full endpoint", ".../v1/chat/completions"),
		ginkgo.Entry("upstream host", errors.New(`the model failed 4 times in a row (last: HTTP 502 cloudproxy: recv first reply: rpc error: cloud-proxy: upstream request failed: Post "https://x.example/v1": dial tcp: lookup x.example: no such host)`),
			CodeUpstreamDown, "", 0, "could not be reached", "Check the address"),
		ginkgo.Entry("timeout", fmt.Errorf("agent: %w", context.DeadlineExceeded),
			CodeTimeout, "", 0, "took too long"),
		ginkgo.Entry("cancelled", fmt.Errorf("agent: %w", context.Canceled),
			CodeCancelled, "", 0, "cancelled"),
		ginkgo.Entry("refused", errors.New("Post x: "+MarkNotAllowed), CodeNotAllowed, "", 0, "not allowed"),
		ginkgo.Entry("quota", errors.New("agent: "+MarkQuota), CodeQuota, "", 0, "quota"),
		ginkgo.Entry("context", errors.New("prompt is too long for the context"), CodeContext, "", 0, "context window"),
		ginkgo.Entry("unknown", errors.New("connection reset"), CodeAgent, "", 0, "stopped because of an error"),
	)

	ginkgo.It("does not tell the user to check the backend of a provider that failed", func() {
		f := Classify(errors.New("the model failed 4 times in a row (last: HTTP 502 bad gateway)"))
		Expect(f.Reason).NotTo(ContainSubstring("backend"))
	})

	ginkgo.It("does not read a deadline as a context that is too small", func() {
		Expect(Classify(errors.New("context deadline exceeded")).Code).To(Equal(CodeTimeout))
	})

	ginkgo.It("ignores a model name that is not plain", func() {
		f := Classify(errors.New(`the model could not be loaded: model "a<script>" load is in cooldown after a recent failure; retry after 3s`))
		Expect(f.Model).To(BeEmpty())
		Expect(f.Reason).NotTo(ContainSubstring("<script>"))
	})

	ginkgo.Describe("Sanitize", func() {
		ginkgo.It("removes addresses, paths, secrets and stacks", func() {
			got := Sanitize("Post \"http://studio.invalid/v1/x\": dial 10.1.2.3:9000 refused, read /home/bob/models/x.gguf Authorization: Bearer abcdef123456789 api_key=sk-secretsecret1\ngoroutine 1 [running]:\nmain.go:12")
			for _, bad := range []string{"studio.invalid", "10.1.2.3", "/home/bob", "abcdef123456789", "secretsecret1", "goroutine", "main.go"} {
				Expect(got).NotTo(ContainSubstring(bad))
			}
			Expect(got).To(ContainSubstring("[address]"))
			Expect(got).To(ContainSubstring("[path]"))
		})
		ginkgo.It("drops the internal marks", func() {
			Expect(Sanitize("the model could not be loaded " + MarkBackendMissing + ": x")).To(Equal("the model could not be loaded: x"))
		})
		ginkgo.It("cuts a long text", func() {
			Expect(len(Sanitize(strings.Repeat("word ", 500)))).To(BeNumerically("<=", MaxDetail))
		})
	})
})
