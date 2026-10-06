# Theme contract

A theme is one CSS file. It defines the roles below for light and for
dark, with this structure:

```css
:root { /* light */ }
@media (prefers-color-scheme: dark) {
  :root:not([data-theme="light"]) { /* dark */ }
}
:root[data-theme="dark"] { /* dark, forced */ }
```

Put one declaration per line. Colours are `#rrggbb`. `bin/contrast`
reads this layout. The two dark blocks must hold the same values.

## Roles

| Variable | Role | Checked against |
|---|---|---|
| `--dk-canvas` | App background | |
| `--dk-inset` | Recessed surface: secondary buttons, tabs track, skeleton | |
| `--dk-card` | Raised surface: cards, active nav row, dialogs | |
| `--dk-hover` | Hover wash on a surface | |
| `--dk-text` | Primary text | 4.5 on canvas, inset, card, hover, accent-wash |
| `--dk-muted` | Secondary text | 4.5 on canvas, inset, card, hover, accent-wash |
| `--dk-edge` | Decorative divider and card border | none |
| `--dk-control-edge` | Border of inputs and other controls | 3 on canvas, inset, card |
| `--dk-accent` | Accent fill and focus ring | 3 on canvas, card, inset |
| `--dk-on-accent` | Text and icons on the accent fill | 4.5 on accent |
| `--dk-accent-text` | Accent used as text or link colour | 4.5 on canvas, card, accent-wash |
| `--dk-accent-wash` | Soft accent tint: selected row, badge | |
| `--dk-ok` | Success fill or dot | 3 on card |
| `--dk-on-ok` | Text on the success fill | 4.5 on ok |
| `--dk-ok-text` | Success as text | 4.5 on canvas, card |
| `--dk-ok-wash` | Soft success tint: badge, notice | `ok-text` 4.5 |
| `--dk-warn` | Warning fill or dot | 3 on card |
| `--dk-on-warn` | Text on the warning fill | 4.5 on warn |
| `--dk-warn-text` | Warning as text | 4.5 on canvas, card |
| `--dk-warn-wash` | Soft warning tint | `warn-text` 4.5 |
| `--dk-error` | Error fill or dot | 3 on card |
| `--dk-on-error` | Text on the error fill | 4.5 on error |
| `--dk-error-text` | Error as text | 4.5 on canvas, card, hover |
| `--dk-error-wash` | Soft error tint: danger button, error badge | `error-text` 4.5 |
| `--dk-inverse` | Inverse surface: the toast pill | |
| `--dk-on-inverse` | Text on the inverse surface | 4.5 on inverse |
| `--dk-scrim` | Dialog veil. Any CSS colour, may have alpha | not checked |
| `--dk-shadow-tint` | Shadow colour as space-separated channels, for example `20 24 40` | not checked |
| `--dk-shadow-k` | Shadow strength multiplier. About 1 in light, 2 to 3 in dark | not checked |

## Grammar notes

- Added in 0.2.0: the three `-wash` roles, and the extra checks for
  `muted` on hover and accent-wash, `accent` on inset and `error-text`
  on hover. Themes written for 0.1.0 need the three wash roles.
- Success and error toasts are solid fills (`ok` with `on-ok`, `error`
  with `on-error`). The neutral toast uses `inverse`.
- Hover on a filled accent control mixes the accent with `--dk-text`
  using `color-mix`. No extra role is needed.
- Danger buttons are a tinted wash with error-coloured text, never a
  solid error fill. Use `--dk-error-text` on a wash of `--dk-error`.
- The current navigation row lifts onto `--dk-card` with
  `--dk-shadow-rest`. That lift is the whole active indicator.
- Focus is a 2 px outline in `--dk-accent` with an offset. Never remove it.
- Disabled controls use opacity .5 and no pointer events.

## Checking a theme

```sh
bin/contrast tokens/themes/sample-ink.css
```

Thresholds: text pairs 4.5, control and focus pairs 3. The exit status is
non-zero on any miss or any missing role.
