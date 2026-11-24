// SPDX-License-Identifier: MIT

package routes

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// The API reference is the one page in LocalAI that still shipped in someone
// else's colours. Swagger UI has no theming hook, so rather than fight it we
// serve our own index ahead of the library's wildcard and restate the palette
// over its stylesheet. The library's own assets are still what load — this is a
// skin, not a fork, so a swagger-ui upgrade cannot silently break the page.
//
// The page follows the app theme: the choice stored by the app (localai-theme)
// if there is one, else the system setting. Light is the default.
//
// Values are copied from react-ui/src/theme/theme-localai.json rather than
// referenced: this page is served by Go and never sees the app's CSS.
// swagger_theme_test.go fails when a value drifts from that file.
const swaggerThemeCSS = `
:root {
  color-scheme: light;
  --lai-bg: #fff4dc;
  --lai-surface: #fffdf7;
  --lai-sunken: #f9e6bd;
  --lai-line: #e6d9bd;
  --lai-ink: #232a52;
  --lai-muted: #4d5478;
  --lai-accent: #4a5aa0;
  --lai-accent-text: #3d4c93;
  --lai-on-accent: #ffffff;
  --lai-ok: #1f7a4c;
  --lai-ok-text: #1d6e46;
  --lai-warn: #8c5400;
  --lai-warn-text: #7a4a00;
  --lai-error: #b3261e;
  --lai-error-text: #a51d16;
}
@media (prefers-color-scheme: dark) {
  :root:not([data-theme="light"]) {
    color-scheme: dark;
    --lai-bg: #0c1211;
    --lai-surface: #141c1b;
    --lai-sunken: #080d0c;
    --lai-line: #27332f;
    --lai-ink: #fff4dc;
    --lai-muted: #b9bfd0;
    --lai-accent: #7f8fd0;
    --lai-accent-text: #9aa8e0;
    --lai-on-accent: #0c1211;
    --lai-ok: #7fd39b;
    --lai-ok-text: #8fdcaa;
    --lai-warn: #ffc93c;
    --lai-warn-text: #ffd466;
    --lai-error: #ff9d92;
    --lai-error-text: #ffaea5;
  }
}
:root[data-theme="dark"] {
  color-scheme: dark;
  --lai-bg: #0c1211;
  --lai-surface: #141c1b;
  --lai-sunken: #080d0c;
  --lai-line: #27332f;
  --lai-ink: #fff4dc;
  --lai-muted: #b9bfd0;
  --lai-accent: #7f8fd0;
  --lai-accent-text: #9aa8e0;
  --lai-on-accent: #0c1211;
  --lai-ok: #7fd39b;
  --lai-ok-text: #8fdcaa;
  --lai-warn: #ffc93c;
  --lai-warn-text: #ffd466;
  --lai-error: #ff9d92;
  --lai-error-text: #ffaea5;
}

body { background: var(--lai-bg); color: var(--lai-ink); }

.swagger-ui, .swagger-ui .info .title, .swagger-ui .opblock-tag,
.swagger-ui .opblock .opblock-summary-operation-id,
.swagger-ui .opblock .opblock-summary-path,
.swagger-ui .opblock .opblock-summary-description,
.swagger-ui table thead tr td, .swagger-ui table thead tr th,
.swagger-ui .parameter__name, .swagger-ui .parameter__type,
.swagger-ui .response-col_status, .swagger-ui label,
.swagger-ui .tab li, .swagger-ui .model-title, .swagger-ui .model {
  color: var(--lai-ink);
}

.swagger-ui .info li, .swagger-ui .info p, .swagger-ui .info table,
.swagger-ui .markdown p, .swagger-ui .renderedMarkdown p,
.swagger-ui .opblock-description-wrapper p, .swagger-ui .response-col_links,
.swagger-ui .parameter__in, .swagger-ui .opblock-title_normal p {
  color: var(--lai-muted);
}

/* The topbar is the library's branding; the reference is ours. */
.swagger-ui .topbar { background: var(--lai-surface); border-bottom: 1px solid var(--lai-line); }
.swagger-ui .topbar .download-url-wrapper { display: none; }
/* The library's logo gives way to the product mark. */
.swagger-ui .topbar-wrapper a.link { width: 32px; height: 32px; background: url(../static/logo.png) center / contain no-repeat; }
.swagger-ui .topbar-wrapper a.link img, .swagger-ui .topbar-wrapper a.link svg, .swagger-ui .topbar-wrapper a.link span { display: none; }

/* Operations as hairline rows rather than tinted, shadowed cards, matching the
   lane idiom the rest of the app uses. Method colour carries the meaning. */
.swagger-ui .opblock {
  background: var(--lai-surface);
  border: 1px solid var(--lai-line);
  border-radius: 6px;
  box-shadow: none;
  margin: 0 0 8px;
}

.swagger-ui .opblock .opblock-summary { border-color: var(--lai-line); }

/* Swagger tints the whole row per method (.opblock.opblock-post etc). Matching
   its specificity rather than reaching for !important: the method belongs on
   one edge, not washed across the row, or every row is a status colour and
   none of them mean anything. */
.swagger-ui .opblock.opblock-get,
.swagger-ui .opblock.opblock-post,
.swagger-ui .opblock.opblock-put,
.swagger-ui .opblock.opblock-patch,
.swagger-ui .opblock.opblock-delete,
.swagger-ui .opblock.opblock-head,
.swagger-ui .opblock.opblock-options {
  background: var(--lai-surface);
  border-color: var(--lai-line);
}

.swagger-ui .opblock.opblock-get { border-left: 3px solid var(--lai-accent); }
.swagger-ui .opblock.opblock-post { border-left: 3px solid var(--lai-ok); }
.swagger-ui .opblock.opblock-put,
.swagger-ui .opblock.opblock-patch { border-left: 3px solid var(--lai-warn); }
.swagger-ui .opblock.opblock-delete { border-left: 3px solid var(--lai-error); }

/* An outlined chip, not a filled one. White on pale green was the least
   readable thing on the page. */
.swagger-ui .opblock .opblock-summary-method,
.swagger-ui .opblock.opblock-get .opblock-summary-method,
.swagger-ui .opblock.opblock-post .opblock-summary-method,
.swagger-ui .opblock.opblock-put .opblock-summary-method,
.swagger-ui .opblock.opblock-patch .opblock-summary-method,
.swagger-ui .opblock.opblock-delete .opblock-summary-method {
  background: transparent;
  border: 1px solid currentColor;
  border-radius: 3px;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 0.75rem;
  font-weight: 600;
  text-shadow: none;
  min-width: 68px;
}

.swagger-ui .opblock.opblock-get .opblock-summary-method { color: var(--lai-accent-text); }
.swagger-ui .opblock.opblock-post .opblock-summary-method { color: var(--lai-ok-text); }
.swagger-ui .opblock.opblock-put .opblock-summary-method,
.swagger-ui .opblock.opblock-patch .opblock-summary-method { color: var(--lai-warn-text); }
.swagger-ui .opblock.opblock-delete .opblock-summary-method { color: var(--lai-error-text); }

.swagger-ui .opblock-tag { border-bottom: 1px solid var(--lai-line); }
.swagger-ui section.models, .swagger-ui section.models .model-container {
  background: var(--lai-surface);
  border-color: var(--lai-line);
}

.swagger-ui select, .swagger-ui input[type=text], .swagger-ui textarea {
  background: var(--lai-sunken);
  color: var(--lai-ink);
  border: 1px solid var(--lai-line);
  border-radius: 4px;
}

.swagger-ui .btn {
  background: transparent;
  color: var(--lai-ink);
  border: 1px solid var(--lai-line);
  border-radius: 5px;
  box-shadow: none;
}

.swagger-ui .btn.execute { background: var(--lai-accent); border-color: var(--lai-accent); color: var(--lai-on-accent); }
.swagger-ui .btn.authorize { color: var(--lai-ok-text); border-color: var(--lai-ok); }
.swagger-ui .btn.authorize svg { fill: var(--lai-ok-text); }

.swagger-ui .highlight-code, .swagger-ui .microlight,
.swagger-ui .responses-inner pre, .swagger-ui .body-param pre {
  background: var(--lai-sunken) !important;
  border: 1px solid var(--lai-line);
  border-radius: 5px;
}

.swagger-ui .scheme-container { background: var(--lai-surface); box-shadow: none; border-bottom: 1px solid var(--lai-line); }
.swagger-ui .dialog-ux .modal-ux { background: var(--lai-surface); border: 1px solid var(--lai-line); }
.swagger-ui .dialog-ux .modal-ux-header { border-bottom: 1px solid var(--lai-line); }
.swagger-ui svg.arrow { fill: var(--lai-muted); }
.swagger-ui a, .swagger-ui .info a, .swagger-ui .info a.link { color: var(--lai-accent-text); }
.swagger-ui .info .base-url, .swagger-ui .schemes-title, .swagger-ui .servers-title,
.swagger-ui .scheme-container .schemes > label, .swagger-ui .opblock-section-header label { color: var(--lai-muted); }
.swagger-ui .topbar-wrapper { justify-content: flex-start; }

@media (prefers-reduced-motion: reduce) {
  .swagger-ui * { transition: none !important; animation: none !important; }
}
`

