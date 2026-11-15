# @localai/ui-kit

The UI kit of the LocalAI web UI as a package: the shared controls, their CSS,
the icon set and the LocalAI palette. The web UI uses it, and another React app
can use it to look the same.

- `react` (18 or later) is the only peer dependency. The kit has no router,
  no translation library and no app context.
- A control that can be a link takes the app's router link as
  `linkComponent`. Without one, `to` renders a plain `<a href>`.
- Every word a control shows arrives as a prop. The defaults are English.

Usage, the rules for each control and the "which control" table are in the web
UI's kit guide: `src/components/ui/README.md`. To see every control in every
state, open `/app/ui-catalog` in the web UI.

## What is in it

| Path | What |
|---|---|
| `src/index.js` | The one import: the controls, their models and the icon |
| `src/Button.jsx`, `src/buttonClass.js` | Button and the class list for markup that cannot use it |
| `src/Callout.jsx`, `src/Notice.jsx`, `src/noticeModel.js` | Callout and the "this is off, why, the fix" notice |
| `src/LoadError.jsx`, `src/loadErrorModel.js` | The error state of a region that failed to load |
| `src/EmptyState.jsx`, `src/Mascot.jsx` | The empty state and the llama picture |
| `src/StatusChip.jsx`, `src/status.js` | The status chip and the status vocabulary |
| `src/Switch.jsx`, `src/Segmented.jsx`, `src/SearchField.jsx` | Switch, segmented filter, search box |
| `src/Slider.jsx`, `src/sliderModel.js` | The number slider |
| `src/Page.jsx` | The page frame |
| `src/Icon.jsx`, `src/icons.js`, `src/brandGlyphs.jsx` | The icon, drawn inline |
| `styles.css` | The one CSS entry for an app without the web UI theme |
| `css/kit.css` | The rules of the controls, the icons and the page frame |
| `css/defaults.css` | The product tokens that `kit.css` reads |
| `themes/localai.css` | The LocalAI palette: the `--dk-*` colour roles, light and dark |
| `vendor/` | The vendored shared kit: base tokens, motion, `dk-*` classes, icon sprite |
| `scripts/gen-icons.mjs` | Writes `src/icons.js` from `vendor/icons/sprite.svg` |

The web UI keeps thin wrappers at the old paths (`src/components/ui/*.jsx`).
`Button` and `Notice` pass react-router's `Link`. `LoadError` and
`StatusChip` pass the translated words.

## Use it in another app

The package is not published. An app in this repository imports it by path.
With Vite, an alias does it:

```js
// vite.config.js
resolve: {
  alias: {
    '@localai/ui-kit': path.resolve(__dirname, '<path to>/core/http/react-ui/packages/ui-kit'),
  },
  // The kit files resolve `react` from the web UI folder. One copy of React
  // in the bundle:
  dedupe: ['react', 'react-dom'],
},
```

```jsx
import '@fontsource-variable/geist'          // optional: the product font
import '@localai/ui-kit/styles.css'
import { Button, StatusChip } from '@localai/ui-kit'
import { Link } from 'react-router-dom'      // only if the app has a router

<Button variant="primary" icon="plus">New agent</Button>
<Button to="/settings" linkComponent={Link}>Settings</Button>
<StatusChip status="running" />
<StatusChip status="running" words={(key) => t(`chip.${key}`)} />
```

The JSX files need a JSX transform (the React plugin of Vite does it). Set
`data-theme="light"` or `data-theme="dark"` on `<html>` to choose the mode.
Without the attribute, the palette follows the system setting. The kit does
not style `body`; give it `background: var(--dk-canvas)` and
`color: var(--dk-text)`.

A bundle that imports only `Button` and `StatusChip` adds about 8 KB (gzip)
of JavaScript to React, most of it the icon set, and about 16 KB (gzip) of
CSS. Vite also copies the mascot picture (10 KB) to the output, even when
`Mascot` is not used; the page does not load it.

