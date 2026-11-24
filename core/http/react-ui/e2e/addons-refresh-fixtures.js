// A stubbed host for the Add-ons page: a mix of add-ons with an icon file, one
// without any icon and one a third party wrote, two messaging connections (one
// that cannot answer yet) and a library that offers a few more. The data is
// sample data for specs and screenshots; nothing here is a product fact.
import { ADMIN, app, installedAddon, release, sessionFor, mockAddons } from './addons-fixtures.js'

const admin = (id, enabled = true) => ({ canStart: !enabled, canStop: enabled, managementUrl: `/app/addons?manage=${id}` })

function entry(id, title, description, extra = {}) {
  return {
    id, title, description, icon: extra.icon === null ? undefined : `/api/addons/apps/${id}/icon?generation=1`,
    version: '0.2.0', generation: 1, enabled: true, state: 'running', hasUI: false, tools: [], toolCount: 0,
    admin: admin(id), ...extra,
  }
}

export const APPS = [
  entry('slack', 'Slack', 'Bind a Slack app to one of your saved agents, so people can talk to it in direct messages and channels.', { mainPage: '/app/addons/slack/apps', hasUI: true }),
  entry('telegram', 'Telegram', 'Bind a Telegram bot to one of your saved agents, so people can talk to it from Telegram.', { mainPage: '/app/addons/telegram/bots', hasUI: true }),
  entry('github', 'GitHub', 'Read issues and repositories, and propose comments and reviews that you confirm.', { tools: ['issues', 'repos', 'comment'], toolCount: 9 }),
  entry('mail', 'Mail', 'Read and search your mailbox, and draft replies that you send yourself.', { enabled: false, state: 'stopped', tools: ['search'], toolCount: 6, admin: admin('mail', false) }),
  entry('calendar', 'Calendar', 'A calendar kept inside LocalAI. Agents can read it and add events.', { icon: null, mainPage: '/app/addons/calendar/home', hasUI: true, tools: ['events'], toolCount: 5 }),
  entry('office', 'Office', 'Read and write documents and spreadsheets.', { mainPage: '/app/addons/office/home', hasUI: true, tools: ['read'], toolCount: 12 }),
  entry('voice', 'Voice', 'Speak and listen: transcription, speech and voice cloning.', { mainPage: '/app/addons/voice/home', hasUI: true }),
  entry('sandbox', 'Sandbox', 'Run commands for an agent inside an isolated box.', { mainPage: '/app/addons/sandbox/home', hasUI: true, tools: ['run'], toolCount: 4, update: 'update-pending' }),
  entry('acme-crm', 'Acme CRM', 'Look up customers in the Acme CRM.', { icon: null, tools: ['lookup'], toolCount: 3 }),
]

const rec = (a) => installedAddon({
  id: a.id, title: a.title, description: a.description, icon: a.icon, version: a.version, enabled: a.enabled, state: a.state,
  mainPage: a.mainPage, hasUI: a.hasUI, tools: a.tools, toolCount: a.toolCount, generation: 1,
  ...(a.id === 'sandbox' ? { version: '0.6.0', update: { available: true, state: 'update-available', version: '0.7.0', digest: `sha256:${'b'.repeat(64)}`, checkedAt: '2026-11-01T00:00:00Z' } } : {}),
})

export const AGENTS = [{ id: 'a1', name: 'Support', model: 'qwen3-8b' }, { id: 'a2', name: 'Research', model: 'qwen3-8b' }]
export const KINDS = [
  { addon: 'slack', kind: 'slack', title: 'Slack', description: 'Talk to an agent from Slack.', page: 'apps' },
  { addon: 'telegram', kind: 'telegram', title: 'Telegram', description: 'Talk to an agent from Telegram.', page: 'bots' },
]
export const CONNECTIONS = [
  { id: 'c1', name: 'Team workspace', kind: 'slack', addon: 'slack', active: true, enabled: true, agent_id: 'a2', accept_any: false, allow: ['u1', 'u2', 'u3'], has_token: true },
  { id: 'c2', name: 'Support bot', kind: 'telegram', addon: 'telegram', active: true, enabled: true, agent_id: 'a1', accept_any: true, allow: [], has_token: false },
]

// More add-ons the library offers, besides the installed ones.
const OFFERED = [
  release('discord', { name: 'Discord', description: 'Answer in servers and direct messages with one of your saved agents.', toolCount: 0 }),
  release('contacts', { name: 'Contacts', description: 'An address book kept inside LocalAI, for agents and for you.', toolCount: 4, ui: { mainPage: 'home' } }),
  release('home-assistant', { name: 'Home Assistant', description: 'Let an agent read your home and run scenes you approve.', toolCount: 8 }),
  release('jellyfin', { name: 'Jellyfin', description: 'Search your media library and see what is playing.', toolCount: 4 }),
  release('face', { name: 'Face', description: 'Recognise and compare faces in images you choose.', ui: { mainPage: 'home' } }),
  release('pikvm', { name: 'PiKVM', description: 'See and control a machine through a PiKVM, with every action confirmed.', toolCount: 5 }),
]

// Installs the stubs. `connections: false` leaves the messaging API out.
export async function addonsScene(page, { theme, connections = true, apps = APPS, withReleases = true } = {}) {
  if (theme) await page.addInitScript((value) => { try { localStorage.setItem('localai-theme', value) } catch { /* private window */ } }, theme)
  await page.route('**/api/auth/status', route => route.fulfill({ json: sessionFor(ADMIN, { addons: true, chats: true }) }))
  const state = await mockAddons(page, {
    apps,
    installed: apps.map(rec),
    releases: withReleases ? [...apps.map(a => release(a.id, { name: a.title, description: a.description, version: '0.2.0', toolCount: a.toolCount, ui: a.mainPage ? { mainPage: 'home' } : {} })), ...OFFERED] : [],
  })
  if (connections) {
    await page.route('**/api/chats/agents*', r => r.fulfill({ json: { agents: AGENTS } }))
    await page.route(/\/api\/chats\/connectors\/kinds$/, r => r.fulfill({ json: { kinds: KINDS } }))
    await page.route(/\/api\/chats\/connectors(\?.*)?$/, r => r.fulfill({ json: { connectors: CONNECTIONS } }))
  }
  return state
}
