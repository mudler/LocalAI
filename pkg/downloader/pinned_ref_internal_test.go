// pinnedImageRef is unexported, so its tests live in package downloader
// (alongside the external _test package's specs — both share Ginkgo's
// global registry, so the external suite's RunSpecs picks these up too).
package downloader

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("pinnedImageRef", func() {
	const dig = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	DescribeTable("rewrites refs to digest form",
		func(in, want string) {
			Expect(pinnedImageRef(in, dig)).To(Equal(want))
		},
		Entry("repo:tag", "quay.io/foo/bar:latest", "quay.io/foo/bar@"+dig),
		Entry("repo without tag", "quay.io/foo/bar", "quay.io/foo/bar@"+dig),
		Entry("dockerhub library tag", "docker.io/library/alpine:3.20", "docker.io/library/alpine@"+dig),
		// Registry with explicit port: the ':5000' must not be mistaken
		// for a tag separator.
		Entry("registry port + tag", "localhost:5000/foo:latest", "localhost:5000/foo@"+dig),
		Entry("registry port without tag", "localhost:5000/foo", "localhost:5000/foo@"+dig),
		// Already-digested ref: rewrite cleanly rather than appending.
		Entry("already digested", "quay.io/foo/bar@sha256:deadbeef", "quay.io/foo/bar@"+dig),
		Entry("tag and digest", "quay.io/foo/bar:latest@sha256:deadbeef", "quay.io/foo/bar@"+dig),
	)
})
