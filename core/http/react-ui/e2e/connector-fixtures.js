// The core API of the connector bindings, stubbed for the page of a connector
// add-on (Telegram, Slack): it records the calls and keeps the bindings and
// the sender log in memory.

export const AGENTS = [
  { id: 'a1', name: 'Support', model: 'qwen3-8b', mcp_servers: [], addons: [], collections: [], skills: [] },
  { id: 'a2', name: 'No model', model: '', mcp_servers: [], addons: [], collections: [], skills: [] },
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
  { name: 'proposals.confirm', title: 'Confirm changes from the chat', description: 'A sender can confirm a change that the agent proposed, by answering in the same chat.', sensitive: false, usual: true },
  { name: 'talkback.channels', title: 'Post to approved channels', description: 'The agent may write to the approved channels when a sender asks.', sensitive: false, usual: true },
  { name: 'talkback.new_channel', title: 'Post to a new channel', description: 'The agent may write to a channel that is not approved yet.', sensitive: true, usual: false },
  { name: 'tools.write', title: 'Change things outside LocalAI', description: 'An add-on tool may change something with the credential of the binding.', sensitive: false, usual: false },
  { name: 'triggers.propose', title: 'Propose triggers', description: 'The agent may propose a timer or an event trigger when a sender asks for one.', sensitive: false, usual: true },
  { name: 'triggers.run', title: 'Fire event triggers', description: 'A message from a sender may start a run of an event trigger.', sensitive: false, usual: true },
]

// A kind that declares two roles and what it can give to them.
export const ROLES_KIND = {
  roles: [{ id: 'workspace_admin', title: 'Workspace admin', description: 'Owners and admins of the workspace.' }, { id: 'member', title: 'Channel member' }],
  permissions: [{ name: 'memory.write', sensitive: false }, { name: 'files.upload', sensitive: false }, { name: 'talkback.channels', sensitive: false }, { name: 'talkback.new_channel', sensitive: true }],
}

export function connectorApi(page, initial = [], kind = 'telegram', senders = [NEWCOMER], files = ONE_MIB, { capabilities = CAPABILITIES, kindExtra = {}, access = null } = {}) {
  const s = { items: structuredClone(initial), calls: [], senders: structuredClone(senders) }
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
      await page.route(/\/api\/chats\/connectors\/[^/?]+\/access$/, r => r.fulfill({ json: access || { binding: 'b1', subjects: [], recent: [] } }))
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
      await page.route(/\/api\/chats\/connectors\/kinds$/, r => r.fulfill({ json: { kinds: [{ addon: kind, kind, title: kind, files, ...kindExtra }], capabilities } }))
    },
  }
}
