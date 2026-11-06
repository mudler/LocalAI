// Studio and Talk are packaged add-ons. A spec that draws one of them installs
// the real built bundle here: the app list names it, and its module and style
// sheet are served from the build output the way the add-on owner serves them.
// Specs open canonical declared pages, never historical standalone redirects.
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { app, mockAddons, stubAddonRoutes } from './addons-fixtures.js'

const addons = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../../../addons')
const DIGEST = 'e'.repeat(64)
const TYPES = { js: 'text/javascript', css: 'text/css', png: 'image/png' }
const built = new Map()

// The build empties and rewrites addons/<id>/ui/dist, and every worker serves
// its files, so two builds at once leave a running spec without its style sheet
// or module. The run builds once, before any worker starts (global-setup.js),
// and says so in this variable.
const BUILT = 'E2E_ADDON_BUNDLES_BUILT'

// Builds the bundles for the whole run. Called by the Playwright global setup.
export async function buildAddonBundles(ids) {
  for (const id of ids) await import(path.join(addons, id, 'ui/build.mjs')).then(m => m.build())
}

// The add-ons the specs serve from their build output: Studio, Talk and Voice
// through installStudioAddons, Face, Fine-tuning and Memory through their own
// routes.
export async function buildStudioAddons(ids = ['studio', 'talk', 'voice', 'face', 'fine-tuning', 'telegram', 'slack', 'github', 'discord', 'home-assistant', 'jellyfin', 'memory', 'pikvm']) {
  await buildAddonBundles(ids)
  process.env[BUILT] = '1'
}

// The bundle of one add-on. A run that did not go through the global setup
// builds it here, once per worker, so a spec never runs against a stale one.
function build(id) {
  if (process.env[BUILT]) return Promise.resolve(path.join(addons, id, 'ui/dist'))
  if (!built.has(id)) built.set(id, import(path.join(addons, id, 'ui/build.mjs')).then(m => m.build()))
  return built.get(id)
}

// The text of a manifest field: a plain string, or a language map. The apps
// list sends the English text in title and description, and the whole map in
// titles and descriptions.
const english = v => (v && typeof v === 'object' ? v.en : v)
const languages = v => (v && typeof v === 'object' ? v : undefined)

export function packagedApp(id, over = {}) {
  const manifest = JSON.parse(fs.readFileSync(path.join(addons, id, 'manifest.json'), 'utf8'))
  const ui = manifest.capabilities.ui
  const base = `/api/addons/${id}/assets/${DIGEST}`
  return app({
    id, name: id, title: english(ui.title), description: english(ui.description), titles: languages(ui.title), descriptions: languages(ui.description), icon: undefined, generation: 1, version: manifest.version,
    mainPage: `/app/addons/${id}/${ui.mainPage}`, tools: [], toolCount: 0,
    ui: { entry: `${base}/${ui.entry}`, css: ui.css.map(c => `${base}/${c}`), sdk: ui.sdk, pages: ui.pages, navigation: ui.navigation, modelActions: ui.modelActions, homeCommands: ui.homeCommands?.map(c => ({ ...c, page: c.page || ui.mainPage, label: c.label || english(ui.title), icon: c.icon || 'puzzle' })) },
    ...over,
  })
}

// Installs the add-ons and the routes that serve them. `ids` picks which.
// Returns the state of mockAddons.
export async function installStudioAddons(page, { ids = ['studio', 'talk'], ...options } = {}) {
  const dirs = await Promise.all(ids.map(build))
  await stubAddonRoutes(page)
  await page.route(/\/api\/addons\/(studio|talk|voice|telegram|slack|github|discord|home-assistant|jellyfin|pikvm)\/assets\//, route => {
    const parts = new URL(route.request().url()).pathname.split('/')
    const id = parts[3]
    const file = parts.slice(6).join('/')
    const dir = dirs[ids.indexOf(id)]
    const ext = file.split('.').pop()
    if (!dir || !TYPES[ext] || !fs.existsSync(path.join(dir, file))) return route.fulfill({ status: 403, json: { error: { type: 'addon_error', message: 'Addon request denied' } } })
    return route.fulfill({
      status: 200,
      contentType: TYPES[ext],
      headers: { 'cache-control': 'private, max-age=31536000, immutable', 'x-content-type-options': 'nosniff' },
      body: fs.readFileSync(path.join(dir, file)),
    })
  })
  return mockAddons(page, { apps: ids.map(id => packagedApp(id)), ...options })
}
