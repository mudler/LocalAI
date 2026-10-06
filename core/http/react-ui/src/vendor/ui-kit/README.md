# Shared UI kit snapshot

Vendored copy of the shared UI kit, version 0.3.0. It holds the base tokens
(`--dk-*`), the motion layer, the `dk-*` component classes, the outline icon
sprite and `icons/fa-map.json`, which maps Font Awesome names to icon ids. Do
not edit these files by hand. To update, take a newer snapshot of the kit and
replace the files, then refresh `.ui-kit.lock`.

The LocalAI theme is not part of the kit: it lives in `src/theme-localai.css`.
The two sample themes are kept because the kit's manifest ships them. The app
does not load them.
