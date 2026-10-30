import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'

// Visual contracts of the UI primitives, read from computed styles on the
// catalog page (/app/ui-catalog), which draws every primitive in every state.
// Set UI_SHOTS_DIR to also save screenshots of the catalog in both themes at
// desktop and phone sizes.

const CATALOG = '/app/ui-catalog'
const SHOTS = process.env.UI_SHOTS_DIR

async function openCatalog(page, { theme = 'light' } = {}) {
  await page.addInitScript((t) => localStorage.setItem('localai-theme', t), theme)
  await page.goto(CATALOG)
  await expect(page.getByTestId('cat-section-buttons')).toBeVisible({ timeout: 15_000 })
  await page.evaluate(() => document.fonts.ready)
}

// WCAG contrast of two computed colours (rgb() or color(srgb …)).
function contrastOf(fg, bg) {
  const parse = (c) => {
    const n = c.match(/[\d.]+/g).map(Number)
    return c.startsWith('color(') ? n.slice(0, 3).map(v => v * 255) : n.slice(0, 3)
  }
  const lum = (rgb) => {
    const [r, g, b] = rgb.map(v => { const s = v / 255; return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4 })
    return 0.2126 * r + 0.7152 * g + 0.0722 * b
  }
  const [a, b] = [lum(parse(fg)), lum(parse(bg))]
  return (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05)
}

