// The UI kit ratchet (scripts/ui-kit-gate.mjs): what each rule finds, what it
// leaves alone, and how a run compares with the baseline.
import test from 'node:test'
import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { scanFile, countAll, compare, formatBaseline, parseBaseline } from '../../scripts/ui-kit-gate.mjs'

const root = join(dirname(fileURLToPath(import.meta.url)), '../..')
const rules = (path, src) => scanFile(path, src).map(v => v.rule)

const BAD_JSX = `
import Toggle from '../components/Toggle'
export default function Bad() {
  return (
    <div className="dk-sheet">
      <input type="checkbox" checked />
      <button className="btn btn-primary">Save</button>
    </div>
  )
}
`

const CLEAN_JSX = `
import { Button, Switch } from '../components/ui'
import SideSheet from '../components/SideSheet'
export default function Clean() {
  return (
    <SideSheet title="Edit">
      <Switch checked onChange={() => {}} label="On" />
      <Button variant="primary">Save</Button>
      <span className="dk-sheet-title my-btn dk-btn">text</span>
    </SideSheet>
  )
}
`

test('ui kit gate: a deliberately bad sample fails every JSX rule', () => {
  const found = new Set(rules('src/pages/Bad.jsx', BAD_JSX))
  assert.deepEqual([...found].sort(), ['btn', 'checkbox', 'sheet-jsx', 'toggle'])
})

test('ui kit gate: a clean sample passes', () => {
  assert.deepEqual(scanFile('src/pages/Clean.jsx', CLEAN_JSX), [])
})

test('ui kit gate: the kit, the vendored CSS and SideSheet are exempt', () => {
  assert.deepEqual(scanFile('src/components/ui/Switch.jsx', BAD_JSX), [])
  assert.deepEqual(scanFile('src/components/SideSheet.jsx', BAD_JSX), [])
  assert.deepEqual(scanFile('src/vendor/ui-kit/components.css', '.dk-sheet { width: 1px }'), [])
})

test('ui kit gate: checkbox matches both quotes, braces and a multi-line tag', () => {
  assert.deepEqual(rules('src/a.jsx', `<input type='checkbox' />`), ['checkbox'])
  assert.deepEqual(rules('src/a.jsx', `<input type={'checkbox'} />`), ['checkbox'])
  assert.deepEqual(rules('src/a.jsx', `<input\n  id="x"\n  type="checkbox"\n/>`), ['checkbox'])
  assert.deepEqual(rules('src/a.jsx', `<input type="text" /><input type="radio" />`), [])
  // The kit checkbox carries dk-check, in any attribute order and around a handler.
  assert.deepEqual(rules('src/a.jsx', `<input type="checkbox" className="dk-check" />`), [])
  assert.deepEqual(rules('src/a.jsx', `<input className={x} onChange={e => set(e.target.checked)} type="checkbox" className="dk-check" />`), [])
  assert.deepEqual(rules('src/a.jsx', `<input onChange={e => set(1)} type="checkbox" />`), ['checkbox'])
  assert.deepEqual(rules('src/a.jsx', `<input type="checkbox" className="ws-check" />`), ['checkbox'])
})

test('ui kit gate: toggle follows the import path, not the word', () => {
  assert.deepEqual(rules('src/components/A.jsx', `import Toggle from './Toggle'`), ['toggle'])
  assert.deepEqual(rules('src/pages/A.jsx', `import Toggle from '../components/Toggle'`), ['toggle'])
  assert.deepEqual(rules('src/pages/A.jsx', `import Toggle from '../components/Toggle.jsx'`), ['toggle'])
  assert.deepEqual(rules('src/components/hub/A.jsx', `import Toggle from '../Toggle'`), ['toggle'])
  // A different Toggle with the same name is not the legacy one.
  assert.deepEqual(rules('src/components/hub/A.jsx', `import Toggle from './Toggle'`), [])
  assert.deepEqual(rules('src/pages/A.jsx', `import { Toggle } from 'some-package/Toggle'`), [])
})

