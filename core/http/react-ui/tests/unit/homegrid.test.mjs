import test from 'node:test'
import assert from 'node:assert/strict'
import { columnsFor, contentUnits, packGrid, spanOf, unitsForPx, widgetSize, WIDGET_SIZES } from '../../src/utils/homeGrid.js'
import { cleanWidgets } from '../../src/utils/wellKnown.js'

const covered = (p, cols) => {
  const g = Array.from({ length: p.rows }, () => new Array(cols).fill(0))
  for (const c of p.cells) for (let y = c.row; y < c.row + c.rh; y++) for (let x = c.col; x < c.col + c.cw; x++) g[y][x]++
  return g
}

test('home grid: the columns follow the width of Home', () => {
  assert.equal(columnsFor(1000), 4)
  assert.equal(columnsFor(800), 4)
  assert.equal(columnsFor(799), 2)
  assert.equal(columnsFor(440), 2)
  assert.equal(columnsFor(358), 1)
  assert.equal(columnsFor(0), 1)
  assert.equal(columnsFor(undefined), 1)
})

test('home grid: a size is a column span and a row height, clamped to the columns', () => {
  assert.deepEqual(spanOf('s', 4), { cw: 1, rh: 1 })
  assert.deepEqual(spanOf('m', 4), { cw: 2, rh: 2 })
  assert.deepEqual(spanOf('l', 4), { cw: 4, rh: 3 })
  assert.deepEqual(spanOf('m', 2), { cw: 2, rh: 2 })
  assert.deepEqual(spanOf('l', 2), { cw: 2, rh: 3 })
  for (const z of WIDGET_SIZES) assert.equal(spanOf(z, 1).cw, 1)
  assert.deepEqual(spanOf('x', 4), { cw: 1, rh: 1 })
})

test('home grid: a widget takes its own size, then the page default, then the default of its type', () => {
  assert.equal(widgetSize({ type: 'list', size: 'l' }), 'l')
  assert.equal(widgetSize({ type: 'page', size: 's' }, { widget: { size: 'large' } }), 's')
  assert.equal(widgetSize({ type: 'page' }, { widget: { size: 'large' } }), 'l')
  assert.equal(widgetSize({ type: 'page' }, { widget: { size: 'small' } }), 's')
  assert.equal(widgetSize({ type: 'page' }, undefined), 'm')
  assert.equal(widgetSize({ type: 'page', size: 'huge' }, { widget: { size: 'medium' } }), 'm')
  assert.equal(widgetSize({ type: 'metric' }), 's')
  assert.equal(widgetSize({ type: 'list' }), 'm')
  assert.equal(widgetSize({ type: 'chats', size: 'M' }), 'm')
})

test('home grid: dense packing puts a later small tile in the hole a bigger one leaves', () => {
  // m l s on 4 columns: the l cannot go beside the m, so it takes the rows
  // under it; the s goes up beside the m. The cells left beside it are not in
  // the last row, so they stay and the s keeps its size.
  const p = packGrid(['m', 'l', 's'], 4)
  assert.deepEqual(p.cells[0], { col: 0, row: 0, cw: 2, rh: 2 })
  assert.deepEqual(p.cells[1], { col: 0, row: 2, cw: 4, rh: 3 })
  assert.deepEqual(p.cells[2], { col: 2, row: 0, cw: 1, rh: 1 })
  assert.equal(p.holes, 3)
})

test('home grid: a hole above the last row stays, only the last row stretches', () => {
  // s then l: the l needs a full row, so it goes under the s; the three cells
  // beside the s stay empty, and the s keeps its size.
  const p = packGrid(['s', 'l'], 4)
  assert.deepEqual(p.cells[0], { col: 0, row: 0, cw: 1, rh: 1 })
  assert.deepEqual(p.cells[1], { col: 0, row: 1, cw: 4, rh: 3 })
  assert.equal(p.holes, 3)
  // m m s: the s is alone in the last row and stretches over it.
  const q = packGrid(['m', 'm', 's'], 4)
  assert.deepEqual(q.cells[2], { col: 0, row: 2, cw: 4, rh: 1 })
  assert.equal(q.holes, 0)
})

