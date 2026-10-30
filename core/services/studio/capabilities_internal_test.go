package studio

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("asksToRun", func() {
	DescribeTable("tells a task that asks to run something from one that does not",
		func(task string, want bool) {
			Expect(asksToRun(task)).To(Equal(want), task)
		},
		Entry("execute", "Actually execute the script and tell me the output", true),
		Entry("execution", "Do the execution of build.sh", true),
		Entry("run a script", "Run the Python script I saved", true),
		Entry("run a file", "run make_deck.py and report", true),
		Entry("run it", "Please run it and check the file exists", true),
		Entry("pip", "pip install openpyxl then build the deck", true),
		Entry("npm", "npm run build", true),
		Entry("research", "Find which spreadsheet library is the most used in Python", false),
		Entry("summary", "Summarize the notes in /workspace/home/notes.md", false),
		Entry("running costs", "List the running costs of the project", false),
		Entry("run a search", "Run a search for the quarterly numbers", false),
		Entry("review a script", "Review make_deck.py for bugs", false),
	)
})
