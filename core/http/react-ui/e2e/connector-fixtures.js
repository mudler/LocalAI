// The core API of the connector bindings, stubbed for the page of a connector
// add-on (Telegram, Slack): it records the calls and keeps the bindings and
// the sender log in memory.

export const AGENTS = [
  { id: 'a1', name: 'Support', model: 'qwen3-8b', mcp_servers: [], addons: [], collections: [], skills: [], builtins: ['memory.write', 'files.upload', 'triggers.propose', 'triggers.run', 'talkback.channels', 'talkback.new_channel', 'profile.write'] },
  { id: 'a2', name: 'No model', model: '', mcp_servers: [], addons: [], collections: [], skills: [], builtins: [] },
  { id: 'a4', name: 'Builder', model: 'qwen3-8b', mcp_servers: [], addons: [], collections: [], skills: [], builtins: ['memory.write', 'triggers.propose', 'act.files', 'act.addons'] },
  { id: 'a3', name: 'Careful', model: 'qwen3-8b', mcp_servers: [], addons: [], collections: [], skills: [], builtins: ['memory.write', 'files.upload'] },
]

const NEWCOMER = { id: '42', name: 'Newcomer', count: 2, allowed: false, first: '2026-10-27T08:00:00Z', last: '2026-10-27T08:05:00Z' }

const MIB = 1024 * 1024

// The files of a kind that declares 1 MiB in each direction.
export const ONE_MIB = {
  receive: { effective_bytes: MIB, layer: 'addon', declared_bytes: MIB, allowed_bytes: MIB },
  send: { effective_bytes: MIB, layer: 'addon', declared_bytes: MIB, allowed_bytes: MIB },
}

// The files of a kind that declares 20 MiB to read and 256 KiB to send.
export const DECLARED = {
  receive: { effective_bytes: 20 * MIB, layer: 'addon', declared_bytes: 20 * MIB, allowed_bytes: 20 * MIB },
  send: { effective_bytes: 256 * 1024, layer: 'addon', declared_bytes: 256 * 1024, allowed_bytes: 256 * 1024 },
}

// The closed list of capabilities that the kinds call returns, as the host writes it.
export const CAPABILITIES = [
  { name: 'files.upload', title: 'Receive files', description: 'The agent may read files that senders attach to a message.', sensitive: false, usual: true },
  { name: 'memory.write', title: 'Save to memory', description: 'The agent may remember what a sender asks it to remember.', sensitive: false, usual: true },
  { name: 'proposals.confirm', title: 'Confirm changes from the chat', description: 'A sender can confirm a trigger or a change that the agent proposed (a memory note, the agent rules, a skill, a page or the dashboard) by answering yes or no in the same chat. Changes to the rules, skills, pages and dashboard need a paired sender.', sensitive: false, usual: true },
  { name: 'talkback.channels', title: 'Post to other channels', description: 'The agent may write to the channels listed under Where it may post when a sender asks.', sensitive: false, usual: true },
  { name: 'talkback.new_channel', title: 'Post to a new channel', description: 'The agent may write to a channel that is not approved yet.', sensitive: true, usual: false },
  { name: 'tools.write', title: 'Let people who are not paired change things with add-on tools', description: 'Paired people already use the write tools of the add-ons the agent is connected to. Turn this on for people on the allowlist who are not paired.', sensitive: false, usual: false },
  { name: 'triggers.propose', title: 'Propose triggers', description: 'The agent may propose a timer or an event trigger when a sender asks for one.', sensitive: false, usual: true },
  { name: 'triggers.run', title: 'Fire event triggers', description: 'A message from a sender may start a run of an event trigger.', sensitive: false, usual: true },
]

// A kind that declares two roles and what it can give to them.
export const ROLES_KIND = {
  roles: [{ id: 'workspace_admin', title: 'Workspace admin', description: 'Owners and admins of the workspace.' }, { id: 'member', title: 'Channel member' }],
  permissions: [{ name: 'memory.write', sensitive: false }, { name: 'files.upload', sensitive: false }, { name: 'talkback.channels', sensitive: false }, { name: 'talkback.new_channel', sensitive: true }],
}

// The held-back capabilities of the grants an agent can hold (those that are
// not grants of the agent are never held back by it).
const AGENT_GRANTS = ['memory.write', 'files.upload', 'triggers.propose', 'triggers.run', 'talkback.channels', 'talkback.new_channel']

