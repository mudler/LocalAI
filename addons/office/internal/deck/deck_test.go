package deck_test

import (
	"testing"

	"github.com/mudler/LocalAI/addons/office/internal/deck"
)

const validDeck = `{"version":1,"theme":"midnight","slides":[{"layout":"title","blocks":[{"type":"heading","text":"Hi"}]}]}`

func TestValidateAcceptsGoodDeck(t *testing.T) {
	if errs := deck.Validate([]byte(validDeck)); len(errs) != 0 {
		t.Fatalf("expected valid, got %v", errs)
	}
}

func TestValidateRejectsBadTheme(t *testing.T) {
	errs := deck.Validate([]byte(`{"version":1,"theme":"neon","slides":[{"layout":"title","blocks":[]}]}`))
	if len(errs) == 0 {
		t.Fatal("expected theme error")
	}
}

func TestValidateRejectsBadLayoutAndBlock(t *testing.T) {
	errs := deck.Validate([]byte(`{"version":1,"theme":"mono","slides":[{"layout":"carousel","blocks":[{"type":"table"}]}]}`))
	if len(errs) < 2 {
		t.Fatalf("expected layout+block errors, got %v", errs)
	}
}

func TestValidateRejectsBadAccentAndMalformedJSON(t *testing.T) {
	if errs := deck.Validate([]byte(`{"version":1,"theme":"mono","accent":"red","slides":[{"layout":"end","blocks":[]}]}`)); len(errs) == 0 {
		t.Fatal("expected accent error")
	}
	if errs := deck.Validate([]byte(`{ not json`)); len(errs) == 0 {
		t.Fatal("expected json parse error")
	}
}

// --- Parity with the deck validator of the web client ---

// A bullets block with a missing "items" is flagged, as in the web client.
func TestValidateRejectsMissingBulletsItems(t *testing.T) {
	errs := deck.Validate([]byte(`{"version":1,"theme":"midnight","slides":[{"layout":"single","blocks":[{"type":"bullets"}]}]}`))
	found := false
	for _, e := range errs {
		if contains(e, "items") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an items error, got %v", errs)
	}
}

// TS validateDeck treats an EMPTY bullets array as valid (some() over [] is false).
// Go must match so a deck valid in one language is valid in the other.
func TestValidateAcceptsEmptyBulletsArray(t *testing.T) {
	errs := deck.Validate([]byte(`{"version":1,"theme":"midnight","slides":[{"layout":"single","blocks":[{"type":"bullets","items":[]}]}]}`))
	if len(errs) != 0 {
		t.Fatalf("expected empty bullets to be valid (TS parity), got %v", errs)
	}
}

// TS rejects non-string bullet items. Go must too.
func TestValidateRejectsNonStringBulletItems(t *testing.T) {
	errs := deck.Validate([]byte(`{"version":1,"theme":"midnight","slides":[{"layout":"single","blocks":[{"type":"bullets","items":[1,2]}]}]}`))
	if len(errs) == 0 {
		t.Fatal("expected non-string bullet items to be rejected (TS parity)")
	}
}

// TS accepts an empty blocks array on a slide (forEach over [] does nothing).
func TestValidateAcceptsEmptyBlocks(t *testing.T) {
	errs := deck.Validate([]byte(`{"version":1,"theme":"paper","slides":[{"layout":"end","blocks":[]}]}`))
	if len(errs) != 0 {
		t.Fatalf("expected empty blocks slide to be valid, got %v", errs)
	}
}

// TS accepts a 3-digit hex accent (#RGB) per the HEX regex.
func TestValidateAcceptsShortHexAccent(t *testing.T) {
	errs := deck.Validate([]byte(`{"version":1,"theme":"mono","accent":"#abc","slides":[{"layout":"title","blocks":[{"type":"heading","text":"Hi"}]}]}`))
	if len(errs) != 0 {
		t.Fatalf("expected #abc accent to be valid, got %v", errs)
	}
}

// TS flags version !== 1.
func TestValidateRejectsWrongVersion(t *testing.T) {
	errs := deck.Validate([]byte(`{"version":2,"theme":"mono","slides":[{"layout":"end","blocks":[]}]}`))
	if len(errs) == 0 {
		t.Fatal("expected version error")
	}
}

// TS rejects a PRESENT accent that is not a string (typeof d.accent !== "string").
// Go must too: {"accent":42} passes the old ok-guarded check but TS rejects it.
func TestValidateRejectsNonStringAccent(t *testing.T) {
	errs := deck.Validate([]byte(`{"version":1,"theme":"midnight","accent":42,"slides":[{"layout":"title","blocks":[]}]}`))
	found := false
	for _, e := range errs {
		if contains(e, "accent must be a hex color") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected non-string accent to be rejected (TS parity), got %v", errs)
	}
}

