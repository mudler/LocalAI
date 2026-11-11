# Coding Style

The project has the following .editorconfig:

```
root = true

[*]
indent_style = space
indent_size = 2
end_of_line = lf
charset = utf-8
trim_trailing_whitespace = true
insert_final_newline = true

[*.go]
indent_style = tab

[Makefile]
indent_style = tab

[*.proto]
indent_size = 2

[*.py]
indent_size = 4

[*.js]
indent_size = 2

[*.yaml]
indent_size = 2

[*.md]
trim_trailing_whitespace = false
```

- Use comments sparingly to explain why code does something, not what it does. Comments are there to add context that would be difficult to deduce from reading the code.
- Prefer modern Go e.g. use `any` not `interface{}`

## Logging

Use `github.com/mudler/xlog` for logging which has the same API as slog.

## Go tests

All Go tests — including backend tests — must use [Ginkgo](https://onsi.github.io/ginkgo/) (v2) with Gomega matchers, not the stdlib `testing` package with `t.Run` / `t.Errorf`. A test file should register a suite with `RegisterFailHandler(Fail)` in a `TestXxx(t *testing.T)` bootstrap and use `Describe`/`Context`/`It` blocks for the actual cases. Look at any existing `*_test.go` under `core/` or `pkg/` for a template.

Do not mix styles within a package. If you are extending tests in a package that already uses Ginkgo, keep using Ginkgo. If you find stdlib-style Go tests in the tree, treat them as tech debt to be migrated rather than as a pattern to follow.

This is enforced by `golangci-lint` via the `forbidigo` linter (see `.golangci.yml`); calls like `t.Errorf` / `t.Fatalf` / `t.Run` / `t.Skip` / `t.Logf` are flagged. Run `make lint` locally before submitting; the same check runs in CI (`.github/workflows/lint.yml`).

## Sentences the host writes to a chat

A sentence that the host (not the model) writes to a person in a messaging chat is a key in
`core/services/studio/hostmsg/locales/*.json`, with a template for each of the six languages
(`en`, `it`, `de`, `es`, `fr`, `pt-BR`), and is written with `msg` or a `hostmsg.Voice` in
`core/services/studio`. Do not pass a fixed English string to `say`, `Post` or `Outgoing`:
`hostmsg_guard_test.go` fails. The catalog test checks that every key has every language and
the same `{placeholders}`. Text for the model (tool descriptions, notes of a turn) stays English
and is not in the catalog.

## Outbound HTTP

All outbound HTTP must go through `github.com/mudler/LocalAI/pkg/httpclient` rather than the standard library's default client. Use `httpclient.New(...)` (no body deadline — safe for streaming/SSE) or `httpclient.NewWithTimeout(d, ...)` (simple request/response). Both **refuse redirects by default** and set a TLS 1.2 floor.

The reason is GHSA-3mj3-57v2-4636: the std default client follows redirects, and on a *cross-host* redirect Go forwards custom credential headers (e.g. Anthropic's `x-api-key`) to the redirect target, leaking the secret. `httpclient` fails closed instead.

- Need to follow redirects (download CDNs, registry blobs, GitHub asset URLs)? Pass `httpclient.WithFollowRedirects()` — it still strips credential headers on any cross-host hop.
- Have a custom transport (IP-pinned dialer, HTTP/2 tuning, a credential-injecting `RoundTripper`)? Pass `httpclient.WithTransport(rt)`, basing the transport on `httpclient.HardenedTransport()` to keep the TLS floor. Handed a `*http.Client` by a library? `httpclient.Harden(c)` applies the policy in place.

This is enforced by `forbidigo` (see `.golangci.yml`): `http.DefaultClient` and `http.Get`/`Post`/`PostForm`/`Head` are flagged. The `&http.Client{}` composite literal can't be matched precisely by forbidigo without also flagging legitimate `*http.Client` type references, so that form is caught by review — don't construct raw clients.

## Documentation

The project documentation is located in `docs/content`. When adding new features or changing existing functionality, it is crucial to update the documentation to reflect these changes. This helps users understand how to use the new capabilities and ensures the documentation stays relevant.

- **Docs-with-code rule**: When you change user-facing behavior (API endpoints, CLI flags, config keys, or features), update the corresponding page under `docs/content/` in the SAME change, not as a follow-up. A user-facing change without a matching docs update is incomplete. The PR template carries a checklist item for this.
- **Feature Documentation**: If you add a new feature (like a new backend or API endpoint), create a new markdown file in `docs/content/features/` explaining what it is, how to configure it, and how to use it.
- **Configuration**: If you modify configuration options, update the relevant sections in `docs/content/`.
- **Examples**: providing concrete examples (like YAML configuration blocks) is highly encouraged to help users get started quickly.
- **Shortcodes**: Use `{{% notice note %}}`, `{{% notice tip %}}`, or `{{% notice warning %}}` for callout boxes. Do **not** use `{{% alert %}}` — that shortcode does not exist in this project's Hugo theme and will break the docs build.

## React UI styling

The React UI ships a design system in `core/http/react-ui/src/App.css`: design
tokens, form grids, data tables, stat cards, callouts, plus a small semantic
primitive layer (`.stack`, `.hstack`, `.text-note`, `.text-meta`, `.tone-*`,
`.icon-chip`). **Use it instead of `style={{ ... }}`.** Inline styles are a
spacing or colour decision made in one file, so no two pages end up sharing a
rhythm, which is the main reason the app reads as unfinished.

Inline styles are still correct for values that are genuinely computed at
runtime: `width: ${pct}%`, a data-driven `background`, a tooltip's coordinates.
Everything else belongs in a class.

A ratchet enforces this:

```sh
cd core/http/react-ui
npm run lint:inline-styles          # fails if the count went UP
npm run lint:inline-styles:report   # per-file counts, worst first
npm run lint:inline-styles:write    # refresh the baseline after converting
```

The gate also fails on **duplicate `className` attributes on one element**. JSX
keeps the last and silently drops the first, so `<i className={icon}
className="text-xs" />` loses its icon while passing lint, the build and the e2e
suite. Converting a style to a class on an element that already has a
`className` is the usual way to introduce one; merge them into a single
attribute instead.

When converting a page, prefer naming the shapes it actually has
(`.p2p-diagram`, `.usage-tile`) over adding more utilities, and check whether an
existing block already covers it: the Nodes page reuses the P2P setup shapes,
and Model Editor reuses the Settings section rail.

## React UI controls

The shared controls live in `core/http/react-ui/src/components/ui/` (read its
`README.md`), with the form classes of the vendored kit (`dk-input`,
`dk-select`, `dk-check`) and `src/components/SideSheet.jsx`. Every screen uses
them. `/app/ui-catalog` shows each one with an example.

- Use the kit for every button (`Button`), text input (`dk-input`), select
  (`dk-select`), switch (`Switch`), checkbox (`<input type="checkbox"
  className="dk-check">`), segmented control (`Segmented`), pill
  (`StatusChip`), callout (`Callout`), pane (`SidePane`) and sheet
  (`SideSheet`). Do not write a new one. Do not restyle one with a page class:
  no size, colour, radius or width override.
- A switch applies at once. A checkbox applies when the person presses Save.
  Do not pair a switch with a Save button.
- One `primary` button per region: the page, an open pane and an open sheet
  each have at most one. Buttons, inputs and selects share one height
  (`--ctl-h`); use the `sm` size on all of a row, not on one of them.
- A warning or an explanation uses `Callout`. A state uses `StatusChip`. Do not
  colour a `span` to look like either.
- A detail beside a list is a `SidePane` (360 px, `wide` 400 px). A create or
  edit form is a `SideSheet` (480 px, `wide` 720 px). Never write `dk-sheet`
  markup in a page. Never set a width on a sheet or a pane.
- A form with more than about eight fields becomes steps or a disclosure.
  Required fields come first.
- `src/components/Toggle.jsx` is legacy. Do not import it in new code. Use
  `Switch` (applies at once) or a checkbox (applies on Save).
- Button wording follows the product. "New X" starts making a new thing, "Add
  X" attaches an existing one, and the primary button of the form says "Save"
  or "Save and test". Do not write "Create X".

A ratchet enforces the parts it can see:

```sh
cd core/http/react-ui
npm run lint:ui-kit          # fails on a new violation, or on a baseline line that is gone
npm run lint:ui-kit:report   # counts per rule
npm run lint:ui-kit:write    # refresh ui-kit-baseline.txt after you fixed some
```

It flags a checkbox without `dk-check`, an import of `Toggle`, `dk-sheet` in
JSX, a CSS width on a sheet or pane, and the legacy `btn` class. Existing
violations sit in `ui-kit-baseline.txt` and may only go down. Do not add a
line to it to make a check pass.

A second ratchet keeps literal colours out of pages. Colour comes from the
theme roles (`--dk-*`, and the `--color-*` names in `src/theme.css`), never
from a hex value or an `rgb()` in a page:

```sh
cd core/http/react-ui
npm run lint:colours          # fails on a new literal colour, or on a baseline line that is gone
npm run lint:colours:report   # counts per file
npm run lint:colours:write    # refresh colour-baseline.txt after you fixed some
```

Use `var(--dk-accent)` and the other roles, or `color-mix(in srgb, var(--dk-text) 20%, transparent)`
for a tint. The palette files, the vendored kit and the third-party vendor
colours in `src/utils/format.js` are exempt. Do not add a line to
`colour-baseline.txt` to make a check pass.

Before you finish UI work, run `npm run lint:ui-kit` and attach screenshots at
1280 px and 390 px wide, in light and dark.