// A stand-in for the host's access preview: the same shape and the same order
// of checks (agent permission, connection permission, sender trust), written
// from the form values of the request.
// The verb phrases the host uses for each capability in its sentences.
const WORDS = {
  'proposals.confirm': 'confirm proposals', 'memory.write': 'save notes to memory', 'files.upload': 'receive files',
  'triggers.propose': 'set reminders and scheduled tasks', 'triggers.run': 'run scheduled tasks',
  'talkback.channels': 'write to other channels', 'talkback.new_channel': 'start conversations in new channels',
  'profile.write': "change the agent's rules", 'skills.write': 'save skills', 'pages.write': 'publish pages',
  'dashboard.write': 'change the dashboard', 'knowledge.write': 'write to knowledge', 'tools.write': 'change things in other services with add-on tools',
}

export function previewFor(body, roles = []) {
  const agent = AGENTS.find(a => a.id === body.agent_id)
  const decide = (c, who) => {
    if (!body.agent_id) return { capability: c.name, allowed: false, code: 'no_agent', blocked_by: 'connection', message: 'This connection has no agent, so the bot cannot answer or act yet.', fix: { kind: 'open_connection', target: 'agent_id' }, then_check: ['sender'] }
    if (agent && AGENT_GRANTS.includes(c.name) && !agent.builtins.includes(c.name)) {
      return { capability: c.name, allowed: false, code: 'agent_permission_off', blocked_by: 'agent', message: `The agent is not allowed to ${WORDS[c.name]}. Turn on the matching permission in the agent.`, fix: { kind: 'open_agent', target: agent.id, field: c.name }, then_check: ['connection', 'sender'] }
    }
    if (!(body.caps || []).includes(c.name)) {
      return { capability: c.name, allowed: false, code: 'connection_permission_off', blocked_by: 'connection', message: `This connection does not allow the bot to ${WORDS[c.name]}. Tick the matching permission on the connection.`, fix: { kind: 'open_connection', target: 'caps', field: c.name }, then_check: ['sender'] }
    }
    if (who === 'anyone') return { capability: c.name, allowed: false, code: 'channel_not_trusted', blocked_by: 'sender', message: `People in a channel are not trusted one by one, so the bot cannot ${WORDS[c.name]} for them. Add the person to the allowed people.`, fix: { kind: 'add_person', target: 'allow' } }
    if (who.startsWith('role:')) {
      const id = who.slice(5)
      return (body.grants || []).some(g => g.role === id && g.caps.includes(c.name)) && !c.sensitive
        ? { capability: c.name, allowed: true, via: who }
        : { capability: c.name, allowed: false, code: 'role_not_granted', blocked_by: 'sender', message: `The role of this person does not let the bot ${WORDS[c.name]}. Allow it for that role on the connection.`, fix: { kind: 'open_connection', target: 'grants' } }
    }
    if (c.sensitive && who !== 'paired') return { capability: c.name, allowed: false, code: 'sender_needs_pairing', blocked_by: 'sender', message: `This person needs to be paired with this bot before the bot can ${WORDS[c.name]} for them.`, fix: { kind: 'pair_sender', target: 'pairing' } }
    return { capability: c.name, allowed: true, via: who === 'paired' ? 'paired' : 'allowlist' }
  }
  const subjects = [
    { id: 'listed', title: 'A sender on the allowlist' },
    { id: 'anyone', title: 'A sender let in through a listed channel' },
    { id: 'paired', title: 'A paired sender' },
    ...roles.map(r => ({ id: `role:${r.id}`, title: r.title })),
  ].map(sub => ({ ...sub, decisions: CAPABILITIES.map(c => decide(c, sub.id)) }))
  return { binding: 'b1', preview: true, subjects, recent: [] }
}

