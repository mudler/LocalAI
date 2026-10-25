// Shared fixtures for the Home page specs: stubbed API routes and conversations
// written to the browser storage the Chat page uses.

export const CHATS_KEY = 'localai_chats_data'

export const CHAT_MODELS = [
  'qwen3-8b-instruct',
  'gemma-4-e4b-it-qat-q4_0',
  'qwen2.5-vl-7b-instruct',
  'qwen3-32b-instruct',
]

const GB = 1024 * 1024 * 1024

// One GPU, 24 GB, 8.8 GB in use.
export const GPU_RESOURCES = {
  type: 'gpu',
  available: true,
  gpus: [{ name: 'RTX 4090', vendor: 'nvidia', total_vram: 24 * GB, used_vram: 8.8 * GB, usage_percent: 36.7 }],
  ram: { total: 64 * GB, used: 18.2 * GB, free: 45.8 * GB, available: 45.8 * GB, usage_percent: 28.4 },
  aggregate: { total_memory: 24 * GB, used_memory: 8.8 * GB, free_memory: 15.2 * GB, usage_percent: 36.7, gpu_count: 1 },
}

export const LOADED = [
  { id: 'qwen3-8b-instruct', backend: 'llama-cpp' },
  { id: 'whisper-large-v3-turbo', backend: 'whisper' },
]

// Stub what Home reads. `models` are the installed ids (v1 list) and
// `chatModels` the ones the picker offers.
export async function mockHome(page, {
  loaded = LOADED,
  models = [...CHAT_MODELS, 'whisper-large-v3-turbo'],
  chatModels = CHAT_MODELS,
  resources = GPU_RESOURCES,
  features = { distributed: false, localai_assistant: true, agents: true, mcp: true },
  nodes = null,
  operations = [],
} = {}) {
  await page.route('**/api/features', route => route.fulfill({ json: features }))
  await page.route('**/system', route => route.fulfill({ json: { backends: ['llama-cpp'], loaded_models: loaded } }))
  await page.route('**/v1/models', route => route.fulfill({ json: { data: models.map(id => ({ id })) } }))
  await page.route('**/api/models/capabilities', route =>
    route.fulfill({ json: { data: chatModels.map(id => ({ id, capabilities: ['FLAG_CHAT'] })) } }))
  await page.route('**/api/resources', route => route.fulfill({ json: resources }))
  await page.route('**/api/operations', route => route.fulfill({ json: operations }))
  if (nodes) await page.route('**/api/nodes', route => route.fulfill({ json: nodes }))
}

// Conversations: today twice, yesterday twice, three days ago once.
export function sampleChats(now = Date.now()) {
  const at = (daysAgo, hour, minute) => {
    const d = new Date(now)
    d.setDate(d.getDate() - daysAgo)
    d.setHours(hour, minute, 0, 0)
    return d.getTime()
  }
  // 90 minutes ago, but never before midnight: it must stay in today's group.
  const earlierToday = Math.max(now - 90 * 60_000, at(0, 0, 1))
  const chat = (id, name, model, updatedAt, history) => ({
    id, name, model, history, updatedAt, createdAt: updatedAt - 60_000,
    systemPrompt: '', mcpMode: false, mcpServers: [], clientMCPServers: [],
    temperature: null, topP: null, topK: null,
    tokenUsage: { prompt: 0, completion: 0, total: 0 }, contextSize: null,
  })
  const pair = (q, a) => [{ role: 'user', content: q }, { role: 'assistant', content: a }]
  return [
    chat('c-today-a', 'Rewrite the 3.4 release notes for clarity', 'qwen3-8b-instruct', now - 60_000,
      [...pair('Rewrite the release notes', 'Moved Migration to the top and cut the intro to two sentences.'), ...pair('Shorter', 'Done.'), ...pair('Thanks', 'Anytime.')]),
    chat('c-today-b', 'Why does llama-cpp stall at a 4096 context', 'qwen3-8b-instruct', earlierToday,
      [...pair('Why does it stall?', 'Check flash attention and the KV cache type first.'), ...pair('Which type?', 'q8_0 is a safe default.')]),
    chat('c-yday-a', 'Describe this architecture diagram', 'qwen2.5-vl-7b-instruct', at(1, 18, 22),
      [{ role: 'user', content: [{ type: 'text', text: 'Describe this diagram' }, { type: 'image_url', image_url: { url: 'data:image/png;base64,AAAA' } }] },
        { role: 'assistant', content: 'Three services behind one gateway, two share a queue.' },
        ...pair('Which two?', 'The billing and the audit service.')].slice(0, 3)),
    chat('c-yday-b', 'Draft a polite reply about the invoice', 'gemma-4-e4b-it-qat-q4_0', at(1, 9, 5),
      pair('Draft a reply about the invoice', 'Thanks for flagging this, I will check with accounting.')),
    chat('c-week-a', 'Regex for semver with prerelease tags', 'qwen3-8b-instruct', at(3, 16, 30),
      [...pair('Regex for semver', 'Use a named group for the prerelease part, then test it.'), { role: 'user', content: 'Thanks' }]),
  ]
}

export async function seedChats(page, chats = sampleChats(), activeChatId = chats[0]?.id) {
  await page.addInitScript(([key, data]) => {
    if (!localStorage.getItem(key)) localStorage.setItem(key, JSON.stringify(data))
  }, [CHATS_KEY, { chats, activeChatId, lastSaved: Date.now() }])
}

export async function setTheme(page, mode) {
  await page.addInitScript((m) => localStorage.setItem('localai-theme', m), mode)
}

// A signed-in person who is not an admin.
export async function mockUser(page, permissions = {}) {
  await page.route('**/api/auth/status', route => route.fulfill({
    json: { authEnabled: true, staticApiKeyRequired: false, providers: ['local'], user: { id: 'u1', name: 'Sam', role: 'user', permissions } },
  }))
}
