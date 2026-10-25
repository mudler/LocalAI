// Fixtures for the Traffic pages: Overview, Usage, Models, GPU and host,
// Traces, a trace, Middleware and Prometheus. Plain data, plus one function
// that answers every call those pages make, so a spec says only what is
// different about its case.
//
// Pure of Playwright imports so a script that is not a test can use it too.

import { GB, LOADED, gpuHost, mockOperate, NODES } from './operate-fixtures.js'

export { GB, LOADED, gpuHost, NODES }

// A small deterministic generator, so a screenshot and a spec see the same day.
function rng(seed) {
  let s = seed
  return () => {
    s = (s * 1664525 + 1013904223) % 4294967296
    return s / 4294967296
  }
}

export const MODELS = [
  { name: 'qwen3-8b-instruct', weight: 1, ratio: 0.32, failRate: 0.011 },
  { name: 'bge-m3', weight: 0.33, ratio: 0.9, failRate: 0 },
  { name: 'gemma-3-12b-it', weight: 0.19, ratio: 0.3, failRate: 0.014 },
  { name: 'kokoro-82m', weight: 0.17, ratio: 0.3, failRate: 0 },
  { name: 'whisper-large-v3', weight: 0.15, ratio: 0.28, failRate: 0.017 },
  { name: 'flux-dev', weight: 0.08, ratio: 0.28, failRate: 0.1 },
]

export const USERS = [
  { id: 'u-alice', name: 'alice', share: 0.5 },
  { id: 'u-bob', name: 'bob', share: 0.3 },
  { id: 'u-carol', name: 'carol', share: 0.2 },
]

const pad = n => String(n).padStart(2, '0')

// Usage buckets for a period, in the shape the usage API returns: hourly for
// day, daily for week and month, monthly for all.
export function usageBuckets(period = 'day', { models = MODELS, scale = 1, now = new Date() } = {}) {
  const rand = rng(period.length * 97 + 11)
  const stamps = []
  if (period === 'day') {
    for (let i = 23; i >= 0; i--) {
      const d = new Date(now.getTime() - i * 3600_000)
      stamps.push({ bucket: `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:00`, factor: 0.25 + 0.75 * Math.sin(((d.getHours() + 18) % 24) / 24 * Math.PI) ** 2 })
    }
  } else if (period === 'all') {
    for (let i = 5; i >= 0; i--) {
      const d = new Date(now.getFullYear(), now.getMonth() - i, 1)
      stamps.push({ bucket: `${d.getFullYear()}-${pad(d.getMonth() + 1)}`, factor: 8 + i })
    }
  } else {
    const days = period === 'week' ? 7 : 30
    for (let i = days - 1; i >= 0; i--) {
      const d = new Date(now.getTime() - i * 86_400_000)
      stamps.push({ bucket: `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`, factor: 8 + rand() * 4 })
    }
  }
  const rows = []
  for (const { bucket, factor } of stamps) {
    for (const m of models) {
      const requests = Math.round(m.weight * factor * scale * (22 + rand() * 6))
      if (requests <= 0) continue
      const prompt = Math.round(requests * 1400 * (0.8 + rand() * 0.4))
      const completion = Math.round(prompt * m.ratio)
      // Split a model's traffic across the users, so a user row has a total.
      for (const u of USERS) {
        const part = Math.max(1, Math.round(requests * u.share))
        rows.push({
          bucket, model: m.name, user_id: u.id, user_name: u.name,
          request_count: part,
          prompt_tokens: Math.round(prompt * u.share),
          completion_tokens: Math.round(completion * u.share),
          total_tokens: Math.round((prompt + completion) * u.share),
        })
      }
    }
  }
  return rows
}