test('home grid: an l fills a full row at every width', () => {
  for (const cols of [1, 2, 4]) {
    const p = packGrid(['s', 'l', 's'], cols)
    const l = p.cells[1]
    assert.equal(l.col, 0)
    assert.equal(l.cw, cols)
  }
})

test('home grid: the last row stretches to the full width', () => {
  // Three s on four columns leave one cell; the row stretches over it.
  const p = packGrid(['s', 's', 's'], 4)
  assert.equal(p.rows, 1)
  assert.equal(p.cells.reduce((n, c) => n + c.cw, 0), 4)
  // m, s, s: the two s sit beside the m with a row of cells under them; they grow down.
  const q = packGrid(['m', 's', 's'], 4)
  assert.equal(q.holes, 0)
  assert.deepEqual(covered(q, 4).flat().filter(n => n !== 1), [])
})

test('home grid: no mix of up to 12 widgets in any order leaves a hole in the last row', () => {
  let seed = 12345
  const rnd = () => { seed = (seed * 1664525 + 1013904223) % 4294967296; return seed / 4294967296 }
  for (let run = 0; run < 3000; run++) {
    const n = 1 + Math.floor(rnd() * 12)
    const sizes = Array.from({ length: n }, () => WIDGET_SIZES[Math.floor(rnd() * 3)])
    for (const cols of [1, 2, 4]) {
      const p = packGrid(sizes, cols)
      const g = covered(p, cols)
      // no overlap, the last row full, and holes counts the empty cells
      assert.ok(g.flat().every(c => c <= 1), `${sizes.join('')} on ${cols}`)
      assert.ok(g[p.rows - 1].every(c => c === 1), `${sizes.join('')} on ${cols}: last row`)
      assert.equal(p.holes, g.flat().filter(c => c === 0).length)
      for (const c of p.cells) assert.ok(c.col >= 0 && c.col + c.cw <= cols)
    }
  }
})

test('home grid: a phone has one column and every tile is one wide', () => {
  const p = packGrid(['s', 'm', 'l'], 1)
  assert.deepEqual(p.cells.map(c => [c.col, c.cw]), [[0, 1], [0, 1], [0, 1]])
  assert.equal(p.rows, 6)
})

test('home grid: the layout editor keeps a valid size and drops another', () => {
  const rows = [
    { id: 'a', type: 'list', title: 'A', source: 'data/a.json', size: 'l' },
    { id: 'b', type: 'chats', title: 'Chats', size: 'huge' },
    { id: 'c', type: 'page', page: 'news', size: 'm' },
    { id: 'd', type: 'metric', title: 'D', source: 'data/d.json' },
  ]
  const out = cleanWidgets(rows)
  assert.deepEqual(out.map(w => w.size), ['l', undefined, 'm', undefined])
  assert.ok(!('size' in out[3]))
})

test('home grid: a built-in widget takes the row units of its content', () => {
  // A unit is 132 px with a gap of 16, so n units are 148n - 16 px high.
  assert.equal(unitsForPx(132), 1)
  assert.equal(unitsForPx(133), 2)
  assert.equal(unitsForPx(280), 2)
  assert.equal(unitsForPx(281), 3)
  assert.equal(unitsForPx(0), 1)
  // The header and three rows of a list fit in two units; eight rows and a
  // footer need three; an empty list or no chats is one line.
  assert.equal(contentUnits('list', { rows: 3 }), 2)
  assert.equal(contentUnits('list', { rows: 1 }), 1)
  assert.equal(contentUnits('list', { rows: 8, footer: true }), 3)
  assert.equal(contentUnits('list', { rows: 0 }), 1)
  assert.equal(contentUnits('chats', { rows: 0 }), 1)
  assert.equal(contentUnits('chats', { rows: 1 }), 1)
  assert.equal(contentUnits('chats', { rows: 5 }), 2)
  // A metric is one line; with a sparkline it needs two units.
  assert.equal(contentUnits('metric', {}), 1)
  assert.equal(contentUnits('metric', { spark: true }), 2)
  assert.equal(contentUnits('metric', { spark: true, footer: true }), 2)
  assert.equal(contentUnits('metric', { footer: true }), 1)
  assert.equal(contentUnits('list', { rows: 1, footer: true }), 1)
  assert.equal(contentUnits('list', { rows: 2, footer: true }), 2)
  assert.equal(contentUnits('chats', { rows: 2 }), 2)
  // More rows than a list shows count as the rows it shows.
  assert.equal(contentUnits('list', { rows: 40 }), contentUnits('list', { rows: 8 }))
  assert.equal(contentUnits('chats', { rows: 40 }), contentUnits('chats', { rows: 5 }))
  assert.equal(contentUnits('nope', { rows: 3 }), 1)
})

