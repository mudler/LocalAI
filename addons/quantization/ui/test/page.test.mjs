import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'
import { load, draw, here, hostUi, React, renderToString } from './load.mjs'
import { createApi } from '../src/api.js'
import en from '../src/i18n/en.js'

const read = (p) => readFileSync(p, 'utf8')
const src = join(here, '../src')
const toolsUi = join(here, '../../../internal/tools-ui/src')
const files = (dir) => readdirSync(dir, { recursive: true }).filter(f => /\.(jsx?|mjs)$/.test(f)).map(f => join(dir, f))
const ownText = files(src).map(read).join('\n')
const sharedText = files(toolsUi).map(read).join('\n')

const flatten = (obj, prefix = '') => Object.entries(obj).flatMap(([k, v]) =>
  typeof v === 'string' ? [[`${prefix}${k}`, v]] : v && typeof v === 'object' && !Array.isArray(v) ? flatten(v, `${prefix}${k}.`) : [])

const job = (extra = {}) => ({ id: 'job-1234567890', model: 'org/base', backend: 'llama-cpp-quantization', quantization_type: 'q4_k_m', created_at: '2026-10-18T10:00:00Z', ...extra })
const noop = { onStop() {}, onReuse() {}, onTerminal() {}, onRefresh() {} }

test('page: the first paint has the core test ids of the form and the job list', async () => {
  const { mod, sdk } = await load()
  const html = draw(await mod.createAddon(sdk), { page: 'jobs', path: '' })
  for (const id of ['quantize-page', 'quantize-form', 'tool-steps', 'tool-checks', 'qz-bar', 'qz-start', 'qz-recipe', 'qz-jobs']) {
    assert.match(html, new RegExp(`data-testid="${id}"`), id)
  }
})

test('page: a viewer without quantization gets the off notice, no form and no core call', async () => {
  const { mod, sdk, calls } = await load({ viewer: { isAdmin: () => false, hasFeature: (f) => f !== 'quantization' } })
  const html = draw(await mod.createAddon(sdk), { page: 'jobs', path: '' })
  assert.match(html, /data-testid="tool-off"/)
  assert.match(html, /Your account cannot quantize/)
  assert.doesNotMatch(html, /quantize-form/)
  assert.deepEqual(calls, [])
})

test('page: a deep link names the model to quantize', async () => {
  const { mod, sdk } = await load()
  const addon = await mod.createAddon(sdk)
  const html = draw(addon, { page: 'jobs', path: 'org' })
  assert.match(html, /value="org"/)
})

test('page: the API helper uses the core quantization paths and bodies', async () => {
  const seen = []
  const ctx = { core: { fetch: async (p, i) => { seen.push([p, i]); return { ok: true } } }, json: async (p, i) => { seen.push([p, i]); return {} } }
  const api = createApi(ctx)
  await api.quantize.listBackends()
  await api.quantize.listJobs()
  await api.quantize.getJob('a b')
  await api.quantize.startJob({ model: 'm' })
  await api.quantize.stopJob('j1')
  await api.quantize.deleteJob('j1')
  await api.quantize.importModel('j1', { name: 'n' })
  await api.resources.get()
  const paths = seen.map(s => `${s[1]?.method || 'GET'} ${s[0]}`)
  assert.deepEqual(paths, [
    'GET /api/quantization/backends', 'GET /api/quantization/jobs', 'GET /api/quantization/jobs/a%20b',
    'POST /api/quantization/jobs', 'POST /api/quantization/jobs/j1/stop', 'DELETE /api/quantization/jobs/j1',
    'POST /api/quantization/jobs/j1/import', 'GET /api/resources',
  ])
  assert.equal(seen[3][1].body, JSON.stringify({ model: 'm' }))
  assert.equal(seen[3][1].headers['Content-Type'], 'application/json')
  assert.equal(seen[6][1].body, JSON.stringify({ name: 'n' }))
})

test('page: a failed call throws the server message', async () => {
  const { mod, sdk } = await load({ fetch: async () => ({ ok: false, status: 413, json: async () => ({ error: { message: 'too big' } }) }) })
  const { api } = await mod.__partsFor(sdk)
  await assert.rejects(api.quantize.listJobs(), /too big/)
})

