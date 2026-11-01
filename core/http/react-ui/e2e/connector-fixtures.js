// The core API of the connector bindings, stubbed for the page of a connector
// add-on (Telegram, Slack): it records the calls and keeps the bindings and
// the sender log in memory.

export const AGENTS = [
  { id: 'a1', name: 'Support', model: 'qwen3-8b', mcp_servers: [], addons: [], collections: [], skills: [] },
  { id: 'a2', name: 'No model', model: '', mcp_servers: [], addons: [], collections: [], skills: [] },
]

const NEWCOMER = { id: '42', name: 'Newcomer', count: 2, allowed: false, first: '2026-10-27T08:00:00Z', last: '2026-10-27T08:05:00Z' }

export function connectorApi(page, initial = [], kind = 'telegram', senders = [NEWCOMER]) {
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
    },
  }
}
