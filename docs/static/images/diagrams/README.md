# LocalAI diagrams

Each diagram is one HTML page on a 1600 x 900 canvas. All pages share `diagram.css` (tokens from the LocalAI branding kit) and `diagram.js` (picks light or dark). Fonts and the tile mark are local files, so a render needs no network.

## Render

```bash
./render-diagrams.sh                      # all diagrams, light and dark
./render-diagrams.sh quickstart-journey   # one diagram
```

The script needs Chrome or Chromium. Set `CHROME` to its path if it is not on the `PATH`. Set `OUT` to write the PNG files to another folder. The output is `<name>.png` (light) and `<name>-dark.png` (dark), 3200 x 1800 px.

To look at a page in a browser, open the HTML file. Add `?theme=dark` or `?theme=light` to force a theme.

## Rules

- Use only the tokens and classes of `diagram.css`: white cards, one teal box for the core, periwinkle connectors, one yellow marker word in the title.
- Text is 18 px or more on the canvas. Check the PNG at 800 px wide: every label must stay readable.
- No offset shadows, no stamps, no network fonts. The llama appears only as the tile mark in the corner.
- Say only what the code does. Name the route, the setting or the backend that you checked.
- Change the HTML, render, and commit the HTML and both PNG files together.

## Fonts

`fonts/` holds Geist (Medium and Bold) and Geist Mono under the SIL Open Font License. The licence file is next to them.
