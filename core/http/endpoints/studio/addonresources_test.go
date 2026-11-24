package studio

import (
	"net/http"
	"net/http/httptest"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	studiosvc "github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

var _ = Describe("the add-on row an agent form reads", func() {
	row := func(target string, header string) (out struct{ Noun, Plural, Lang string }, tools []string) {
		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, target, nil)
		if header != "" {
			req.Header.Set("Accept-Language", header)
		}
		c := e.NewContext(req, httptest.NewRecorder())
		got := addonOut(c, studiosvc.AddonInfo{ID: "tables", Tools: 2,
			ToolList: []studiosvc.AddonToolEntry{{Name: "rows-insert", Write: true}, {Name: "rows-query"}},
			Resources: &studiosvc.ResourceLabel{Noun: "database", NounPlural: "databases",
				Translations: map[string]contract.ResourceText{"it": {Noun: "database", NounPlural: "database"}, "de": {Noun: "Datenbank", NounPlural: "Datenbanken"}}}})
		Expect(got.Resources).NotTo(BeNil())
		for _, t := range got.ToolList {
			tools = append(tools, t.Name+":"+t.Kind)
		}
		return struct{ Noun, Plural, Lang string }{got.Resources.Noun, got.Resources.NounPlural, got.Resources.Language}, tools
	}
	It("names the tools with their kind", func() {
		_, tools := row("/", "")
		Expect(tools).To(Equal([]string{"rows-insert:write", "rows-query:read"}))
	})
	It("says the word in English by default", func() {
		r, _ := row("/", "")
		Expect(r).To(Equal(struct{ Noun, Plural, Lang string }{"database", "databases", ""}))
	})
	It("uses the language of the request when the manifest translates it", func() {
		r, _ := row("/?lang=de", "")
		Expect(r).To(Equal(struct{ Noun, Plural, Lang string }{"Datenbank", "Datenbanken", "de"}))
		r, _ = row("/", "de-AT,en;q=0.5")
		Expect(r.Noun).To(Equal("Datenbank"))
	})
	It("falls back to English for a language the manifest does not translate", func() {
		r, _ := row("/?lang=ko", "")
		Expect(r).To(Equal(struct{ Noun, Plural, Lang string }{"database", "databases", ""}))
	})
	It("shows no resource label for an add-on that does not opt in", func() {
		e := echo.New()
		c := e.NewContext(httptest.NewRequest(http.MethodGet, "/", nil), httptest.NewRecorder())
		Expect(addonOut(c, studiosvc.AddonInfo{ID: "office", Tools: 1}).Resources).To(BeNil())
	})
})
