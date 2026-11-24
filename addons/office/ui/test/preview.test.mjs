import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { load, here, React, renderToString } from './load.mjs'

const model = async () => (await import('../src/lib/model.js')).modelOf

test('preview: a Word answer becomes paragraphs and tables', async () => {
  const modelOf = await model()
  const m = modelOf('docx', { ok: true, paragraphs: [{ index: 0, style: 'Heading1', text: 'Plan' }, { index: 1, text: 'Ship it.' }], tables: [[['a', 'b'], ['1', '2']]], truncated: false })
  assert.equal(m.error, '')
  assert.deepEqual(m.blocks.map(b => b.type), ['heading', 'paragraph', 'table'])
  assert.equal(m.blocks[0].text, 'Plan')
  assert.deepEqual(m.blocks[2].rows, [['a', 'b'], ['1', '2']])
  assert.equal(m.truncated, false)
})

test('preview: an Excel answer becomes one table for each sheet', async () => {
  const modelOf = await model()
  const m = modelOf('xlsx', { ok: true, sheets: [{ name: 'Sales', rows: [['item', 'qty'], ['apples', '3']], rowCount: 2, colCount: 2 }], truncated: true })
  assert.deepEqual(m.blocks.map(b => b.type), ['heading', 'table'])
  assert.equal(m.blocks[0].text, 'Sales')
  assert.equal(m.truncated, true)
})

test('preview: a PowerPoint answer becomes a slide outline', async () => {
  const modelOf = await model()
  const m = modelOf('pptx', { ok: true, slides: [{ index: 1, title: 'Plan', shapes: [{ id: 2, kind: 'body', text: ['ship it', 'test it'] }] }] })
  assert.deepEqual(m.blocks.map(b => b.type), ['slide'])
  assert.equal(m.blocks[0].title, 'Plan')
  assert.deepEqual(m.blocks[0].lines, ['ship it', 'test it'])
})

test('preview: a refusal or a bad answer gives an error and no blocks', async () => {
  const modelOf = await model()
  const refused = modelOf('docx', { ok: false, error: 'the file is not a valid Word document' })
  assert.equal(refused.error, 'the file is not a valid Word document')
  assert.deepEqual(refused.blocks, [])
  for (const bad of [null, undefined, 'text', 42, { ok: true }, { ok: true, paragraphs: 'x' }]) {
    const m = modelOf('docx', bad)
    assert.notEqual(m.error, '', JSON.stringify(bad))
    assert.deepEqual(m.blocks, [])
  }
  assert.notEqual(modelOf('rtf', { ok: true }).error, '')
})

test('preview: text in an answer is data, never markup', async () => {
  const modelOf = await model()
  const m = modelOf('docx', { ok: true, paragraphs: [{ index: 0, text: '<img src=x onerror=alert(1)>' }] })
  const { mod, sdk } = await load()
  const addon = await mod.createAddon(sdk)
  const View = addon.components.OfficePreviewView
  const html = renderToString(React.createElement(View, { model: m }))
  assert.ok(!html.includes('<img'), html)
  assert.match(html, /&lt;img/)
})

test('preview: the component the manifest declares is exported and starts by loading', async () => {
  const { mod, sdk } = await load()
  const addon = await mod.createAddon(sdk)
  assert.equal(typeof addon.components.OfficePreview, 'function')
  const html = renderToString(React.createElement(addon.components.OfficePreview, { path: '/a.docx', name: 'a.docx', extension: 'docx' }))
  assert.match(html, /data-testid="office-preview-loading"/)
  const m = JSON.parse(readFileSync(join(here, '../../manifest.json'), 'utf8'))
  const slot = m.capabilities.ui.slots.find(s => s.slot === 'files.preview')
  assert.equal(slot.component, 'OfficePreview')
  assert.deepEqual(slot.extensions, ['docx', 'xlsx', 'pptx'])
  assert.ok(m.capabilities.tools.includes('read-docx') && m.capabilities.tools.includes('read-xlsx') && m.capabilities.tools.includes('read-pptx'))
})
