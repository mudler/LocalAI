package projects_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/projects"
)

var _ = Describe("the shape of a project folder path", func() {
	DescribeTable("ParseProjectPath",
		func(p, owner, slug, rest string, ok bool) {
			o, s, r, got := projects.ParseProjectPath(p)
			Expect(got).To(Equal(ok))
			Expect([]string{o, s, r}).To(Equal([]string{owner, slug, rest}))
		},
		Entry("a project folder", "/users/alice/Projects/acme", "alice", "acme", "", true),
		Entry("a folder inside it", "/users/alice/Projects/acme/files/a", "alice", "acme", "files/a", true),
		Entry("the Projects folder itself", "/users/alice/Projects", "", "", "", false),
		Entry("a home", "/users/alice", "", "", "", false),
		Entry("another case", "/users/alice/projects/acme", "", "", "", false),
		Entry("the root is not at the start", "/users/alice/users/bob/Projects/x", "", "", "", false),
		Entry("not under users", "/shared/Projects/x/y", "", "", "", false),
		Entry("an empty owner", "/users//Projects/x", "", "", "", false),
		Entry("an empty slug", "/users/alice/Projects//x", "", "", "", false),
	)

	DescribeTable("CoversOtherProjects for bob",
		func(p string, want bool) { Expect(projects.CoversOtherProjects(p, "bob")).To(Equal(want)) },
		Entry("a project of alice", "/users/alice/Projects/acme", true),
		Entry("inside a project of alice", "/users/alice/Projects/acme/files", true),
		Entry("the Projects folder of alice", "/users/alice/Projects", true),
		Entry("the home of alice", "/users/alice", true),
		Entry("the users folder", "/users", true),
		Entry("the root", "/", true),
		Entry("bob's own project", "/users/bob/Projects/acme", false),
		Entry("bob's own home", "/users/bob", false),
		Entry("a plain shared folder", "/shared/Team", false),
		Entry("a folder of alice that is not Projects", "/users/alice/Team", false),
		Entry("another case of Projects", "/users/alice/projects/acme", false),
		Entry("a nested look-alike", "/users/alice/users/bob/Projects/x", false),
		Entry("a Projects folder deeper in a folder", "/users/alice/Other/Projects/x", false),
	)
})