// TS rejects a PRESENT background that is not a valid enum string, including a
// present-but-non-string value (BACKGROUNDS.includes(5) is false).
func TestValidateRejectsNonStringBackground(t *testing.T) {
	errs := deck.Validate([]byte(`{"version":1,"theme":"midnight","background":5,"slides":[{"layout":"title","blocks":[]}]}`))
	found := false
	for _, e := range errs {
		if contains(e, "background must be one of solid, gradient, grid") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected non-string background to be rejected (TS parity), got %v", errs)
	}
}

// Positive parity case: valid string accent + background still passes.
func TestValidateAcceptsValidAccentAndBackground(t *testing.T) {
	errs := deck.Validate([]byte(`{"version":1,"theme":"midnight","accent":"#abc","background":"grid","slides":[{"layout":"title","blocks":[]}]}`))
	if len(errs) != 0 {
		t.Fatalf("expected valid accent+background to pass, got %v", errs)
	}
}

// --- Media/embed scheme allowlist parity (deck.ts validateDeck) ---

func hasErr(errs []string, sub string) bool {
	for _, e := range errs {
		if contains(e, sub) {
			return true
		}
	}
	return false
}

func TestValidateRejectsExternalImageRef(t *testing.T) {
	errs := deck.Validate([]byte(`{"version":1,"theme":"midnight","slides":[{"layout":"single","blocks":[{"type":"image","ref":"https://evil/x.png"}]}]}`))
	if !hasErr(errs, "image ref must be an internal image reference") {
		t.Fatalf("expected external image ref rejection, got %v", errs)
	}
}

func TestValidateAcceptsInternalImageRefs(t *testing.T) {
	for _, ref := range []string{"/_/image/x", "/api/documents/1/file"} {
		errs := deck.Validate([]byte(`{"version":1,"theme":"midnight","slides":[{"layout":"single","blocks":[{"type":"image","ref":"` + ref + `"}]}]}`))
		if len(errs) != 0 {
			t.Fatalf("expected internal image ref %q to be valid, got %v", ref, errs)
		}
	}
}

func TestValidateRejectsExternalVideoRefAndPoster(t *testing.T) {
	errs := deck.Validate([]byte(`{"version":1,"theme":"midnight","slides":[{"layout":"single","blocks":[{"type":"video","ref":"https://evil/x.mp4"}]}]}`))
	if !hasErr(errs, "image ref must be an internal image reference") {
		t.Fatalf("expected external video ref rejection, got %v", errs)
	}
	errs = deck.Validate([]byte(`{"version":1,"theme":"midnight","slides":[{"layout":"single","blocks":[{"type":"video","ref":"/_/image/x","poster":"https://evil/p.png"}]}]}`))
	if !hasErr(errs, "image ref must be an internal image reference") {
		t.Fatalf("expected external video poster rejection, got %v", errs)
	}
}

func TestValidateAcceptsInternalVideoRefAndPoster(t *testing.T) {
	errs := deck.Validate([]byte(`{"version":1,"theme":"midnight","slides":[{"layout":"single","blocks":[{"type":"video","ref":"/_/image/x","poster":"/api/documents/1/file"}]}]}`))
	if len(errs) != 0 {
		t.Fatalf("expected internal video ref+poster to be valid, got %v", errs)
	}
}

func TestValidateRejectsNonHttpEmbedURL(t *testing.T) {
	for _, url := range []string{"data:text/html,x", "javascript:alert(1)"} {
		errs := deck.Validate([]byte(`{"version":1,"theme":"midnight","slides":[{"layout":"single","blocks":[{"type":"embed","url":"` + url + `"}]}]}`))
		if !hasErr(errs, "embed url must be an http(s) URL") {
			t.Fatalf("expected embed url %q to be rejected, got %v", url, errs)
		}
	}
}

func TestValidateAcceptsHttpEmbedURL(t *testing.T) {
	errs := deck.Validate([]byte(`{"version":1,"theme":"midnight","slides":[{"layout":"single","blocks":[{"type":"embed","url":"https://ok.example/"}]}]}`))
	if len(errs) != 0 {
		t.Fatalf("expected https embed url to be valid, got %v", errs)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestCountOutlineSlides(t *testing.T) {
	cases := map[string]int{
		"Slide 1: Intro\n- a\nSlide 2: Next\n- b":            2,
		"## Slide 1: Intro\n**Slide 2:** Next\n> slide 3: x": 3,
		"Il deck è stato generato con successo.":             0,
		"": 0,
		"see the slide 2: inline, not a line start": 0,
	}
	for in, want := range cases {
		if got := deck.CountOutlineSlides(in); got != want {
			t.Errorf("CountOutlineSlides(%q) = %d, want %d", in, got, want)
		}
	}
}
