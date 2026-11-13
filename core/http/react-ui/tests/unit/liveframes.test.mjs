import test from 'node:test'
import assert from 'node:assert/strict'
import { liveFrames } from '../../src/utils/liveFrames.js'
import { MAX_LIVE_FRAMES, MAX_PAGE_TILES } from '../../src/utils/pageTiles.js'

// The holders use the ids of the components: tile-N (PageTile on Home),
// slot-N (SlotTile in the slots of ui.json), area-N (the page area of Home)
// and dock-N (DockFrame, the tab in front of the dock). They all draw from the
// one pool of the app, so together they never run more than eight frames.
test('live frames: Home tiles, slots, the page area and the dock share one cap of eight', () => {
  assert.equal(liveFrames.max(), MAX_LIVE_FRAMES)
  assert.equal(MAX_LIVE_FRAMES, 8)
  const evicted = []
  const hold = (id) => liveFrames.acquire(id, () => evicted.push(id))
  for (let i = 1; i <= MAX_PAGE_TILES; i++) assert.equal(hold(`tile-${i}`), true, `Home tile ${i}`)
  for (let i = 1; i <= 3; i++) assert.equal(hold(`slot-${i}`), true, `slot tile ${i}`)
  assert.equal(hold('area-1'), true, 'the page area')
  assert.equal(hold('dock-1'), true, 'the dock tab in front')
  assert.equal(liveFrames.held(), 8)

  // A ninth frame of any kind waits as a static card.
  for (const id of ['tile-9', 'slot-9', 'area-9', 'dock-9']) assert.equal(liveFrames.acquire(id, () => {}), false, `${id} waits`)
  assert.equal(liveFrames.held(), 8)

  // A page opened in the dock takes a frame like a click: the frame that
  // started first (a Home tile) stops, and the count stays at eight.
  liveFrames.release('dock-1') // the old front tab goes when another tab comes to the front
  assert.equal(hold('slot-4'), true, 'the freed frame goes to the next holder')
  assert.equal(liveFrames.take('dock-2', () => evicted.push('dock-2')), true)
  assert.deepEqual(evicted, ['tile-1'])
  assert.equal(liveFrames.held(), 8)
  assert.equal(liveFrames.holds('tile-1'), false)

  // A dock that comes back with a reload asks without a click and waits.
  liveFrames.release('dock-2')
  assert.equal(hold('tile-1'), true)
  assert.equal(liveFrames.acquire('dock-3', () => {}), false, 'a restored dock shows Run it here')

  for (const id of ['tile-1', 'tile-2', 'tile-3', 'slot-1', 'slot-2', 'slot-3', 'slot-4', 'area-1']) liveFrames.release(id)
  assert.equal(liveFrames.held(), 0)
})
