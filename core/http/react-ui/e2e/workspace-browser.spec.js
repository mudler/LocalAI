import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'

// The Files page as a file browser, with the API stubbed by a small in-memory
// folder: sorting, the filter, the views, selection and bulk actions, the
// keyboard, the context menu, drag and drop, paste and the name clash of an
// upload. The real-server flows are in workspace.spec.js.

const T0 = Date.parse('2026-10-30T10:00:00Z')
const at = (mins) => new Date(T0 - mins * 60_000).toISOString()

function seed() {
  return [
    { path: '/Projects', name: 'Projects', kind: 'dir', size: 0, version: 1, rev: 1, modified: at(600) },
    { path: '/archive', name: 'archive', kind: 'dir', size: 0, version: 1, rev: 2, modified: at(30) },
    { path: '/b-report.pdf', name: 'b-report.pdf', kind: 'file', size: 300_000, version: 1, rev: 3, modified: at(5) },
    { path: '/a-notes.md', name: 'a-notes.md', kind: 'file', size: 10, version: 1, rev: 4, modified: at(60 * 48) },
    { path: '/c-budget.xlsx', name: 'c-budget.xlsx', kind: 'file', size: 5_000, version: 1, rev: 5, modified: at(1) },
    { path: '/Note 10.txt', name: 'Note 10.txt', kind: 'file', size: 20, version: 1, rev: 6, modified: at(2) },
    { path: '/Note 2.txt', name: 'Note 2.txt', kind: 'file', size: 30, version: 1, rev: 7, modified: at(3) },
  ]
}

// stubFs answers the files API from an array that the moves, deletes, renames
// and uploads change, and records every call.
async function stubFs(page, { entries = seed(), maxFileSize = 0 } = {}) {
  const fs = { entries, calls: [], trash: [], rev: 100 }
  const parent = (p) => p.slice(0, p.lastIndexOf('/')) || '/'
  const name = (p) => p.slice(p.lastIndexOf('/') + 1)
  await page.route(/\/api\/chats\/files\/trash(\?.*)?$/, route => route.fulfill({ json: { entries: fs.trash } }))
  await page.route(/\/api\/chats\/files\/undo(\?.*)?$/, route => {
    const { rev } = route.request().postDataJSON()
    fs.calls.push(['undo', rev])
    const t = fs.trash.find(x => x.rev === rev)
    if (t) { fs.entries.push(t.entry); fs.trash = fs.trash.filter(x => x !== t) }
    const m = fs.moves?.[rev]
    if (m) { const e = fs.entries.find(x => x.path === m.to); if (e) { e.path = m.from; e.name = name(m.from) } }
    return route.fulfill({ json: { rev: ++fs.rev } })
  })
  await page.route(/\/api\/chats\/files\/move(\?.*)?$/, route => {
    const { from, to } = route.request().postDataJSON()
    fs.calls.push(['move', from, to])
    const e = fs.entries.find(x => x.path === from)
    const rev = ++fs.rev
    fs.moves = { ...(fs.moves || {}), [rev]: { from, to } }
    if (e) { e.path = to; e.name = name(to); e.rev = rev }
    return route.fulfill({ json: { ...(e || {}), path: to, name: name(to), rev } })
  })
  await page.route(/\/api\/chats\/files\/folders(\?.*)?$/, route => {
    const { path } = route.request().postDataJSON()
    fs.calls.push(['mkdir', path])
    const e = { path, name: name(path), kind: 'dir', size: 0, version: 1, rev: ++fs.rev, modified: new Date().toISOString() }
    fs.entries.push(e)
    return route.fulfill({ json: e })
  })
  await page.route('**/api/chats/files/content*', route => {
    const req = route.request()
    const url = new URL(req.url())
    const path = url.searchParams.get('path')
    if (req.method() !== 'PUT') return route.fulfill({ body: 'hello', contentType: 'text/plain' })
    const replace = url.searchParams.get('overwrite') === '1'
    fs.calls.push(['put', path, replace ? 'replace' : 'keep'])
    const size = req.postDataBuffer()?.length || 0
    const existing = fs.entries.find(x => x.path === path)
    let out = path
    if (existing && !replace) out = path.replace(/(\.[^.]+)?$/, ' (conflict)$1')
    const e = { path: out, name: name(out), kind: 'file', size, version: 1, rev: ++fs.rev, modified: new Date().toISOString(), ...(out !== path ? { conflict_of: path } : {}) }
    if (existing && replace) Object.assign(existing, e)
    else if (parent(out) === '/') fs.entries.push(e)
    return route.fulfill({ json: e })
  })
  await page.route(/\/api\/chats\/files(\?.*)?$/, route => {
    const req = route.request()
    const url = new URL(req.url())
    const path = url.searchParams.get('path') || '/'
    if (req.method() === 'DELETE') {
      fs.calls.push(['delete', path])
      const e = fs.entries.find(x => x.path === path)
      fs.entries = fs.entries.filter(x => x !== e)
      fs.trash.unshift({ rev: ++fs.rev, title: `Delete ${path}`, at: new Date().toISOString(), paths: [path], entry: e })
      return route.fulfill({ json: { deleted: true } })
    }
    const list = path === '/' ? fs.entries.filter(e => parent(e.path) === '/') : []
    return route.fulfill({ json: { path, entries: [...list].sort((a, b) => (a.name < b.name ? -1 : 1)), ...(maxFileSize ? { max_file_size: maxFileSize } : {}) } })
  })
  return fs
}

