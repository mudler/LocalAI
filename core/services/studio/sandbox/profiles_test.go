package sandbox_test

import (
	"strings"
	"testing"

	"github.com/mudler/LocalAI/core/services/studio/sandbox"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

func TestProfileNoteNamesTheProfileItsToolsAndTheOthers(t *testing.T) {
	base := contract.ExecutionProfile{ID: "base", Tools: []contract.ProfileTool{{Name: "jq"}, {Name: "python3", Detail: "pandas"}}}
	media := contract.ExecutionProfile{ID: "media", Tools: []contract.ProfileTool{{Name: "jq"}, {Name: "python3", Detail: "pandas"}, {Name: "ffmpeg"}, {Name: "sox"}}}
	all := []contract.ExecutionProfile{base, media}
	got := sandbox.ProfileNote(base, all)
	for _, want := range []string{"profile is base", "jq, python3 (pandas)", "media (adds ffmpeg, sox)", sandbox.NoInstallNote} {
		if !strings.Contains(got, want) {
			t.Errorf("note %q lacks %q", got, want)
		}
	}
	got = sandbox.ProfileNote(media, all)
	if strings.Contains(got, "Other profiles") {
		t.Errorf("media has every tool of base, so no other profile adds anything: %q", got)
	}
	if !strings.Contains(got, "ffmpeg, sox") {
		t.Errorf("note %q lacks the tools of media", got)
	}
	if s := sandbox.ProfileToolsSentence(base); s != "Sandbox profile base: jq, python3 (pandas)." {
		t.Errorf("sentence = %q", s)
	}
}

func TestMissingToolNoteNamesTheProfileAndThoseWithMore(t *testing.T) {
	base := contract.ExecutionProfile{ID: "base", Tools: []contract.ProfileTool{{Name: "jq"}}}
	media := contract.ExecutionProfile{ID: "media", Tools: []contract.ProfileTool{{Name: "jq"}, {Name: "ffmpeg"}}}
	got := sandbox.MissingToolNote("base", []contract.ExecutionProfile{base, media})
	for _, want := range []string{"not found in the base sandbox profile", "media (adds ffmpeg)", "sandbox_profile", "Do not try to install"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q lacks %q", got, want)
		}
	}
	if got := sandbox.MissingToolNote("media", []contract.ExecutionProfile{base, media}); strings.Contains(got, "Profiles with more programs") {
		t.Errorf("media has the most programs, so no profile has more: %q", got)
	}
}
