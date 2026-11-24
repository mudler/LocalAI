import { test } from 'node:test'
import assert from 'node:assert/strict'
import { createAutoscroll } from '../src/autoscroll.js'

function fixture() {
  let top = 0, enabled = true, pending
  const pane = { scrollHeight: 1000, clientHeight: 200, get scrollTop() { return top }, set scrollTop(value) { top = Math.min(value, this.scrollHeight - this.clientHeight) } }
  const controller = createAutoscroll(pane, value => { enabled = value }, {
    setTimeout(fn, delay) { assert.equal(delay, 250); pending = fn; return 1 },
    clearTimeout() { pending = undefined },
  })
  controller.follow()
  return { pane, controller, enabled: () => enabled, settle() { const fn = pending; pending = undefined; fn?.() }, scroll(value) { pane.scrollTop = value; controller.scroll() } }
}

test('follows growing output and pauses when scrolled up', () => {
  const f = fixture()
  f.pane.scrollHeight += 100
  f.controller.follow()
  assert.equal(f.pane.scrollTop, 900)
  f.scroll(400)
  assert.equal(f.enabled(), false)
  f.pane.scrollHeight += 100
  f.controller.follow()
  assert.equal(f.pane.scrollTop, 400)
})

test('returning to bottom enables only after debounce', () => {
  const f = fixture()
  f.scroll(400); f.scroll(800)
  assert.equal(f.enabled(), false)
  f.settle()
  assert.equal(f.enabled(), true)
})

test('explicit off cancels pending enable and requires a fresh up/down trip', () => {
  const f = fixture()
  f.scroll(400); f.scroll(800)
  f.controller.toggle(false)
  f.settle(); f.scroll(800); f.settle()
  assert.equal(f.enabled(), false)
  f.scroll(400); f.scroll(800); f.settle()
  assert.equal(f.enabled(), true)
  f.controller.toggle(false)
  f.scroll(800); f.settle()
  assert.equal(f.enabled(), false)
})

test('leaving bottom or growing output during debounce cancels enable', () => {
  const f = fixture()
  f.scroll(400); f.scroll(800); f.scroll(600); f.settle()
  assert.equal(f.enabled(), false)
  f.scroll(800); f.pane.scrollHeight += 100; f.settle()
  assert.equal(f.enabled(), false)
  f.controller.toggle(true)
  assert.equal(f.pane.scrollTop, 900)
})

test('dispose cancels pending enable', () => {
  const f = fixture()
  f.scroll(400); f.scroll(800); f.controller.dispose(); f.settle()
  assert.equal(f.enabled(), false)
})