const names = (page) => page.getByTestId('ws-row').evaluateAll(els => els.map(e => e.getAttribute('data-path').slice(1)))
const row = (page, name) => page.getByTestId('ws-row').filter({ has: page.getByRole('button', { name, exact: true }) })

test.describe('Files: browsing, selection and actions', () => {
  test.beforeEach(async ({ page }) => {
    await page.clock.setFixedTime(T0)
    await mockChat(page)
  })

  test('sorts folders first by name, naturally, and by any column both ways, and remembers it', async ({ page }) => {
    await stubFs(page)
    await page.goto('/app/files')
    await expect(page.getByTestId('ws-row')).toHaveCount(7)
    expect(await names(page)).toEqual(['archive', 'Projects', 'a-notes.md', 'b-report.pdf', 'c-budget.xlsx', 'Note 2.txt', 'Note 10.txt'])
    await expect(page.getByRole('columnheader', { name: 'Name' })).toHaveAttribute('aria-sort', 'ascending')

    await page.getByTestId('ws-sort-size').click()
    await expect(page.getByRole('columnheader', { name: 'Size' })).toHaveAttribute('aria-sort', 'descending')
    expect(await names(page)).toEqual(['archive', 'Projects', 'b-report.pdf', 'c-budget.xlsx', 'Note 2.txt', 'Note 10.txt', 'a-notes.md'])
    await page.getByTestId('ws-sort-size').click()
    await expect(page.getByRole('columnheader', { name: 'Size' })).toHaveAttribute('aria-sort', 'ascending')

    await page.getByTestId('ws-sort-modified').click()
    expect(await names(page)).toEqual(['archive', 'Projects', 'c-budget.xlsx', 'Note 10.txt', 'Note 2.txt', 'b-report.pdf', 'a-notes.md'])
    const modified = row(page, 'b-report.pdf').locator('.ws-cell--modified')
    await expect(modified).toHaveText('5 minutes ago')
    await expect(modified).toHaveAttribute('title', /2026/)

    await page.reload()
    await expect(page.getByRole('columnheader', { name: 'Modified' })).toHaveAttribute('aria-sort', 'descending')
  })

  test('filters the folder as you type, offers a search of every file, and says when nothing matches', async ({ page }) => {
    await stubFs(page)
    let searched = ''
    await page.route('**/api/chats/files/search*', route => {
      searched = new URL(route.request().url()).searchParams.get('q')
      return route.fulfill({ json: { hits: [] } })
    })
    await page.goto('/app/files')
    const box = page.getByRole('searchbox', { name: 'Search your files' })
    await box.fill('note')
    expect(await names(page)).toEqual(['a-notes.md', 'Note 2.txt', 'Note 10.txt'])
    await expect(page.getByTestId('ws-filtering')).toContainText('3 items in this folder match “note”.')
    await box.fill('zebra')
    await expect(page.getByTestId('ws-no-match')).toContainText('Nothing in this folder matches “zebra”.')
    await page.getByTestId('ws-no-match').getByRole('button', { name: 'Search all files' }).click()
    await expect(page.getByText('Files that contain “zebra”')).toBeVisible()
    expect(searched).toBe('zebra')
    await page.getByRole('button', { name: 'Back to the folder' }).click()
    await box.press('Escape')
    await expect(page.getByTestId('ws-row')).toHaveCount(7)
  })

  test('switches to thumbnails and keeps the choice for the next visit', async ({ page }) => {
    await stubFs(page)
    await page.goto('/app/files')
    await page.getByTestId('ws-view-grid').click()
    await expect(page.getByTestId('ws-list')).toHaveClass(/ws-table--grid/)
    await expect(page.getByTestId('ws-view-grid')).toHaveAttribute('aria-checked', 'true')
    await page.reload()
    await expect(page.getByTestId('ws-list')).toHaveClass(/ws-table--grid/)
    await page.getByTestId('ws-view-list').click()
    await expect(page.getByTestId('ws-list')).toHaveClass(/ws-table--list/)
  })

  test('selects with click, Ctrl and Shift, and moves the selection to the Trash with one Undo', async ({ page }) => {
    const fs = await stubFs(page)
    await page.goto('/app/files')
    await row(page, 'a-notes.md').locator('.ws-cell--size').click()
    await row(page, 'c-budget.xlsx').locator('.ws-cell--size').click({ modifiers: ['Shift'] })
    await row(page, 'Note 10.txt').locator('.ws-cell--size').click({ modifiers: ['Control'] })
    const bar = page.getByTestId('ws-selection-bar')
    await expect(bar).toContainText('4 selected')
    await expect(page.getByTestId('ws-row').and(page.locator('[aria-selected="true"]'))).toHaveCount(4)

    await bar.getByRole('button', { name: 'Move to Trash' }).click()
    await expect(page.getByTestId('ws-row')).toHaveCount(3)
    expect(fs.calls.filter(c => c[0] === 'delete').map(c => c[1])).toEqual(['/a-notes.md', '/b-report.pdf', '/c-budget.xlsx', '/Note 10.txt'])
    await page.getByTestId('ws-toast').filter({ hasText: '4 items moved to the Trash.' }).getByRole('button', { name: 'Undo' }).click()
    await expect(page.getByTestId('ws-row')).toHaveCount(7)
    expect(fs.calls.filter(c => c[0] === 'undo')).toHaveLength(4)
  })

  test('the checkbox and the select-all box select, Escape clears', async ({ page }) => {
    await stubFs(page)
    await page.goto('/app/files')
    await row(page, 'b-report.pdf').getByRole('checkbox', { name: 'Select b-report.pdf' }).check()
    await expect(page.getByTestId('ws-selection-bar')).toContainText('1 selected')
    await page.getByRole('checkbox', { name: 'Select all' }).check()
    await expect(page.getByTestId('ws-selection-bar')).toContainText('7 selected')
    await row(page, 'b-report.pdf').focus()
    await page.keyboard.press('Escape')
    await expect(page.getByTestId('ws-selection-bar')).toHaveCount(0)
  })

  test('works from the keyboard: arrows, Shift, Space, Enter, F2, Delete, Ctrl+A and type to find', async ({ page }) => {
    const fs = await stubFs(page)
    await page.goto('/app/files')
    await row(page, 'archive').focus()
    await page.keyboard.press('ArrowDown')
    await expect(row(page, 'Projects')).toBeFocused()
    await page.keyboard.press('Shift+ArrowDown')
    await expect(page.getByTestId('ws-selection-bar')).toContainText('2 selected')
    await page.keyboard.press('Escape')
    await page.keyboard.type('c-b')
    await expect(row(page, 'c-budget.xlsx')).toBeFocused()
    await page.keyboard.press('Space')
    await expect(row(page, 'c-budget.xlsx')).toHaveAttribute('aria-selected', 'true')
    await page.keyboard.press('Control+a')
    await expect(page.getByTestId('ws-selection-bar')).toContainText('7 selected')
    await page.keyboard.press('Escape')

    await row(page, 'Note 2.txt').focus()
    await page.keyboard.press('F2')
    const box = page.getByRole('textbox', { name: 'Rename' })
    await expect(box).toBeFocused()
    // The name without its extension is selected.
    expect(await box.evaluate(el => el.value.slice(el.selectionStart, el.selectionEnd))).toBe('Note 2')
    await page.keyboard.type('Note 3')
    await page.keyboard.press('Enter')
    await expect.poll(() => fs.calls.find(c => c[0] === 'move')).toEqual(['move', '/Note 2.txt', '/Note 3.txt'])

    await row(page, 'a-notes.md').focus()
    await page.keyboard.press('Delete')
    await expect.poll(() => fs.calls.find(c => c[0] === 'delete')).toEqual(['delete', '/a-notes.md'])

    await row(page, 'b-report.pdf').focus()
    await page.keyboard.press('Enter')
    await expect(page.getByTestId('file-preview')).toContainText('b-report.pdf')
  })

  test('a rename that cannot be used says why and keeps the field open', async ({ page }) => {
    const fs = await stubFs(page)
    await page.goto('/app/files')
    await row(page, 'a-notes.md').focus()
    await page.keyboard.press('F2')
    const box = page.getByRole('textbox', { name: 'Rename' })
    await box.fill('b-report.pdf')
    await box.press('Enter')
    await expect(page.getByRole('alert').filter({ hasText: 'Something in this folder already has that name.' })).toBeVisible()
    await box.fill('x/y')
    await box.press('Enter')
    await expect(page.getByRole('alert').filter({ hasText: 'A name cannot contain / or \\.' })).toBeVisible()
    await box.press('Escape')
    await expect(box).toHaveCount(0)
    expect(fs.calls).toEqual([])
  })

  test('a right click opens the menu of the row and of the selection', async ({ page }) => {
    await stubFs(page)
    await page.goto('/app/files')
    await row(page, 'b-report.pdf').locator('.ws-cell--type').click({ button: 'right' })
    const menu = page.getByRole('menu')
    await expect(menu.getByRole('menuitem')).toHaveText(['Preview', 'Download', /Rename/, 'Move to…', /Move to Trash/])
    await page.keyboard.press('Escape')
    await expect(menu).toHaveCount(0)

    await row(page, 'a-notes.md').locator('.ws-cell--size').click({ modifiers: ['Control'] })
    await row(page, 'b-report.pdf').locator('.ws-cell--type').click({ button: 'right' })
    await expect(page.getByRole('menu').getByRole('menuitem')).toHaveText(['Download 2 files', 'Move 2 items to…', /Move 2 items to Trash/])
  })

  test('creates a new file, named so it does not clash, and opens its preview', async ({ page }) => {
    const fs = await stubFs(page)
    fs.entries.push({ path: '/Untitled.md', name: 'Untitled.md', kind: 'file', size: 0, version: 1, rev: 9, modified: at(1) })
    await page.goto('/app/files')
    await page.getByRole('button', { name: 'New file' }).click()
    const box = page.getByRole('textbox', { name: 'File name' })
    await expect(box).toHaveValue('Untitled 2.md')
    await box.press('Enter')
    await expect.poll(() => fs.calls.find(c => c[0] === 'put')).toEqual(['put', '/Untitled 2.md', 'keep'])
    await expect(page.getByTestId('file-preview')).toContainText('Untitled 2.md')
  })

  test('dragging rows onto a folder moves them, and Undo moves them back', async ({ page }) => {
    const fs = await stubFs(page)
    await page.goto('/app/files')
    await row(page, 'a-notes.md').locator('.ws-cell--size').click()
    await row(page, 'b-report.pdf').locator('.ws-cell--size').click({ modifiers: ['Control'] })
    await row(page, 'a-notes.md').dragTo(row(page, 'archive'))
    await expect.poll(() => fs.calls.filter(c => c[0] === 'move')).toEqual([
      ['move', '/a-notes.md', '/archive/a-notes.md'],
      ['move', '/b-report.pdf', '/archive/b-report.pdf'],
    ])
    const toast = page.getByTestId('ws-toast').filter({ hasText: '2 items moved to archive.' })
    await toast.getByRole('button', { name: 'Undo' }).click()
    await expect.poll(() => fs.calls.filter(c => c[0] === 'undo').length).toBe(2)
  })

  test('files dropped from outside show the drop target and upload to the open folder', async ({ page }) => {
    const fs = await stubFs(page)
    await page.goto('/app/files')
    await expect(page.getByTestId('ws-row')).toHaveCount(7)
    const dt = await page.evaluateHandle(() => {
      const d = new DataTransfer()
      d.items.add(new File(['one'], 'dropped-1.txt', { type: 'text/plain' }))
      d.items.add(new File(['two!'], 'dropped-2.txt', { type: 'text/plain' }))
      return d
    })
    const main = page.locator('.ws-main')
    await main.dispatchEvent('dragenter', { dataTransfer: dt })
    await main.dispatchEvent('dragover', { dataTransfer: dt })
    await expect(page.getByTestId('ws-drop-overlay')).toContainText('Drop to upload to Home')
    await main.dispatchEvent('drop', { dataTransfer: dt })
    await expect(page.getByTestId('ws-drop-overlay')).toHaveCount(0)
    await expect.poll(() => fs.calls.filter(c => c[0] === 'put').map(c => c[1]).sort()).toEqual(['/dropped-1.txt', '/dropped-2.txt'])
    await expect(page.getByTestId('ws-upload-tray')).toContainText('2 files uploaded')
  })

  test('a pasted screenshot uploads with a name of its own', async ({ page }) => {
    const fs = await stubFs(page)
    await page.goto('/app/files')
    await expect(page.getByTestId('ws-row')).toHaveCount(7)
    await page.evaluate(() => {
      const d = new DataTransfer()
      d.items.add(new File([new Uint8Array([137, 80, 78, 71])], 'image.png', { type: 'image/png' }))
      document.body.dispatchEvent(new ClipboardEvent('paste', { clipboardData: d, bubbles: true }))
    })
    await expect.poll(() => fs.calls.find(c => c[0] === 'put')?.[1]).toMatch(/^\/Pasted image 2026-10-30 \d\d\.\d\d\.\d\d\.png$/)
  })

  test('asks before an upload replaces a file: Replace, Keep both or Skip', async ({ page }) => {
    const fs = await stubFs(page)
    await page.goto('/app/files')
    const input = page.getByTestId('ws-file-input')
    const files = [{ name: 'a-notes.md', mimeType: 'text/plain', buffer: Buffer.from('new') }, { name: 'fresh.txt', mimeType: 'text/plain', buffer: Buffer.from('x') }]

    await input.setInputFiles(files)
    const dialog = page.getByTestId('ws-conflict')
    await expect(dialog).toContainText('A file with this name already exists')
    await expect(dialog).toContainText('a-notes.md')
    await dialog.getByRole('button', { name: 'Skip it' }).click()
    await expect.poll(() => fs.calls.filter(c => c[0] === 'put')).toEqual([['put', '/fresh.txt', 'keep']])

    await input.setInputFiles(files.slice(0, 1))
    await page.getByTestId('ws-conflict').getByRole('button', { name: 'Keep both' }).click()
    await expect(page.getByTestId('ws-upload-row').filter({ hasText: 'a-notes.md' }).first()).toContainText('Saved as a-notes (conflict).md')

    await input.setInputFiles(files.slice(0, 1))
    await page.getByTestId('ws-conflict').getByRole('button', { name: 'Replace' }).click()
    await expect.poll(() => fs.calls.filter(c => c[0] === 'put').at(-1)).toEqual(['put', '/a-notes.md', 'replace'])
  })

  test('refuses a file over the server limit before sending it, and says the limit', async ({ page }) => {
    const fs = await stubFs(page, { maxFileSize: 1024 })
    await page.goto('/app/files')
    await page.getByTestId('ws-file-input').setInputFiles({ name: 'huge.bin', mimeType: 'application/octet-stream', buffer: Buffer.alloc(4096) })
    const up = page.getByTestId('ws-upload-row').filter({ hasText: 'huge.bin' })
    await expect(up).toContainText('Larger than the 1.0 KB limit of this server.')
    await expect(up.getByRole('button', { name: 'Try huge.bin again' })).toHaveCount(0)
    expect(fs.calls).toEqual([])
  })

  test('an upload keeps its folder when the user opens another one, and Cancel stops a slow one', async ({ page }) => {
    await stubFs(page)
    const puts = []
    let release
    const held = new Promise(r => { release = r })
    await page.route('**/api/chats/files/content*', async route => {
      puts.push(new URL(route.request().url()).searchParams.get('path'))
      await held
      return route.fulfill({ json: { path: '/slow.bin', name: 'slow.bin', kind: 'file', size: 1, version: 1, rev: 1 } }).catch(() => {})
    })
    await page.goto('/app/files')
    await page.getByTestId('ws-file-input').setInputFiles([
      { name: 'slow.bin', mimeType: 'application/octet-stream', buffer: Buffer.from('a') },
      { name: 'second.bin', mimeType: 'application/octet-stream', buffer: Buffer.from('b') },
      { name: 'third.bin', mimeType: 'application/octet-stream', buffer: Buffer.from('c') },
    ])
    const tray = page.getByTestId('ws-upload-tray')
    await expect(tray).toContainText('Uploading 3 files')
    await expect(page.getByTestId('ws-upload-row').filter({ hasText: 'third.bin' })).toContainText('Waiting')
    await page.getByTestId('ws-upload-row').filter({ hasText: 'third.bin' }).getByRole('button', { name: 'Cancel the upload of third.bin' }).click()
    await expect(page.getByTestId('ws-upload-row').filter({ hasText: 'third.bin' })).toContainText('Canceled')

    await row(page, 'archive').getByRole('button', { name: 'archive', exact: true }).click()
    await expect(page).toHaveURL(/path=%2Farchive/)
    release()
    await expect(tray).toContainText('2 files uploaded')
    expect(puts.sort()).toEqual(['/second.bin', '/slow.bin'])
  })
})