export function usagePayload(period, opts = {}) {
  const usage = usageBuckets(period, opts)
  const totals = usage.reduce((t, r) => ({
    request_count: t.request_count + r.request_count,
    prompt_tokens: t.prompt_tokens + r.prompt_tokens,
    completion_tokens: t.completion_tokens + r.completion_tokens,
    total_tokens: t.total_tokens + r.total_tokens,
  }), { request_count: 0, prompt_tokens: 0, completion_tokens: 0, total_tokens: 0 })
  return { viewer: { id: 'local', name: 'local', role: 'admin', provider: 'local' }, usage, totals }
}

// GET /api/auth/usage/sources and its admin form.
export function sourcesPayload(period = 'day') {
  const rows = usageBuckets(period)
  const keys = [
    { api_key_id: 'k-ci', api_key_name: 'ci-bot', user_id: 'u-alice', user_name: 'alice', share: 0.45, last_used: new Date(Date.now() - 12 * 60_000).toISOString() },
    { api_key_id: 'k-notebook', api_key_name: 'notebook', user_id: 'u-bob', user_name: 'bob', share: 0.25, last_used: new Date(Date.now() - 3 * 3600_000).toISOString() },
    { api_key_id: 'k-home', api_key_name: 'home-assistant', user_id: 'u-carol', user_name: 'carol', share: 0.2, last_used: new Date(Date.now() - 26 * 3600_000).toISOString() },
  ]
  const buckets = []
  const byBucket = new Map()
  for (const r of rows) {
    const p = byBucket.get(r.bucket) || { tokens: 0, requests: 0 }
    p.tokens += r.total_tokens
    p.requests += r.request_count
    byBucket.set(r.bucket, p)
  }
  for (const [bucket, p] of byBucket) {
    for (const k of keys) {
      buckets.push({ bucket, api_key_id: k.api_key_id, api_key_name: k.api_key_name, source: 'api', total_tokens: Math.round(p.tokens * k.share), request_count: Math.round(p.requests * k.share) })
    }
    buckets.push({ bucket, source: 'web', total_tokens: Math.round(p.tokens * 0.1), request_count: Math.round(p.requests * 0.1) })
  }
  const total = rows.reduce((s, r) => s + r.total_tokens, 0)
  const requests = rows.reduce((s, r) => s + r.request_count, 0)
  return {
    buckets,
    truncated: false,
    totals: {
      by_source: { web: { tokens: Math.round(total * 0.1), requests: Math.round(requests * 0.1) } },
      by_key: keys.map(k => ({ api_key_id: k.api_key_id, api_key_name: k.api_key_name, user_id: k.user_id, user_name: k.user_name, tokens: Math.round(total * k.share), requests: Math.round(requests * k.share), last_used: k.last_used })),
      grand_total: { tokens: total, requests },
    },
  }
}

// GET /api/traces/summary: twelve columns oldest first.
export function traceSummary({ total = 723, errors = 7, p95 = 1400, hours = 24 } = {}) {
  const buckets = Array.from({ length: 12 }, (_, i) => {
    const wave = 0.5 + 0.5 * Math.sin((i / 12) * Math.PI * 1.4)
    const count = Math.round((total / 12) * (0.5 + wave))
    const failed = errors === 0 ? 0 : (i === 9 ? Math.ceil(errors * 0.6) : i === 10 ? Math.floor(errors * 0.4) : 0)
    return { start: new Date(Date.now() - (12 - i) * (hours / 12) * 3600_000).toISOString(), count: Math.max(count, failed), errors: failed }
  })
  return { total, errors, p95_ms: p95, window_hours: hours, buckets }
}

const b64 = value => Buffer.from(typeof value === 'string' ? value : JSON.stringify(value)).toString('base64')
const iso = ms => new Date(Date.now() - ms).toISOString()