export function connectorApi(page, initial = [], kind = 'telegram', senders = [NEWCOMER], files = ONE_MIB, { capabilities = CAPABILITIES, kindExtra = {}, access = null, preview = null, previewDelay = 0, previewStatus = 200 } = {}) {
  const s = { items: structuredClone(initial), calls: [], senders: structuredClone(senders), previews: [], urls: [] }
  page.on('request', r => { if (r.url().includes('/api/chats/connectors/')) s.urls.push(r.url()) })
  const rec = (method, url, body) => s.calls.push({ method, url: new URL(url).pathname, body })
  return {
    s,
    async install() {
      await page.route('**/api/chats/agents*', r => r.fulfill({ json: { agents: AGENTS } }))
      await page.route(/\/api\/chats\/connectors\/[^/?]+\/senders\/[^/?]+\/allow$/, r => {
        rec('POST', r.request().url(), null)
        s.senders = s.senders.map(x => ({ ...x, allowed: true }))
        return r.fulfill({ json: s.items[0] })
      })
      await page.route(/\/api\/chats\/connectors\/[^/?]+\/access\/preview(\?.*)?$/, async r => {
        const body = r.request().postDataJSON()
        s.previews.push({ id: decodeURIComponent(new URL(r.request().url()).pathname.split('/').at(-3)), body })
        if (previewDelay) await new Promise(ok => setTimeout(ok, previewDelay))
        if (previewStatus !== 200) return r.fulfill({ status: previewStatus, json: { error: { message: 'The check is not available right now.' } } })
        return r.fulfill({ json: typeof preview === 'function' ? preview(body) : preview || previewFor(body, kindExtra.roles || []) })
      })
      await page.route(/\/api\/chats\/connectors\/[^/?]+\/access(\?.*)?$/, r => r.fulfill({ json: access || { binding: 'b1', subjects: [], recent: [] } }))
      await page.route(/\/api\/chats\/connectors\/[^/?]+\/pairing$/, r => {
        rec('POST', r.request().url(), null)
        return r.fulfill({ json: { code: 'LA-K7M2QX9P', expires: new Date(Date.now() + 10 * 60 * 1000).toISOString() } })
      })
      await page.route(/\/api\/chats\/connectors\/[^/?]+\/paired\/[^/?]+$/, r => {
        rec('DELETE', r.request().url(), null)
        const sender = decodeURIComponent(new URL(r.request().url()).pathname.split('/').pop())
        s.items = s.items.map(x => ({ ...x, paired: (x.paired || []).filter(p => p.sender_id !== sender) }))
        return r.fulfill({ json: s.items[0] })
      })
      await page.route(/\/api\/chats\/connectors\/[^/?]+\/senders$/, r => r.fulfill({ json: { senders: s.senders } }))
      await page.route(/\/api\/chats\/connectors\/[^/?]+\/test$/, r => r.fulfill({ json: { bot_id: 1, bot_username: 'support_bot' } }))
      await page.route(/\/api\/chats\/connectors\/[^/?]+\/(enable|disable)$/, r => {
        const verb = new URL(r.request().url()).pathname.split('/').pop()
        rec('POST', r.request().url(), null)
        s.items = s.items.map(x => ({ ...x, enabled: verb === 'enable' }))
        return r.fulfill({ json: s.items[0] })
      })
      await page.route(/\/api\/chats\/connectors\/[^/?]+$/, r => {
        const req = r.request()
        rec(req.method(), req.url(), req.postDataJSON?.())
        if (req.method() === 'DELETE') { s.items = []; return r.fulfill({ status: 204, body: '' }) }
        s.items = s.items.map(x => ({ ...x, ...req.postDataJSON(), token: undefined, has_token: true }))
        return r.fulfill({ json: s.items[0] })
      })
      await page.route(/\/api\/chats\/connectors(\?.*)?$/, r => {
        const req = r.request()
        if (req.method() === 'POST') {
          const b = req.postDataJSON()
          rec('POST', req.url(), b)
          s.items.push({ id: 'b1', kind, allow: [], ...b, token: undefined, has_token: true })
          return r.fulfill({ status: 201, json: s.items.at(-1) })
        }
        return r.fulfill({ json: { connectors: s.items } })
      })
      await page.route(/\/api\/chats(\?.*)?$/, r => r.fulfill({ json: { conversations: [], persistent: true, files: true } }))
      await page.route('**/api/chats/notifications*', r => r.fulfill({ json: { notifications: [], unread: 0 } }))
      // Registered last, so it answers before the route of one binding.
      await page.route(/\/api\/chats\/connectors\/kinds(\?.*)?$/, r => r.fulfill({ json: { kinds: [{ addon: kind, kind, title: kind, files, ...kindExtra }], capabilities } }))
    },
  }
}