// swaggerIndexHTML loads the library's own bundle from the same directory, so
// the only thing we own is the skin.
const swaggerIndexHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>LocalAI API reference</title>
<link rel="icon" type="image/svg+xml" href="../favicon.svg">
<script>
try { var t = localStorage.getItem('localai-theme'); if (t === 'light' || t === 'dark') document.documentElement.setAttribute('data-theme', t) } catch (e) {}
</script>
<link rel="stylesheet" href="./swagger-ui.css">
<style>` + swaggerThemeCSS + `</style>
</head>
<body>
<div id="swagger-ui"></div>
<script src="./swagger-ui-bundle.js"></script>
<script src="./swagger-ui-standalone-preset.js"></script>
<script>
window.onload = function () {
  window.ui = SwaggerUIBundle({
    url: "doc.json",
    dom_id: "#swagger-ui",
    deepLinking: true,
    presets: [SwaggerUIBundle.presets.apis, SwaggerUIStandalonePreset],
    plugins: [SwaggerUIBundle.plugins.DownloadUrl],
    layout: "StandaloneLayout",
    persistAuthorization: true,
  })
}
</script>
</body>
</html>`

// RegisterSwaggerTheme serves the themed index. It must be registered BEFORE
// the /swagger/* wildcard: echo prefers the more specific route, but relying on
// registration order as well costs nothing and makes the intent obvious.
func RegisterSwaggerTheme(router *echo.Echo) {
	handler := func(c echo.Context) error {
		return c.HTMLBlob(http.StatusOK, []byte(swaggerIndexHTML))
	}
	router.GET("/swagger/", handler)
	router.GET("/swagger/index.html", handler)
}
