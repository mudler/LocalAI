// Pure registry rules: what the apps list says about each add-on, which loaded
// instances it invalidates, and what the host may draw from the ones that
// remain. No React and no network.
import { sdkAccepts } from './sdk.js'
import { localizedText } from '../components/addons/addonState.js'

// A reason a loaded instance can no longer be used, or ''.
//
//   removed   not listed any more (uninstalled, or the viewer lost access)
//   disabled  the owner stopped it
//   failed    the process crashed
//   no-ui     the new package has no UI
//   updated   another generation or another package is installed
export function invalidReason(app, instance) {
  if (!app) return 'removed'
  if (!app.enabled) return 'disabled'
  if (app.state === 'failed') return 'failed'
  if (!app.ui) return 'no-ui'
  if (app.generation !== instance.generation || app.ui.entry !== instance.entry) return 'updated'
  return ''
}

// Whether the shell can show this app's UI right now, before loading it.
export function usable(app) {
  return Boolean(app && app.enabled && app.state !== 'failed' && app.ui && sdkAccepts(app.ui.sdk))
}

// The status of an app for the page that wants to open it, from the list alone.
export function appStatus(app) {
  if (!app) return 'absent'
  if (!app.enabled) return 'disabled'
  if (app.state === 'failed') return 'failed'
  if (!app.ui) return 'no-ui'
  if (!sdkAccepts(app.ui.sdk)) return 'incompatible'
  return 'ready'
}

// Whether any listed app contributes UI to the host outside its own page
// (navigation entries, chat actions or pages). Those contributions have to be
// kept current, so the list is read off the Add-ons page only while this holds.
export function contributesUI(apps) {
  return (apps || []).some(app => {
    // A disabled, failed or incompatible app draws nothing, so there is
    // nothing left to keep current once a list has said so.
    if (!usable(app)) return false
    const ui = app.ui
    return ['navigation', 'chatActions', 'actions', 'slots', 'pages'].some(k => Array.isArray(ui[k]) && ui[k].length > 0)
  })
}

// Whether the registry reads the apps list now. Never without the feature. On
// an Add-ons page always. Anywhere else only while a listed app contributes UI,
// or once, to learn that, when a consumer needs a list that was never read (a
// failed read is not retried for a consumer: the next page load asks again).
export function shouldPoll({ allowed, onAddonsPage, apps, status, consumers }) {
  if (!allowed) return false
  if (onAddonsPage) return true
  if (contributesUI(apps)) return true
  return consumers > 0 && status === 'idle'
}

// Pages an app contributes to navigation, only while it is usable.
export function navigationItems(apps, { language = 'en' } = {}) {
  const out = []
  for (const app of apps || []) {
    if (!usable(app)) continue
    const title = localizedText(app, 'title', language) || app.id
    const pages = app.ui.navigation || []
    for (const page of pages) {
      if (!(app.ui.pages || []).includes(page)) continue
      out.push({
        addonId: app.id,
        page,
        path: `/app/addons/${encodeURIComponent(app.id)}/${encodeURIComponent(page)}`,
        label: pages.length > 1 ? `${title}: ${page}` : title,
      })
    }
  }
  return out
}

// The chat actions the host may draw: from ready instances of the generation
// that is listed now, while the viewer holds the feature. An instance that was
// invalidated, stale or still loading contributes nothing.
export function availableChatActions(instances, apps, { hasFeature = () => false } = {}) {
  if (!hasFeature('addons')) return []
  const byId = new Map((apps || []).map(a => [a.id, a]))
  const out = []
  for (const instance of instances || []) {
    if (instance.status !== 'ready') continue
    const app = byId.get(instance.id)
    if (!app || invalidReason(app, instance)) continue
    for (const action of instance.chatActions || []) {
      out.push({ addonId: instance.id, generation: instance.generation, ...action })
    }
  }
  return out
}
