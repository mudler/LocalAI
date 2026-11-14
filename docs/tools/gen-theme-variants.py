#!/usr/bin/env python3
"""Write the two Relearn colour variants from the LocalAI token file.

Input:  docs/brand/theme-localai.css   (copy of the LocalAI theme tokens)
Output: docs/assets/css/theme-localai-light.css
        docs/assets/css/theme-localai-dark.css

Run `python3 docs/tools/gen-theme-variants.py` after the token copy changes.
Run it with `--check` to fail when the output files are out of date.
The variants hold literal colours. Relearn bundles each variant file into
its own selector, so they cannot read the token variables at run time.
"""
import os
import re
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
DOCS = os.path.dirname(HERE)
SRC = os.path.join(DOCS, "brand", "theme-localai.css")
OUT = os.path.join(DOCS, "assets", "css")


def blocks(text):
    """Return the token dictionaries of the light and the dark block."""
    light = re.search(r"^:root\s*\{(.*?)^\}", text, re.S | re.M).group(1)
    dark = re.search(r':root\[data-theme="dark"\]\s*\{(.*?)^\}', text, re.S | re.M).group(1)

    def parse(body):
        return dict(re.findall(r"--([a-z0-9-]+):\s*([^;]+);", body))

    t_light = parse(light)
    t_dark = dict(t_light)
    t_dark.update(parse(dark))
    return t_light, t_dark


def rgb(value):
    """Turn #rrggbb into the rgba( r, g, b, 1 ) form the theme uses."""
    value = value.strip()
    m = re.fullmatch(r"#([0-9a-fA-F]{6})", value)
    if not m:
        return value
    h = m.group(1)
    return "rgba( %d, %d, %d, 1 )" % (int(h[0:2], 16), int(h[2:4], 16), int(h[4:6], 16))


