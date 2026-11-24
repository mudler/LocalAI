package triggers

import (
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Event matching", func() {
	prBody := []byte(`{"action":"opened","number":42,"draft":false,"pull_request":{"user":{"login":"octo"}},"commits":[{"id":"abc"}]}`)
	ev := func(src, typ string, body []byte) Event { return Event{Source: src, Type: typ, Payload: body} }

	It("matches the source, type and fields", func() {
		tr := goodEvent()
		Expect(tr.MatchesEvent(ev("github", "pull_request", prBody))).To(BeTrue())
		Expect(tr.MatchesEvent(ev("GitHub", "Pull_Request", prBody))).To(BeTrue(), "case-insensitive")
		Expect(tr.MatchesEvent(ev("github", "pull_request", []byte(`{"action":"closed"}`)))).To(BeFalse())
		Expect(tr.MatchesEvent(ev("gitlab", "pull_request", prBody))).To(BeFalse())
		Expect(tr.MatchesEvent(ev("github", "push", prBody))).To(BeFalse())
	})

	It("matches any type when the trigger has none", func() {
		tr := goodEvent()
		tr.EventType = ""
		Expect(tr.MatchesEvent(ev("github", "push", prBody))).To(BeTrue())
	})

	It("reads nested paths, array indexes, numbers and booleans", func() {
		tr := goodEvent()
		tr.Matches = []string{"pull_request.user.login=octo", "commits.0.id=abc", "number=42", "draft=false"}
		Expect(tr.MatchesEvent(ev("github", "pull_request", prBody))).To(BeTrue())
		for _, m := range []string{"commits.1.id=abc", "pull_request.user=octo", "missing.x=1", "action.x=opened", "commits.-1.id=abc"} {
			tr.Matches = []string{m}
			Expect(tr.MatchesEvent(ev("github", "pull_request", prBody))).To(BeFalse(), m)
		}
		tr.Matches = []string{"action=opened", "number=43"}
		Expect(tr.MatchesEvent(ev("github", "pull_request", prBody))).To(BeFalse(), "all must hold")
	})

	It("does not match matches against a payload that is not JSON", func() {
		tr := goodEvent()
		Expect(tr.MatchesEvent(ev("github", "pull_request", []byte("hello")))).To(BeFalse())
		tr.Matches = nil
		Expect(tr.MatchesEvent(ev("github", "pull_request", []byte("hello")))).To(BeTrue())
	})
})

var _ = Describe("TrimPayload", func() {
	It("compacts and cuts valid JSON", func() {
		big := `{"a":"` + strings.Repeat("x", 20000) + `"}`
		got := TrimPayload([]byte(big), 8000)
		Expect(got).To(HavePrefix(`{"a":"xxx`))
		Expect(got).To(HaveSuffix("…[cut]"))
		Expect(len(got)).To(BeNumerically("<=", 8000+len("…[cut]")))
		Expect(TrimPayload([]byte("{ \"a\" : 1 }"), 100)).To(Equal(`{"a":1}`))
	})

	It("keeps text, strips control characters and cuts on a rune boundary", func() {
		Expect(TrimPayload([]byte("hello"), 100)).To(Equal("hello"))
		Expect(TrimPayload([]byte("he\x00l\x1blo\tx\ny"), 100)).To(Equal("hello\tx\ny"))
		got := TrimPayload([]byte("ééé"), 3)
		Expect(got).To(Equal("é…[cut]"))
	})
})

var _ = Describe("Signatures", func() {
	body := []byte(`{"a":1}`)
	now := time.Unix(1_800_000_000, 0)

	It("verifies a fresh signature and refuses a changed body", func() {
		sig := Sign("s3cret", now, body)
		Expect(sig).To(HavePrefix("sha256="))
		Expect(Verify("s3cret", sig, now, body, now.Add(time.Minute), 5*time.Minute)).To(BeTrue())
		Expect(Verify("s3cret", sig, now, []byte(`{"a":2}`), now, 5*time.Minute)).To(BeFalse())
		Expect(Verify("other", sig, now, body, now, 5*time.Minute)).To(BeFalse())
		Expect(Verify("s3cret", "", now, body, now, 5*time.Minute)).To(BeFalse())
	})

	It("refuses a timestamp outside the window", func() {
		sig := Sign("s3cret", now, body)
		Expect(Verify("s3cret", sig, now, body, now.Add(6*time.Minute), 5*time.Minute)).To(BeFalse())
		Expect(Verify("s3cret", sig, now, body, now.Add(-6*time.Minute), 5*time.Minute)).To(BeFalse())
	})

	It("verifies the GitHub documentation vector", func() {
		sig := "sha256=757107ea0eb2509fc211221cce984b8a37570b6d7586c22c46f4379c8b043e17"
		Expect(VerifyGitHub("It's a Secret to Everybody", sig, []byte("Hello, World!"))).To(BeTrue())
		Expect(VerifyGitHub("It's a Secret to Everybody", sig, []byte("Hello, World?"))).To(BeFalse())
		Expect(VerifyGitHub("It's a Secret to Everybody", "", []byte("Hello, World!"))).To(BeFalse())
	})
})
