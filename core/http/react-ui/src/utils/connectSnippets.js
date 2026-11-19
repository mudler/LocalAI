// Config snippets that point a coding agent at this LocalAI server. Pure: the
// panel passes the values it knows and prints what comes back, so the formats
// are unit-tested without a browser.
//
// The formats follow docs/content/features/coding-agents.md. Agents change
// fast; when a key is rejected, the agent's own docs win.

export const PLACEHOLDER_KEY = 'YOUR_API_KEY'
export const PLACEHOLDER_MODEL = 'my-model'
// Not a secret: any value works when the server does not check keys.
export const DUMMY_KEY = 'sk-localai'
// The context size the examples carry. The person sets the real one.
export const PLACEHOLDER_CONTEXT = 65536
export const PLACEHOLDER_OUTPUT = 8192
// The docs page of this guide, on the public docs site (the server itself does
// not serve the docs).
export const DOCS_PATH = '/features/coding-agents'
export const DOCS_URL = `https://localai.io${DOCS_PATH}/`

export const AGENT_IDS = ['codex', 'claude-code', 'opencode', 'hermes', 'nib', 'pi']

// What each agent speaks, the file it reads, and the LocalAI route behind it.
export const AGENTS = {
  codex: { name: 'Codex CLI', endpoint: '/v1/responses', file: '~/.codex/config.toml' },
  'claude-code': { name: 'Claude Code', endpoint: '/v1/messages', file: '~/.claude/settings.json' },
  opencode: { name: 'opencode', endpoint: '/v1/chat/completions', file: '~/.config/opencode/opencode.json' },
  hermes: { name: 'Hermes Agent', endpoint: '/v1/chat/completions', file: '~/.hermes/config.yaml' },
  nib: { name: 'nib', endpoint: '/v1/chat/completions', file: '~/.config/nib/config.yaml' },
  pi: { name: 'pi', endpoint: '/v1/chat/completions', file: '~/.pi/agent/models.json' },
}

// How the server checks keys, from /api/auth/status.
//   none    no accounts and no static key: any value works
//   static  one server key (LOCALAI_API_KEY) and no accounts
//   user    accounts: each person has their own keys
export function authMode({ authEnabled, staticApiKeyRequired } = {}) {
  if (authEnabled) return 'user'
  if (staticApiKeyRequired) return 'static'
  return 'none'
}

// The key to print: the real one when we hold it, a dummy when the server does
// not check, the placeholder otherwise.
export function pickKey({ mode, key }) {
  if (key) return key
  return mode === 'none' ? DUMMY_KEY : PLACEHOLDER_KEY
}

const trimBase = (u) => String(u || 'http://localhost:8080').replace(/\/+$/, '')

// A value in a shell command: bare when it is plain, single-quoted otherwise.
function sh(v) {
  return /^[\w./:@%+=,-]+$/.test(v) ? v : `'${String(v).replace(/'/g, `'\\''`)}'`
}
const js = (v) => JSON.stringify(String(v))

// Returns { id, name, endpoint, file, blocks: [{ id, file, lang, text }] }.
// opts: baseUrl (origin plus any sub-path, no /v1), key, model, mode.
export function buildSnippet(agent, opts = {}) {
  const meta = AGENTS[agent]
  if (!meta) throw new Error(`unknown agent: ${agent}`)
  const root = trimBase(opts.baseUrl)
  const v1 = `${root}/v1`
  const model = opts.model || PLACEHOLDER_MODEL
  const key = pickKey({ mode: opts.mode, key: opts.key })
  const ctx = PLACEHOLDER_CONTEXT
  const out = PLACEHOLDER_OUTPUT
  let blocks
  switch (agent) {
    case 'codex':
      blocks = [
        {
          id: 'config', file: meta.file, lang: 'toml',
          text: `model = ${js(model)}
model_provider = "localai"
model_context_window = ${ctx}

[model_providers.localai]
name = "LocalAI"
base_url = ${js(v1)}
env_key = "LOCALAI_API_KEY"
wire_api = "responses"`,
        },
        { id: 'shell', file: '', lang: 'bash', text: `export LOCALAI_API_KEY=${sh(key)}\ncodex` },
      ]
      break
    case 'claude-code':
      // Claude Code adds /v1/messages itself: the base URL has no /v1.
      blocks = [{
        id: 'shell', file: '', lang: 'bash',
        text: `export ANTHROPIC_BASE_URL=${sh(root)}
export ANTHROPIC_AUTH_TOKEN=${sh(key)}
export ANTHROPIC_MODEL=${sh(model)}
export ANTHROPIC_DEFAULT_HAIKU_MODEL=${sh(model)}
claude`,
      }]
      break
    case 'opencode':
      blocks = [
        {
          id: 'config', file: meta.file, lang: 'jsonc',
          text: `{
  "$schema": "https://opencode.ai/config.json",
  "providers": {
    "localai": {
      "name": "LocalAI",
      "env": ["LOCALAI_API_KEY"],
      "package": "@opencode/ai/providers/openai-compatible",
      "settings": { "baseURL": ${js(v1)} },
      "models": {
        ${js(model)}: {
          "name": ${js(model)},
          "limit": { "context": ${ctx}, "output": ${out} }
        }
      }
    }
  }
}`,
        },
        { id: 'shell', file: '', lang: 'bash', text: `export LOCALAI_API_KEY=${sh(key)}\nopencode` },
      ]
      break
    case 'hermes':
      blocks = [{
        id: 'config', file: meta.file, lang: 'yaml',
        text: `model:
  default: ${js(model)}
  provider: custom
  base_url: ${js(v1)}
  api_key: ${js(key)}
  context_length: ${ctx}`,
      }]
      break
    case 'nib':
      blocks = [{
        id: 'config', file: meta.file, lang: 'yaml',
        text: `model: ${js(model)}
api_key: ${js(key)}
base_url: ${js(v1)}`,
      }]
      break
    case 'pi':
      blocks = [{
        id: 'config', file: meta.file, lang: 'json',
        text: `{
  "$schema": "https://pi.dev/schemas/models.schema.json",
  "providers": {
    "localai": {
      "baseUrl": ${js(v1)},
      "api": "openai-completions",
      "apiKey": ${js(key)},
      "models": [
        { "id": ${js(model)}, "contextWindow": ${ctx}, "maxTokens": ${out} }
      ]
    }
  }
}`,
      }]
      break
    default:
      blocks = []
  }
  return { id: agent, name: meta.name, endpoint: meta.endpoint, file: meta.file, blocks }
}
