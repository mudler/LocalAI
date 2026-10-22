import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'
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

const job = (extra = {}) => ({ id: 'job-1234567890', model: 'org/base', backend: 'trl', training_type: 'lora', training_method: 'sft', created_at: '2026-10-18T10:00:00Z', ...extra })

test('page: the first paint has the core test ids of the form and the job list', async () => {
  const { mod, sdk } = await load()
  const html = draw(await mod.createAddon(sdk), { page: 'jobs', path: '' })
  for (const id of ['fine-tune-page', 'fine-tune-form', 'tool-steps', 'tool-checks', 'ft-bar', 'ft-start', 'ft-recipe', 'ft-jobs', 'ft-more-toggle']) {
    if (id === 'ft-more-toggle') continue // inside the closed disclosure
    assert.match(html, new RegExp(`data-testid="${id}"`), id)
  }
})

test('page: a viewer without fine_tuning gets the off notice, no form and no core call', async () => {
  const { mod, sdk, calls } = await load({ viewer: { isAdmin: () => false, hasFeature: (f) => f !== 'fine_tuning' } })
  const html = draw(await mod.createAddon(sdk), { page: 'jobs', path: '' })
  assert.match(html, /data-testid="tool-off"/)
  assert.match(html, /Your account cannot fine-tune/)
  assert.doesNotMatch(html, /fine-tune-form/)
  assert.deepEqual(calls, [])
})

test('page: the API helper uses the core fine-tuning paths and bodies', async () => {
  const seen = []
  const reply = { ok: true, json: async () => ({}) }
  const ctx = {
    core: { fetch: async (p, i) => { seen.push([p, i]); return reply } },
    json: async (p, i) => { seen.push([p, i]); return {} },
  }
  const api = createApi(ctx)
  await api.fineTune.listBackends()
  await api.fineTune.listJobs()
  await api.fineTune.getJob('a b')
  await api.fineTune.startJob({ model: 'm' })
  await api.fineTune.stopJob('j1', true)
  await api.fineTune.stopJob('j1', false)
  await api.fineTune.deleteJob('j1')
  await api.fineTune.listCheckpoints('j1')
  await api.fineTune.exportModel('j1', { name: 'n' })
  await api.resources.get()
  const paths = seen.map(s => `${s[1]?.method || 'GET'} ${s[0]}`)
  assert.deepEqual(paths, [
    'GET /api/fine-tuning/backends', 'GET /api/fine-tuning/jobs', 'GET /api/fine-tuning/jobs/a%20b',
    'POST /api/fine-tuning/jobs', 'POST /api/fine-tuning/jobs/j1/stop?save_checkpoint=true',
    'POST /api/fine-tuning/jobs/j1/stop?save_checkpoint=false', 'DELETE /api/fine-tuning/jobs/j1',
    'GET /api/fine-tuning/jobs/j1/checkpoints', 'POST /api/fine-tuning/jobs/j1/export', 'GET /api/resources',
  ])
  assert.equal(seen[3][1].body, JSON.stringify({ model: 'm' }))
  assert.equal(seen[3][1].headers['Content-Type'], 'application/json')
})

test('page: the dataset upload is a FormData body with no Content-Type header', async () => {
  const seen = []
  const ctx = { json: async (p, i) => { seen.push([p, i]); return { path: '/x/y.jsonl' } } }
  const out = await createApi(ctx).fineTune.uploadDataset(new Blob(['{}'], { type: 'application/json' }))
  assert.equal(out.path, '/x/y.jsonl')
  const [path, init] = seen[0]
  assert.equal(path, '/api/fine-tuning/datasets')
  assert.equal(init.method, 'POST')
  assert.ok(init.body instanceof FormData)
  assert.equal(init.headers, undefined)
})

test('page: a failed upload or call throws the server message', async () => {
  const { mod, sdk } = await load({ fetch: async () => ({ ok: false, status: 413, json: async () => ({ error: { message: 'too big' } }) }) })
  const { api } = await mod.__partsFor(sdk)
  await assert.rejects(api.fineTune.uploadDataset(new Blob(['x'])), /too big/)
  await assert.rejects(api.fineTune.listJobs(), /too big/)
})

