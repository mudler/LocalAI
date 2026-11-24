import test from 'node:test'
import assert from 'node:assert/strict'
import {
  DOCK_DEFAULT_WIDTH, DOCK_MIN_WIDTH, MAX_DOCK_TABS, clampDockWidth, closeTab, dockKey, dockToServer, emptyDock,
  isDockKey, keyStep, openTab, readDock, sameDock, selectTab, setDockOpen, setDockWidth,
} from '../../src/utils/dock.js'

test('dock: the width stays between 320 px and 60% of the window', () => {
  assert.equal(clampDockWidth(452, 1440), 452)
  assert.equal(clampDockWidth(100, 1440), DOCK_MIN_WIDTH, 'never under the minimum')
  assert.equal(clampDockWidth(2000, 1440), 864, 'at most 60% of the window')
  assert.equal(clampDockWidth(0, 1440), DOCK_DEFAULT_WIDTH, '0 is the default width')
  assert.equal(clampDockWidth(undefined, 1440), DOCK_DEFAULT_WIDTH)
  assert.equal(clampDockWidth(NaN, 1440), DOCK_DEFAULT_WIDTH)
  assert.equal(clampDockWidth(452.6, 1440), 453, 'whole pixels')
  assert.equal(clampDockWidth(452, 600), 360, 'the default shrinks with a narrow window')
  assert.equal(clampDockWidth(500, 500), DOCK_MIN_WIDTH, 'a window too narrow for 60% keeps the minimum')
})

test('dock: the arrow keys move the edge, Home and End go to the bounds', () => {
  assert.equal(keyStep('ArrowLeft', 452, 1440), 468, 'left widens: the edge is on the left of the dock')
  assert.equal(keyStep('ArrowRight', 452, 1440), 436)
  assert.equal(keyStep('ArrowLeft', 452, 1440, true), 516, 'Shift moves further')
  assert.equal(keyStep('Home', 452, 1440), DOCK_MIN_WIDTH)
  assert.equal(keyStep('End', 452, 1440), 864)
  assert.equal(keyStep('Enter', 452, 1440), DOCK_DEFAULT_WIDTH, 'Enter resets, as a double click does')
  assert.equal(keyStep('a', 452, 1440), null, 'another key does nothing')
  assert.equal(keyStep('ArrowRight', 330, 1440), DOCK_MIN_WIDTH, 'the bounds hold')
})

test('dock: a tab is keyed by its folder and name', () => {
  assert.equal(dockKey({ page: 'tasks' }), 'tasks')
  assert.equal(dockKey({ page: 'tasks', folder: 4 }), '4/tasks')
  assert.equal(dockKey({ name: 'tasks', folder: 0 }), 'tasks', 'a ref of the tiles works too')
  assert.ok(isDockKey('tasks') && isDockKey('4/tasks'))
  assert.ok(!isDockKey('../x') && !isDockKey(''))
})

test('dock: the server value is read with the bounds, and a bad value is no dock', () => {
  assert.deepEqual(readDock(null), emptyDock())
  assert.deepEqual(readDock('nope'), emptyDock())
  const d = readDock({ open: true, width: 500, active: 1, tabs: [{ page: 'a' }, { page: 'Bad Name' }, { page: 'b', folder: 3 }, { page: 'a' }] })
  assert.deepEqual(d.tabs, [{ page: 'a', folder: 0 }, { page: 'b', folder: 3 }], 'bad and repeated tabs are left out')
  assert.equal(d.active, 1)
  assert.equal(d.open, true)
  assert.equal(d.width, 500)
  assert.equal(readDock({ open: true, tabs: [{ page: 'a' }], active: 9 }).active, 0, 'an active index out of range is the first tab')
  assert.equal(readDock({ tabs: [{ page: 'a' }], width: 12 }).width, 0, 'a width out of bounds is the default')
  const many = Array.from({ length: 9 }, (_, i) => ({ page: `p${i}` }))
  assert.equal(readDock({ tabs: many }).tabs.length, MAX_DOCK_TABS)
})

test('dock: opening a page brings it to the front, adds it once and drops the oldest when full', () => {
  let d = openTab(emptyDock(), { name: 'a', folder: 0 })
  assert.deepEqual(d, { open: true, tabs: [{ page: 'a', folder: 0 }], active: 0, width: 0 })
  d = openTab(d, { name: 'b', folder: 2 })
  assert.equal(d.active, 1)
  d = openTab({ ...d, open: false }, { name: 'a', folder: 0 })
  assert.equal(d.tabs.length, 2, 'a page is docked once')
  assert.equal(d.active, 0)
  assert.equal(d.open, true, 'opening a page opens the panel')
  for (const p of ['c', 'd', 'e', 'f', 'g']) d = openTab(d, { name: p })
  assert.equal(d.tabs.length, MAX_DOCK_TABS)
  assert.equal(d.tabs[0].page, 'b', 'the oldest tab went')
  assert.equal(d.active, MAX_DOCK_TABS - 1)
  assert.equal(openTab(d, { name: 'Bad Name' }), d, 'a bad ref changes nothing')
})

test('dock: closing a tab keeps a sensible tab in front, and the last one closes the panel', () => {
  const d = { open: true, tabs: [{ page: 'a', folder: 0 }, { page: 'b', folder: 0 }, { page: 'c', folder: 0 }], active: 1, width: 0 }
  assert.equal(closeTab(d, 1).active, 1, 'the next tab comes to the front')
  assert.deepEqual(closeTab(d, 1).tabs.map(t => t.page), ['a', 'c'])
  assert.equal(closeTab(d, 2).active, 1, 'a tab after the front one leaves it in front')
  assert.equal(closeTab(d, 0).active, 0, 'a tab before the front one keeps the same page in front')
  assert.equal(closeTab(d, 0).tabs[0].page, 'b')
  assert.equal(closeTab({ ...d, active: 2 }, 2).active, 1, 'the last tab hands over to the one before')
  const one = closeTab({ open: true, tabs: [{ page: 'a', folder: 0 }], active: 0, width: 400 }, 0)
  assert.deepEqual(one, { open: false, tabs: [], active: 0, width: 400 })
  assert.equal(closeTab(d, 7), d, 'an index out of range changes nothing')
})

test('dock: select, open, close and width change only what they say', () => {
  const d = { open: true, tabs: [{ page: 'a', folder: 0 }, { page: 'b', folder: 0 }], active: 0, width: 0 }
  assert.equal(selectTab(d, 1).active, 1)
  assert.equal(selectTab(d, 5), d)
  assert.equal(setDockOpen(d, false).open, false)
  assert.deepEqual(setDockOpen(d, false).tabs, d.tabs)
  assert.equal(setDockWidth(d, 512).width, 512)
  assert.equal(setDockWidth(d, 12).width, DOCK_MIN_WIDTH)
})

test('dock: the value sent to the server, and when two docks are the same', () => {
  const d = { open: true, tabs: [{ page: 'a', folder: 0 }, { page: 'b', folder: 3 }], active: 1, width: 480 }
  assert.deepEqual(dockToServer(d), { open: true, tabs: [{ page: 'a' }, { page: 'b', folder: 3 }], active: 1, width: 480 })
  assert.ok(sameDock(d, readDock(dockToServer(d))), 'the server value reads back to the same dock')
  assert.ok(!sameDock(d, { ...d, width: 481 }))
  assert.ok(!sameDock(d, { ...d, tabs: [d.tabs[1], d.tabs[0]] }))
  assert.ok(sameDock(emptyDock(), readDock(null)))
})
