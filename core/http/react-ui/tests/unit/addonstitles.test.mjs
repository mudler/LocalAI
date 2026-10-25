import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync, readdirSync, existsSync, statSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

// One rule for the title of an add-on page: it is the host's page title (the
// `page-title` class, drawn by PageHeader or by an h1 that carries the class),
// at the host's size. An add-on style sheet does not resize it, and a card or a
// section inside the page is an h2, never a second h1.
const here = dirname(fileURLToPath(import.meta.url))
const addons = join(here, '../../../../../addons')
const ids = readdirSync(addons).filter(n => existsSync(join(addons, n, 'ui/src')))

function files(dir, ext) {
  const out = []
  for (const name of readdirSync(dir)) {
    if (name === 'node_modules' || name === 'dist') continue
    const p = join(dir, name)
    if (statSync(p).isDirectory()) out.push(...files(p, ext))
    else if (ext.test(name)) out.push(p)
  }
  return out
}

test('addon titles: every h1 an add-on draws itself carries the host page-title class', () => {
  const bad = []
  for (const id of ids) {
    for (const f of files(join(addons, id, 'ui/src'), /\.jsx$/)) {
      for (const m of readFileSync(f, 'utf8').matchAll(/<h1\b([^>]*)>/g)) {
        if (!/page-title/.test(m[1])) bad.push(`${id}: ${f.split('/ui/src/')[1]}: <h1${m[1]}>`)
      }
    }
  }
  assert.deepEqual(bad, [])
})

test('addon titles: no add-on style sheet sets the size of its page title', () => {
  const bad = []
  for (const id of ids) {
    for (const f of files(join(addons, id, 'ui/src'), /\.css$/)) {
      for (const m of readFileSync(f, 'utf8').matchAll(/([^{}]*(?:title|__title|-hd__title)[^{}]*)\{([^}]*)\}/g)) {
        const selector = m[1].trim()
        // Only the rules for the page's own title: the three that used to resize it.
        if (/^(\.tbl-title|\.talk-hd__title|\.ws-title\b.*)$/.test(selector) && /font-size|font:/.test(m[2]) && selector !== '.ws-title') {
          bad.push(`${id}: ${selector}`)
        }
      }
    }
  }
  assert.deepEqual(bad, [])
})

test('addon titles: the Studio card heading is a section, and Studio draws the one page title', () => {
  const workspace = readFileSync(join(addons, 'studio/ui/src/components/studio/Workspace.jsx'), 'utf8')
  assert.match(workspace, /<h2 className="ws-title">/)
  const studio = readFileSync(join(addons, 'studio/ui/src/pages/Studio.jsx'), 'utf8')
  assert.match(studio, /<PageHeader/)
})

test('addon titles: the experimental badge of Fine-tune and Quantize sits outside the h1', () => {
  for (const [id, file] of [['fine-tuning', 'FineTune.jsx'], ['quantization', 'Quantize.jsx']]) {
    const src = readFileSync(join(addons, id, 'ui/src', file), 'utf8')
    const title = /title=\{([^\n]*)\}\n\s*supporting=\{([^\n]*)\}/.exec(src)
    assert.ok(title, `${id}: header not found`)
    assert.doesNotMatch(title[1], /experimental/)
    assert.match(title[2], /landing\.experimental/)
  }
})
