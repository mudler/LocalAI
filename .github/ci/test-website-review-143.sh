#!/usr/bin/env bash
set -euo pipefail

python3 - <<'PY'
from pathlib import Path

home = Path("website/layouts/index.html").read_text()
css = Path("website/static/css/site.css").read_text()
install = Path("docs/content/getting-started/install.md").read_text()
containers = Path("docs/content/getting-started/containers.md").read_text()

def require(condition, message):
    if not condition:
        raise SystemExit(f"FAIL: {message}")

require("Drop-in replacement for most upstream APIs." in home,
        "homepage must use the requested drop-in API heading")
require("Everything else plugs into LocalAI." not in home,
        "old runtime heading must be removed")
require("When the engine we need" not in home,
        "hero must describe user outcomes instead of team implementation")
require('href="mailto:contact@localai.io"' in home and "business" in home.lower(),
        "homepage must provide a direct business contact action")
require(home.index('id="localai"') < home.index('id="proof-quotes"') < home.index('id="mission"'),
        "headline testimonials must directly follow the runtime section")
require(home.count('id="proof-quotes"') == 1,
        "headline testimonials must appear exactly once")
require('id="engines"' not in home and "Engines we build" not in home,
        "homepage engine showcase must be removed")
require('href="/docs/installation/index.html"' in home,
        "installation guide action must use the direct installation URL")
require('<iframe' in install and "youtube.com/embed/cMVNnlqwfw4" in install,
        "installation page must embed the walkthrough video")
require("## Quick Start" not in install,
        "installation landing page must not duplicate Quick Start")
for text in ("CUDA 12", "CUDA 13", "ROCm", "Intel", "Jetson", "Vulkan", "fallback"):
    require(text.lower() in containers.lower(), f"GPU chooser must explain {text}")
require('class="sn__e"><a href="https://github.com/mudler/parakeet.cpp">parakeet.cpp</a>' in home,
        "capability engine names must link to their repositories")
require(".pane{min-height:" in css.replace(" ", ""),
        "all installation panes must have a fixed minimum height")

print("website review 143 source checks passed")
PY
