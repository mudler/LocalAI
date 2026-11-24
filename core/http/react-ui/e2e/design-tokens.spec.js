import { test, expect } from './coverage-fixtures.js'

// The vendored UI kit ships OS font stacks and 40px controls; theme.css points
// its roles at Geist and the product's control scale. These checks read the
// computed style, so they fail if the kit's own values win again.

const SANS = /^"Geist Variable"/
const MONO = /^"Geist Mono Variable"/

// Kit parts that a settings page may not render (a heading, a label, a mono
// value) are added as probes, so each role is checked on every run.
async function probe(page) {
  return page.evaluate(() => {
    const host = document.createElement('div')
    host.setAttribute('data-testid', 'token-probe')
    host.innerHTML = `
      <h2 class="dk-title-2">Title</h2>
      <label class="dk-label" for="token-probe-input">Label</label>
      <input class="dk-input" id="token-probe-input">
      <button class="dk-btn">Button</button>
      <button class="dk-btn dk-btn--sm">Small</button>
      <button class="btn">Legacy</button>
      <span class="dk-mono">abc123</span>
      <h1>Page title</h1>`
    document.body.appendChild(host)
    const cs = (sel) => getComputedStyle(host.querySelector(sel))
    const h = (sel) => host.querySelector(sel).getBoundingClientRect().height
    return {
      font: {
        title: cs('.dk-title-2').fontFamily,
        label: cs('.dk-label').fontFamily,
        input: cs('.dk-input').fontFamily,
        button: cs('.dk-btn').fontFamily,
        mono: cs('.dk-mono').fontFamily,
      },
      height: { button: h('.dk-btn:not(.dk-btn--sm)'), small: h('.dk-btn--sm'), input: h('.dk-input'), legacy: h('.btn') },
      h1: cs('h1').fontSize,
    }
  })
}

test.describe('Design tokens', () => {
  test('kit parts render in Geist, and only mono parts in Geist Mono', async ({ page }) => {
    await page.goto('/app/settings')
    const realButton = page.locator('.dk-btn').first()
    await expect(realButton).toBeVisible({ timeout: 15_000 })
    expect(await realButton.evaluate(el => getComputedStyle(el).fontFamily)).toMatch(SANS)
    const realInput = page.locator('.dk-input').first()
    expect(await realInput.evaluate(el => getComputedStyle(el).fontFamily)).toMatch(SANS)

    const p = await probe(page)
    expect(p.font.title).toMatch(SANS)
    expect(p.font.label).toMatch(SANS)
    expect(p.font.input).toMatch(SANS)
    expect(p.font.button).toMatch(SANS)
    expect(p.font.mono).toMatch(MONO)
    // The face is loaded, not only named.
    expect(await page.evaluate(() => document.fonts.ready.then(() => document.fonts.check('15px "Geist Variable"')))).toBe(true)
    expect(p.h1).toBe('28px')
  })

  test('controls are 36px on a desktop, 28px compact', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 })
    await page.goto('/app/settings')
    await expect(page.locator('.dk-btn').first()).toBeVisible({ timeout: 15_000 })
    expect(await page.evaluate(() => matchMedia('(pointer: coarse)').matches)).toBe(false)
    const p = await probe(page)
    expect(p.height).toEqual({ button: 36, small: 28, input: 36, legacy: 36 })
  })

  test.describe('on a touch screen', () => {
    test.use({ hasTouch: true })

    test('controls keep a 40px target on a coarse pointer', async ({ page }) => {
      await page.goto('/app/settings')
      await expect(page.locator('.dk-btn').first()).toBeVisible({ timeout: 15_000 })
      expect(await page.evaluate(() => matchMedia('(pointer: coarse)').matches)).toBe(true)
      const p = await probe(page)
      expect(p.height).toEqual({ button: 40, small: 32, input: 40, legacy: 40 })
    })
  })

  test.describe('with reduced motion', () => {
    // The fixture option form does not reach the coverage page fixture, so
    // the media feature is set on the page (as design-system.spec.js does).
    test('nothing moves and buttons do not press in', async ({ page }) => {
      await page.emulateMedia({ reducedMotion: 'reduce' })
      await page.goto('/app/settings')
      await expect(page.locator('.dk-btn').first()).toBeVisible({ timeout: 15_000 })
      await probe(page)
      const cs = await page.locator('[data-testid="token-probe"] .dk-btn').first().evaluate(el => ({
        duration: Math.max(...getComputedStyle(el).transitionDuration.split(',').map(parseFloat)),
        press: getComputedStyle(document.documentElement).getPropertyValue('--dk-press-scale').trim(),
      }))
      expect(cs.duration).toBeLessThan(0.001)
      expect(cs.press).toBe('1')
    })
  })
})
