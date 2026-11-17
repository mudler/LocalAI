---
title: "Desktop app"
weight: 13
card: "Install, start and update LocalAI from a window. macOS and Linux."
headline: 'Start LocalAI from a <span class="hl">window</span>.'
lede: "The desktop app installs, updates, starts and stops the LocalAI server on your computer. It shows the LocalAI web interface in its own window."
docs: "/docs/start/install/desktop/"
dev: true
icon: "desktop"
illus: "chat"
shot: "launcher-home"
alt: "The LocalAI desktop app on first start. It shows the model that it recommends for this machine, with an Install button."
# demo: a name under static/media/ (<name>.mp4, .webm, .jpg) swaps the illustration for a muted loop.
demo: ""
points:
  - "The release carries a DMG for a Mac with Apple silicon, and an AppImage for Linux (x86-64 and ARM64)."
  - "On the first start, choose to run LocalAI on this computer or to connect to a server that runs somewhere else."
  - "A status bar shows whether the server runs. Logs and settings have their own pages."
  - "Keep several servers and pick a default. The app opens the default one at start."
  - "It checks for a new server version once a day and never installs one without asking. Choose the Stable, Release candidates or Development channel."
notes:
  - "Your models, backends and settings stay in the `~/.localai` folder. The older launcher used the same folder."
  - "To run LocalAI on a server, in a container or in a cluster, use the install methods on the Download page."
---
