// SPDX-License-Identifier: MIT
// Installed metadata only: projecting launch actions never loads addon code.
import * as capabilities from '../utils/capabilities.js'
import { homeAppStatus } from './homeCommands.js'
import { localizedText } from '../components/addons/addonState.js'
import { addonPageAddress } from './addresses.js'
const supported = new Set(Object.entries(capabilities).filter(([k]) => k.startsWith('CAP_') && !['CAP_CHAT', 'CAP_COMPLETION'].includes(k)).map(([, v]) => v))
const object = v => v && typeof v === 'object' && !Array.isArray(v)
const name = v => typeof v === 'string' && /^[a-z][a-z0-9-]{0,63}$/.test(v)
const compare = (a, b) => a < b ? -1 : a > b ? 1 : 0
function declarations(ui) {
  if (!Array.isArray(ui.pages) || ui.pages.length > 32 || !ui.pages.every(name) || new Set(ui.pages).size !== ui.pages.length) return null
  const actions = ui.modelActions ?? []
  if (!Array.isArray(actions) || actions.length > 27) return null
  const seen = new Set()
  for (const a of actions) {
    if (!object(a) || Object.keys(a).some(k => !['capability', 'page', 'path'].includes(k)) || !supported.has(a.capability) || seen.has(a.capability) || !name(a.page) || !ui.pages.includes(a.page)) return null
    seen.add(a.capability)
    if (a.path !== undefined && (typeof a.path !== 'string' || !a.path)) return null
    const path = a.page + (a.path === undefined ? '' : `/${a.path}`)
    if (path.length > 256 || path.split('/').length > 8 || path.split('/').some(s => !/^[A-Za-z0-9._~-]{1,64}$/.test(s) || s === '.' || s === '..')) return null
  }
  return actions
}
export function projectModelActions(snapshot, capability, model) {
  const result = { status: 'absent', choices: [], states: [] }
  if (!snapshot.allowed) return { ...result, status: snapshot.known === false ? 'loading' : 'forbidden' }
  if (snapshot.status === 'error' || snapshot.error) return { ...result, status: 'list-error' }
  if (snapshot.status !== 'ready') return { ...result, status: 'loading' }
  if (!supported.has(capability) || typeof model !== 'string') return result
  const apps = Array.isArray(snapshot.apps) ? [...snapshot.apps] : []
  if (apps.some(a => !object(a) || !name(a.id))) return { ...result, status: 'invalid-metadata' }
  apps.sort((a, b) => compare(a.id, b.id))
  for (const app of apps) {
    const actions = object(app.ui) ? declarations(app.ui) : null
    let status = homeAppStatus(app)
    if (app.state === 'starting' && app.enabled) status = 'starting'
    if (apps.filter(a => a.id === app.id).length > 1 || (app.ui && !actions)) status = 'invalid-metadata'
    if (!actions?.some(a => a.capability === capability)) continue
    result.states.push({ addonId: app.id, status })
    if (status !== 'ready') continue
    const a = actions.find(a => a.capability === capability)
    const path = a.path || ''
    const title = localizedText(app, 'title', snapshot.language || 'en')
    result.choices.push(Object.freeze({ actor: snapshot.actor, addonId: app.id, capability, page: a.page, path,
      generation: app.generation, entry: app.ui.entry, model, label: title ? `${title} (${app.id})` : app.id,
      url: addonPageAddress({ addon: app.id, page: a.page + (path ? `/${path}` : ''), model }) }))
  }
  result.status = result.choices.length ? 'ready' : result.states[0]?.status || 'absent'
  return result
}
export async function dispatchModelAction(captured, { refresh, current, navigate, sessionCurrent = () => true }) {
  const actor = current().actor
  if (!sessionCurrent() || !captured || captured.actor !== actor || !current().allowed) return false
  const apps = await refresh()
  const now = current()
  if (!sessionCurrent() || !apps || !now.allowed || actor !== now.actor || now.list.apps !== apps) return false
  const live = projectModelActions({ ...now.list, allowed: true, actor }, captured.capability, captured.model).choices.find(c => c.addonId === captured.addonId)
  if (!live || ['page', 'path', 'generation', 'entry', 'capability', 'model', 'url'].some(k => live[k] !== captured[k])) return false
  navigate(live.url)
  return true
}
