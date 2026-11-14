import test from 'node:test'
import assert from 'node:assert/strict'
import { AGENT_IDS, AGENTS, authMode, buildSnippet, pickKey, PLACEHOLDER_KEY, PLACEHOLDER_MODEL, DUMMY_KEY, DOCS_URL } from '../../src/utils/connectSnippets.js'

const BASE = 'http://localhost:8080'
const text = (agent, opts) => buildSnippet(agent, { baseUrl: BASE, ...opts }).blocks.map(b => b.text).join('\n')

test('connect snippets: every agent has a name, an endpoint, a file and a block', () => {
  assert.deepEqual(AGENT_IDS, ['codex', 'claude-code', 'opencode', 'hermes', 'nib', 'pi'])
  for (const id of AGENT_IDS) {
    const s = buildSnippet(id, { baseUrl: BASE })
    assert.equal(s.name, AGENTS[id].name)
    assert.match(s.endpoint, /^\/v1\//)
    assert.match(s.file, /^~\//)
    assert.ok(s.blocks.length >= 1)
  }
  assert.throws(() => buildSnippet('nope'), /unknown agent/)
})

test('connect snippets: endpoints per agent', () => {
  assert.equal(AGENTS.codex.endpoint, '/v1/responses')
  assert.equal(AGENTS['claude-code'].endpoint, '/v1/messages')
  for (const id of ['opencode', 'hermes', 'nib', 'pi']) assert.equal(AGENTS[id].endpoint, '/v1/chat/completions')
})

test('connect snippets: codex uses the responses wire API and a /v1 base', () => {
  const s = buildSnippet('codex', { baseUrl: BASE, model: 'qwen3', key: 'k1' })
  assert.equal(s.file, '~/.codex/config.toml')
  const toml = s.blocks[0].text
  assert.match(toml, /wire_api = "responses"/)
  assert.match(toml, /base_url = "http:\/\/localhost:8080\/v1"/)
  assert.match(toml, /model = "qwen3"/)
  assert.match(toml, /model_context_window = 65536/)
  assert.match(s.blocks[1].text, /^export LOCALAI_API_KEY=k1\ncodex$/)
})

test('connect snippets: claude code base URL has no /v1 and sets the haiku model', () => {
  const t = text('claude-code', { model: 'qwen3', key: 'k1' })
  assert.match(t, /^export ANTHROPIC_BASE_URL=http:\/\/localhost:8080$/m)
  assert.doesNotMatch(t, /ANTHROPIC_BASE_URL=\S*\/v1/)
  assert.match(t, /ANTHROPIC_AUTH_TOKEN=k1/)
  assert.match(t, /ANTHROPIC_MODEL=qwen3/)
  assert.match(t, /ANTHROPIC_DEFAULT_HAIKU_MODEL=qwen3/)
})

test('connect snippets: opencode, hermes, nib and pi carry the /v1 base, key and model', () => {
  const o = JSON.parse(buildSnippet('opencode', { baseUrl: BASE, model: 'm1' }).blocks[0].text)
  assert.equal(o.providers.localai.settings.baseURL, 'http://localhost:8080/v1')
  assert.ok(o.providers.localai.models.m1)
  const p = JSON.parse(buildSnippet('pi', { baseUrl: BASE, model: 'm1', key: 'k1' }).blocks[0].text)
  assert.equal(p.providers.localai.baseUrl, 'http://localhost:8080/v1')
  assert.equal(p.providers.localai.apiKey, 'k1')
  assert.equal(p.providers.localai.models[0].id, 'm1')
  const h = text('hermes', { model: 'm1', key: 'k1' })
  assert.match(h, /base_url: "http:\/\/localhost:8080\/v1"/)
  assert.match(h, /api_key: "k1"/)
  assert.match(h, /context_length: 65536/)
  assert.equal(AGENTS.hermes.file, '~/.hermes/config.yaml')
  const n = text('nib', { model: 'm1', key: 'k1' })
  assert.match(n, /base_url: "http:\/\/localhost:8080\/v1"/)
  assert.equal(AGENTS.nib.file, '~/.config/nib/config.yaml')
  assert.equal(AGENTS.pi.file, '~/.pi/agent/models.json')
  assert.equal(AGENTS.opencode.file, '~/.config/opencode/opencode.json')
})

test('connect snippets: a sub-path mount stays in every base URL', () => {
  const base = 'https://example.org/localai/'
  for (const id of AGENT_IDS) {
    const t = buildSnippet(id, { baseUrl: base }).blocks.map(b => b.text).join('\n')
    assert.ok(t.includes('https://example.org/localai'), id)
    assert.ok(!t.includes('localai//'), id)
  }
  assert.match(text('claude-code', { baseUrl: base }), /ANTHROPIC_BASE_URL=https:\/\/example\.org\/localai\n/)
  assert.match(text('codex', { baseUrl: base }), /base_url = "https:\/\/example\.org\/localai\/v1"/)
})

test('connect snippets: a key is printed when given, the placeholder otherwise', () => {
  assert.ok(text('nib', { key: 'lai-abc', mode: 'user' }).includes('lai-abc'))
  const t = text('nib', { mode: 'user' })
  assert.ok(t.includes(PLACEHOLDER_KEY))
  assert.ok(!t.includes(DUMMY_KEY))
})

test('connect snippets: no model gives the placeholder model', () => {
  assert.ok(text('hermes', {}).includes(`"${PLACEHOLDER_MODEL}"`))
  assert.ok(text('claude-code', {}).includes(`ANTHROPIC_MODEL=${PLACEHOLDER_MODEL}`))
})

test('connect snippets: auth mode wording input', () => {
  assert.equal(authMode({ authEnabled: false, staticApiKeyRequired: false }), 'none')
  assert.equal(authMode({ authEnabled: false, staticApiKeyRequired: true }), 'static')
  assert.equal(authMode({ authEnabled: true, staticApiKeyRequired: false }), 'user')
  assert.equal(authMode(), 'none')
  // No auth: a dummy value instead of the placeholder.
  assert.equal(pickKey({ mode: 'none' }), DUMMY_KEY)
  assert.ok(text('claude-code', { mode: 'none' }).includes(`ANTHROPIC_AUTH_TOKEN=${DUMMY_KEY}`))
  // Static key: the person must supply the server's key.
  assert.equal(pickKey({ mode: 'static' }), PLACEHOLDER_KEY)
  assert.equal(pickKey({ mode: 'user' }), PLACEHOLDER_KEY)
  assert.equal(pickKey({ mode: 'user', key: 'k' }), 'k')
})

test('connect snippets: shell values with odd characters are quoted', () => {
  assert.match(text('claude-code', { model: "a b'c" }), /ANTHROPIC_MODEL='a b'\\''c'/)
})

test('connect snippets: docs link points at the coding agents page', () => {
  assert.match(DOCS_URL, /\/features\/coding-agents\/$/)
})
