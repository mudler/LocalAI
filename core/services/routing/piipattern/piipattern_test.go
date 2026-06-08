package piipattern

import (
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestPiipattern(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "piipattern suite")
}

var _ = Describe("ValidatePattern", func() {
	DescribeTable("accepts anchored, bounded patterns",
		func(src string) { Expect(ValidatePattern(src)).To(Succeed()) },
		Entry("anthropic", `sk-ant-[A-Za-z0-9_-]{20,200}`),
		Entry("github via alternation", `(?:ghp|gho|ghs)_[A-Za-z0-9]{36,}`),
		Entry("custom token", `tok-\w{32,64}`),
		Entry("aws", `AKIA[0-9A-Z]{16}`),
		Entry("anchored by mid-literal", `(?:sk|rk)_live_[0-9A-Za-z]{16,}`),
	)

	DescribeTable("rejects unanchored or unsafe patterns",
		func(src string) { Expect(ValidatePattern(src)).NotTo(Succeed()) },
		Entry("email (no fixed anchor)", `[\w.]+@[\w.]+\.\w+`),
		Entry("bare word run", `\w+`),
		Entry("any-char greedy", `sk-.*`),
		Entry("capturing group", `(sk-ant-[A-Za-z0-9]+)`),
		Entry("two fixed chars only", `ab[0-9]{8,}`),
		Entry("over-long source", "sk-ant-"+strings.Repeat("a", MaxPatternLen)),
		Entry("huge bounded repeat", `sk-ant-[A-Za-z0-9]{5000}`),
		Entry("empty", ``),
	)
})

var _ = Describe("Compile", func() {
	It("compiles a valid pattern with leftmost-longest semantics", func() {
		re, err := Compile(`sk-ant-[A-Za-z0-9_-]{4,}`)
		Expect(err).NotTo(HaveOccurred())
		// Longest() makes the match span the whole key, not a shorter prefix.
		loc := re.FindString("key sk-ant-AAAA1111bbbb end")
		Expect(loc).To(Equal("sk-ant-AAAA1111bbbb"))
	})
	It("refuses an invalid pattern", func() {
		_, err := Compile(`.*`)
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("builtins", func() {
	It("every built-in validates, compiles, and is uniquely named", func() {
		seen := map[string]bool{}
		for _, b := range BuiltinCatalogue() {
			Expect(seen[b.Name]).To(BeFalse(), "duplicate builtin %s", b.Name)
			seen[b.Name] = true
			Expect(ValidatePattern(b.Pattern)).To(Succeed(), "builtin %s pattern %q", b.Name, b.Pattern)
		}
	})

	DescribeTable("matches a real sample and not a decoy",
		func(name, sample, decoy string) {
			b, ok := LookupBuiltin(name)
			Expect(ok).To(BeTrue())
			re, err := Compile(b.Pattern)
			Expect(err).NotTo(HaveOccurred())
			Expect(re.MatchString(sample)).To(BeTrue(), "should match %q", sample)
			Expect(re.MatchString(decoy)).To(BeFalse(), "should not match %q", decoy)
		},
		Entry("anthropic", "anthropic_api_key", "sk-ant-api03-AbCdEf012345_-AbCdEf012345", "sk-ant-short"),
		Entry("aws", "aws_access_key", "AKIAIOSFODNN7EXAMPLE", "AKIAshort"),
		Entry("github", "github_token", "ghp_"+strings.Repeat("a", 36), "ghp_short"),
	)
})

var _ = Describe("Matcher", func() {
	It("reports the whole key as one span under its group", func() {
		m, err := NewMatcher([]string{"anthropic_api_key"}, nil)
		Expect(err).NotTo(HaveOccurred())
		got := m.Find("my key is sk-ant-api03-AbCdEf012345AbCdEf012345 thanks")
		Expect(got).To(HaveLen(1))
		Expect(got[0].Group).To(Equal("ANTHROPIC_KEY"))
		Expect(got[0].Text).To(Equal("sk-ant-api03-AbCdEf012345AbCdEf012345"))
	})

	It("compiles custom patterns and honours MinLen", func() {
		m, err := NewMatcher(nil, []Pattern{{Group: "INTERNAL", Pattern: `tok-[A-Za-z0-9]{4,}`, MinLen: 12}})
		Expect(err).NotTo(HaveOccurred())
		// "tok-AAAA" (8 bytes) is below MinLen 12 and is dropped.
		Expect(m.Find("tok-AAAA")).To(BeEmpty())
		Expect(m.Find("tok-AAAABBBBCCCC")).To(HaveLen(1))
	})

	It("fails closed on an unknown built-in", func() {
		_, err := NewMatcher([]string{"nope"}, nil)
		Expect(err).To(HaveOccurred())
	})

	It("rejects an invalid custom pattern", func() {
		_, err := NewMatcher(nil, []Pattern{{Group: "X", Pattern: `.*`}})
		Expect(err).To(HaveOccurred())
	})
})
