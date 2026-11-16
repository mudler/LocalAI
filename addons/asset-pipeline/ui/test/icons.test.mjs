import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createIcon } from '../src/icons.js'

test('the add-on ships its own sidebar PNG instead of the blank fallback tile', () => {
 const png = readFileSync(new URL('../icon.png', import.meta.url))
 assert.deepEqual([...png.subarray(0, 8)], [137, 80, 78, 71, 13, 10, 26, 10])
 assert.equal(png.readUInt32BE(16), 128)
 assert.equal(png.readUInt32BE(20), 128)
 assert.ok(png.length < 256 * 1024)
})

test('stage and action icons are decorative and do not replace accessible labels', () => {
 const Icon = createIcon({ createElement: (tag, props, child) => ({ tag, props, child }) })
 for (const name of ['source', 'review', 'image', 'extract', 'mesh', 'run', 'step', 'retry', 'rewind', 'download', 'history', 'check']) {
   const icon = Icon({ name })
   assert.equal(icon.tag, 'svg')
   assert.equal(icon.props['aria-hidden'], true)
   assert.equal(icon.props.focusable, 'false')
   assert.ok(icon.child.props.d)
 }
})
