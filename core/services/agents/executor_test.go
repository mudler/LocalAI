package agents

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = DescribeTable("stripThinkingTags",
	func(input, want string) {
		Expect(stripThinkingTags(input)).To(Equal(want))
	},
	Entry("empty string", "", ""),
	Entry("no tags", "Hello, world!", "Hello, world!"),
	Entry("single tag pair", "before<thinking>secret thoughts</thinking>after", "beforeafter"),
	Entry("multiple tag pairs", "a<thinking>one</thinking>b<thinking>two</thinking>c", "abc"),
	Entry("nested tags", "<thinking>outer<thinking>inner</thinking>still outer</thinking>visible", "still outer</thinking>visible"),
	Entry("unclosed opening tag", "hello<thinking>this is unclosed", "hello<thinking>this is unclosed"),
	Entry("only closing tag", "hello</thinking>world", "hello</thinking>world"),
	Entry("tags with whitespace around content", "before<thinking> spaced out </thinking>after", "beforeafter"),
	Entry("empty thinking block", "before<thinking></thinking>after", "beforeafter"),
	Entry("multiline thinking block", "before<thinking>\nline1\nline2\n</thinking>after", "beforeafter"),
	Entry("adjacent tag pairs", "<thinking>a</thinking><thinking>b</thinking>", ""),
)
