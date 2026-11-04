import test from 'node:test'
import assert from 'node:assert/strict'
import {
  kindOf, hasThumb, sortEntries, filterEntries, relativeDate, rangeOf, nextIndex, findByPrefix,
  uniqueName, nameProblem, pastedName, parseViewPrefs,
} from '../../src/utils/fileBrowser.js'

const dir = (name, extra = {}) => ({ path: `/${name}`, name, kind: 'dir', size: 0, ...extra })
const file = (name, size = 1, extra = {}) => ({ path: `/${name}`, name, kind: 'file', size, ...extra })

test('file browser: an entry\'s kind comes from its extension, a folder is a folder', () => {
  assert.equal(kindOf(dir('photos.jpg')), 'folder')
  assert.equal(kindOf(file('a.JPG')), 'image')
  assert.equal(kindOf(file('Quarterly report.pdf')), 'pdf')
  assert.equal(kindOf(file('budget.xlsx')), 'sheet')
  assert.equal(kindOf(file('notes.md')), 'text')
  assert.equal(kindOf(file('main.go')), 'code')
  assert.equal(kindOf(file('README')), 'file')
})

test('file browser: only a small image of a common format gets a thumbnail', () => {
  assert.equal(hasThumb(file('a.png', 10)), true)
  assert.equal(hasThumb(file('a.svg', 10)), false, 'an SVG could carry script; it gets an icon')
  assert.equal(hasThumb(file('a.png', 50 * 1024 * 1024)), false)
  assert.equal(hasThumb(dir('a.png')), false)
})

test('file browser: folders come first, names sort naturally and without case, ties keep the name order', () => {
  const list = [file('b.txt'), file('note-10.md'), dir('Zeta'), file('A.txt'), file('note-2.md'), dir('alpha')]
  assert.deepEqual(sortEntries(list).map(e => e.name), ['alpha', 'Zeta', 'A.txt', 'b.txt', 'note-2.md', 'note-10.md'])
  assert.deepEqual(sortEntries(list, { key: 'name', dir: 'desc' }).map(e => e.name), ['Zeta', 'alpha', 'note-10.md', 'note-2.md', 'b.txt', 'A.txt'])
})

test('file browser: sorts by size, by modified time and by type, folders still first', () => {
  const list = [
    file('big.bin', 900, { modified: '2026-01-02T00:00:00Z' }),
    file('small.txt', 1, { modified: '2026-03-01T00:00:00Z' }),
    dir('docs', { modified: '2025-01-01T00:00:00Z' }),
    file('mid.pdf', 50, { modified: '2026-02-01T00:00:00Z' }),
  ]
  assert.deepEqual(sortEntries(list, { key: 'size', dir: 'desc' }).map(e => e.name), ['docs', 'big.bin', 'mid.pdf', 'small.txt'])
  assert.deepEqual(sortEntries(list, { key: 'modified', dir: 'desc' }).map(e => e.name), ['docs', 'small.txt', 'mid.pdf', 'big.bin'])
  assert.deepEqual(sortEntries(list, { key: 'type' }).map(e => e.name), ['docs', 'big.bin', 'mid.pdf', 'small.txt'])
  assert.equal(sortEntries(undefined).length, 0)
})

test('file browser: the filter keeps names with every word, in any case and order', () => {
  const list = [file('Quarterly report.pdf'), file('report-draft.md'), file('budget.xlsx')]
  assert.deepEqual(filterEntries(list, 'REPORT').map(e => e.name), ['Quarterly report.pdf', 'report-draft.md'])
  assert.deepEqual(filterEntries(list, 'report quarterly').map(e => e.name), ['Quarterly report.pdf'])
  assert.equal(filterEntries(list, '   ').length, 3)
})

test('file browser: dates read as words for the last week, as a date before, with the full date for a tooltip', () => {
  const now = Date.parse('2026-10-30T12:00:00Z')
  assert.equal(relativeDate('2026-10-30T11:59:50Z', { now, locale: 'en' }).label, 'now')
  assert.equal(relativeDate('2026-10-30T11:55:00Z', { now, locale: 'en' }).label, '5 minutes ago')
  assert.equal(relativeDate('2026-10-29T12:00:00Z', { now, locale: 'en' }).label, 'yesterday')
  assert.equal(relativeDate('2026-09-01T12:00:00Z', { now, locale: 'en' }).label, 'Sep 1')
  assert.equal(relativeDate('2024-09-01T12:00:00Z', { now, locale: 'en' }).label, 'Sep 1, 2024')
  assert.match(relativeDate('2026-09-01T12:00:00Z', { now, locale: 'en' }).exact, /2026/)
  assert.deepEqual(relativeDate(undefined, { now }), { label: '', exact: '' })
})

