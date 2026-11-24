# LocalAI documentation

The documentation is a Hugo site that uses the Relearn theme (git submodule in `themes/hugo-theme-relearn`). It is published under `/docs/` of the main site.

## Build it

```bash
git submodule update --init docs/themes/hugo-theme-relearn
go run ./.github/ci/modelslist.go ./gallery/index.yaml > docs/static/gallery.html
cd docs
hugo --minify --baseURL https://localai.io/docs/
```

Use the Hugo version that `.github/workflows/docs-pr.yml` pins (extended edition). Run `hugo server` to preview.

## Layout

| Path | What it holds |
|---|---|
| `content/` | The pages. One folder per section. |
| `redirects.txt` | Every URL that was ever published. Never remove a line. |
| `brand/` | Copy of the LocalAI colour tokens. Do not edit it. |
| `assets/css/` | Colour variants (generated) and small style additions. |
| `assets/images/` | Logo and favicon. |
| `tools/` | Scripts for the docs build. |

## Rules for pages

- The URL of a page follows its file path. Do not set `url` in the front matter.
- When you move or rename a page, put its old URL under `aliases` in the front matter of the new page.
- Link to other pages with `{{% relref "/section/page" %}}`.
- Run `.github/ci/check-docs-redirects.sh docs/public` after a build. It checks every URL in `redirects.txt`.
- Colours: edit nothing by hand. Update `brand/theme-localai.css`, then run `python3 docs/tools/gen-theme-variants.py`.