test('home grid: the content height never goes past the units of the size', () => {
  // an l list of 3 items: two units, not three
  assert.deepEqual(packGrid(['l'], 4, [2]).cells[0], { col: 0, row: 0, cw: 4, rh: 2 })
  // an s list of 8 items stays one unit; no units means the units of the size
  assert.deepEqual(packGrid(['s', 'l'], 4, [3]).cells.map(c => c.rh), [1, 3])
  assert.deepEqual(packGrid(['m', 'l'], 4, [undefined, 'x']).cells.map(c => c.rh), [2, 3])
  assert.deepEqual(packGrid(['l'], 4, [0]).cells[0].rh, 3)
})

test('home grid: a later tile fills the room a short tile leaves, else the short tile grows back', () => {
  // m (2 units) and m (1 unit): the s tiles go under the short one.
  const p = packGrid(['m', 'm', 's', 's'], 4, [2, 1])
  assert.deepEqual(p.cells.map(c => [c.col, c.row, c.cw, c.rh]), [[0, 0, 2, 2], [2, 0, 2, 1], [2, 1, 1, 1], [3, 1, 1, 1]])
  assert.equal(p.holes, 0)
  // Nothing fits there: the short tile grows into the room under it, up to
  // the units of its size, so no cell between tiles is empty.
  const q = packGrid(['m', 'm', 'l'], 4, [2, 1, 1])
  assert.deepEqual(q.cells.map(c => [c.col, c.row, c.cw, c.rh]), [[0, 0, 2, 2], [2, 0, 2, 2], [0, 2, 4, 1]])
  assert.equal(q.holes, 0)
  // An s cannot grow past one unit: the hole beside it stays, as before.
  const r = packGrid(['m', 's', 'l'], 4, [2, 1])
  assert.deepEqual(r.cells[1], { col: 2, row: 0, cw: 1, rh: 1 })
  assert.equal(r.holes, 3)
})

test('home grid: any mix of 12 widgets with content heights packs with no overlap and a full last row', () => {
  let seed = 4242
  const rnd = () => { seed = (seed * 1664525 + 1013904223) % 4294967296; return seed / 4294967296 }
  for (let run = 0; run < 3000; run++) {
    const n = 1 + Math.floor(rnd() * 12)
    const sizes = Array.from({ length: n }, () => WIDGET_SIZES[Math.floor(rnd() * 3)])
    const units = sizes.map(() => (rnd() < 0.25 ? undefined : 1 + Math.floor(rnd() * 3)))
    for (const cols of [1, 2, 4]) {
      const p = packGrid(sizes, cols, units)
      const g = covered(p, cols)
      const label = `${sizes.join('')} ${units.join(',')} on ${cols}`
      assert.ok(g.flat().every(c => c <= 1), label)
      assert.ok(g[p.rows - 1].every(c => c === 1), `${label}: last row`)
      assert.equal(p.holes, g.flat().filter(c => c === 0).length)
      p.cells.forEach((c, i) => {
        const cap = spanOf(sizes[i], cols).rh
        // only the last row stretches past the units of the size
        if (c.row + c.rh < p.rows) assert.ok(c.rh <= cap, `${label}: tile ${i} is ${c.rh} high`)
      })
    }
  }
})