## CSS

### One entry

`styles.css` loads the files in this order. The order matters: a later file
overrides an earlier one.

1. `vendor/tokens.css`: the kit's base tokens (`--dk-space-*`, `--dk-z-*`).
2. `themes/localai.css`: the LocalAI palette (`--dk-canvas`, `--dk-text`,
   `--dk-accent` and the other colour roles).
3. `vendor/motion.css`: keyframes and motion classes (`dk-spin`, `dk-veil-in`).
4. `vendor/components.css`: the `dk-*` classes (`dk-btn`, `dk-switch`,
   `dk-input`).
5. `css/defaults.css`: the product tokens that `kit.css` reads.
6. `css/kit.css`: the rules of the controls, the icons and the page frame.

Load your own rules after `styles.css`, so that a class of yours on the same
element wins over a base rule of the kit.

### The exact look of the web UI

The web UI loads the same files one by one, with its own global rules and its
theme bridge in place of `css/defaults.css`:

```js
import '<kit>/vendor/tokens.css'
import '<kit>/themes/localai.css'
import '<kit>/vendor/motion.css'
import '<kit>/vendor/components.css'
import '<web UI>/src/index.css'   // global rules: body, focus, reduced motion
import '<web UI>/src/theme.css'   // product tokens and the --color-* names
import '<kit>/css/kit.css'
```

`src/theme.css` also points the kit's own tokens at the product scale (Geist,
the sharper radii, the control heights) and sets the coarse-pointer and phone
sizes. An app that wants exactly the same look imports it by path in this
order. `css/defaults.css` copies only the tokens the kit rules need, with the
same values (a unit test keeps them equal), so `styles.css` alone comes close.

### Variables a theme must define

To use another palette, replace `themes/localai.css` with a file that defines
every colour role of `vendor/theme-contract.md` (`--dk-canvas`, `--dk-card`,
`--dk-text`, `--dk-muted`, `--dk-edge`, `--dk-accent`, the `ok`, `warn` and
`error` sets, the shadow and chart roles and the others in its table), in the
three blocks it describes: light on `:root`, dark under the system setting and
dark under `[data-theme="dark"]`.

`css/defaults.css` lists the product tokens: spacing (`--sp-*`), type
(`--fs-*`, `--lh-*`, `--font-sans`, `--font-weight-*`), radii (`--r-*`),
control heights (`--ctl-h`, `--ctl-h-sm`), motion (`--dur-*`, `--ease-*`),
focus (`--focus-ring`) and a few text and border names (`--color-*`). Override
any of them after `styles.css`.

## Icons

`<Icon name="trash" />` draws an outline icon inline, as an `<svg>` that
follows the font size and `currentColor`. It needs no sprite in the page and no
font file. An unknown name draws `circle`. `ICON_IDS` lists the names, and
`vendor/icons/fa-map.json` maps Font Awesome names to them.

`src/icons.js` is generated. After a new kit snapshot, run:

```sh
node packages/ui-kit/scripts/gen-icons.mjs
```

The palette in `themes/localai.css` is generated too, from the web UI's
`src/theme/theme-localai.json`: run `node scripts/gen-theme.mjs` in
`core/http/react-ui`.

## Checks

The web UI's checks cover the package (run them in `core/http/react-ui`):

- `npm run lint`, `npm run lint:ui-kit`, `npm run lint:colours` and
  `npm run lint:inline-styles` scan `packages/ui-kit` as well as `src`.
- `tests/unit/designtokens.test.mjs` counts the raw font sizes, radii and
  keyframes in `css/kit.css`.
- `tests/unit/uikitpackage.test.mjs` checks that the sources import only
  React and their own files, that `src/icons.js` is current, that the CSS
  defines every token the rules read, that `css/defaults.css` has the values
  of `src/theme.css`, and that the English defaults match `common.json`.