def variant(name, t, dark):
    c = lambda k: rgb(t[k])
    lines = [
        ("PRIMARY-color", c("dk-accent-text"), "brand primary color, used for links"),
        ("PRIMARY-HOVER-color", c("dk-text"), "brand primary hover color"),
        ("SECONDARY-color", c("brand-teal-bright") if dark else c("brand-logo-teal"), "brand secondary color"),
        ("SECONDARY-HOVER-color", c("dk-text"), "brand secondary hover color"),
        ("ACCENT-color", c("dk-warn") if dark else c("dk-warn-text"), "brand accent color, used for search highlights"),
        ("ACCENT-HOVER-color", c("dk-warn-text"), "brand accent hover color"),
        ("MENU-BORDER-color", c("dk-edge"), "border color between menu and content"),
        ("MENU-TOPBAR-SEPARATOR-color", c("dk-edge"), "separator color of vertical line between menu and topbar"),
        ("MENU-HEADER-BG-color", c("dk-canvas"), "background color of menu header"),
        ("MENU-HEADER-color", c("dk-canvas"), "color of menu header"),
        ("MENU-SEARCH-color", c("dk-text"), "text and icon color of search box"),
        ("MENU-SEARCH-BG-color", c("dk-card"), "background color of search box"),
        ("MENU-SEARCH-BORDER-color", c("dk-control-edge"), "border color of search box"),
        ("MENU-HOME-LINK-color", c("dk-text"), "home button color if configured"),
        ("MENU-HOME-LINK-HOVER-color", c("dk-accent-text"), "hovered home button color if configured"),
        ("MENU-HOME-TOP-SEPARATOR-color", c("dk-edge"), "separator color between menu search box and home menu"),
        ("MENU-HOME-SEPARATOR-color", c("dk-edge"), "separator color between home menus"),
        ("MENU-HOME-BOTTOM-SEPARATOR-color", c("dk-edge"), "separator color between home menu and menu"),
        ("MENU-SECTIONS-BG-color", c("dk-inset"), "background of the menu"),
        ("MENU-SECTIONS-ACTIVE-BG-color", c("dk-hover"), "background color of the active menu section"),
        ("MENU-SECTIONS-LINK-color", c("dk-text"), "link color of menu topics"),
        ("MENU-SECTIONS-LINK-HOVER-color", c("dk-accent-text"), "hovered link color of menu topics"),
        ("MENU-SECTION-ACTIVE-CATEGORY-color", c("dk-accent-text"), "text color of the displayed menu topic"),
        ("MENU-SECTION-SEPARATOR-color", c("dk-edge"), "separator color between menus"),
        ("TOPBAR-BG-color", c("dk-canvas"), "background color of topbar"),
        ("MAIN-TOPBAR-BORDER-color", c("dk-edge"), "border color between topbar and content"),
        ("MAIN-BG-color", c("dk-canvas"), "background color of content"),
        ("MAIN-TEXT-color", c("dk-text"), "text color of content and titles"),
        ("MAIN-TITLES-TEXT-color", c("dk-text"), "text color of titles and transparent box titles"),
        ("CODE-theme", "relearn-dark" if dark else "relearn-light", "name of the chroma stylesheet file"),
        ("CODE-BLOCK-color", c("dk-text"), "fallback text color of block code"),
        ("CODE-BLOCK-BG-color", c("dk-card") if dark else c("dk-inset"), "fallback background color of block code"),
        ("CODE-INLINE-color", c("dk-accent-text"), "text color of inline code"),
        ("CODE-INLINE-BG-color", c("dk-accent-wash"), "background color of inline code"),
        ("CODE-INLINE-BORDER-color", c("dk-edge"), "border color of inline code"),
        ("BROWSER-theme", "dark" if dark else "light", "theme for controls drawn by the browser"),
        ("MERMAID-theme", "dark" if dark else "default", "default Mermaid theme"),
        ("OPENAPI-theme", "dark" if dark else "light", "default OpenAPI theme"),
        ("OPENAPI-CODE-theme", "obsidian" if dark else "idea", "default OpenAPI code theme"),
        ("BOX-CAPTION-color", c("dk-on-accent"), "text color of colored box titles"),
        ("BOX-BG-color", c("dk-card"), "background color of colored boxes"),
        ("BOX-TEXT-color", c("dk-text"), "text color of colored box content"),
        ("BOX-NEUTRAL-color", c("dk-edge"), "background color of neutral boxes"),
        ("BOX-BLUE-color", c("dk-accent"), "info boxes"),
        ("BOX-BLUE-TEXT-color", c("dk-accent-text"), "text color of blue boxes"),
        ("BOX-CYAN-color", c("brand-teal-bright") if dark else c("brand-logo-teal"), "cyan boxes"),
        ("BOX-CYAN-TEXT-color", c("brand-teal-bright") if dark else c("brand-logo-teal"), "text color of cyan boxes"),
        ("BOX-GREEN-color", c("dk-ok"), "tip boxes"),
        ("BOX-GREEN-TEXT-color", c("dk-ok-text"), "text color of green boxes"),
        ("BOX-GREY-color", c("dk-muted"), "grey boxes"),
        ("BOX-GREY-TEXT-color", c("dk-muted"), "text color of grey boxes"),
        ("BOX-ORANGE-color", c("dk-warn"), "note boxes"),
        ("BOX-ORANGE-TEXT-color", c("dk-warn-text"), "text color of orange boxes"),
        ("BOX-YELLOW-color", c("dk-warn"), "yellow boxes"),
        ("BOX-YELLOW-TEXT-color", c("dk-warn-text"), "text color of yellow boxes"),
        ("BOX-RED-color", c("dk-error"), "warning and caution boxes"),
        ("BOX-RED-TEXT-color", c("dk-error-text"), "text color of red boxes"),
    ]
    head = (
        "/* Generated by docs/tools/gen-theme-variants.py from docs/brand/theme-localai.css.\n"
        "   Do not edit by hand. */\n"
    )
    body = "".join("  --%s: %s; /* %s */\n" % (k, v, d) for k, v, d in lines)
    return head + ":root {\n  /* %s */\n%s}\n" % (name, body)


def main():
    text = open(SRC).read()
    t_light, t_dark = blocks(text)
    out = {
        "theme-localai-light.css": variant("localai-light", t_light, False),
        "theme-localai-dark.css": variant("localai-dark", t_dark, True),
    }
    stale = False
    for name, content in out.items():
        path = os.path.join(OUT, name)
        if "--check" in sys.argv:
            if not os.path.exists(path) or open(path).read() != content:
                print("out of date: " + os.path.relpath(path, DOCS))
                stale = True
        else:
            with open(path, "w") as f:
                f.write(content)
    if stale:
        sys.exit(1)


if __name__ == "__main__":
    main()