test.describe('UI primitives', () => {
  test.beforeEach(async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
  })

  test('buttons: one size scale for the component and the legacy classes', async ({ page }) => {
    await openCatalog(page)
    const box = async (id) => (await page.getByTestId(id).boundingBox()).height
    expect(await box('btn-primary')).toBe(36)
    expect(await box('btn-primary-sm')).toBe(28)
    expect(await box('legacy-primary')).toBe(36)
    expect(await box('legacy-sm')).toBe(28)
    const style = (id) => page.getByTestId(id).evaluate(el => {
      const cs = getComputedStyle(el)
      return { font: cs.fontFamily, size: cs.fontSize, weight: cs.fontWeight, bg: cs.backgroundColor, shadow: cs.boxShadow, radius: cs.borderTopLeftRadius }
    })
    const kit = await style('btn-primary')
    const legacy = await style('legacy-primary')
    expect(kit.font).toMatch(/^"Geist Variable"/)
    expect(kit).toEqual(legacy)
    expect(kit.size).toBe('13px')
    expect(kit.weight).toBe('500')
    expect(kit.radius).toBe('8px')
    // Secondary: card fill and a 1px edge; the legacy class matches it.
    const sec = await style('btn-secondary')
    expect(sec.shadow).toMatch(/0px 0px 0px 1px inset/)
    expect(await style('legacy-secondary')).toEqual(sec)
    expect(await style('legacy-plain')).toEqual(sec)
    expect((await style('btn-secondary-sm')).size).toBe('12px')
  })

  test('buttons: busy keeps the width and says so; focus shows the ring', async ({ page }) => {
    await openCatalog(page)
    const busy = page.getByTestId('btn-secondary-busy')
    await expect(busy).toHaveAttribute('aria-busy', 'true')
    const widths = await busy.evaluate(el => {
      const probe = document.createElement('button')
      probe.className = 'dk-btn dk-btn--secondary'
      probe.innerHTML = '<span class="ui-btn__label">Saving</span>'
      el.parentElement.appendChild(probe)
      const w = [el.getBoundingClientRect().width, probe.getBoundingClientRect().width]
      probe.remove()
      return w
    })
    expect(widths[0]).toBeCloseTo(widths[1], 0)

    const primary = page.getByTestId('btn-primary')
    await primary.focus()
    await page.keyboard.press('Shift+Tab')
    await page.keyboard.press('Tab')
    await expect(primary).toBeFocused()
    const ring = await primary.evaluate(el => {
      const cs = getComputedStyle(el)
      return { style: cs.outlineStyle, width: cs.outlineWidth, offset: cs.outlineOffset, color: cs.outlineColor }
    })
    expect(ring).toMatchObject({ style: 'solid', width: '2px', offset: '2px' })
    expect(ring.color).not.toMatch(/rgba\(0, 0, 0, 0\)|transparent/)
  })

  for (const theme of ['light', 'dark']) {
    test(`status chips: words, an icon and 4.5:1 text in ${theme}`, async ({ page }) => {
      await openCatalog(page, { theme })
      const chips = page.getByTestId('chips').locator('.ui-chip')
      expect(await chips.count()).toBe(7)
      const read = await chips.evaluateAll(els => els.map(el => {
        const cs = getComputedStyle(el)
        return { text: el.textContent, icon: !!el.querySelector('.ui-chip__icon'), fg: cs.color, bg: cs.backgroundColor, h: el.getBoundingClientRect().height }
      }))
      expect(read.map(c => c.text)).toEqual(['Running', 'Ready', 'Starting', 'Stopped', 'Failed', 'Update available', 'No runs yet'])
      for (const c of read) {
        expect(c.icon).toBe(true)
        expect(c.h).toBe(22)
        expect(contrastOf(c.fg, c.bg), `${c.text} ${c.fg} on ${c.bg}`).toBeGreaterThanOrEqual(4.5)
      }
    })
  }

  test('the old StatusPill is a chip and keeps its classes and words', async ({ page }) => {
    await openCatalog(page)
    const pill = page.getByTestId('legacy-pill').locator('.status-pill')
    await expect(pill).toHaveText('degraded')
    await expect(pill).toHaveClass(/status-pill--error/)
    await expect(pill).toHaveClass(/ui-chip--error/)
    const alias = page.locator('.ui-chip[data-status="healthy"]')
    await expect(alias).toHaveText('healthy (alias)')
    await expect(alias).toHaveClass(/ui-chip--ok/)
  })

  test('load error: inline, with the server message, and Try again works', async ({ page }) => {
    await openCatalog(page)
    const error = page.getByTestId('cat-load-error')
    await expect(error).toHaveAttribute('role', 'alert')
    await expect(error).toContainText('Could not load your MCP servers.')
    await expect(error).toContainText('“database is locked”')
    await expect(error).toContainText('Nothing was changed.')
    await expect(page.locator('.toast')).toHaveCount(0)
    const retry = error.getByRole('button', { name: 'Try again' })
    await retry.click()
    await expect(retry).toHaveAttribute('aria-busy', 'true')
    await expect(page.getByTestId('cat-load-ready')).toContainText('After 1 retry.')
    await expect(error).toHaveCount(0)
  })

  test('empty state: a left-aligned dashed outline with the action on the right', async ({ page }) => {
    await openCatalog(page)
    const empty = page.getByTestId('cat-empty-first-run')
    const cs = await empty.evaluate(el => ({ border: getComputedStyle(el).borderTopStyle, align: getComputedStyle(el).textAlign, title: getComputedStyle(el.querySelector('.ui-empty__title')).fontSize }))
    expect(cs).toEqual({ border: 'dashed', align: 'left', title: '17px' })
    const text = await empty.locator('.ui-empty__text').boundingBox()
    const action = await empty.getByRole('button', { name: 'Add MCP server' }).boundingBox()
    expect(action.x).toBeGreaterThan(text.x + text.width - 1)
  })

  test('toast: an inverse surface, Undo runs its action and closes the toast', async ({ page }) => {
    await openCatalog(page)
    await page.getByTestId('toast-undo').click()
    const toast = page.getByTestId('cat-undo-toast')
    await expect(toast).toBeVisible()
    await expect(toast).toHaveAttribute('data-state', 'open')
    const bg = await toast.evaluate(el => getComputedStyle(el).backgroundColor)
    const inverse = await page.evaluate(() => {
      const p = document.createElement('i'); p.style.color = 'var(--dk-inverse)'; document.body.appendChild(p)
      const c = getComputedStyle(p).color; p.remove(); return c
    })
    expect(bg).toBe(inverse)
    await expect(toast.locator('.toast__bar')).toHaveCount(1)
    // Bottom centre, 24 px from the edge, once the enter transition ends.
    await expect.poll(async () => { const b = await toast.boundingBox(); return Math.round(900 - (b.y + b.height)) }).toBe(24)
    const box = await toast.boundingBox()
    expect(box.x + box.width / 2).toBeCloseTo(720, 0)
    await toast.getByRole('button', { name: 'Undo' }).click()
    await expect(page.getByTestId('undo-count')).toHaveText('Undo clicked: 1')
    await expect(toast).toHaveCount(0)
  })

  test('toast: at most three at once', async ({ page }) => {
    await openCatalog(page)
    for (let i = 0; i < 5; i++) await page.getByRole('button', { name: 'Error' }).click()
    await expect(page.locator('.toast:not([data-state="exit"])')).toHaveCount(3)
    await expect(page.locator('.toast')).toHaveCount(3)
  })

  test('toast: the stack rises above a bar fixed at the bottom', async ({ page }) => {
    await openCatalog(page)
    await page.getByTestId('toast-bar').click()
    const bar = await page.getByTestId('cat-bottom-bar').boundingBox()
    await page.getByTestId('toast-info').click()
    const toast = page.locator('.toast').last()
    await expect(toast).toHaveAttribute('data-state', 'open')
    await expect.poll(async () => {
      const b = await toast.boundingBox()
      return Math.round(bar.y - (b.y + b.height))
    }).toBe(24)
  })

  test('toast: in an open chat the stack is lifted above the composer', async ({ page }) => {
    await mockChat(page)
    const detail = { id: 'c1', title: 'Quotes', model: 'qwen3-8b', kind: 'chat', created: '2026-10-25T10:00:00Z', updated: '2026-10-25T10:00:00Z', running: false, last_turn: '', pending: [] }
    await page.route('**/api/chats/c1/events', route => route.abort())
    await page.route(/\/api\/chats\/c1\/messages(\?.*)?$/, route => route.fulfill({ json: { messages: [
      { seq: 1, turn: 1, role: 'user', text: 'hello', at: '2026-10-25T10:00:00Z' },
      { seq: 2, turn: 1, role: 'assistant', text: 'Hi. What do you need?', at: '2026-10-25T10:00:01Z' },
    ] } }))
    await page.route('**/api/chats/c1', route => route.fulfill({ json: detail }))
    await page.route(/\/api\/chats(\?.*)?$/, route => route.fulfill({ json: { conversations: [detail], persistent: true, files: true, files_reason: '' } }))
    await page.goto('/app/chats/c1')
    await expect(page.getByText('Hi. What do you need?')).toBeVisible({ timeout: 15_000 })
    const composer = page.locator('.home-cmd').first()
    await expect(composer).toBeVisible()
    // The stack clears the whole dock, the shortcut row over the composer too.
    const dock = page.locator('.cx-dock__in')
    const read = () => dock.evaluate(el => ({
      lift: parseFloat(getComputedStyle(document.documentElement).getPropertyValue('--toast-lift')) || 0,
      top: el.getBoundingClientRect().top,
      vh: window.innerHeight,
      stack: parseFloat(getComputedStyle(document.querySelector('.toast-container')).bottom),
    }))
    // The composer sits at the bottom of an open chat, so the stack clears it.
    await expect.poll(async () => { const r = await read(); return r.lift > 0 && Math.abs(r.lift - (r.vh - r.top)) < 2 }).toBe(true)
    const r = await read()
    expect(r.vh - r.stack).toBeLessThanOrEqual(r.top - 20)
  })

  test('side pane: docked on a wide screen, Escape closes it and returns focus', async ({ page }) => {
    await openCatalog(page)
    const trigger = page.getByTestId('open-pane-Tables')
    await trigger.click()
    const pane = page.getByTestId('cat-pane')
    await expect(pane).toHaveAttribute('data-state', 'open')
    await expect(pane).toHaveAttribute('role', 'complementary')
    await expect(page.locator('.ui-pane-scrim')).toHaveCount(0)
    expect((await pane.boundingBox()).width).toBeCloseTo(360, 0)
    await expect(pane.getByRole('heading', { name: 'Tables' })).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(pane).toHaveCount(0)
    await expect(trigger).toBeFocused()
  })

  test('side pane: an overlay with a scrim below 1280 px', async ({ page }) => {
    await page.setViewportSize({ width: 1024, height: 800 })
    await openCatalog(page)
    await page.getByTestId('open-pane-Tables').click()
    const pane = page.getByTestId('cat-pane')
    await expect(pane).toHaveAttribute('role', 'dialog')
    await expect(page.locator('.ui-pane-scrim')).toBeVisible()
    // Flush with the right edge once it has slid in.
    await expect.poll(async () => { const b = await pane.boundingBox(); return Math.round(b.x + b.width) }).toBe(1024)
    await page.locator('.ui-pane-scrim').click({ position: { x: 20, y: 400 } })
    await expect(pane).toHaveCount(0)
  })

  test('cold start draws the app shell, not a blank screen', async ({ page }) => {
    let release
    const held = new Promise(r => { release = r })
    await page.route('**/locales/**', async route => { await held; await route.continue() })
    await page.goto('/app', { waitUntil: 'commit' })
    const shell = page.getByTestId('shell-fallback')
    await expect(shell).toBeVisible({ timeout: 15_000 })
    const side = await shell.locator('.app-shell-fallback__side').boundingBox()
    expect(side.width).toBe(240)
    release()
    await expect(shell).toHaveCount(0, { timeout: 15_000 })
  })

  test('reduced motion: skeletons stand still, toasts and panes switch at once', async ({ page }) => {
    await page.emulateMedia({ reducedMotion: 'reduce' })
    await openCatalog(page)
    const sweep = await page.locator('.skeleton').first().evaluate(el => getComputedStyle(el, '::after').animationName)
    expect(sweep).toBe('none')
    await page.getByTestId('toast-undo').click()
    const toast = page.getByTestId('cat-undo-toast')
    const t = await toast.evaluate(el => ({
      duration: Math.max(...getComputedStyle(el).transitionDuration.split(',').map(parseFloat)),
      bar: getComputedStyle(el.querySelector('.toast__bar')).animationName,
    }))
    expect(t.duration).toBeLessThan(0.001)
    // The Undo window line stays full instead of running.
    expect(t.bar).toBe('none')
  })

  test.describe('screenshots', () => {
    test.skip(!SHOTS, 'set UI_SHOTS_DIR to save the catalog screenshots')
    for (const theme of ['light', 'dark']) {
      for (const [name, size] of [['desk', { width: 1440, height: 900 }], ['mob', { width: 390, height: 844 }]]) {
        test(`catalog ${name} ${theme}`, async ({ page }) => {
          await page.setViewportSize(size)
          await openCatalog(page, { theme })
          await page.waitForTimeout(400)
          await page.screenshot({ path: `${SHOTS}/catalog-${name}-${theme}.png`, fullPage: true })
          for (const id of ['buttons', 'header', 'chips', 'callouts', 'load-error', 'empty', 'loading']) {
            await page.getByTestId(`cat-section-${id}`).screenshot({ path: `${SHOTS}/section-${id}-${name}-${theme}.png` })
          }
          // The states that need a click: busy retry, toasts, the open pane.
          await page.getByTestId('toast-undo').click()
          await page.getByRole('button', { name: 'Error' }).click()
          await page.getByTestId('cat-section-toasts').scrollIntoViewIfNeeded()
          await page.waitForTimeout(400)
          await page.screenshot({ path: `${SHOTS}/toasts-${name}-${theme}.png` })
          await page.getByTestId('open-pane-Tables').click()
          await page.waitForTimeout(500)
          await page.screenshot({ path: `${SHOTS}/pane-${name}-${theme}.png` })
          await page.keyboard.press('Escape')
          await page.getByTestId('cat-load-error').getByRole('button', { name: 'Try again' }).click()
          await page.getByTestId('cat-load-error').scrollIntoViewIfNeeded()
          await page.screenshot({ path: `${SHOTS}/load-error-busy-${name}-${theme}.png` })
        })
      }
    }
  })
})
