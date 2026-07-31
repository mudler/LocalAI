# localai.io marketing site

The Hugo site served at the root of localai.io. The documentation is a separate
Hugo site in [`../docs`](../docs) and is served under `/docs/`. CI builds both
and uploads them as a single GitHub Pages artifact
(see [`.github/workflows/gh-pages.yml`](../.github/workflows/gh-pages.yml)).

## Running locally

From the repository root:

```bash
make website    # marketing site only, http://localhost:1313/
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
