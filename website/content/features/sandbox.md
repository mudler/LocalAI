---
title: "Sandbox"
weight: 6
card: "Run Python on your files in an isolated sandbox."
headline: 'Run Python on your <span class="hl">files</span>.'
lede: "Put a spreadsheet in Files and ask for a chart. The Sandbox runs the code and saves the result in Files."
docs: "/docs/addons/catalogue/work/sandbox/"
dev: true
icon: "sandbox"
illus: "sandbox"
# demo: a name under static/media/ (<name>.mp4, .webm, .jpg) swaps the illustration for a muted loop.
demo: ""
points:
  - "Run Python and shell commands on your files."
  - "Code runs in an isolated sandbox (KVM or gVisor)."
  - "The sandbox has no internet by default."
  - "Results are saved as files in Files."
  - "Changes are listed in History, with an Undo button."
  - "With the media profile (ffmpeg, sox, ImageMagick), the assistant can join, trim, mix and subtitle clips."
notes:
  - "Sandbox is an add-on. An admin installs it. It runs on Linux. The macOS package for Apple silicon is built but not released yet."
  - "An admin can allow network access."
films:
  - {name: "files-and-sandbox", title: "Files and Sandbox.", caption: "Ask for a chart of a CSV file. The assistant runs Python in the Sandbox, saves the chart in Files, and Undo removes it. With the network off, a download fails.", seconds: 55}
---
