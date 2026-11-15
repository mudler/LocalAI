# localai.io

The Hugo site served at the root of localai.io. The documentation is a separate
Hugo site in [`../docs`](../docs) and is served under `/docs/`. CI builds both
and uploads them as a single GitHub Pages artifact
(see [`.github/workflows/gh-pages.yml`](../.github/workflows/gh-pages.yml)).

## Running locally

From the repository root:

```bash
make website    # this site only, http://localhost:1313/
make docs       # documentation only, http://localhost:1313/docs/
make site       # build both, merged into website/public, exactly as CI does
make site-serve # the same merged build, served on http://localhost:8000
```

`make website` and `make docs` run `hugo serve`, so they pick up edits live but
only cover one site at a time. Use `make site-serve` when you need the cross
links between the two sites, or the redirects from the pre-split URLs, to work.

`make site` also runs `.github/ci/gen-redirects.sh`, which leaves a meta-refresh
page at every URL the documentation used to occupy before it moved under
`/docs/`. GitHub Pages has no server-side redirects, so those files are the only
thing keeping the old links alive.

Set `SITE_BASE_URL` to change the base URL the merged build is generated for
(default `http://localhost:8000`).

## Demo slots

Every feature section of the home page and every feature page shows its visual in a
`<figure class="demo-slot">`. Today the slot holds a coded illustration: a window frame with
generic content, marked "Illustration". It is a picture of an idea, not a screenshot, and it
shows no data that claims a real result.

To replace an illustration with a muted looping video, set one field:

- Home page: `demo` of the section in `data/home.yaml`.
- Feature page: `demo` in the front matter of `content/features/<page>.md`.

The value is a name. Put three files under `static/media/` with that name:
`<name>.mp4`, `<name>.webm` and the poster `<name>.jpg` (1280x800, under 1.5 MB for the two
videos together). The partial `layouts/partials/demo-slot.html` then renders a `<video>` with
`muted loop playsinline preload="none"` and the poster. `static/js/main.js` plays it only while
it is on screen and never when the visitor asks for reduced motion. The poster stays in that case.

The illustration of a section is `illus` (a file in `layouts/partials/illus/`).

## Pages and where they live

| Page | Template | Content |
|---|---|---|
| Home | `layouts/index.html` | `data/home.yaml`, `data/stats.yaml` |
| Features | `layouts/features/` | `content/features/*.md` |
| Engines | `layouts/engines/list.html` | `data/engines.yaml` |
| Blog and tags | `layouts/blog/`, `layouts/_default/term.html` | `content/blog/*.md` |
| 404 | `layouts/404.html` | none |

Style sheets: `localai.css` (shell and components), `illus.css` (illustrations), and `blog.css`
and `engines.css` (loaded by the front matter field `extracss`). Colours come from
`theme-localai.css`, which the branding kit generates: do not edit it here.

## Search engines and sharing

`layouts/partials/head.html` writes the title, description, canonical link, one Open Graph and
Twitter set, and the JSON-LD of the home page. A page can set `description`, `image` and
`noindex` in its front matter. A feature page uses `static/img/share/<page>.jpg` as its share
card when the file exists. All other pages use `static/img/brand/og-card-1200x630.jpg`.

`static/sitemap.xml` is a sitemap index. It lists `sitemap-site.xml` (this site, made from
`layouts/index.sitemapsite.xml`) and `docs/sitemap.xml` (the docs site). `static/robots.txt`
points to the index.
