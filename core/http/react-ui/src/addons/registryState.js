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
