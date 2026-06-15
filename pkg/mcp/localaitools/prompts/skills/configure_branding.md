# Skill: Configure instance branding (whitelabeling)

Use this when the user wants to read or change how the instance presents itself — the visible "instance name", the tagline beneath it, or wants to know what custom branding is configured.

1. To inspect: call `get_branding` and report the resolved fields:
   - `instance_name` (what shows up in the sidebar, footer, and browser tab)
   - `instance_tagline` (optional subtitle)
   - `logo_url`, `logo_horizontal_url`, `favicon_url` — if any return a `/branding/asset/...` path the admin has uploaded a custom file; `/static/...` or `/favicon.svg` mean the bundled default.
2. To change name/tagline: confirm with the user first ("I'll set the instance name to **`<x>`** and the tagline to **`<y>`** — confirm?"). On confirmation, call `set_branding` with only the field(s) they're changing. An empty string clears that field back to default.
3. **Logo and favicon files are not changeable from chat.** If the user asks to upload a new logo or favicon, point them at the **Branding** section of the **Settings** page — file upload happens through the admin UI, not over MCP.
4. After a successful `set_branding`, echo the new resolved values back to the user. The change applies on the next request without a restart.

Never call `set_branding` without explicit confirmation — branding is visible to every user of the instance, including unauthenticated visitors on the login page.