test('page: the progress stream is read through core.fetch with the abort signal', async () => {
  const seen = []
  const ctx = { core: { fetch: async (p, i) => { seen.push([p, i]); return { ok: true } } } }
  const controller = new AbortController()
  await createApi(ctx).quantize.progress('j 1', controller.signal)
  assert.equal(seen[0][0], '/api/quantization/jobs/j%201/progress')
  assert.equal(seen[0][1].signal, controller.signal)
})

test('page: the download link carries the base path, and so do the chat and models links', async () => {
  const { mod, sdk } = await load()
  globalThis.document = { querySelector: () => ({ getAttribute: () => 'http://localhost/base/' }) }
  let ResultPanel
  try { ResultPanel = (await mod.__partsFor(sdk)).parts.ResultPanel } finally { delete globalThis.document }
  const html = renderToString(React.createElement(ResultPanel, { job: job({ status: 'completed', import_status: 'completed', import_model_name: 'my-model' }), onRefresh: async () => {} }))
  assert.match(html, /data-testid="quantize-next"/)
  assert.match(html, /href="\/base\/api\/quantization\/jobs\/job-1234567890\/download"/)
  assert.match(html, /href="\/base\/app\/chat\/my-model"/)
  assert.match(html, /href="\/base\/app\/models\?view=installed"/)
})

test('result: a finished job that is not imported offers the name field, the import and the download', async () => {
  const { mod, sdk } = await load()
  const { ResultPanel } = (await mod.__partsFor(sdk)).parts
  const html = renderToString(React.createElement(ResultPanel, { job: job({ status: 'completed', output_file: 'model-q4.gguf' }), onRefresh: async () => {} }))
  assert.match(html, /data-testid="quantize-output"/)
  assert.match(html, /data-testid="quantize-import"/)
  assert.match(html, /id="qz-import-name"/)
  assert.match(html, /model-q4\.gguf/)
  assert.doesNotMatch(html, /quantize-next/)
  const running = renderToString(React.createElement(ResultPanel, { job: job({ status: 'quantizing' }), onRefresh: async () => {} }))
  assert.equal(running, '')
})

