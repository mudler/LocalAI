package profiles_test

import (
	"os"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/addons/sandbox/internal/profiles"
)

const docsPath = "../../../../docs/content/addons/contract-reference.md"

// The add-on documentation lists the programs of each profile. The list is
// generated from the table the manifest and the note for the model come from,
// so the three cannot differ. UPDATE_DOCS=1 writes the block.
func TestDocumentationListsTheProfilesOfTheManifest(t *testing.T) {
	raw, err := os.ReadFile(docsPath)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	from, to := strings.Index(doc, profiles.DocsStart), strings.Index(doc, profiles.DocsEnd)
	if from < 0 || to < from {
		t.Fatalf("the documentation has no %s ... %s block", profiles.DocsStart, profiles.DocsEnd)
	}
	want := profiles.DocsStart + "\n" + profiles.MarkdownTable() + "\n" + profiles.DocsEnd
	if got := doc[from : to+len(profiles.DocsEnd)]; got != want {
		if os.Getenv("UPDATE_DOCS") == "1" {
			if err := os.WriteFile(docsPath, []byte(doc[:from]+want+doc[to+len(profiles.DocsEnd):]), 0o644); err != nil {
				t.Fatal(err)
			}
			return
		}
		t.Fatalf("the profile table of the documentation is stale; run UPDATE_DOCS=1 go test ./addons/sandbox/internal/profiles\nwant:\n%s\ngot:\n%s", want, got)
	}
}
