import { test, expect } from './coverage-fixtures.js'
import { readFileSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
import { iconFromFa, faSpins, FALLBACK_ICON } from '../src/utils/faIcon.js'

// Icons are inline svg elements that point into the kit sprite, which the app
// inlines into the page once. A name the sprite lacks, or a sprite that does
// not resolve, draws nothing and fails no build, so these tests check both.

const HERE = dirname(fileURLToPath(import.meta.url))
const VENDOR = join(HERE, '..', 'src', 'vendor', 'ui-kit', 'icons')
const faMap = JSON.parse(readFileSync(join(VENDOR, 'fa-map.json'), 'utf8'))
const spriteIds = [...readFileSync(join(VENDOR, 'sprite.svg'), 'utf8').matchAll(/id="dk-icon-([^"]+)"/g)].map((m) => m[1])
const mappedIds = [...new Set(Object.entries(faMap).filter(([k]) => k !== 'unmapped').map(([, v]) => v))]

test.describe('icon sprite', () => {
  test('every icon id in the Font Awesome map has a symbol in the sprite', () => {
    expect(mappedIds.length).toBeGreaterThan(150)
    expect(mappedIds.filter((id) => !spriteIds.includes(id))).toEqual([])
  })

  test('the page holds the sprite and every mapped symbol draws something', async ({ page }) => {
    await page.goto('/app')
    await expect(page.locator('.sidebar-nav svg[data-icon]').first()).toBeVisible({ timeout: 15_000 })
    const result = await page.evaluate((ids) => {
      const missing = []
      const empty = []
      for (const id of ids) {
        const symbol = document.getElementById(`dk-icon-${id}`)
        if (!symbol) missing.push(id)
        else if (!symbol.querySelector('path, circle, rect, line, polyline, ellipse')) empty.push(id)
      }
      return { missing, empty, holders: document.querySelectorAll('#lai-icon-sprite').length }
    }, mappedIds)
    expect(result.missing).toEqual([])
    expect(result.empty).toEqual([])
    // Inlined once, however many icons the page draws.
    expect(result.holders).toBe(1)
  })

  test('an icon paints pixels, in the root build', async ({ page }) => {
    await page.goto('/app')
    const icon = page.locator('.sidebar-nav svg[data-icon]').first()
    await expect(icon).toBeVisible({ timeout: 15_000 })
    await expectPainted(page, icon)
  })

  test('an icon still resolves when the app is served under a path prefix', async ({ browser, baseURL }) => {
    const context = await browser.newContext({ baseURL, extraHTTPHeaders: { 'X-Forwarded-Prefix': '/llm' } })
    const page = await context.newPage()
    await page.goto('/llm/app')
    const base = await page.evaluate(() => document.querySelector('base')?.href)
    expect(base).toContain('/llm/')
    const icon = page.locator('.sidebar-nav svg[data-icon]').first()
    await expect(icon).toBeVisible({ timeout: 15_000 })
    await expectPainted(page, icon)
    await context.close()
  })
})

// A uniform square compresses to a tiny PNG; a line icon does not. Compare with
// a blank element of the same size so the check does not depend on the theme.
async function expectPainted(page, icon) {
  const box = await icon.boundingBox()
  const drawn = await icon.screenshot()
  const blank = await page.evaluate(({ w, h }) => {
    const el = document.createElement('div')
    el.id = 'icon-blank-probe'
    el.style.cssText = `position:fixed;left:2px;bottom:2px;width:${w}px;height:${h}px;background:Canvas`
    document.body.appendChild(el)
    return el.id
  }, { w: Math.round(box.width), h: Math.round(box.height) })
  const empty = await page.locator(`#${blank}`).screenshot()
  expect(drawn.length).toBeGreaterThan(empty.length)
}

test.describe('Font Awesome names', () => {
  test('a name maps with or without its prefix and style class', () => {
    expect(iconFromFa('trash')).toBe('trash')
    expect(iconFromFa('fa-trash')).toBe('trash')
    expect(iconFromFa('fas fa-trash')).toBe('trash')
    expect(iconFromFa('fa-solid fa-xmark')).toBe('close')
    expect(iconFromFa('far fa-copy')).toBe('copy')
    expect(iconFromFa('fa-spinner fa-spin')).toBe('spinner')
    expect(faSpins('fas fa-spinner fa-spin')).toBe(true)
    expect(faSpins('fas fa-spinner')).toBe(false)
  })

  test('every mapped name resolves to a symbol in the sprite', () => {
    const names = Object.keys(faMap).filter((k) => k !== 'unmapped')
    expect(names.length).toBeGreaterThan(200)
    expect(names.filter((n) => !spriteIds.includes(iconFromFa(`fas fa-${n}`)))).toEqual([])
  })

  test('brand marks resolve to the local glyphs and unknown names fall back', () => {
    expect(iconFromFa('fab fa-github')).toBe('github')
    expect(iconFromFa('fas fa-apple-whole')).toBe('apple')
    expect(spriteIds).toContain(FALLBACK_ICON)
    // An unknown name passes through; the Icon component swaps it for the fallback.
    expect(iconFromFa('')).toBe(FALLBACK_ICON)
    expect(iconFromFa('fa-fw')).toBe(FALLBACK_ICON)
  })
})