// The list as GET /api/traces returns it: bodies stripped.
export const TRACES = [
  { id: 't1', timestamp: iso(40_000), duration: 30_000_000_000, user_name: 'alice', request: { method: 'POST', path: '/v1/chat/completions' }, response: { status: 503 }, error: 'qwen3-8b-instruct: backend did not answer within 30 s' },
  { id: 't2', timestamp: iso(70_000), duration: 2_900_000_000, user_name: 'alice', request: { method: 'POST', path: '/v1/images/generations' }, response: { status: 500 }, error: 'load model: out of memory (needs 12.4 GB, 5.6 GB free)' },
  { id: 't3', timestamp: iso(100_000), duration: 38_000_000, user_name: 'bob', request: { method: 'POST', path: '/v1/embeddings' }, response: { status: 200 } },
  { id: 't4', timestamp: iso(130_000), duration: 3_200_000_000, user_name: 'bob', request: { method: 'POST', path: '/v1/audio/transcriptions' }, response: { status: 200 } },
  { id: 't5', timestamp: iso(160_000), duration: 6_000_000, user_name: 'alice', request: { method: 'POST', path: '/v1/chat/completions' }, response: { status: 429 } },
  { id: 't6', timestamp: iso(200_000), duration: 1_800_000_000, user_name: 'alice', request: { method: 'POST', path: '/v1/chat/completions' }, response: { status: 200 } },
  { id: 't7', timestamp: iso(240_000), duration: 5_100_000_000, user_name: 'carol', request: { method: 'POST', path: '/v1/chat/completions' }, response: { status: 200 } },
  { id: 't8', timestamp: iso(280_000), duration: 12_000_000, user_name: 'bob', request: { method: 'GET', path: '/v1/models' }, response: { status: 200 } },
]

export const TRACE_DETAIL = {
  t2: {
    ...TRACES[1],
    client_ip: '192.0.2.41',
    user_agent: 'Mozilla/5.0 (X11; Linux x86_64)',
    request: { method: 'POST', path: '/v1/images/generations', body: b64({ model: 'flux-dev', prompt: 'a lighthouse at dusk, oil painting', size: '1024x1024', n: 1 }) },
    response: { status: 500, body: b64({ error: { message: 'load model: out of memory (needs 12.4 GB, 5.6 GB free)', code: 500 } }) },
  },
  t6: {
    ...TRACES[5],
    client_ip: '192.0.2.18',
    user_agent: 'curl/8.4.0',
    request: { method: 'POST', path: '/v1/chat/completions', body: b64({ model: 'qwen3-8b-instruct', messages: [{ role: 'user', content: 'hello from the request' }] }) },
    response: { status: 200, body: b64({ choices: [{ message: { role: 'assistant', content: 'hello from the response' } }] }) },
  },
  t5: {
    ...TRACES[4],
    client_ip: '192.0.2.18',
    request: { method: 'POST', path: '/v1/chat/completions', body: b64({ model: 'qwen3-8b-instruct' }) },
    response: { status: 429, body: b64({ error: 'rate limit: 60 requests per minute' }) },
  },
}

export function backendTraces() {
  const t = Date.parse(TRACES[1].timestamp)
  return [
    { id: 'b1', type: 'model_load', timestamp: new Date(t + 6).toISOString(), model_name: 'flux-dev', backend: 'diffusers', summary: 'load model', duration: 2_890_000_000, error: 'out of memory (needs 12.4 GB, 5.6 GB free)' },
    { id: 'b2', type: 'llm', timestamp: iso(600_000), model_name: 'qwen3-8b-instruct', backend: 'llama-cpp', summary: 'generated a reply', duration: 1_700_000_000 },
    { id: 'b3', type: 'llm', timestamp: iso(700_000), model_name: 'qwen3-8b-instruct', backend: 'llama-cpp', summary: 'generated a reply', duration: 4_900_000_000 },
    { id: 'b4', type: 'embedding', timestamp: iso(101_000), model_name: 'bge-m3', backend: 'llama-cpp', summary: 'embedded 12 inputs', duration: 34_000_000 },
  ]
}

export const SETTINGS_ON = {
  enable_tracing: true, enable_backend_logging: true, tracing_max_items: 500, tracing_max_body_bytes: 65536,
}
export const SETTINGS_OFF = { ...SETTINGS_ON, enable_tracing: false, enable_backend_logging: false }

