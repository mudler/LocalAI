import { installAgents, AGENTS, DRAFT_TOOLS, SKILLS } from './agents-fixtures.js'

// The data of the "What it can use" specs: add-on rows as the draft endpoint
// sends them (tool_list, resources, the person's accounts), the skills list
// with the skills of add-ons and the two the host grants with the notes, and
// the resources an agent may be given. The names are stub data of the spec.

export const WORK = { id: 'b1', name: 'Work account' }
export const HOME = { id: 'b2', name: 'Home account' }
export const LAMP = { id: 'l1', name: 'Hall lamp' }

const r = (name) => ({ name, kind: 'read' })
const w = (name) => ({ name, kind: 'write' })

export const USES_ADDONS = [
  { id: 'office', tools: 4, off: false, title: 'Office', description: 'Read and write documents and spreadsheets.', tool_list: [r('read_range'), w('write_range'), r('read_doc'), w('create_document')] },
  {
    id: 'tracker', tools: 9, off: false, title: 'Tracker', description: 'Look up and update issues.', credentialed: true, connections: [WORK, HOME], enabled_connections: [], needs_connection: true,
    tool_list: [r('list_issues'), r('get_issue'), r('search_issues'), r('list_projects'), w('create_issue'), w('update_issue'), w('close_issue'), w('comment'), w('assign')],
  },
  { id: 'lamp', tools: 3, off: false, title: 'Lamp control', description: 'Switch the lights.', credentialed: true, connections: [LAMP], enabled_connections: ['l1'], connection_by: 'only', tool_list: [r('state'), w('on'), w('off')] },
  {
    id: 'tables', tools: 5, off: false, title: 'Tables', description: 'Databases of rows you can query and edit.',
    resources: { noun: 'database', noun_plural: 'databases' },
    tool_list: [r('databases-list'), r('rows-query'), w('rows-append'), w('rows-update'), w('rows-delete')],
  },
]

export const DATABASES = {
  addon_id: 'tables', noun: 'database', noun_plural: 'databases',
  items: [
    { id: 'db-sales', name: 'Sales', role: 'owner' },
    { id: 'db-people', name: 'People', role: 'editor' },
    { id: 'db-ledger', name: 'Ledger', role: 'viewer' },
    { id: 'db-notes', name: 'Field notes', role: 'owner' },
  ],
}

const skill = (name, over) => ({ name, description: `The ${name} skill.`, enabled: true, editable: false, path: `/s/${name}/SKILL.md`, ...over })
export const USES_SKILLS = [
  ...SKILLS.filter((s) => s.layer !== 'addon'),
  skill('pdf', { layer: 'addon', addon: 'office', addon_title: 'Office', description: 'Read and create PDFs.' }),
  skill('xlsx', { layer: 'addon', addon: 'office', addon_title: 'Office', description: 'Read and write spreadsheets.' }),
  skill('tracker-howto', { layer: 'addon', addon: 'tracker', addon_title: 'Tracker', description: 'How to write a good issue.' }),
  skill('tables-readonly', { layer: 'addon', addon: 'tables', addon_title: 'Tables', description: 'Query a database without changing it.' }),
  skill('memory', { layer: 'builtin', provider: 'memory', locked: true, description: 'Read and save notes.' }),
  skill('memory-check-own-files', { layer: 'builtin', provider: 'memory', locked: true, description: 'Look at its own notes.' }),
]

// The agent editor with these add-ons. `agents` replaces the saved agents.
export async function installUses(page, { agents = AGENTS, addons = USES_ADDONS, databases = DATABASES, bindings = [], skills = USES_SKILLS, resourcesStatus = 200, connectors } = {}) {
  const s = await installAgents(page, { agents, bindings, skills, ...(connectors ? { connectors } : {}) })
  await page.route('**/api/chats/draft/tools*', route => route.fulfill({ json: { ...DRAFT_TOOLS, servers: [], off: [], connectors: (connectors || [{ id: 'm1', name: 'Docs', enabled: true }, { id: 'm2', name: 'Parked', enabled: false }]).map(c => ({ id: c.id, name: c.name, off: false, usable: c.enabled, reason: c.enabled ? undefined : 'off' })), addons } }))
  await page.route(/\/api\/chats\/agents\/addons\/[^/]+\/resources/, route => (resourcesStatus === 200
    ? route.fulfill({ json: databases })
    : route.fulfill({ status: resourcesStatus, json: { error: { message: 'no' } } })))
  return s
}

export const row = (page, title) => page.getByTestId('agent-addon-box').filter({ has: page.getByRole('switch', { name: title, exact: true }) })
