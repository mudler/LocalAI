// Stubs for the Connections and Automations tabs of a project: the agents
// stubs of project-agents-fixtures.js plus the three listing routes and the
// connector kinds. `s.links.calls` lists the reads, `s.links.fail` the routes
// that answer 500 now (connections, triggers, loops).
import { installProjectAgents, PROJECTS, AGENTS } from './project-agents-fixtures.js'

const NOW = Date.now()
const DAY = 24 * 3600 * 1000

// A token-looking string the server never sends. The page must not draw it: a
// leak would show here.
export const SECRET = 'xoxb-1234-secret-token-value'

export const LINK_PROJECTS = [
  ...PROJECTS,
  { id: 'p5', name: 'Single agent project', description: 'One agent.', slug: 'single-agent-project', status: 'active', defaults: {}, updated: new Date(NOW).toISOString(), chat_count: 0, agent_count: 1 },
]
export const LINK_AGENTS = [
  ...AGENTS,
  { ...AGENTS[0], id: 'ap5', name: 'Solo helper', project_id: 'p5' },
]

export const KINDS = [
  { addon: 'slack', kind: 'slack', title: 'Slack', page: 'connections' },
  { addon: 'telegram', kind: 'telegram', title: 'Telegram', page: 'connections' },
  { addon: 'mail', kind: 'email', title: 'Email', page: 'accounts', agent_optional: true },
]

const conn = (id, kind, name, agent_id, agent_name, enabled) => ({ id, kind, name, agent_id, agent_name, enabled, token: SECRET, allow: [SECRET] })

const CONNECTIONS = {
  p1: [
    conn('b1', 'slack', 'Design team channel', 'ap1', 'Copy editor', true),
    conn('b2', 'telegram', 'Client updates bot', 'ap3', 'Client questions', true),
    conn('b3', 'matrix', 'Studio room', 'ap2', 'Asset checker', false),
  ],
  p3: [conn('b4', 'slack', 'Podcast channel', 'ap4', 'Podcast notes', true), conn('b5', 'telegram', 'Old bot', 'ap4', 'Podcast notes', false)],
}

const TRIGGERS = {
  p1: [
    { id: 't1', name: 'Morning copy review', kind: 'schedule', agent_id: 'ap1', agent_name: 'Copy editor', schedule: 'daily 08:00', enabled: true, proposed: false, next: new Date(NOW + DAY).toISOString() },
    { id: 't2', name: 'Check new images', kind: 'file', agent_id: 'ap2', agent_name: 'Asset checker', folder: '/Projects/launch-site/files/incoming', enabled: true, proposed: false },
    { id: 't3', name: 'Weekly open questions', kind: 'schedule', agent_id: 'ap3', agent_name: 'Client questions', schedule: 'weekly fri 16:30', enabled: false, proposed: false },
    { id: 't6', name: 'Build failed hook', kind: 'event', agent_id: 'ap1', agent_name: 'Copy editor', enabled: true, proposed: false },
  ],
  p3: [
    { id: 't4', name: 'Transcript cleanup', kind: 'schedule', agent_id: 'ap4', agent_name: 'Podcast notes', schedule: 'daily 09:00', enabled: true, proposed: false, next: new Date(NOW + DAY).toISOString() },
    { id: 't5', name: 'Old reminder', kind: 'schedule', agent_id: 'ap4', agent_name: 'Podcast notes', schedule: 'daily 10:00', enabled: false, proposed: false },
  ],
}

const LOOPS = {
  p1: [
    { id: 'l1', chat_id: 'c1', chat_title: 'Homepage hero rewrite', prompt: 'Review the hero copy again after each edit.', state: 'active' },
    { id: 'l2', chat_id: 'c2', chat_title: 'Pricing page review', prompt: 'Keep checking the pricing table.', state: 'ended' },
  ],
  p3: [{ id: 'l3', chat_id: 'c9', chat_title: 'Episode outline', prompt: 'Refine the outline.', state: 'active' }],
}

export async function installProjectLinks(page, { kinds = KINDS } = {}) {
  const s = await installProjectAgents(page, { projects: LINK_PROJECTS, agents: LINK_AGENTS })
  s.links = { calls: [], fail: new Set(), kindsFail: false, delay: {} } // delay[name][projectId]: ms to hold the answer
  const data = { connections: CONNECTIONS, triggers: TRIGGERS, loops: LOOPS }
  for (const name of ['connections', 'triggers', 'loops']) {
    await page.route(new RegExp(`/api/chats/projects/[^/?]+/${name}(\\?.*)?$`), async (route) => {
      const id = decodeURIComponent(new URL(route.request().url()).pathname.split('/')[4])
      s.links.calls.push({ name, id })
      const hold = s.links.delay[name]?.[id]
      if (hold) await new Promise((resolve) => setTimeout(resolve, hold))
      if (s.links.fail.has(name)) return route.fulfill({ status: 500, json: { error: { message: 'database is locked' } } })
      return route.fulfill({ json: { [name]: structuredClone(data[name][id] || []) } })
    })
  }
  await page.route(/\/api\/chats\/connectors\/kinds(\?.*)?$/, (route) => {
    if (s.links.kindsFail) return route.fulfill({ status: 500, json: { error: { message: 'no' } } })
    return route.fulfill({ json: { kinds } })
  })
  return s
}