export const METRICS_TEXT = [
  '# HELP api_call api calls',
  '# TYPE api_call histogram',
  'api_call_bucket{method="POST",path="/v1/chat/completions",le="1"} 12',
  '# HELP localai_tokens_total Cumulative tokens accounted',
  '# TYPE localai_tokens_total counter',
  'localai_tokens_total{user="alice",served_model="qwen3-8b-instruct",kind="prompt"} 4120',
  '# TYPE go_goroutines gauge',
  'go_goroutines 41',
  '# TYPE process_cpu_seconds_total counter',
  'process_cpu_seconds_total 12.5',
].join('\n') + '\n'

// Route every call the Traffic pages make, on a no-auth single-user server.
//
//   scenario    'healthy' | 'errors' | 'empty'
//   tracing     whether /api/settings says tracing is on
//   metrics     'ok' | 'off' | 'denied'
//   distributed features.distributed and the node list
//   delayMs     holds every answer, for a loading state
export async function mockTraffic(page, {
  scenario = 'healthy',
  tracing = true,
  metrics = 'ok',
  distributed = false,
  nodes = NODES,
  resources = gpuHost(),
  delayMs = 0,
  traces = TRACES,
  backend = backendTraces(),
} = {}) {
  const empty = scenario === 'empty'
  const summary = empty
    ? traceSummary({ total: 0, errors: 0, p95: 0 })
    : traceSummary(scenario === 'errors' ? { total: 723, errors: 37, p95: 1400 } : { total: 723, errors: 7, p95: 1400 })
  await mockOperate(page, { distributed, nodes, resources, traces: summary, delayMs })
  const answer = async (route, body, status = 200) => {
    if (delayMs) await new Promise(resolve => setTimeout(resolve, delayMs))
    return route.fulfill({ status, json: body })
  }
  await page.route('**/api/auth/status', route => route.fulfill({ json: { authEnabled: false, staticApiKeyRequired: false, providers: [] } }))
  const period = route => new URL(route.request().url()).searchParams.get('period') || 'month'
  const usage = route => answer(route, empty ? { usage: [], totals: {} } : usagePayload(period(route)))
  await page.route('**/api/usage?*', usage)
  await page.route('**/api/usage/all?*', usage)
  await page.route('**/api/traces/summary*', route => {
    const hours = Number(new URL(route.request().url()).searchParams.get('hours')) || 24
    return answer(route, { ...summary, window_hours: hours })
  })
  await page.route('**/api/traces?*', route => route.fulfill({ json: empty ? [] : traces, headers: { 'X-Total-Count': String(empty ? 0 : traces.length) } }))
  await page.route('**/api/traces/*', route => {
    const url = new URL(route.request().url())
    if (url.pathname.endsWith('/summary')) return route.fallback()
    const id = decodeURIComponent(url.pathname.split('/').pop())
    const detail = TRACE_DETAIL[id] || traces.find(t => t.id === id)
    return detail ? answer(route, detail) : answer(route, { error: { message: 'trace not found', code: 404 } }, 404)
  })
  await page.route('**/api/backend-traces?*', route => route.fulfill({ json: empty ? [] : backend, headers: { 'X-Total-Count': String(empty ? 0 : backend.length) } }))
  await page.route('**/api/settings', route => {
    if (route.request().method() === 'POST') return route.fulfill({ json: { success: true } })
    return answer(route, tracing ? SETTINGS_ON : SETTINGS_OFF)
  })
  await page.route('**/metrics', route => {
    if (new URL(route.request().url()).pathname !== '/metrics') return route.fallback()
    if (metrics === 'off') return route.fulfill({ status: 404, body: 'not found' })
    if (metrics === 'denied') return route.fulfill({ status: 403, body: 'forbidden' })
    return route.fulfill({ status: 200, contentType: 'text/plain', body: METRICS_TEXT })
  })
}
