package deck_test

import (
	"github.com/mudler/LocalAI/addons/office/internal/deck"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const validDeck = `{"version":1,"theme":"midnight","slides":[{"layout":"title","blocks":[{"type":"heading","text":"Hi"}]}]}`

var _ = Describe("deck validation", func() {
	It("accepts a good deck", func() {
		Expect(deck.Validate([]byte(validDeck))).To(BeEmpty())
	})

	It("rejects a bad theme", func() {
		Expect(deck.Validate([]byte(`{"version":1,"theme":"neon","slides":[{"layout":"title","blocks":[]}]}`))).NotTo(BeEmpty(), "expected theme error")
	})

	It("rejects a bad layout and block", func() {
		errs := deck.Validate([]byte(`{"version":1,"theme":"mono","slides":[{"layout":"carousel","blocks":[{"type":"table"}]}]}`))
		Expect(len(errs)).To(BeNumerically(">=", 2), "expected layout+block errors, got %v", errs)
	})

	It("rejects a bad accent and malformed JSON", func() {
		Expect(deck.Validate([]byte(`{"version":1,"theme":"mono","accent":"red","slides":[{"layout":"end","blocks":[]}]}`))).NotTo(BeEmpty(), "expected accent error")
		Expect(deck.Validate([]byte(`{ not json`))).NotTo(BeEmpty(), "expected json parse error")
	})

	Describe("parity with the deck validator of the web client", func() {
		// A bullets block with a missing "items" is flagged, as in the web client.
		It("rejects a bullets block with missing items", func() {
			errs := deck.Validate([]byte(`{"version":1,"theme":"midnight","slides":[{"layout":"single","blocks":[{"type":"bullets"}]}]}`))
			Expect(errs).To(ContainElement(ContainSubstring("items")))
		})

		// TS validateDeck treats an EMPTY bullets array as valid (some() over [] is false).
		// Go must match so a deck valid in one language is valid in the other.
		It("accepts an empty bullets array", func() {
			Expect(deck.Validate([]byte(`{"version":1,"theme":"midnight","slides":[{"layout":"single","blocks":[{"type":"bullets","items":[]}]}]}`))).To(BeEmpty(), "expected empty bullets to be valid (TS parity)")
		})

		// TS rejects non-string bullet items. Go must too.
		It("rejects non-string bullet items", func() {
			Expect(deck.Validate([]byte(`{"version":1,"theme":"midnight","slides":[{"layout":"single","blocks":[{"type":"bullets","items":[1,2]}]}]}`))).NotTo(BeEmpty(), "expected non-string bullet items to be rejected (TS parity)")
		})

		// TS accepts an empty blocks array on a slide (forEach over [] does nothing).
		It("accepts an empty blocks array", func() {
			Expect(deck.Validate([]byte(`{"version":1,"theme":"paper","slides":[{"layout":"end","blocks":[]}]}`))).To(BeEmpty())
		})

		// TS accepts a 3-digit hex accent (#RGB) per the HEX regex.
		It("accepts a short hex accent", func() {
			Expect(deck.Validate([]byte(`{"version":1,"theme":"mono","accent":"#abc","slides":[{"layout":"title","blocks":[{"type":"heading","text":"Hi"}]}]}`))).To(BeEmpty())
		})

		// TS flags version !== 1.
		It("rejects a wrong version", func() {
			Expect(deck.Validate([]byte(`{"version":2,"theme":"mono","slides":[{"layout":"end","blocks":[]}]}`))).NotTo(BeEmpty(), "expected version error")
		})

		// TS rejects a PRESENT accent that is not a string (typeof d.accent !== "string").
		// Go must too: {"accent":42} passes the old ok-guarded check but TS rejects it.
		It("rejects a non-string accent", func() {
			errs := deck.Validate([]byte(`{"version":1,"theme":"midnight","accent":42,"slides":[{"layout":"title","blocks":[]}]}`))
			Expect(errs).To(ContainElement(ContainSubstring("accent must be a hex color")))
		})

		// TS rejects a PRESENT background that is not a valid enum string, including a
		// present-but-non-string value (BACKGROUNDS.includes(5) is false).
		It("rejects a non-string background", func() {
			errs := deck.Validate([]byte(`{"version":1,"theme":"midnight","background":5,"slides":[{"layout":"title","blocks":[]}]}`))
			Expect(errs).To(ContainElement(ContainSubstring("background must be one of solid, gradient, grid")))
		})

		// Positive parity case: valid string accent + background still passes.
		It("accepts a valid accent and background", func() {
			Expect(deck.Validate([]byte(`{"version":1,"theme":"midnight","accent":"#abc","background":"grid","slides":[{"layout":"title","blocks":[]}]}`))).To(BeEmpty())
		})
	})

	// Media/embed scheme allowlist parity (deck.ts validateDeck).
	Describe("media and embed allowlist", func() {
		It("rejects an external image ref", func() {
			errs := deck.Validate([]byte(`{"version":1,"theme":"midnight","slides":[{"layout":"single","blocks":[{"type":"image","ref":"https://evil/x.png"}]}]}`))
			Expect(errs).To(ContainElement(ContainSubstring("image ref must be an internal image reference")))
		})

		It("accepts internal image refs", func() {
			for _, ref := range []string{"/_/image/x", "/api/documents/1/file"} {
				errs := deck.Validate([]byte(`{"version":1,"theme":"midnight","slides":[{"layout":"single","blocks":[{"type":"image","ref":"` + ref + `"}]}]}`))
				Expect(errs).To(BeEmpty(), "internal image ref %q", ref)
			}
		})

		It("rejects an external video ref and poster", func() {
			errs := deck.Validate([]byte(`{"version":1,"theme":"midnight","slides":[{"layout":"single","blocks":[{"type":"video","ref":"https://evil/x.mp4"}]}]}`))
			Expect(errs).To(ContainElement(ContainSubstring("image ref must be an internal image reference")), "external video ref")
			errs = deck.Validate([]byte(`{"version":1,"theme":"midnight","slides":[{"layout":"single","blocks":[{"type":"video","ref":"/_/image/x","poster":"https://evil/p.png"}]}]}`))
			Expect(errs).To(ContainElement(ContainSubstring("image ref must be an internal image reference")), "external video poster")
		})

		It("accepts an internal video ref and poster", func() {
			Expect(deck.Validate([]byte(`{"version":1,"theme":"midnight","slides":[{"layout":"single","blocks":[{"type":"video","ref":"/_/image/x","poster":"/api/documents/1/file"}]}]}`))).To(BeEmpty())
		})

		It("rejects a non-http embed URL", func() {
			for _, url := range []string{"data:text/html,x", "javascript:alert(1)"} {
				errs := deck.Validate([]byte(`{"version":1,"theme":"midnight","slides":[{"layout":"single","blocks":[{"type":"embed","url":"` + url + `"}]}]}`))
				Expect(errs).To(ContainElement(ContainSubstring("embed url must be an http(s) URL")), "embed url %q", url)
			}
		})

		It("accepts an http embed URL", func() {
			Expect(deck.Validate([]byte(`{"version":1,"theme":"midnight","slides":[{"layout":"single","blocks":[{"type":"embed","url":"https://ok.example/"}]}]}`))).To(BeEmpty())
		})
	})

	It("counts the slides of an outline", func() {
		cases := map[string]int{
			"Slide 1: Intro\n- a\nSlide 2: Next\n- b":            2,
			"## Slide 1: Intro\n**Slide 2:** Next\n> slide 3: x": 3,
			"Il deck è stato generato con successo.":             0,
			"": 0,
			"see the slide 2: inline, not a line start": 0,
		}
		for in, want := range cases {
			Expect(deck.CountOutlineSlides(in)).To(Equal(want), "CountOutlineSlides(%q)", in)
		}
	})
})
