package studio

import (
	"context"
	"encoding/json"
	"errors"
	"testing/fstest"

	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("hints on binary files", func() {
	// advice runs read_file on a file that is binary, as the file tool does, in
	// a chat with the skills and the commands the spec chooses.
	advice := func(name string, canRun bool, skillNames ...string) string {
		GinkgoHelper()
		ctx := context.Background()
		svc, f := newSkillService(&skillBroker{})
		sk := fstest.MapFS{}
		for _, n := range skillNames {
			sk[n+"/SKILL.md"] = &fstest.MapFile{Data: md(n, "A skill.")}
		}
		Expect(f.SyncBuiltin(ctx, sk)).To(Succeed())
		binary := func(context.Context, json.RawMessage) (string, error) {
			return "", &files.NotTextError{Name: name, Size: 43963}
		}
		tools := svc.binaryHintTools(f.Actor("alice"), canRun, []session.Tool{{Name: "read_file", Run: binary}, {Name: "list_files", Run: binary}})
		_, err := tools[0].Run(ctx, json.RawMessage(`{"path":"/x"}`))
		Expect(err).To(HaveOccurred())
		_, err = tools[1].Run(ctx, json.RawMessage(`{"path":"/x"}`))
		var nt *files.NotTextError
		Expect(errors.As(err, &nt)).To(BeTrue(), "a tool that is not a text reader is left alone")
		_, err = tools[0].Run(ctx, json.RawMessage(`{"path":"/x"}`))
		return err.Error()
	}

	It("names the skill of the file type when the chat has one", func() {
		got := advice("report.docx", true, "docx", "xlsx")
		Expect(got).To(Equal("report.docx is a binary file (43963 bytes), not text. Use the docx skill for .docx files (load_skill)."))
	})

	It("suggests a script when there is no skill and the chat can run commands", func() {
		Expect(advice("report.docx", true, "xlsx")).To(HaveSuffix("Run a script with the commands tool instead, for example with python-docx."))
		Expect(advice("data.bin", true)).To(HaveSuffix("Run a script with the commands tool instead."))
	})

	It("learns the files of a format from the add-on that declares its slot", func() {
		ctx := context.Background()
		b := &skillBroker{}
		b.set(AddonSkillSet{AddonID: "odfkit", Title: "ODF", Skills: []string{"odt"}, Shadows: []string{"odt"}, Running: true,
			Slots: []contract.Slot{{Name: "odt", SlotDefinition: contract.SlotDefinition{Kind: contract.SlotKindDocuments, Extensions: []string{".odt"}, Words: []string{"odt"}, Library: "odfpy"}}}})
		svc, f := newSkillService(b)
		binary := func(context.Context, json.RawMessage) (string, error) {
			return "", &files.NotTextError{Name: "memo.odt", Size: 10}
		}
		tools := svc.binaryHintTools(f.Actor("alice"), true, []session.Tool{{Name: "read_file", Run: binary}})
		_, err := tools[0].Run(ctx, json.RawMessage(`{"path":"/x"}`))
		Expect(err).To(MatchError(HaveSuffix("for example with odfpy.")))
	})

	It("says so when the chat has neither", func() {
		Expect(advice("report.pdf", false)).To(HaveSuffix("This chat has no tool that opens it."))
	})
})