test('page: the progress stream is read through core.fetch with the abort signal', async () => {
  const seen = []
  const ctx = { core: { fetch: async (p, i) => { seen.push([p, i]); return { ok: true } } } }
  const controller = new AbortController()
  await createApi(ctx).fineTune.progress('j 1', controller.signal)
  assert.equal(seen[0][0], '/api/fine-tuning/jobs/j%201/progress')
  assert.equal(seen[0][1].signal, controller.signal)
})

test('page: the download link carries the base path', async () => {
  const { mod, sdk } = await load()
  // The base path is read from the <base href> element, as the host reads it.
  globalThis.document = { querySelector: () => ({ getAttribute: () => 'http://localhost/base/' }) }
  let ExportPanel
  try { ExportPanel = (await mod.__partsFor(sdk)).parts.ExportPanel } finally { delete globalThis.document }
  const html = renderToString(React.createElement(ExportPanel, { job: job({ status: 'completed', export_status: 'completed', export_model_name: 'my-model' }) }))
  assert.match(html, /href="\/base\/api\/fine-tuning\/jobs\/job-1234567890\/download"/)
  assert.match(html, /data-testid="export-next"/)
  assert.match(html, /href="\/base\/app\/chat\/my-model"/)
})

test('progress: a running job whose percent reads 0 shows an unknown progress, not 0%', async () => {
  const { mod, sdk } = await load()
  const { JobView } = (await mod.__partsFor(sdk)).parts
  const html = renderToString(React.createElement(JobView, {
    job: job({ status: 'training' }),
    seed: { latest: { status: 'training', progress_percent: 0, current_step: 0, total_steps: 100 } },
    onStop() {}, onReuse() {}, onResume() {}, onTerminal() {}, exportCheckpoint() {},
  }))
  assert.match(html, /data-testid="job-view"/)
  assert.match(html, /data-indeterminate/)
  assert.doesNotMatch(html, /bt-job__percent">0/)
  assert.match(html, /Progress not reported yet/)
})

test('progress: a running job with a percent shows it', async () => {
  const { mod, sdk } = await load()
  const { JobView } = (await mod.__partsFor(sdk)).parts
  const html = renderToString(React.createElement(JobView, {
    job: job({ status: 'training' }),
    seed: { latest: { status: 'training', progress_percent: 40, current_step: 40, total_steps: 100 } },
    onStop() {}, onReuse() {}, onResume() {}, onTerminal() {}, exportCheckpoint() {},
  }))
  assert.match(html, /bt-job__percent">40<small>/)
  assert.match(html, /aria-valuenow="40"/)
  assert.doesNotMatch(html, /data-indeterminate/)
  assert.match(html, /data-testid="job-stop"/)
})

test('progress: a failed job shows its message and the retry control', async () => {
  const { mod, sdk } = await load()
  const { JobView } = (await mod.__partsFor(sdk)).parts
  const html = renderToString(React.createElement(JobView, {
    job: job({ status: 'failed', message: 'CUDA out of memory' }),
    seed: { latest: null }, onStop() {}, onReuse() {}, onResume() {}, onTerminal() {}, exportCheckpoint() {},
  }))
  assert.match(html, /data-testid="job-failed-message"[^>]*>CUDA out of memory/)
  assert.match(html, /data-testid="job-retry"/)
  assert.match(html, /data-testid="job-try"/)
})

test('export: the export panel and the checkpoint export buttons are for an administrator', async () => {
  const done = job({ status: 'completed' })
  const seed = { checkpoints: [{ path: '/srv/c1', step: 10, loss: 0.5, epoch: 1 }] }
  const admin = await load({ viewer: { isAdmin: () => true, hasFeature: () => true } })
  const a = (await admin.mod.__partsFor(admin.sdk)).parts
  const adminPanel = renderToString(React.createElement(a.ExportPanel, { job: done, seed }))
  assert.match(adminPanel, /id="ft-export-format"/)
  assert.doesNotMatch(adminPanel, /data-testid="export-admin-only"/)
  const adminJob = renderToString(React.createElement(a.JobView, { job: done, seed, onStop() {}, onReuse() {}, onResume() {}, onTerminal() {}, exportCheckpoint() {} }))
  assert.match(adminJob, /data-testid="job-checkpoints"/)
  assert.match(adminJob, /data-icon="save"/)

  const member = await load({ viewer: { isAdmin: () => false, hasFeature: () => true } })
  const m = (await member.mod.__partsFor(member.sdk)).parts
  const memberPanel = renderToString(React.createElement(m.ExportPanel, { job: done, seed }))
  assert.match(memberPanel, /data-testid="export-admin-only"/)
  assert.match(memberPanel, /Exporting a model is for administrators/)
  assert.doesNotMatch(memberPanel, /ft-export-format/)
  const memberJob = renderToString(React.createElement(m.JobView, { job: done, seed, onStop() {}, onReuse() {}, onResume() {}, onTerminal() {}, exportCheckpoint() {} }))
  assert.match(memberJob, /data-testid="job-checkpoints"/)
  assert.doesNotMatch(memberJob, /data-icon="save"/)
})

test('dialogs: the stop and delete dialogs are host layers with the core test ids', async () => {
  const { mod, sdk } = await load()
  const { StopDialogBody, DeleteDialogBody } = (await mod.__partsFor(sdk)).parts
  const stop = renderToString(React.createElement(StopDialogBody, { onChoose() {}, close() {} }))
  for (const id of ['stop-dialog', 'stop-discard', 'stop-keep']) assert.match(stop, new RegExp(`data-testid="${id}"`), id)
  const del = renderToString(React.createElement(DeleteDialogBody, { onConfirm() {}, close() {} }))
  for (const id of ['delete-dialog', 'delete-confirm']) assert.match(del, new RegExp(`data-testid="${id}"`), id)
})

test('dialogs: choosing in the stop dialog stops with or without a checkpoint and closes the layer', async () => {
  const { mod, sdk } = await load()
  const { StopDialogBody } = (await mod.__partsFor(sdk)).parts
  const out = []
  const tree = StopDialogBody({ onChoose: (keep) => out.push(['choose', keep]), close: () => out.push(['close']) })
  const buttons = []
  const walk = (n) => { if (!n || typeof n !== 'object') return; if (n.props?.['data-testid']?.startsWith('stop-') && n.props.onClick) buttons.push(n); [].concat(n.props?.children || []).forEach(walk) }
  walk(tree)
  buttons.find(b => b.props['data-testid'] === 'stop-keep').props.onClick()
  buttons.find(b => b.props['data-testid'] === 'stop-discard').props.onClick()
  assert.deepEqual(out, [['close'], ['choose', true], ['close'], ['choose', false]])
})

test('chart: the loss chart draws a line from two steps and a waiting note before', async () => {
  const { mod, sdk } = await load()
  const { LossChart } = (await mod.__partsFor(sdk)).parts
  const some = renderToString(React.createElement(LossChart, { events: [{ current_step: 1, loss: 2 }, { current_step: 2, loss: 1.5 }], totalSteps: 10 }))
  assert.match(some, /data-testid="job-chart"/)
  assert.match(some, /<polyline class="bt-chart__line"/)
  const none = renderToString(React.createElement(LossChart, { events: [], totalSteps: 10 }))
  assert.match(none, /The chart draws once the job has reported two steps/)
})

test('plural: the job count picks the singular and the plural key', async () => {
  const { mod, sdk } = await load()
  const { ctx } = await mod.__partsFor(sdk)
  assert.equal(ctx.plural('jobs.count', 1), '1 kept on this server')
  assert.equal(ctx.plural('jobs.count', 3), '3 kept on this server')
})

test('parity: every data-testid of the core page and chart is in the add-on or the shared views', () => {
  const core = ['pages/FineTune.jsx', 'components/tools/LossChart.jsx'].map(f => read(join(hostUi, 'src', f))).join('\n')
  const ids = new Set([...core.matchAll(/(?:data-testid|testId)="([^"]+)"/g)].map(m => m[1]))
  assert.ok(ids.size >= 20, `found only ${ids.size} test ids in the core sources`)
  const mine = ownText + sharedText
  for (const id of ids) assert.ok(mine.includes(`"${id}"`) || mine.includes(`'${id}'`), `missing data-testid ${id}`)
})

test('i18n: every literal key the add-on and the shared views read is in en.js', () => {
  const keys = new Set()
  for (const text of [ownText, sharedText]) for (const m of text.matchAll(/\bt\(\s*'([A-Za-z0-9_.]+)'/g)) keys.add(m[1])
  assert.ok(keys.size > 100, `found only ${keys.size} literal keys`)
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
  assert.deepEqual(own.sort(), ['export.adminOnly.text', 'export.adminOnly.title', 'run.unknownPercent'])
  assert.equal(Object.keys(en).some(k => k.startsWith('quantize.')), false)
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