test('ui kit gate: sheet-jsx ignores the other dk-sheet-* parts', () => {
  assert.deepEqual(rules('src/a.jsx', `<div className="dk-sheet-veil"><div className="dk-sheet dk-sheet--wide">`), ['sheet-jsx'])
  assert.deepEqual(rules('src/a.jsx', `<div className="dk-sheet-veil"><h2 className="dk-sheet-title">`), [])
})

test('ui kit gate: btn finds the legacy class and leaves dk-btn and data ids alone', () => {
  assert.deepEqual(rules('src/a.jsx', `<button className="btn btn-sm">x</button>`), ['btn'])
  assert.deepEqual(rules('src/a.jsx', 'className={`btn ${ok ? "btn-primary" : "btn-ghost"}`}'), ['btn'])
  assert.deepEqual(rules('src/a.jsx', `<button className="dk-btn dk-btn--sm ui-btn__label">x</button>`), [])
  assert.deepEqual(rules('src/a.jsx', `<button className="my-btn" data-testid="btn-save">x</button>`), [])
  assert.deepEqual(rules('src/a.jsx', `const btn = e.target.closest('.code-btn')`), [])
})

test('ui kit gate: width finds a size set on a sheet or pane, and only that', () => {
  assert.deepEqual(rules('src/a.css', `.lib-sheet { width: min(560px, 100%); }`), ['width'])
  assert.deepEqual(rules('src/a.css', `.x .dk-sheet--wide { max-width: 800px }`), ['width'])
  assert.deepEqual(rules('src/a.css', `.x.ui-pane { --pane-w: 500px; }`), ['width'])
  assert.deepEqual(rules('src/a.css', `@media (max-width: 640px) { .lib-sheet { min-width: 0; } }`), ['width'])
  // Other properties on a sheet, and a width on something else, are fine.
  assert.deepEqual(rules('src/a.css', `.lib-sheet .dk-sheet-head { padding: 4px; }`), [])
  assert.deepEqual(rules('src/a.css', `.ui-pane { height: auto; max-height: 90dvh; }`), [])
  assert.deepEqual(rules('src/a.css', `.lib-pane { min-width: 0; } .card { width: 10px }`), [])
  assert.deepEqual(rules('src/a.css', `/* .dk-sheet { width: 1px } */ .card { color: red }`), [])
})

test('ui kit gate: new violations fail, gone ones fail, equal passes', () => {
  const files = [['src/pages/Bad.jsx', BAD_JSX], ['src/pages/Clean.jsx', CLEAN_JSX]]
  const base = countAll(files)
  assert.deepEqual(compare(countAll(files), base), { added: [], stale: [] })

  const worse = countAll([...files, ['src/pages/New.jsx', '<input type="checkbox" />']])
  const r1 = compare(worse, base)
  assert.deepEqual(r1.added.map(a => a.key), ['checkbox src/pages/New.jsx'])
  assert.deepEqual(r1.stale, [])

  const twice = countAll([['src/pages/Bad.jsx', BAD_JSX + '<input type="checkbox" />'], files[1]])
  assert.deepEqual(compare(twice, base).added.map(a => a.key), ['checkbox src/pages/Bad.jsx'])

  const better = countAll([['src/pages/Bad.jsx', 'export default () => null'], files[1]])
  const r2 = compare(better, base)
  assert.deepEqual(r2.added, [])
  assert.equal(r2.stale.length, 4)
})

test('ui kit gate: the baseline text round-trips', () => {
  const counts = countAll([['src/pages/Bad.jsx', BAD_JSX]])
  const text = formatBaseline(counts)
  assert.deepEqual(parseBaseline(text), counts)
  assert.throws(() => parseBaseline('nonsense'))
})

test('ui kit gate: the real tree is at its baseline', () => {
  const out = execFileSync(process.execPath, ['scripts/ui-kit-gate.mjs'], { cwd: root, encoding: 'utf8' })
  assert.match(out, /at baseline/)
})
