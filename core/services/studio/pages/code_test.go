package pages_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/pages"
)

// The consent to a shared page covers the files of code that the UI puts into
// it: a change to one of them asks the viewer again, a change to a data file
// does not.
var _ = Describe("the code of a shared page", func() {
	ctx := context.Background()
	const dir = team + "/pages/board/"

	// A page with a classic script, a style sheet that imports another, a
	// module that imports a second one, and a data file it reads with fetch.
	rig := func() *sharedRig {
		r := newSharedRig()
		adminPut(r.f, dir+"index.html", `<!doctype html>
<link rel="stylesheet" href="style.css">
<link rel="alternate stylesheet" href="other.css">
<script src="app.js"></script>
<script src="https://cdn.example.com/x.js"></script>
<script type="module">import { draw } from './lib/draw.js'; draw()</script>
<script type="text/plain" src="notes.txt"></script>
<!-- <script src="commented.js"></script> -->
<div style="background: url(bg.png)"></div>
<script>fetch('data.json')</script>`)
		adminPut(r.f, dir+"page.json", `{"title":"Board"}`)
		adminPut(r.f, dir+"app.js", "console.log(1)")
		adminPut(r.f, dir+"style.css", `@import url("theme/base.css"); body{}`)
		adminPut(r.f, dir+"theme/base.css", `@import '../fonts.css'; :root{}`)
		adminPut(r.f, dir+"fonts.css", `a{}`)
		adminPut(r.f, dir+"lib/draw.js", `import './util.js'; export const draw = () => import("../late.js")`)
		adminPut(r.f, dir+"lib/util.js", `import x from 'react'; export default 1`)
		adminPut(r.f, dir+"late.js", `export default 2`)
		adminPut(r.f, dir+"data.json", `{"n":1}`)
		adminPut(r.f, dir+"other.css", `b{}`)
		adminPut(r.f, dir+"notes.txt", `notes`)
		return r
	}

	It("names the scripts, style sheets and modules that the UI puts into the page, and nothing else", func() {
		r := rig()
		p, err := r.svc.GetShared(ctx, "marta", r.team, "board")
		Expect(err).NotTo(HaveOccurred())
		Expect(p.CodeFiles).To(Equal([]string{"app.js", "fonts.css", "late.js", "lib/draw.js", "lib/util.js", "style.css", "theme/base.css"}))
	})

	It("asks again when a script, a style sheet or a module of the page changes", func() {
		for _, file := range []string{"app.js", "style.css", "theme/base.css", "fonts.css", "lib/draw.js", "lib/util.js", "late.js"} {
			r := rig()
			p, err := r.svc.GetShared(ctx, "marta", r.team, "board")
			Expect(err).NotTo(HaveOccurred())
			_, err = r.trust.Allow(ctx, "marta", r.team, "board", p.Hash, false)
			Expect(err).NotTo(HaveOccurred())
			Expect(r.trust.Check(ctx, "marta", r.team, "board", p.Hash, "")).To(Succeed())

			adminPut(r.f, dir+file, "/* changed */ console.log('new code')")
			now, err := r.svc.GetShared(ctx, "marta", r.team, "board")
			Expect(err).NotTo(HaveOccurred())
			Expect(now.Hash).NotTo(Equal(p.Hash), file)
			rev, err := r.trust.State(ctx, "marta", now)
			Expect(err).NotTo(HaveOccurred())
			Expect(rev.State).To(Equal(pages.StateChanged), file)
			Expect(r.trust.Check(ctx, "marta", r.team, "board", now.Hash, "")).To(MatchError(pages.ErrNoConsent), file)
		}
	})

	It("keeps the consent when a data file, an image or a file the page does not load changes", func() {
		for _, file := range []string{"data.json", "bg.png", "other.css", "notes.txt", "commented.js", "new-data.json"} {
			r := rig()
			p, err := r.svc.GetShared(ctx, "marta", r.team, "board")
			Expect(err).NotTo(HaveOccurred())
			adminPut(r.f, dir+file, "changed")
			now, err := r.svc.GetShared(ctx, "marta", r.team, "board")
			Expect(err).NotTo(HaveOccurred())
			Expect(now.Hash).To(Equal(p.Hash), file)
		}
	})

	It("asks again when a file of code that was missing appears", func() {
		r := rig()
		adminPut(r.f, dir+"index.html", `<script src="later.js"></script>`)
		p, err := r.svc.GetShared(ctx, "marta", r.team, "board")
		Expect(err).NotTo(HaveOccurred())
		Expect(p.CodeFiles).To(Equal([]string{"later.js"}))
		adminPut(r.f, dir+"later.js", "alert(1)")
		now, err := r.svc.GetShared(ctx, "marta", r.team, "board")
		Expect(err).NotTo(HaveOccurred())
		Expect(now.Hash).NotTo(Equal(p.Hash))
	})

	It("reads a file of code only from a frame of the current version", func() {
		r := rig()
		p, err := r.svc.GetShared(ctx, "marta", r.team, "board")
		Expect(err).NotTo(HaveOccurred())
		_, err = r.trust.Allow(ctx, "marta", r.team, "board", p.Hash, true)
		Expect(err).NotTo(HaveOccurred())
		c, err := r.trust.Allow(ctx, "marta", r.team, "board", p.Hash, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.trust.CheckRead(ctx, "marta", r.team, "board", c.Hash, "", "app.js")).To(Succeed())

		adminPut(r.f, dir+"app.js", "steal()")
		// The open frame keeps its consent for data, but cannot read the new code.
		Expect(r.trust.CheckRead(ctx, "marta", r.team, "board", c.Hash, "", "app.js")).To(MatchError(pages.ErrCodeChanged))
		Expect(r.trust.CheckRead(ctx, "marta", r.team, "board", c.Hash, "", "data.json")).To(Succeed())
		_, err = r.trust.Allow(ctx, "marta", r.team, "board", p.Hash, false)
		Expect(err).To(MatchError(pages.ErrChanged), "the old version cannot be allowed again")
	})

	It("lets a frame of an allowed version read data, but not code that changed", func() {
		r := rig()
		p, err := r.svc.GetShared(ctx, "marta", r.team, "board")
		Expect(err).NotTo(HaveOccurred())
		visit, err := r.trust.Allow(ctx, "marta", r.team, "board", p.Hash, true)
		Expect(err).NotTo(HaveOccurred())
		adminPut(r.f, dir+"lib/util.js", "steal()")
		Expect(r.trust.CheckRead(ctx, "marta", r.team, "board", p.Hash, visit.Token, "data.json")).To(Succeed())
		Expect(r.trust.CheckRead(ctx, "marta", r.team, "board", p.Hash, visit.Token, "lib/util.js")).To(MatchError(pages.ErrCodeChanged))
		Expect(r.trust.CheckRead(ctx, "marta", r.team, "board", p.Hash, visit.Token, "index.html")).To(MatchError(pages.ErrCodeChanged))
		// The store finds a file whatever the case of its name, so the check
		// does too.
		for _, rel := range []string{"Index.html", "INDEX.HTML", "Page.json", "LIB/Util.js", "lib/UTIL.JS"} {
			Expect(r.trust.CheckRead(ctx, "marta", r.team, "board", p.Hash, visit.Token, rel)).To(MatchError(pages.ErrCodeChanged), rel)
		}
		Expect(r.trust.CheckRead(ctx, "marta", r.team, "board", p.Hash, visit.Token, "Data.json")).To(Succeed())
	})

	It("ignores references in comments, in other tags and outside the folder", func() {
		r := rig()
		adminPut(r.f, dir+"index.html", `<textarea><script src="in-text.js"></script></textarea>
<SCRIPT SRC="Upper.js"></SCRIPT>
<script src="../../secret.js"></script>
<script src="/abs.js"></script>
<script src="a%2e%2e/b.js"></script>
<script src="%2e%2e/x.js"></script>
<script src='q.js?v=2#x'></script>
<script src=bare.js defer></script>
<link rel=stylesheet href="s.css" media="print">
<style>@import "i.css"; @import url(https://x.example/y.css);</style>
<script type="module" src="./m.js"></script>`)
		adminPut(r.f, dir+"m.js", "import a from \"../../escape.js\"; import b from `./tpl.js`; import(`./dyn.js`)")
		p, err := r.svc.GetShared(ctx, "marta", r.team, "board")
		Expect(err).NotTo(HaveOccurred())
		Expect(p.CodeFiles).To(Equal([]string{"Upper.js", "a../b.js", "bare.js", "dyn.js", "i.css", "m.js", "q.js", "s.css"}))
	})

	It("gives an own page no files of code: it needs no consent", func() {
		r := rig()
		put(r.f, "marta", "/pages/mine/index.html", `<script src="app.js"></script>`)
		p, err := r.svc.Get(ctx, "marta", "mine")
		Expect(err).NotTo(HaveOccurred())
		Expect(p.CodeFiles).To(BeEmpty())
	})
})