test('file browser: Shift selects the range between the anchor and the target, either way', () => {
  const order = ['/a', '/b', '/c', '/d']
  assert.deepEqual(rangeOf(order, '/b', '/d'), ['/b', '/c', '/d'])
  assert.deepEqual(rangeOf(order, '/d', '/b'), ['/b', '/c', '/d'])
  assert.deepEqual(rangeOf(order, null, '/c'), ['/c'])
  assert.deepEqual(rangeOf(order, '/a', '/zz'), [])
})

test('file browser: the arrows move by one in a list and by a row in a grid, and stop at the ends', () => {
  assert.equal(nextIndex('ArrowDown', 0, 5), 1)
  assert.equal(nextIndex('ArrowUp', 0, 5), 0)
  assert.equal(nextIndex('ArrowRight', 2, 5), 2, 'a list has no left and right')
  assert.equal(nextIndex('ArrowDown', 1, 10, 4), 5)
  assert.equal(nextIndex('ArrowRight', 1, 10, 4), 2)
  assert.equal(nextIndex('ArrowDown', 8, 10, 4), 9)
  assert.equal(nextIndex('End', 1, 10), 9)
  assert.equal(nextIndex('Home', 7, 10), 0)
  assert.equal(nextIndex('ArrowDown', -1, 3), 0)
  assert.equal(nextIndex('ArrowDown', 0, 0), -1)
})

test('file browser: typing a name finds the next match and wraps around', () => {
  const names = ['alpha', 'beta', 'bravo', 'charlie']
  assert.equal(findByPrefix(names, 'b', 0), 1)
  assert.equal(findByPrefix(names, 'b', 2), 2)
  assert.equal(findByPrefix(names, 'a', 3), 0)
  assert.equal(findByPrefix(names, 'BR', 0), 2)
  assert.equal(findByPrefix(names, 'z', 0), -1)
})

test('file browser: a new name never lands on an existing one, and keeps its extension', () => {
  assert.equal(uniqueName('New folder', ['a']), 'New folder')
  assert.equal(uniqueName('New folder', ['new folder', 'New folder 2']), 'New folder 3')
  assert.equal(uniqueName('Untitled.md', ['Untitled.md']), 'Untitled 2.md')
})

test('file browser: a name that cannot be used says why', () => {
  assert.equal(nameProblem('  ', []), 'naming.empty')
  assert.equal(nameProblem('a/b', []), 'naming.slash')
  assert.equal(nameProblem('..', []), 'naming.slash')
  assert.equal(nameProblem('Notes.md', ['notes.md']), 'naming.taken')
  assert.equal(nameProblem('NOTES.md', ['notes.md'], 'notes.md'), '', 'a change of case of the same entry is allowed')
  assert.equal(nameProblem('plan.md', ['notes.md']), '')
})

test('file browser: a pasted screenshot gets the time as its name, a named file keeps its name', () => {
  const at = new Date(2026, 9, 30, 9, 5, 7)
  assert.equal(pastedName('image.png', at), 'Pasted image 2026-10-30 09.05.07.png')
  assert.equal(pastedName('', at), 'Pasted image 2026-10-30 09.05.07.png')
  assert.equal(pastedName('report.pdf', at), 'report.pdf')
})

test('file browser: the view settings read back safely from storage', () => {
  assert.deepEqual(parseViewPrefs('{"view":"grid","key":"size","dir":"desc"}'), { view: 'grid', key: 'size', dir: 'desc' })
  assert.deepEqual(parseViewPrefs('{"view":"tiles","key":"owner"}'), { view: 'list', key: 'name', dir: 'asc' })
  assert.deepEqual(parseViewPrefs('not json'), { view: 'list', key: 'name', dir: 'asc' })
  assert.deepEqual(parseViewPrefs(null), { view: 'list', key: 'name', dir: 'asc' })
})