test('progress: a working job whose percent reads 0 shows an unknown progress, not 0%', async () => {
  const { mod, sdk } = await load()
  const { JobView } = (await mod.__partsFor(sdk)).parts
  const html = renderToString(React.createElement(JobView, { job: job({ status: 'quantizing' }), seed: { latest: { status: 'quantizing', progress_percent: 0 } }, ...noop }))
  assert.match(html, /data-testid="job-view"/)
  assert.match(html, /data-indeterminate/)
  assert.doesNotMatch(html, /bt-job__percent">0/)
  assert.match(html, /Progress not reported yet/)
})

test('progress: a working job with a percent shows it and the stop control', async () => {
  const { mod, sdk } = await load()
  const { JobView } = (await mod.__partsFor(sdk)).parts
  const html = renderToString(React.createElement(JobView, { job: job({ status: 'quantizing' }), seed: { latest: { status: 'quantizing', progress_percent: 40 } }, ...noop }))
  assert.match(html, /bt-job__percent">40<small>/)
  assert.match(html, /aria-valuenow="40"/)
  assert.doesNotMatch(html, /data-indeterminate/)
  assert.match(html, /data-testid="job-stop"/)
})

test('progress: a failed job shows its message and the retry control', async () => {
  const { mod, sdk } = await load()
  const { JobView } = (await mod.__partsFor(sdk)).parts
  const html = renderToString(React.createElement(JobView, { job: job({ status: 'failed', message: 'unsupported architecture' }), seed: { latest: null }, ...noop }))
  assert.match(html, /data-testid="job-failed-message"[^>]*>unsupported architecture/)
  assert.match(html, /data-testid="job-retry"/)
  assert.match(html, /data-testid="job-try"/)
})

test('progress: a completed job reads 100 and says it finished', async () => {
  const { mod, sdk } = await load()
  const { JobView } = (await mod.__partsFor(sdk)).parts
  const html = renderToString(React.createElement(JobView, { job: job({ status: 'completed' }), seed: { latest: null }, ...noop }))
  assert.match(html, /bt-job__percent">100<small>/)
  assert.match(html, /data-testid="job-finished"/)
})

test('dialogs: the delete dialog is a host layer with the core test ids', async () => {
  const { mod, sdk } = await load()
  const { DeleteDialogBody } = (await mod.__partsFor(sdk)).parts
  const del = renderToString(React.createElement(DeleteDialogBody, { onConfirm() {}, close() {} }))
  for (const id of ['delete-dialog', 'delete-confirm']) assert.match(del, new RegExp(`data-testid="${id}"`), id)
})

test('dialogs: confirming the delete closes the layer and then deletes', async () => {
  const { mod, sdk } = await load()
  const { DeleteDialogBody } = (await mod.__partsFor(sdk)).parts
  const out = []
  const tree = DeleteDialogBody({ onConfirm: () => out.push('confirm'), close: () => out.push('close') })
  const found = []
  const walk = (n) => { if (!n || typeof n !== 'object') return; if (n.props?.['data-testid'] === 'delete-confirm') found.push(n); [].concat(n.props?.children || []).forEach(walk) }
  walk(tree)
  found[0].props.onClick()
  assert.deepEqual(out, ['close', 'confirm'])
})

test('plural: the job count picks the singular and the plural key', async () => {
  const { mod, sdk } = await load()
  const { ctx } = await mod.__partsFor(sdk)
  assert.equal(ctx.plural('jobs.count', 1), '1 kept on this server')
  assert.equal(ctx.plural('jobs.count', 3), '3 kept on this server')
})

test('parity: every data-testid of the core page is in the add-on or the shared views', () => {
  const core = read(join(hostUi, 'src/pages/Quantize.jsx'))
  const ids = new Set([...core.matchAll(/(?:data-testid|testId)="([^"]+)"/g)].map(m => m[1]))
  assert.ok(ids.size >= 10, `found only ${ids.size} test ids in the core source`)
  const mine = ownText + sharedText
  for (const id of ids) assert.ok(mine.includes(`"${id}"`) || mine.includes(`'${id}'`), `missing data-testid ${id}`)
})

test('icons: every icon name the page draws is one the SDK knows', async () => {
  const { ICON_NAMES } = await import(pathToFileURL(join(hostUi, 'src/addons/sdk.js')).href)
  const known = new Set(ICON_NAMES)
  const used = new Set([...ownText.matchAll(/<Icon name=(?:"([a-z-]+)"|\{[^}]*\})/g)].map(m => m[1]).filter(Boolean))
  for (const m of ownText.matchAll(/'(chevron-down|chevron-right)'/g)) used.add(m[1])
  assert.ok(used.size >= 8, `found only ${used.size} icons`)
  for (const name of used) assert.ok(known.has(name), `unknown icon ${name}`)
})

test('i18n: every literal key the add-on and the shared views read is in en.js', () => {
  const keys = new Set()
  for (const text of [ownText, sharedText]) for (const m of text.matchAll(/\bt\(\s*'([A-Za-z0-9_.]+)'/g)) keys.add(m[1])
  assert.ok(keys.size > 60, `found only ${keys.size} literal keys`)
  const missing = [...keys].filter(k => typeof en[k] !== 'string')
  assert.deepEqual(missing, [])
})

test('i18n: en.js repeats the host strings and adds only the keys the add-on owns', () => {
  const tools = Object.fromEntries(flatten(JSON.parse(read(join(hostUi, 'public/locales/en/tools.json')))))
  const own = []
  for (const [k, v] of Object.entries(en)) {
    if (k in tools) assert.equal(v, tools[k], k)
    else own.push(k)
  }
  assert.deepEqual(own.sort(), ['run.unknownPercent'])
  assert.equal(Object.keys(en).some(k => k.startsWith('rewards.') || k.startsWith('checkpoints.') || k.startsWith('chart.')), false)
})

test('style: the stylesheet starts with the shared tools rules unchanged', () => {
  assert.ok(read(join(src, 'style.css')).startsWith(read(join(toolsUi, 'tools.css'))))
})

test('source: no file imports a core UI file or the router', () => {
  for (const f of files(src)) {
    const text = read(f)
    assert.doesNotMatch(text, /from\s+['"][^'"]*core\/http\/react-ui/, f)
    assert.doesNotMatch(text, /from\s+['"](react|react-dom|react-router-dom|react-i18next)['"]/, f)
    assert.doesNotMatch(text, /\bEventSource\b/, f)
  }
})
